package storm

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"math/big"
	"sync"
	"sync/atomic"
	"time"

	"github.com/balinderwalia/nebulacb/internal/metrics"
	"github.com/balinderwalia/nebulacb/internal/models"
	"github.com/balinderwalia/nebulacb/pkg/couchbase"
)

// Generator produces high-throughput data storms against a Couchbase cluster.
type Generator struct {
	config    models.StormConfig
	client    *couchbase.Client
	collector *metrics.Collector

	sequenceID atomic.Uint64
	running    atomic.Bool
	paused     atomic.Bool
	cancel     context.CancelFunc
	wg         sync.WaitGroup

	mu       sync.RWMutex
	hotKeys  []string
}

// NewGenerator creates a new Data Storm Generator.
func NewGenerator(cfg models.StormConfig, client *couchbase.Client, collector *metrics.Collector) *Generator {
	return &Generator{
		config:    cfg,
		client:    client,
		collector: collector,
		hotKeys:   make([]string, 0, 1000),
	}
}

// Start begins the data storm.
func (g *Generator) Start(ctx context.Context) error {
	if g.running.Load() {
		return fmt.Errorf("storm already running")
	}

	ctx, g.cancel = context.WithCancel(ctx)
	g.running.Store(true)
	g.paused.Store(false)

	log.Printf("[Storm] Starting with %d workers, %d writes/sec, region=%s",
		g.config.Workers, g.config.WritesPerSecond, g.config.Region)

	// Calculate per-worker rate
	writeInterval := time.Second / time.Duration(g.config.WritesPerSecond/g.config.Workers)

	for i := 0; i < g.config.Workers; i++ {
		g.wg.Add(1)
		go g.worker(ctx, i, writeInterval)
	}

	// Burst scheduler
	if g.config.BurstEnabled {
		g.wg.Add(1)
		go g.burstScheduler(ctx)
	}

	return nil
}

// Stop halts the data storm.
func (g *Generator) Stop() {
	if !g.running.Load() {
		return
	}
	log.Println("[Storm] Stopping...")
	g.cancel()
	g.wg.Wait()
	g.running.Store(false)
	log.Println("[Storm] Stopped")
}

// Pause pauses the storm without stopping workers.
func (g *Generator) Pause() {
	g.paused.Store(true)
	log.Println("[Storm] Paused")
}

// Resume resumes the storm.
func (g *Generator) Resume() {
	g.paused.Store(false)
	log.Println("[Storm] Resumed")
}

// IsRunning returns whether the generator is active.
func (g *Generator) IsRunning() bool {
	return g.running.Load()
}

func (g *Generator) worker(ctx context.Context, id int, interval time.Duration) {
	defer g.wg.Done()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if g.paused.Load() {
				continue
			}
			g.executeOperation(ctx)
		}
	}
}

func (g *Generator) executeOperation(ctx context.Context) {
	// Determine operation type
	r := randomFloat()
	switch {
	case r < g.config.DeletePercentage:
		g.doDelete(ctx)
	case r < g.config.DeletePercentage+g.config.UpdatePercentage:
		g.doUpdate(ctx)
	default:
		g.doInsert(ctx)
	}
}

func (g *Generator) doInsert(ctx context.Context) {
	seq := g.sequenceID.Add(1)
	doc := g.generateDocument(seq)

	start := time.Now()
	err := g.client.Upsert(ctx, doc.ID, doc)
	latency := float64(time.Since(start).Milliseconds())

	if err != nil {
		g.collector.RecordError()
		return
	}

	g.collector.RecordWrite(latency)

	// Track hot keys
	if randomFloat() < g.config.HotKeyPercentage {
		g.mu.Lock()
		g.hotKeys = append(g.hotKeys, doc.ID)
		if len(g.hotKeys) > 10000 {
			g.hotKeys = g.hotKeys[5000:]
		}
		g.mu.Unlock()
	}
}

func (g *Generator) doUpdate(ctx context.Context) {
	g.mu.RLock()
	if len(g.hotKeys) == 0 {
		g.mu.RUnlock()
		g.doInsert(ctx)
		return
	}
	idx := randomInt(len(g.hotKeys))
	key := g.hotKeys[idx]
	g.mu.RUnlock()

	seq := g.sequenceID.Add(1)
	doc := g.generateDocument(seq)
	doc.ID = key

	start := time.Now()
	err := g.client.Upsert(ctx, doc.ID, doc)
	latency := float64(time.Since(start).Milliseconds())

	if err != nil {
		g.collector.RecordError()
		return
	}
	g.collector.RecordWrite(latency)
}

func (g *Generator) doDelete(ctx context.Context) {
	g.mu.Lock()
	if len(g.hotKeys) == 0 {
		g.mu.Unlock()
		return
	}
	idx := randomInt(len(g.hotKeys))
	key := g.hotKeys[idx]
	g.hotKeys = append(g.hotKeys[:idx], g.hotKeys[idx+1:]...)
	g.mu.Unlock()

	start := time.Now()
	err := g.client.Remove(ctx, key)
	latency := float64(time.Since(start).Milliseconds())

	if err != nil {
		g.collector.RecordError()
		return
	}
	g.collector.RecordWrite(latency)
}

func (g *Generator) generateDocument(seq uint64) models.Document {
	size := g.config.DocSizeMin
	if g.config.DocSizeMax > g.config.DocSizeMin {
		size = g.config.DocSizeMin + randomInt(g.config.DocSizeMax-g.config.DocSizeMin)
	}

	payload := make([]byte, size)
	rand.Read(payload)

	hash := sha256.Sum256(payload)

	doc := models.Document{
		ID:         fmt.Sprintf("%s::%s::%d", g.config.KeyPrefix, g.config.Region, seq),
		SequenceID: seq,
		Timestamp:  time.Now(),
		Region:     g.config.Region,
		Checksum:   hex.EncodeToString(hash[:]),
		Size:       size,
		Payload:    payload,
	}

	return doc
}

func (g *Generator) burstScheduler(ctx context.Context) {
	defer g.wg.Done()
	ticker := time.NewTicker(g.config.BurstInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if g.paused.Load() {
				continue
			}
			log.Printf("[Storm] Burst started (%.1fx for %s)", g.config.BurstMultiplier, g.config.BurstDuration)
			g.executeBurst(ctx)
		}
	}
}

func (g *Generator) executeBurst(ctx context.Context) {
	burstRate := int(float64(g.config.WritesPerSecond) * g.config.BurstMultiplier)
	interval := time.Second / time.Duration(burstRate/g.config.Workers)

	timer := time.NewTimer(g.config.BurstDuration)
	defer timer.Stop()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			log.Println("[Storm] Burst ended")
			return
		case <-ticker.C:
			g.doInsert(ctx)
		}
	}
}

// GetSequenceID returns the current sequence counter.
func (g *Generator) GetSequenceID() uint64 {
	return g.sequenceID.Load()
}

// GetConfig returns the current storm config as JSON.
func (g *Generator) GetConfigJSON() ([]byte, error) {
	return json.Marshal(g.config)
}

func randomFloat() float64 {
	n, _ := rand.Int(rand.Reader, big.NewInt(10000))
	return float64(n.Int64()) / 10000.0
}

func randomInt(max int) int {
	if max <= 0 {
		return 0
	}
	n, _ := rand.Int(rand.Reader, big.NewInt(int64(max)))
	return int(n.Int64())
}
