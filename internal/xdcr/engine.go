package xdcr

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/balinderwalia/nebulacb/internal/metrics"
	"github.com/balinderwalia/nebulacb/internal/models"
)

// Engine monitors and validates XDCR replication.
type Engine struct {
	config    models.XDCRConfig
	collector *metrics.Collector

	mu     sync.RWMutex
	status models.XDCRStatus
	cancel context.CancelFunc

	// Track GOXDCR delay windows
	delayWindows []DelayWindow

	// Pipeline restart counter
	restartCount int
}

// DelayWindow tracks a GOXDCR-induced replication delay.
type DelayWindow struct {
	Start    time.Time     `json:"start"`
	End      time.Time     `json:"end,omitempty"`
	Duration time.Duration `json:"duration,omitempty"`
	Cause    string        `json:"cause"`
}

// NewEngine creates a new XDCR validation engine.
func NewEngine(cfg models.XDCRConfig, collector *metrics.Collector) *Engine {
	return &Engine{
		config:       cfg,
		collector:    collector,
		delayWindows: make([]DelayWindow, 0),
		status: models.XDCRStatus{
			ReplicationID: cfg.ReplicationID,
			State:         "Unknown",
		},
	}
}

// Start begins XDCR monitoring.
func (e *Engine) Start(ctx context.Context) error {
	ctx, e.cancel = context.WithCancel(ctx)
	log.Printf("[XDCR] Starting monitoring for replication %s", e.config.ReplicationID)

	go e.monitorLoop(ctx)
	return nil
}

// Stop halts XDCR monitoring.
func (e *Engine) Stop() {
	if e.cancel != nil {
		e.cancel()
	}
	log.Println("[XDCR] Monitoring stopped")
}

// GetStatus returns current XDCR status.
func (e *Engine) GetStatus() models.XDCRStatus {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.status
}

// GetDelayWindows returns tracked GOXDCR delay windows.
func (e *Engine) GetDelayWindows() []DelayWindow {
	e.mu.RLock()
	defer e.mu.RUnlock()
	result := make([]DelayWindow, len(e.delayWindows))
	copy(result, e.delayWindows)
	return result
}

// RestartPipeline attempts to restart the XDCR pipeline via REST API.
func (e *Engine) RestartPipeline(ctx context.Context) error {
	host := extractHost(e.config.SourceCluster.Host)
	url := fmt.Sprintf("http://%s/controller/restartXDCR/%s", host, e.config.ReplicationID)

	req, err := http.NewRequestWithContext(ctx, "POST", url, nil)
	if err != nil {
		return err
	}
	req.SetBasicAuth(e.config.SourceCluster.Username, e.config.SourceCluster.Password)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("restart XDCR pipeline: %w", err)
	}
	defer resp.Body.Close()

	e.mu.Lock()
	e.restartCount++
	e.status.PipelineRestarts = e.restartCount
	e.mu.Unlock()

	log.Printf("[XDCR] Pipeline restarted (count: %d)", e.restartCount)
	return nil
}

