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

// StormClient is the interface the storm generator needs to write/read/delete docs.
type StormClient interface {
	Upsert(ctx context.Context, id string, doc interface{}) error
	Get(ctx context.Context, id string) ([]byte, error)
	Remove(ctx context.Context, id string) error
}

// Generator produces high-throughput data storms against a Couchbase cluster.
type Generator struct {
	config    models.StormConfig
	client    *couchbase.Client
	restClient *couchbase.RESTClient
	active    StormClient // whichever client is available
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

// SetRESTClient sets a REST-based fallback client for when the SDK is unavailable.
func (g *Generator) SetRESTClient(rc *couchbase.RESTClient) {
	g.restClient = rc
}

// Start begins the data storm.
func (g *Generator) Start(ctx context.Context) error {
	if g.running.Load() {
		return fmt.Errorf("storm already running")
	}

	// Determine which client to use
	if g.client != nil {
		g.active = g.client
		log.Println("[Storm] Using SDK client (KV protocol)")
	} else if g.restClient != nil {
		g.active = g.restClient
		// REST mode: cap workers and rate to avoid overwhelming the HTTP endpoint
		if g.config.Workers > 2 {
			g.config.Workers = 2
		}
		if g.config.WritesPerSecond > 20 {
			g.config.WritesPerSecond = 20
		}
		log.Printf("[Storm] Using REST client (HTTP fallback — capped to %d workers, %d writes/sec)",
			g.config.Workers, g.config.WritesPerSecond)
	} else {
		return fmt.Errorf("no Couchbase connection available — configure cluster access to start load test")
	}

	// Use a detached context so the storm outlives the HTTP request that started it
	ctx, g.cancel = context.WithCancel(context.Background())
	g.running.Store(true)
	g.paused.Store(false)

	log.Printf("[Storm] Starting with %d workers, %d writes/sec, region=%s",
		g.config.Workers, g.config.WritesPerSecond, g.config.Region)

	// Calculate per-worker rate
	perWorkerRate := g.config.WritesPerSecond / g.config.Workers
	if perWorkerRate <= 0 {
		perWorkerRate = 1
	}
	writeInterval := time.Second / time.Duration(perWorkerRate)

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
	g.active = nil
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
	if id == 0 {
		log.Printf("[Storm] Worker %d started, interval=%v", id, interval)
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			if id == 0 {
				log.Printf("[Storm] Worker %d context done", id)
			}
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
	if seq <= 3 {
		log.Printf("[Storm] doInsert called, seq=%d", seq)
	}
	doc := g.generateDocument(seq)

	start := time.Now()
	err := g.active.Upsert(ctx, doc.ID, doc)
	latency := float64(time.Since(start).Milliseconds())

	if err != nil {
		g.collector.RecordError()
		if seq <= 5 || g.sequenceID.Load()%100 == 0 {
			log.Printf("[Storm] Write error (seq %d, latency %.0fms): %v", seq, latency, err)
		}
		return
	}

	g.collector.RecordWrite(latency)
	if seq <= 5 {
		log.Printf("[Storm] Write OK (seq %d, id=%s, latency %.0fms)", seq, doc.ID, latency)
	}

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
	err := g.active.Upsert(ctx, doc.ID, doc)
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
	err := g.active.Remove(ctx, key)
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

// GetConfigJSON returns the current storm config as JSON.
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
