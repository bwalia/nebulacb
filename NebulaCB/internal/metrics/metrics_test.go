package metrics

import (
	"sync"
	"testing"
	"time"

	"github.com/balinderwalia/nebulacb/internal/models"
)

func TestNewCollector(t *testing.T) {
	c := NewCollector()
	if c == nil {
		t.Fatal("expected non-nil collector")
	}
	if c.totalWrites.Load() != 0 {
		t.Error("expected zero initial writes")
	}
	if c.totalReads.Load() != 0 {
		t.Error("expected zero initial reads")
	}
}

func TestRecordWriteAndRead(t *testing.T) {
	c := NewCollector()

	c.RecordWrite(1.5)
	c.RecordWrite(2.5)
	c.RecordRead(0.5)

	if c.totalWrites.Load() != 2 {
		t.Errorf("expected 2 writes, got %d", c.totalWrites.Load())
	}
	if c.totalReads.Load() != 1 {
		t.Errorf("expected 1 read, got %d", c.totalReads.Load())
	}
}

func TestRecordError(t *testing.T) {
	c := NewCollector()
	c.RecordError()
	c.RecordError()
	if c.totalErrors.Load() != 2 {
		t.Errorf("expected 2 errors, got %d", c.totalErrors.Load())
	}
}

func TestAddAlert(t *testing.T) {
	c := NewCollector()
	c.AddAlert(models.Alert{
		ID:       "a1",
		Severity: "critical",
		Title:    "Test Alert",
	})

	alerts := c.GetAlerts()
	if len(alerts) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(alerts))
	}
	if alerts[0].Title != "Test Alert" {
		t.Errorf("expected 'Test Alert', got %s", alerts[0].Title)
	}
}

func TestAlertEviction(t *testing.T) {
	c := NewCollector()
	for i := 0; i < 1100; i++ {
		c.AddAlert(models.Alert{ID: "a"})
	}
	alerts := c.GetAlerts()
	if len(alerts) > 1000 {
		t.Errorf("expected alerts capped at 1000, got %d", len(alerts))
	}
}

func TestLatencyEviction(t *testing.T) {
	c := NewCollector()
	for i := 0; i < 110000; i++ {
		c.RecordWrite(float64(i))
	}
	c.mu.RLock()
	n := len(c.writeLatencies)
	c.mu.RUnlock()
	if n > 100001 {
		t.Errorf("expected write latencies capped, got %d", n)
	}
}

func TestGetDashboardState(t *testing.T) {
	c := NewCollector()
	c.RecordWrite(10.0)
	c.RecordWrite(20.0)
	c.RecordRead(5.0)
	c.RecordError()
	c.AddAlert(models.Alert{ID: "a1", Severity: "warning"})
	c.SetUpgradeStatus(models.UpgradeStatus{Phase: "upgrading"})
	c.SetXDCRStatus(models.XDCRStatus{State: "Running"})

	state := c.GetDashboardState()

	if state.StormMetrics.TotalWritten != 2 {
		t.Errorf("expected 2 total written, got %d", state.StormMetrics.TotalWritten)
	}
	if state.StormMetrics.TotalRead != 1 {
		t.Errorf("expected 1 total read, got %d", state.StormMetrics.TotalRead)
	}
	if state.StormMetrics.TotalErrors != 1 {
		t.Errorf("expected 1 error, got %d", state.StormMetrics.TotalErrors)
	}
	if len(state.Alerts) != 1 {
		t.Errorf("expected 1 alert, got %d", len(state.Alerts))
	}
	if state.UpgradeStatus.Phase != "upgrading" {
		t.Errorf("expected phase upgrading, got %s", state.UpgradeStatus.Phase)
	}
	if state.XDCRStatus.State != "Running" {
		t.Errorf("expected XDCR state Running, got %s", state.XDCRStatus.State)
	}
	if state.Timestamp.IsZero() {
		t.Error("expected non-zero timestamp")
	}
}

func TestSetClusterMetrics(t *testing.T) {
	c := NewCollector()
	c.SetSourceCluster(models.ClusterMetrics{ClusterName: "src", Healthy: true})
	c.SetTargetCluster(models.ClusterMetrics{ClusterName: "tgt", Healthy: false})
	c.SetAllClusterMetrics(map[string]models.ClusterMetrics{
		"dc1": {ClusterName: "dc1", Healthy: true},
	})

	state := c.GetDashboardState()
	if state.SourceCluster.ClusterName != "src" {
		t.Errorf("expected source cluster src, got %s", state.SourceCluster.ClusterName)
	}
	if state.TargetCluster.ClusterName != "tgt" {
		t.Errorf("expected target cluster tgt, got %s", state.TargetCluster.ClusterName)
	}
	if len(state.Clusters) != 1 {
		t.Errorf("expected 1 cluster in map, got %d", len(state.Clusters))
	}
}

func TestSetDataLossProof(t *testing.T) {
	c := NewCollector()
	c.SetDataLossProof(models.DataLossProof{
		IsConverged:       true,
		ZeroLossConfirmed: true,
		Timeline: []models.DocCountSample{
			{SourceDocs: 100, TargetDocs: 100},
		},
	})

	state := c.GetDashboardState()
	if !state.DataLossProof.IsConverged {
		t.Error("expected converged")
	}
	if len(state.DataLossProof.Timeline) != 1 {
		t.Errorf("expected 1 timeline entry, got %d", len(state.DataLossProof.Timeline))
	}
}

func TestConcurrentAccess(t *testing.T) {
	c := NewCollector()
	var wg sync.WaitGroup

	for i := 0; i < 100; i++ {
		wg.Add(3)
		go func() {
			defer wg.Done()
			c.RecordWrite(1.0)
		}()
		go func() {
			defer wg.Done()
			c.RecordRead(1.0)
		}()
		go func() {
			defer wg.Done()
			c.GetDashboardState()
		}()
	}

	wg.Wait()

	if c.totalWrites.Load() != 100 {
		t.Errorf("expected 100 writes, got %d", c.totalWrites.Load())
	}
	if c.totalReads.Load() != 100 {
		t.Errorf("expected 100 reads, got %d", c.totalReads.Load())
	}
}

func TestPercentile(t *testing.T) {
	data := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	p50 := percentile(data, 0.50)
	if p50 < 4 || p50 > 6 {
		t.Errorf("expected p50 around 5, got %f", p50)
	}

	p99 := percentile(data, 0.99)
	if p99 < 9 {
		t.Errorf("expected p99 >= 9, got %f", p99)
	}

	empty := percentile([]float64{}, 0.50)
	if empty != 0 {
		t.Errorf("expected 0 for empty slice, got %f", empty)
	}
}

func TestDashboardWritesPerSec(t *testing.T) {
	c := NewCollector()
	c.startTime = time.Now().Add(-10 * time.Second)
	for i := 0; i < 100; i++ {
		c.RecordWrite(1.0)
	}

	state := c.GetDashboardState()
	// ~10 writes/sec (100 writes over ~10 seconds)
	if state.StormMetrics.WritesPerSec < 5 || state.StormMetrics.WritesPerSec > 15 {
		t.Errorf("expected ~10 writes/sec, got %f", state.StormMetrics.WritesPerSec)
	}
}
