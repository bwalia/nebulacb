package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math/big"
	mrand "math/rand"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/balinderwalia/nebulacb/internal/config"
	"github.com/balinderwalia/nebulacb/pkg/couchbase"
)

// TestDocument is the payload written to both clusters.
type TestDocument struct {
	ID        string    `json:"id"`
	Cluster   string    `json:"cluster"`
	Sequence  uint64    `json:"sequence"`
	Timestamp time.Time `json:"timestamp"`
	Checksum  string    `json:"checksum"`
	Size      int       `json:"size"`
	Payload   string    `json:"payload"`
}

// Stats tracks write/error counters per cluster.
type Stats struct {
	cluster1Writes uint64
	cluster2Writes uint64
	errors         uint64
	totalLatencyUs uint64
	totalOps       uint64
}

func main() {
	configPath := flag.String("config", "config.json", "Path to NebulaCB config file")
	ratePerSec := flag.Int("rate", 100, "Total writes per second across both clusters")
	docSizeMin := flag.Int("doc-min", 256, "Minimum document size in bytes")
	docSizeMax := flag.Int("doc-max", 4096, "Maximum document size in bytes")
	workers := flag.Int("workers", 8, "Number of concurrent writer goroutines")
	duration := flag.Duration("duration", 0, "Test duration (0 = run until interrupted)")
	keyPrefix := flag.String("prefix", "xdcr-test", "Document key prefix")
	ratio := flag.Float64("ratio", 0.5, "Ratio of writes to cluster1 vs cluster2 (0.5 = equal)")
	flag.Parse()

	cfg, err := config.LoadFromFile(*configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	log.Printf("Connecting to cluster 1 (source): %s bucket=%s", cfg.Source.Host, cfg.Source.Bucket)
	client1, err := couchbase.NewClient(cfg.Source)
	if err != nil {
		log.Fatalf("Failed to connect to source cluster: %v", err)
	}
	defer client1.Close()

	log.Printf("Connecting to cluster 2 (target): %s bucket=%s", cfg.Target.Host, cfg.Target.Bucket)
	client2, err := couchbase.NewClient(cfg.Target)
	if err != nil {
		log.Fatalf("Failed to connect to target cluster: %v", err)
	}
	defer client2.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if *duration > 0 {
		ctx, cancel = context.WithTimeout(ctx, *duration)
		defer cancel()
	}

	// Handle graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Println("Shutting down...")
		cancel()
	}()

	var stats Stats
	var seq atomic.Uint64

	// Rate limiter: distribute writes across a ticker
	interval := time.Second / time.Duration(*ratePerSec)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Work channel
	type writeJob struct {
		client  *couchbase.Client
		cluster string
	}
	jobs := make(chan writeJob, *workers*2)

	// Start workers
	var wg sync.WaitGroup
	for i := 0; i < *workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobs {
				seqNum := seq.Add(1)
				doc := generateDoc(*keyPrefix, job.cluster, seqNum, *docSizeMin, *docSizeMax)

				start := time.Now()
				writeErr := job.client.Upsert(ctx, doc.ID, doc)
				latency := time.Since(start)

				if writeErr != nil {
					atomic.AddUint64(&stats.errors, 1)
					log.Printf("[ERROR] %s seq=%d: %v", job.cluster, seqNum, writeErr)
				} else {
					atomic.AddUint64(&stats.totalLatencyUs, uint64(latency.Microseconds()))
					atomic.AddUint64(&stats.totalOps, 1)
					if job.cluster == "cluster1" {
						atomic.AddUint64(&stats.cluster1Writes, 1)
					} else {
						atomic.AddUint64(&stats.cluster2Writes, 1)
					}
				}
			}
		}()
	}

	// Stats reporter
	go func() {
		reportTicker := time.NewTicker(5 * time.Second)
		defer reportTicker.Stop()
		var lastC1, lastC2, lastErr uint64
		for {
			select {
			case <-ctx.Done():
				return
			case <-reportTicker.C:
				c1 := atomic.LoadUint64(&stats.cluster1Writes)
				c2 := atomic.LoadUint64(&stats.cluster2Writes)
				errs := atomic.LoadUint64(&stats.errors)
				ops := atomic.LoadUint64(&stats.totalOps)
				latUs := atomic.LoadUint64(&stats.totalLatencyUs)

				avgLat := float64(0)
				if ops > 0 {
					avgLat = float64(latUs) / float64(ops) / 1000.0 // ms
				}

				log.Printf("[STATS] cluster1=%d (+%d) cluster2=%d (+%d) errors=%d (+%d) avg_latency=%.2fms",
					c1, c1-lastC1, c2, c2-lastC2, errs, errs-lastErr, avgLat)
				lastC1, lastC2, lastErr = c1, c2, errs
			}
		}
	}()

	log.Printf("Starting XDCR load test: rate=%d/s workers=%d ratio=%.2f prefix=%s",
		*ratePerSec, *workers, *ratio, *keyPrefix)

	// Dispatch loop
	func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				target := pickCluster(client1, client2, *ratio)
				select {
				case jobs <- target:
				default:
					// Drop if backpressured — workers are saturated
					atomic.AddUint64(&stats.errors, 1)
				}
			}
		}
	}()

	close(jobs)
	wg.Wait()

	// Final summary
	c1 := atomic.LoadUint64(&stats.cluster1Writes)
	c2 := atomic.LoadUint64(&stats.cluster2Writes)
	errs := atomic.LoadUint64(&stats.errors)
	ops := atomic.LoadUint64(&stats.totalOps)
	latUs := atomic.LoadUint64(&stats.totalLatencyUs)

	avgLat := float64(0)
	if ops > 0 {
		avgLat = float64(latUs) / float64(ops) / 1000.0
	}

	fmt.Println("\n========== XDCR Load Test Summary ==========")
	fmt.Printf("Cluster 1 (source) writes: %d\n", c1)
	fmt.Printf("Cluster 2 (target) writes: %d\n", c2)
	fmt.Printf("Total writes:              %d\n", c1+c2)
	fmt.Printf("Total errors:              %d\n", errs)
	fmt.Printf("Avg write latency:         %.2f ms\n", avgLat)
	fmt.Println("=============================================")
}

func pickCluster(c1, c2 *couchbase.Client, ratio float64) struct {
	client  *couchbase.Client
	cluster string
} {
	type writeJob struct {
		client  *couchbase.Client
		cluster string
	}
	r := mrand.Float64()
	if r < ratio {
		return struct {
			client  *couchbase.Client
			cluster string
		}{c1, "cluster1"}
	}
	return struct {
		client  *couchbase.Client
		cluster string
	}{c2, "cluster2"}
}

func generateDoc(prefix, cluster string, seq uint64, minSize, maxSize int) TestDocument {
	size := minSize
	if maxSize > minSize {
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(maxSize-minSize)))
		size = minSize + int(n.Int64())
	}

	payload := make([]byte, size)
	rand.Read(payload)
	payloadHex := hex.EncodeToString(payload[:size/2]) // hex-encode half for readable JSON

	id := fmt.Sprintf("%s::%s::%d", prefix, cluster, seq)

	raw, _ := json.Marshal(payloadHex)
	h := sha256.Sum256(raw)
	checksum := hex.EncodeToString(h[:])

	return TestDocument{
		ID:        id,
		Cluster:   cluster,
		Sequence:  seq,
		Timestamp: time.Now(),
		Checksum:  checksum,
		Size:      size,
		Payload:   payloadHex,
	}
}
