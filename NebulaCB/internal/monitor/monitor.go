package monitor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/balinderwalia/nebulacb/internal/metrics"
	"github.com/balinderwalia/nebulacb/internal/models"
)

// Monitor polls N Couchbase clusters for real-time metrics
// and tracks data-loss proof between source and target roles.
type Monitor struct {
	clusters  map[string]models.ClusterConfig
	collector *metrics.Collector
	interval  time.Duration

	mu        sync.RWMutex
	clusterMetrics map[string]models.ClusterMetrics
	proof     models.DataLossProof
	startTime time.Time
	cancel    context.CancelFunc
}

// NewMonitor creates a multi-cluster monitor.
func NewMonitor(clusters map[string]models.ClusterConfig, collector *metrics.Collector, interval time.Duration) *Monitor {
	return &Monitor{
		clusters:       clusters,
		collector:      collector,
		interval:       interval,
		clusterMetrics: make(map[string]models.ClusterMetrics, len(clusters)),
		proof: models.DataLossProof{
			Timeline: make([]models.DocCountSample, 0, 1000),
		},
		startTime: time.Now(),
	}
}

// Start begins polling all clusters.
func (m *Monitor) Start(ctx context.Context) {
	ctx, m.cancel = context.WithCancel(ctx)
	log.Printf("[Monitor] Starting multi-cluster monitoring (%d clusters, interval: %s)", len(m.clusters), m.interval)
	for name, cfg := range m.clusters {
		log.Printf("[Monitor]   %s: %s (role=%s bucket=%s)", name, cfg.Host, cfg.Role, cfg.Bucket)
	}
	go m.pollLoop(ctx)
}

// Stop halts monitoring.
func (m *Monitor) Stop() {
	if m.cancel != nil {
		m.cancel()
	}
}

// GetClusterMetrics returns metrics for a single cluster.
func (m *Monitor) GetClusterMetrics(name string) models.ClusterMetrics {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.clusterMetrics[name]
}

// GetAllClusterMetrics returns all cluster metrics.
func (m *Monitor) GetAllClusterMetrics() map[string]models.ClusterMetrics {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make(map[string]models.ClusterMetrics, len(m.clusterMetrics))
	for k, v := range m.clusterMetrics {
		result[k] = v
	}
	return result
}

// GetDataLossProof returns the current data loss proof state.
func (m *Monitor) GetDataLossProof() models.DataLossProof {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p := m.proof
	p.Timeline = make([]models.DocCountSample, len(m.proof.Timeline))
	copy(p.Timeline, m.proof.Timeline)
	return p
}

// findByRole returns cluster name and metrics for a given role.
func (m *Monitor) findByRole(role string) (string, models.ClusterMetrics) {
	for name, cfg := range m.clusters {
		if cfg.Role == role {
			return name, m.clusterMetrics[name]
		}
	}
	return "", models.ClusterMetrics{}
}

func (m *Monitor) pollLoop(ctx context.Context) {
	m.poll(ctx)
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.poll(ctx)
		}
	}
}

type pollResult struct {
	name    string
	metrics models.ClusterMetrics
	err     error
}

