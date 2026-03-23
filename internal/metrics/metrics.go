package metrics

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/balinderwalia/nebulacb/internal/models"
)

// Collector gathers and exposes metrics for all NebulaCB components.
type Collector struct {
	mu sync.RWMutex

	// Storm metrics
	totalWrites   atomic.Uint64
	totalReads    atomic.Uint64
	totalErrors   atomic.Uint64
	writeLatencies []float64
	readLatencies  []float64

	// Alert store
	alerts []models.Alert

	// Snapshot caches
	lastStorm          models.StormMetrics
	lastCluster        models.ClusterHealth
	lastSourceCluster  models.ClusterMetrics
	lastTargetCluster  models.ClusterMetrics
	allClusterMetrics  map[string]models.ClusterMetrics
	lastXDCR           models.XDCRStatus
	lastIntegrity      models.IntegrityResult
	lastUpgrade        models.UpgradeStatus
	lastProof          models.DataLossProof

	startTime time.Time
}

// NewCollector creates a new metrics collector.
func NewCollector() *Collector {
	return &Collector{
		writeLatencies: make([]float64, 0, 10000),
		readLatencies:  make([]float64, 0, 10000),
		alerts:         make([]models.Alert, 0),
		startTime:      time.Now(),
	}
}

// RecordWrite records a write operation.
func (c *Collector) RecordWrite(latencyMs float64) {
	c.totalWrites.Add(1)
	c.mu.Lock()
	c.writeLatencies = append(c.writeLatencies, latencyMs)
	if len(c.writeLatencies) > 100000 {
		c.writeLatencies = c.writeLatencies[50000:]
	}
	c.mu.Unlock()
}

// RecordRead records a read operation.
func (c *Collector) RecordRead(latencyMs float64) {
	c.totalReads.Add(1)
	c.mu.Lock()
	c.readLatencies = append(c.readLatencies, latencyMs)
	if len(c.readLatencies) > 100000 {
		c.readLatencies = c.readLatencies[50000:]
	}
	c.mu.Unlock()
}

// RecordError records an operation error.
func (c *Collector) RecordError() {
	c.totalErrors.Add(1)
}

// AddAlert adds a new alert.
func (c *Collector) AddAlert(alert models.Alert) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.alerts = append(c.alerts, alert)
	if len(c.alerts) > 1000 {
		c.alerts = c.alerts[500:]
	}
}

// SetClusterHealth updates the cluster health snapshot.
func (c *Collector) SetClusterHealth(h models.ClusterHealth) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastCluster = h
}

// SetXDCRStatus updates the XDCR status snapshot.
func (c *Collector) SetXDCRStatus(x models.XDCRStatus) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastXDCR = x
}

// SetIntegrityResult updates the integrity result snapshot.
func (c *Collector) SetIntegrityResult(r models.IntegrityResult) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastIntegrity = r
}

// SetUpgradeStatus updates the upgrade status snapshot.
func (c *Collector) SetUpgradeStatus(u models.UpgradeStatus) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastUpgrade = u
}

// SetSourceCluster updates the source cluster metrics snapshot.
func (c *Collector) SetSourceCluster(m models.ClusterMetrics) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastSourceCluster = m
}

// SetTargetCluster updates the target cluster metrics snapshot.
func (c *Collector) SetTargetCluster(m models.ClusterMetrics) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastTargetCluster = m
}

// SetDataLossProof updates the data loss proof snapshot.
func (c *Collector) SetDataLossProof(p models.DataLossProof) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastProof = p
}

// SetAllClusterMetrics updates the full multi-cluster metrics map.
func (c *Collector) SetAllClusterMetrics(m map[string]models.ClusterMetrics) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.allClusterMetrics = m
}

// GetDashboardState computes the full dashboard state.
func (c *Collector) GetDashboardState() models.DashboardState {
	c.mu.RLock()
	defer c.mu.RUnlock()

	storm := models.StormMetrics{
		TotalWritten:      c.totalWrites.Load(),
		TotalRead:         c.totalReads.Load(),
		TotalErrors:       c.totalErrors.Load(),
		ActiveConnections: 0,
		Timestamp:         time.Now(),
	}

	elapsed := time.Since(c.startTime).Seconds()
	if elapsed > 0 {
		storm.WritesPerSec = float64(storm.TotalWritten) / elapsed
		storm.ReadsPerSec = float64(storm.TotalRead) / elapsed
	}

	if len(c.writeLatencies) > 0 {
		storm.LatencyP50 = percentile(c.writeLatencies, 0.50)
		storm.LatencyP95 = percentile(c.writeLatencies, 0.95)
		storm.LatencyP99 = percentile(c.writeLatencies, 0.99)
	}

	alertsCopy := make([]models.Alert, len(c.alerts))
	copy(alertsCopy, c.alerts)

	proofCopy := c.lastProof
	proofCopy.Timeline = make([]models.DocCountSample, len(c.lastProof.Timeline))
	copy(proofCopy.Timeline, c.lastProof.Timeline)

	// Copy all cluster metrics
	clustersCopy := make(map[string]models.ClusterMetrics, len(c.allClusterMetrics))
	for k, v := range c.allClusterMetrics {
		clustersCopy[k] = v
	}

	return models.DashboardState{
		Clusters:        clustersCopy,
		SourceCluster:   c.lastSourceCluster,
		TargetCluster:   c.lastTargetCluster,
		XDCRStatus:      c.lastXDCR,
		DataLossProof:   proofCopy,
		IntegrityResult: c.lastIntegrity,
		StormMetrics:    storm,
		UpgradeStatus:   c.lastUpgrade,
		Alerts:          alertsCopy,
		Timestamp:       time.Now(),
	}
}

// GetAlerts returns recent alerts.
func (c *Collector) GetAlerts() []models.Alert {
	c.mu.RLock()
	defer c.mu.RUnlock()
	result := make([]models.Alert, len(c.alerts))
	copy(result, c.alerts)
	return result
}

// percentile computes an approximate percentile from an unsorted slice.
func percentile(data []float64, p float64) float64 {
	if len(data) == 0 {
		return 0
	}
	// Simple approach: sort a copy
	sorted := make([]float64, len(data))
	copy(sorted, data)
	sortFloat64s(sorted)
	idx := int(float64(len(sorted)-1) * p)
	return sorted[idx]
}

func sortFloat64s(a []float64) {
	// Simple insertion sort for bounded slices — in production use sort.Float64s
	for i := 1; i < len(a); i++ {
		key := a[i]
		j := i - 1
		for j >= 0 && a[j] > key {
			a[j+1] = a[j]
			j--
		}
		a[j+1] = key
	}
}