func (e *Engine) monitorLoop(ctx context.Context) {
	ticker := time.NewTicker(e.config.PollInterval)
	defer ticker.Stop()

	var lastChangesLeft uint64
	var delayDetected bool

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			stats, err := e.fetchXDCRStats(ctx)
			if err != nil {
				log.Printf("[XDCR] Error fetching stats: %v", err)
				continue
			}

			e.mu.Lock()
			e.status = stats
			e.status.PipelineRestarts = e.restartCount

			// Detect GOXDCR delay pattern:
			// changesLeft growing or stale + topology change
			if stats.TopologyChange && stats.ChangesLeft > lastChangesLeft && !delayDetected {
				delayDetected = true
				e.status.GoxdcrDelayStart = time.Now()
				e.delayWindows = append(e.delayWindows, DelayWindow{
					Start: time.Now(),
					Cause: "topology_change_rebalance",
				})

				e.collector.AddAlert(models.Alert{
					ID:        fmt.Sprintf("goxdcr-delay-%d", time.Now().Unix()),
					Severity:  "warning",
					Category:  "replication",
					Title:     "GOXDCR Delay Detected",
					Message:   "XDCR pipeline paused due to topology change. Expected ~5 minute delay.",
					Source:    "xdcr-engine",
					Timestamp: time.Now(),
				})
			}

			// Detect delay recovery
			if delayDetected && !stats.TopologyChange && stats.ChangesLeft < lastChangesLeft {
				delayDetected = false
				e.status.GoxdcrDelayEnd = time.Now()
				if len(e.delayWindows) > 0 {
					last := &e.delayWindows[len(e.delayWindows)-1]
					last.End = time.Now()
					last.Duration = last.End.Sub(last.Start)
					log.Printf("[XDCR] GOXDCR delay resolved (duration: %s)", last.Duration)
				}
			}

			// Detect stalled replication
			if stats.ChangesLeft > 0 && stats.State == "Paused" {
				e.collector.AddAlert(models.Alert{
					ID:        fmt.Sprintf("xdcr-stalled-%d", time.Now().Unix()),
					Severity:  "critical",
					Category:  "replication",
					Title:     "Replication Stalled",
					Message:   fmt.Sprintf("XDCR pipeline paused with %d changes pending", stats.ChangesLeft),
					Source:    "xdcr-engine",
					Timestamp: time.Now(),
				})
			}

			lastChangesLeft = stats.ChangesLeft
			e.mu.Unlock()

			e.collector.SetXDCRStatus(stats)
		}
	}
}

func (e *Engine) fetchXDCRStats(ctx context.Context) (models.XDCRStatus, error) {
	host := extractHost(e.config.SourceCluster.Host)

	// Get XDCR tasks
	tasksURL := fmt.Sprintf("http://%s/pools/default/tasks", host)
	req, err := http.NewRequestWithContext(ctx, "GET", tasksURL, nil)
	if err != nil {
		return models.XDCRStatus{}, err
	}
	req.SetBasicAuth(e.config.SourceCluster.Username, e.config.SourceCluster.Password)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return models.XDCRStatus{}, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return models.XDCRStatus{}, err
	}

	var tasks []map[string]interface{}
	if err := json.Unmarshal(body, &tasks); err != nil {
		return models.XDCRStatus{}, err
	}

	status := models.XDCRStatus{
		ReplicationID: e.config.ReplicationID,
		State:         "Running",
		Timestamp:     time.Now(),
	}

	// Find XDCR tasks and extract metrics
	for _, task := range tasks {
		taskType, _ := task["type"].(string)
		if taskType == "xdcr" {
			if cl, ok := task["changesLeft"].(float64); ok {
				status.ChangesLeft = uint64(cl)
			}
			if dp, ok := task["docsProcessed"].(float64); ok {
				status.DocsProcessed = uint64(dp)
			}
			if st, ok := task["status"].(string); ok {
				status.State = st
			}
		}

		// Detect rebalance/topology changes
		if taskType == "rebalance" {
			if st, ok := task["status"].(string); ok && st == "running" {
				status.TopologyChange = true
			}
		}
	}

	// Get XDCR-specific stats for latency
	statsURL := fmt.Sprintf("http://%s/pools/default/buckets/%s/stats",
		host, e.config.SourceCluster.Bucket)
	req2, err := http.NewRequestWithContext(ctx, "GET", statsURL, nil)
	if err != nil {
		return status, nil // Return partial status
	}
	req2.SetBasicAuth(e.config.SourceCluster.Username, e.config.SourceCluster.Password)

	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		return status, nil
	}
	defer resp2.Body.Close()

	return status, nil
}

func extractHost(host string) string {
	if strings.Contains(host, "://") {
		parts := strings.SplitN(host, "://", 2)
		host = parts[1]
	}
	if !strings.Contains(host, ":") {
		host = host + ":8091"
	}
	return host
}