func (m *Monitor) poll(ctx context.Context) {
	results := make(chan pollResult, len(m.clusters))
	var wg sync.WaitGroup

	for name, cfg := range m.clusters {
		if cfg.Host == "" {
			continue
		}
		wg.Add(1)
		go func(n string, c models.ClusterConfig) {
			defer wg.Done()
			met, err := m.fetchClusterMetrics(ctx, c)
			results <- pollResult{name: n, metrics: met, err: err}
		}(name, cfg)
	}
	wg.Wait()
	close(results)

	m.mu.Lock()
	for r := range results {
		if r.err != nil {
			log.Printf("[Monitor] %s poll error: %v", r.name, r.err)
			r.metrics = models.ClusterMetrics{
				ClusterName: r.name,
				Healthy:     false,
				Timestamp:   time.Now(),
			}
		}
		m.clusterMetrics[r.name] = r.metrics
	}

	// Data loss proof: compare source vs target roles
	_, srcMetrics := m.findByRole("source")
	_, tgtMetrics := m.findByRole("target")

	sample := models.DocCountSample{
		Timestamp:  time.Now(),
		SourceDocs: srcMetrics.TotalDocs,
		TargetDocs: tgtMetrics.TotalDocs,
		Delta:      int64(srcMetrics.TotalDocs) - int64(tgtMetrics.TotalDocs),
	}
	m.proof.Timeline = append(m.proof.Timeline, sample)
	if len(m.proof.Timeline) > 1000 {
		m.proof.Timeline = m.proof.Timeline[len(m.proof.Timeline)-1000:]
	}
	m.proof.CurrentSourceDocs = srcMetrics.TotalDocs
	m.proof.CurrentTargetDocs = tgtMetrics.TotalDocs
	m.proof.CurrentDelta = sample.Delta
	m.proof.MonitoringDuration = time.Since(m.startTime).Round(time.Second).String()
	m.proof.Timestamp = time.Now()

	absDelta := int64(math.Abs(float64(sample.Delta)))
	if absDelta > m.proof.MaxDeltaObserved {
		m.proof.MaxDeltaObserved = absDelta
		m.proof.MaxDeltaTime = time.Now()
	}

	if sample.Delta == 0 && srcMetrics.TotalDocs > 0 {
		if !m.proof.IsConverged {
			m.proof.IsConverged = true
			m.proof.ConvergedAt = time.Now()
			log.Printf("[Monitor] CONVERGED: source=%d target=%d", srcMetrics.TotalDocs, tgtMetrics.TotalDocs)
		}
	} else {
		m.proof.IsConverged = false
		m.proof.ConvergedAt = time.Time{}
	}
	m.proof.ZeroLossConfirmed = m.proof.IsConverged && m.proof.HashSamplesFailed == 0
	m.mu.Unlock()

	// Publish to collector
	allMetrics := m.GetAllClusterMetrics()
	m.collector.SetAllClusterMetrics(allMetrics)
	// Backward compat: also set source/target shortcuts
	m.collector.SetSourceCluster(srcMetrics)
	m.collector.SetTargetCluster(tgtMetrics)
	m.collector.SetDataLossProof(m.proof)

	if absDelta > 1000 {
		m.collector.AddAlert(models.Alert{
			ID:        fmt.Sprintf("divergence-%d", time.Now().Unix()),
			Severity:  "warning",
			Category:  "data_loss",
			Title:     "Document Count Divergence",
			Message:   fmt.Sprintf("Source: %d, Target: %d, Delta: %d", srcMetrics.TotalDocs, tgtMetrics.TotalDocs, sample.Delta),
			Source:    "monitor",
			Timestamp: time.Now(),
		})
	}
}

// RecordHashCheck records a hash sample result.
func (m *Monitor) RecordHashCheck(passed bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if passed {
		m.proof.HashSamplesPassed++
	} else {
		m.proof.HashSamplesFailed++
	}
	m.proof.LastHashCheckTime = time.Now()
}

