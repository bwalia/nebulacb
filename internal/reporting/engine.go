package reporting

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/balinderwalia/nebulacb/internal/metrics"
	"github.com/balinderwalia/nebulacb/internal/models"
)

// Engine generates post-run reports.
type Engine struct {
	collector *metrics.Collector
	outputDir string

	mu      sync.RWMutex
	reports []models.Report
}

// NewEngine creates a new reporting engine.
func NewEngine(collector *metrics.Collector, outputDir string) *Engine {
	os.MkdirAll(outputDir, 0755)
	return &Engine{
		collector: collector,
		outputDir: outputDir,
		reports:   make([]models.Report, 0),
	}
}

// GenerateReport creates a comprehensive report from current state.
func (e *Engine) GenerateReport(ctx context.Context) (models.Report, error) {
	log.Println("[Report] Generating report...")

	state := e.collector.GetDashboardState()

	report := models.Report{
		ID:        fmt.Sprintf("report-%d", time.Now().Unix()),
		RunID:     state.IntegrityResult.RunID,
		StartTime: state.UpgradeStatus.StartTime,
		EndTime:   time.Now(),
	}

	// Upgrade success
	report.UpgradeSuccess = state.UpgradeStatus.Phase == "completed"

	// Data loss summary
	report.DataLossSummary = state.IntegrityResult

	// XDCR gap timeline from alerts
	for _, alert := range state.Alerts {
		if alert.Category == "replication" {
			report.XDCRGapTimeline = append(report.XDCRGapTimeline, models.XDCRStatus{
				State:     alert.Title,
				Timestamp: alert.Timestamp,
			})
		}
	}

	// Root Cause Analysis
	report.RootCauseAnalysis = e.generateRCA(state)

	// Recommendations
	report.Recommendations = e.generateRecommendations(state)

	// Save report
	e.mu.Lock()
	e.reports = append(e.reports, report)
	e.mu.Unlock()

	// Export to JSON
	if err := e.exportJSON(report); err != nil {
		log.Printf("[Report] JSON export error: %v", err)
	}

	log.Printf("[Report] Report %s generated", report.ID)
	return report, nil
}

// GetReports returns all generated reports.
func (e *Engine) GetReports() []models.Report {
	e.mu.RLock()
	defer e.mu.RUnlock()
	result := make([]models.Report, len(e.reports))
	copy(result, e.reports)
	return result
}

func (e *Engine) generateRCA(state models.DashboardState) string {
	rca := ""

	if state.IntegrityResult.MissingCount > 0 {
		rca += fmt.Sprintf("DATA LOSS: %d documents missing from target. ", state.IntegrityResult.MissingCount)
	}

	if state.IntegrityResult.HashMismatches > 0 {
		rca += fmt.Sprintf("INCONSISTENCY: %d documents have hash mismatches. ", state.IntegrityResult.HashMismatches)
	}

	if state.XDCRStatus.TopologyChange {
		rca += "XDCR topology change detected — GOXDCR replication delay expected (~5 min). "
	}

	if state.XDCRStatus.State == "Paused" {
		rca += "XDCR pipeline was paused during observation period. "
	}

	if state.UpgradeStatus.Phase == "failed" {
		rca += fmt.Sprintf("UPGRADE FAILURE: %v. ", state.UpgradeStatus.Errors)
	}

	if rca == "" {
		rca = "No issues detected. Upgrade and replication completed successfully with zero data loss."
	}

	return rca
}

func (e *Engine) generateRecommendations(state models.DashboardState) []string {
	var recs []string

	if state.IntegrityResult.MissingCount > 0 {
		recs = append(recs, "Investigate missing documents — check XDCR pipeline state during upgrade window")
		recs = append(recs, "Consider reducing write load during upgrade to minimize replication backlog")
	}

	if state.XDCRStatus.ChangesLeft > 10000 {
		recs = append(recs, fmt.Sprintf("High replication backlog (%d changes). Consider increasing XDCR worker count", state.XDCRStatus.ChangesLeft))
	}

	nodeCount := len(state.SourceCluster.Nodes)
	if nodeCount < 3 {
		recs = append(recs, "WARNING: Cluster has fewer than 3 nodes. Add nodes before upgrade for safety")
	}

	if state.StormMetrics.LatencyP99 > 100 {
		recs = append(recs, "P99 latency exceeds 100ms — consider scaling cluster or reducing load before upgrade")
	}

	if state.UpgradeStatus.Phase == "completed" && state.IntegrityResult.MissingCount == 0 {
		recs = append(recs, "Upgrade completed with zero data loss. Safe to proceed with production rollout")
	}

	if len(recs) == 0 {
		recs = append(recs, "System healthy. No action required.")
	}

	return recs
}

func (e *Engine) exportJSON(report models.Report) error {
	filename := filepath.Join(e.outputDir, report.ID+".json")
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filename, data, 0644)
}
