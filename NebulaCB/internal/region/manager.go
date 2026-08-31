package region

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/balinderwalia/nebulacb/internal/metrics"
	"github.com/balinderwalia/nebulacb/internal/models"
)

// Manager handles multi-region cluster topology and cross-region replication.
type Manager struct {
	regions   []models.RegionConfig
	clusters  map[string]models.ClusterConfig
	collector *metrics.Collector

	mu               sync.RWMutex
	regionStatuses   []models.RegionStatus
	replications     []models.RegionReplication
	cancel           context.CancelFunc
}

// NewManager creates a new region manager.
func NewManager(regions []models.RegionConfig, clusters map[string]models.ClusterConfig, collector *metrics.Collector) *Manager {
	return &Manager{
		regions:        regions,
		clusters:       clusters,
		collector:      collector,
		regionStatuses: make([]models.RegionStatus, 0),
		replications:   make([]models.RegionReplication, 0),
	}
}

// Start begins regional monitoring.
func (m *Manager) Start(ctx context.Context) {
	if len(m.regions) == 0 {
		log.Println("[Region] No regions configured, skipping multi-region management")
		return
	}

	ctx, m.cancel = context.WithCancel(ctx)
	log.Printf("[Region] Multi-region management started (%d regions)", len(m.regions))
	for _, r := range m.regions {
		primary := ""
		if r.Primary {
			primary = " [PRIMARY]"
		}
		log.Printf("[Region]   %s (%s) - %d clusters%s", r.Name, r.Provider, len(r.Clusters), primary)
	}

	go m.monitorLoop(ctx)
}

// Stop halts regional monitoring.
func (m *Manager) Stop() {
	if m.cancel != nil {
		m.cancel()
	}
}

func (m *Manager) monitorLoop(ctx context.Context) {
	m.updateRegionStatuses()

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.updateRegionStatuses()
		}
	}
}

func (m *Manager) updateRegionStatuses() {
	// Get current cluster metrics from the collector
	state := m.collector.GetDashboardState()

	var statuses []models.RegionStatus

	for _, region := range m.regions {
		status := models.RegionStatus{
			Name:        region.Name,
			DisplayName: region.DisplayName,
			Provider:    region.Provider,
			Primary:     region.Primary,
			Status:      "healthy",
			Timestamp:   time.Now(),
		}

		for _, clusterName := range region.Clusters {
			if cm, ok := state.Clusters[clusterName]; ok {
				status.ClusterCount++
				if cm.Healthy {
					status.HealthyClusters++
				}
				status.TotalDocs += cm.TotalDocs
				status.OpsPerSec += cm.OpsPerSec
			}
		}

		if status.ClusterCount > 0 && status.HealthyClusters == 0 {
			status.Status = "down"
		} else if status.HealthyClusters < status.ClusterCount {
			status.Status = "degraded"
		}

		statuses = append(statuses, status)
	}

	m.mu.Lock()
	m.regionStatuses = statuses
	m.mu.Unlock()

	// Check for cross-region issues
	for _, status := range statuses {
		if status.Status == "down" {
			m.collector.AddAlert(models.Alert{
				ID:        "region-down-" + status.Name,
				Severity:  "critical",
				Category:  "failover",
				Title:     "Region Down",
				Message:   status.DisplayName + " (" + status.Name + ") is completely down",
				Source:    "region",
				Timestamp: time.Now(),
			})
		}
	}
}

// GetRegionStatuses returns the current status of all regions.
func (m *Manager) GetRegionStatuses() []models.RegionStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]models.RegionStatus, len(m.regionStatuses))
	copy(result, m.regionStatuses)
	return result
}

// GetRegionStatus returns the status of a specific region.
func (m *Manager) GetRegionStatus(name string) *models.RegionStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, s := range m.regionStatuses {
		if s.Name == name {
			result := s
			return &result
		}
	}
	return nil
}

// GetReplications returns cross-region replication statuses.
func (m *Manager) GetReplications() []models.RegionReplication {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]models.RegionReplication, len(m.replications))
	copy(result, m.replications)
	return result
}

// GetRegionConfigs returns the region configurations.
func (m *Manager) GetRegionConfigs() []models.RegionConfig {
	return m.regions
}

// GetClustersInRegion returns clusters belonging to a region.
func (m *Manager) GetClustersInRegion(regionName string) []models.ClusterConfig {
	for _, region := range m.regions {
		if region.Name == regionName {
			var clusters []models.ClusterConfig
			for _, clusterName := range region.Clusters {
				if cfg, ok := m.clusters[clusterName]; ok {
					clusters = append(clusters, cfg)
				}
			}
			return clusters
		}
	}
	return nil
}

// PromoteRegion promotes a region to primary.
func (m *Manager) PromoteRegion(regionName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for i := range m.regions {
		if m.regions[i].Name == regionName {
			m.regions[i].Primary = true
		} else {
			m.regions[i].Primary = false
		}
	}

	log.Printf("[Region] Promoted region %s to primary", regionName)
	m.collector.AddAlert(models.Alert{
		ID:        "region-promote-" + regionName,
		Severity:  "info",
		Category:  "failover",
		Title:     "Region Promoted",
		Message:   regionName + " promoted to primary region",
		Source:    "region",
		Timestamp: time.Now(),
	})
	return nil
}