func (m *Monitor) fetchClusterMetrics(ctx context.Context, cfg models.ClusterConfig) (models.ClusterMetrics, error) {
	host := extractHost(cfg.Host)
	result := models.ClusterMetrics{
		ClusterName: cfg.Name,
		Timestamp:   time.Now(),
	}

	// 1. /pools/default
	poolData, err := httpGet(ctx, host, "/pools/default", cfg.Username, cfg.Password)
	if err != nil {
		return result, fmt.Errorf("pools/default: %w", err)
	}

	var pool struct {
		Nodes []struct {
			Hostname          string `json:"hostname"`
			Status            string `json:"status"`
			Version           string `json:"version"`
			ClusterMembership string `json:"clusterMembership"`
			SystemStats       struct {
				CPURate  float64 `json:"cpu_utilization_rate"`
				MemFree  uint64  `json:"mem_free"`
				MemTotal uint64  `json:"mem_total"`
			} `json:"systemStats"`
			InterestingStats struct {
				CurrItems float64 `json:"curr_items"`
				CmdGet    float64 `json:"cmd_get"`
				CmdSet    float64 `json:"cmd_set"`
				Ops       float64 `json:"ops"`
			} `json:"interestingStats"`
		} `json:"nodes"`
		RebalanceStatus string `json:"rebalanceStatus"`
	}
	if err := json.Unmarshal(poolData, &pool); err != nil {
		return result, fmt.Errorf("parse pools: %w", err)
	}

	result.RebalanceState = pool.RebalanceStatus
	result.Healthy = true

	for _, n := range pool.Nodes {
		memUsage := 0.0
		if n.SystemStats.MemTotal > 0 {
			memUsage = float64(n.SystemStats.MemTotal-n.SystemStats.MemFree) / float64(n.SystemStats.MemTotal) * 100
		}
		status := n.Status
		if n.ClusterMembership != "active" {
			status = "inactive"
			result.Healthy = false
		}
		result.Nodes = append(result.Nodes, models.NodeStatus{
			Host:          n.Hostname,
			Status:        status,
			Version:       n.Version,
			CPUUsage:      n.SystemStats.CPURate,
			MemoryUsage:   memUsage,
			LastHeartbeat: time.Now(),
		})
		if result.Version == "" {
			result.Version = n.Version
		}
		result.OpsPerSec += n.InterestingStats.Ops
		result.CmdGetPerSec += n.InterestingStats.CmdGet
		result.CmdSetPerSec += n.InterestingStats.CmdSet
		result.TotalDocs += uint64(n.InterestingStats.CurrItems)
		result.TotalMemTotalMB += float64(n.SystemStats.MemTotal) / (1024 * 1024)
		result.TotalMemUsedMB += float64(n.SystemStats.MemTotal-n.SystemStats.MemFree) / (1024 * 1024)
		if status != "healthy" && status != "warmup" {
			result.Healthy = false
		}
	}

	// 2. Bucket stats
	bucketData, err := httpGet(ctx, host, fmt.Sprintf("/pools/default/buckets/%s", cfg.Bucket), cfg.Username, cfg.Password)
	if err == nil {
		var bucket struct {
			BasicStats struct {
				ItemCount   uint64  `json:"itemCount"`
				DiskFetches float64 `json:"diskFetches"`
				OpsPerSec   float64 `json:"opsPerSec"`
			} `json:"basicStats"`
		}
		if err := json.Unmarshal(bucketData, &bucket); err == nil {
			result.TotalDocs = bucket.BasicStats.ItemCount
			result.DiskWriteQueue = bucket.BasicStats.DiskFetches
		}
	}

	// 3. Detailed bucket stats
	statsData, err := httpGet(ctx, host,
		fmt.Sprintf("/pools/default/buckets/%s/stats?zoom=minute", cfg.Bucket),
		cfg.Username, cfg.Password)
	if err == nil {
		var bs struct {
			Op struct {
				Samples map[string][]float64 `json:"samples"`
			} `json:"op"`
		}
		if err := json.Unmarshal(statsData, &bs); err == nil {
			result.ResidentRatio = lastSample(bs.Op.Samples["vb_active_resident_items_ratio"])
			result.CacheMissRate = lastSample(bs.Op.Samples["ep_cache_miss_rate"])
			result.DiskWriteQueue = lastSample(bs.Op.Samples["disk_write_queue"])
			result.DiskReadRate = lastSample(bs.Op.Samples["ep_bg_fetched"])
			if v := lastSample(bs.Op.Samples["ops"]); v > 0 {
				result.OpsPerSec = v
			}
			if v := lastSample(bs.Op.Samples["cmd_get"]); v > 0 {
				result.CmdGetPerSec = v
			}
			if v := lastSample(bs.Op.Samples["cmd_set"]); v > 0 {
				result.CmdSetPerSec = v
			}
		}
	}

	return result, nil
}

func httpGet(ctx context.Context, host, path, user, pass string) ([]byte, error) {
	url := fmt.Sprintf("http://%s%s", host, path)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(user, pass)
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d from %s", resp.StatusCode, path)
	}
	return io.ReadAll(resp.Body)
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

func lastSample(arr []float64) float64 {
	if len(arr) == 0 {
		return 0
	}
	return arr[len(arr)-1]
}
