package failover

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

// Manager handles High Availability and automatic failover for Couchbase clusters.
type Manager struct {
	config    models.FailoverConfig
	clusters  map[string]models.ClusterConfig
	collector *metrics.Collector

	mu              sync.RWMutex
	status          models.FailoverStatus
	failoverHistory []models.FailoverEvent
	nodeHealthCache map[string]time.Time // node_host -> last_healthy_time
	cancel          context.CancelFunc
}

// NewManager creates a new failover manager.
func NewManager(cfg models.FailoverConfig, clusters map[string]models.ClusterConfig, collector *metrics.Collector) *Manager {
	clusterStates := make(map[string]string, len(clusters))
	for name, c := range clusters {
		state := "active"
		if c.Role == "standby" {
			state = "standby"
		}
		clusterStates[name] = state
	}

	return &Manager{
		config:    cfg,
		clusters:  clusters,
		collector: collector,
		status: models.FailoverStatus{
			Mode:                "active-passive",
			AutoFailoverEnabled: cfg.AutoFailover,
			ClusterStates:       clusterStates,
			Timestamp:           time.Now(),
		},
		failoverHistory: make([]models.FailoverEvent, 0),
		nodeHealthCache: make(map[string]time.Time),
	}
}

// Start begins the HA monitoring loop.
func (m *Manager) Start(ctx context.Context) {
	if !m.config.Enabled {
		log.Println("[Failover] HA/Failover management disabled")
		return
	}

	ctx, m.cancel = context.WithCancel(ctx)
	interval := m.config.HealthCheckInterval
	if interval == 0 {
		interval = 10 * time.Second
	}

	log.Printf("[Failover] HA monitoring started (auto_failover=%v, interval=%s, timeout=%s)",
		m.config.AutoFailover, interval, m.config.FailoverTimeout)

	go m.monitorLoop(ctx, interval)
}

// Stop halts HA monitoring.
func (m *Manager) Stop() {
	if m.cancel != nil {
		m.cancel()
	}
}

func (m *Manager) monitorLoop(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.checkHealth(ctx)
		}
	}
}

func (m *Manager) checkHealth(ctx context.Context) {
	m.mu.Lock()
	m.status.LastHealthCheck = time.Now()
	m.mu.Unlock()

	for name, cfg := range m.clusters {
		if cfg.Host == "" {
			continue
		}

		healthy, nodes := m.checkClusterHealth(ctx, cfg)

		m.mu.Lock()
		if healthy {
			m.nodeHealthCache[name] = time.Now()
			if m.status.ClusterStates[name] == "failed" {
				m.status.ClusterStates[name] = "recovering"
				log.Printf("[Failover] Cluster %s recovering", name)
			}
		} else {
			lastHealthy, exists := m.nodeHealthCache[name]
			if !exists {
				lastHealthy = time.Now()
				m.nodeHealthCache[name] = lastHealthy
			}

			downDuration := time.Since(lastHealthy)
			timeout := m.config.FailoverTimeout
			if timeout == 0 {
				timeout = 120 * time.Second
			}

			if downDuration > timeout && m.status.ClusterStates[name] != "failed" {
				m.status.ClusterStates[name] = "failed"
				m.mu.Unlock()

				log.Printf("[Failover] CRITICAL: Cluster %s has been down for %s (threshold: %s)", name, downDuration, timeout)

				m.collector.AddAlert(models.Alert{
					ID:        fmt.Sprintf("cluster-down-%s-%d", name, time.Now().Unix()),
					Severity:  "critical",
					Category:  "failover",
					Title:     "Cluster Down - Failover Required",
					Message:   fmt.Sprintf("Cluster %s has been unreachable for %s", name, downDuration.Round(time.Second)),
					Source:    "failover",
					Timestamp: time.Now(),
				})

				if m.config.AutoFailover {
					m.triggerAutoFailover(ctx, name, nodes)
				}
				continue
			}
		}
		m.mu.Unlock()
	}
}

func (m *Manager) checkClusterHealth(ctx context.Context, cfg models.ClusterConfig) (bool, []string) {
	host := extractHost(cfg.Host)
	url := fmt.Sprintf("http://%s/pools/default", host)

	reqCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, "GET", url, nil)
	if err != nil {
		return false, nil
	}
	req.SetBasicAuth(cfg.Username, cfg.Password)

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false, nil
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return false, nil
	}

	var pool struct {
		Nodes []struct {
			Hostname          string `json:"hostname"`
			Status            string `json:"status"`
			ClusterMembership string `json:"clusterMembership"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(body, &pool); err != nil {
		return false, nil
	}

	var unhealthyNodes []string
	allHealthy := true
	for _, n := range pool.Nodes {
		if n.Status != "healthy" || n.ClusterMembership != "active" {
			allHealthy = false
			unhealthyNodes = append(unhealthyNodes, n.Hostname)
		}
	}

	return allHealthy, unhealthyNodes
}

func (m *Manager) triggerAutoFailover(ctx context.Context, failedCluster string, failedNodes []string) {
	m.mu.RLock()
	maxFailovers := m.config.MaxAutoFailovers
	if maxFailovers == 0 {
		maxFailovers = 1
	}

	// Count recent auto-failovers
	recentCount := 0
	for _, ev := range m.failoverHistory {
		if ev.Type == "auto" && time.Since(ev.Timestamp) < 24*time.Hour {
			recentCount++
		}
	}
	m.mu.RUnlock()

	if recentCount >= maxFailovers {
		log.Printf("[Failover] Auto-failover limit reached (%d/%d in 24h), skipping", recentCount, maxFailovers)
		m.collector.AddAlert(models.Alert{
			ID:        fmt.Sprintf("failover-limit-%d", time.Now().Unix()),
			Severity:  "warning",
			Category:  "failover",
			Title:     "Auto-Failover Limit Reached",
			Message:   fmt.Sprintf("Max auto-failovers (%d) in 24h reached. Manual intervention required.", maxFailovers),
			Source:    "failover",
			Timestamp: time.Now(),
		})
		return
	}

	// Find a target cluster for failover
	targetCluster := m.findFailoverTarget(failedCluster)
	if targetCluster == "" {
		log.Printf("[Failover] No suitable failover target found for %s", failedCluster)
		return
	}

	event := models.FailoverEvent{
		ID:            fmt.Sprintf("fo-%d", time.Now().UnixNano()),
		Type:          "auto",
		SourceCluster: failedCluster,
		TargetCluster: targetCluster,
		Reason:        "cluster health check failure",
		NodesAffected: failedNodes,
		Status:        "in_progress",
		Timestamp:     time.Now(),
	}

	log.Printf("[Failover] AUTO-FAILOVER: %s -> %s (affected nodes: %v)", failedCluster, targetCluster, failedNodes)

	// Attempt node-level failover via REST API
	cfg := m.clusters[failedCluster]
	err := m.executeNodeFailover(ctx, cfg, failedNodes)

	event.Duration = time.Since(event.Timestamp).Seconds()
	if err != nil {
		event.Status = "failed"
		event.DataLoss = false
		log.Printf("[Failover] Auto-failover failed: %v", err)
	} else {
		event.Status = "completed"
		log.Printf("[Failover] Auto-failover completed: %s -> %s", failedCluster, targetCluster)
	}

	m.mu.Lock()
	m.failoverHistory = append(m.failoverHistory, event)
	m.status.FailoverHistory = m.failoverHistory
	m.mu.Unlock()

	m.collector.AddAlert(models.Alert{
		ID:        fmt.Sprintf("failover-exec-%d", time.Now().Unix()),
		Severity:  "critical",
		Category:  "failover",
		Title:     fmt.Sprintf("Auto-Failover %s", event.Status),
		Message:   fmt.Sprintf("Failover from %s to %s: %s (%.1fs)", failedCluster, targetCluster, event.Status, event.Duration),
		Source:    "failover",
		Timestamp: time.Now(),
	})
}

func (m *Manager) executeNodeFailover(ctx context.Context, cfg models.ClusterConfig, nodes []string) error {
	host := extractHost(cfg.Host)

	for _, node := range nodes {
		url := fmt.Sprintf("http://%s/controller/failOver", host)
		body := strings.NewReader(fmt.Sprintf("otpNode=ns_1@%s", strings.Split(node, ":")[0]))

		req, err := http.NewRequestWithContext(ctx, "POST", url, body)
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.SetBasicAuth(cfg.Username, cfg.Password)

		client := &http.Client{Timeout: 30 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("failover node %s: %w", node, err)
		}
		resp.Body.Close()

		if resp.StatusCode >= 400 {
			return fmt.Errorf("failover node %s: HTTP %d", node, resp.StatusCode)
		}
		log.Printf("[Failover] Node %s failed over successfully", node)
	}
	return nil
}

func (m *Manager) findFailoverTarget(failedCluster string) string {
	// Prefer standby clusters in the preferred region
	for name, state := range m.status.ClusterStates {
		if name != failedCluster && state == "standby" {
			return name
		}
	}
	// Fallback to any active cluster
	for name, state := range m.status.ClusterStates {
		if name != failedCluster && state == "active" {
			return name
		}
	}
	return ""
}

// ManualFailover triggers a manual failover.
func (m *Manager) ManualFailover(ctx context.Context, sourceCluster, targetCluster string) (*models.FailoverEvent, error) {
	m.mu.RLock()
	srcState := m.status.ClusterStates[sourceCluster]
	m.mu.RUnlock()

	if srcState == "" {
		return nil, fmt.Errorf("source cluster %s not found", sourceCluster)
	}

	event := models.FailoverEvent{
		ID:            fmt.Sprintf("fo-%d", time.Now().UnixNano()),
		Type:          "manual",
		SourceCluster: sourceCluster,
		TargetCluster: targetCluster,
		Reason:        "manual failover initiated by operator",
		Status:        "in_progress",
		Timestamp:     time.Now(),
	}

	log.Printf("[Failover] MANUAL FAILOVER: %s -> %s", sourceCluster, targetCluster)

	// Promote standby to active
	m.mu.Lock()
	m.status.ClusterStates[sourceCluster] = "failed"
	m.status.ClusterStates[targetCluster] = "active"
	m.status.PrimaryCluster = targetCluster
	m.status.StandbyCluster = sourceCluster
	m.mu.Unlock()

	event.Status = "completed"
	event.Duration = time.Since(event.Timestamp).Seconds()

	m.mu.Lock()
	m.failoverHistory = append(m.failoverHistory, event)
	m.status.FailoverHistory = m.failoverHistory
	m.mu.Unlock()

	return &event, nil
}

// GracefulFailover performs a graceful failover with data drain.
func (m *Manager) GracefulFailover(ctx context.Context, clusterName string, nodes []string) (*models.FailoverEvent, error) {
	cfg, ok := m.clusters[clusterName]
	if !ok {
		return nil, fmt.Errorf("cluster %s not found", clusterName)
	}

	host := extractHost(cfg.Host)
	event := models.FailoverEvent{
		ID:            fmt.Sprintf("fo-%d", time.Now().UnixNano()),
		Type:          "graceful",
		SourceCluster: clusterName,
		NodesAffected: nodes,
		Reason:        "graceful failover requested",
		Status:        "in_progress",
		Timestamp:     time.Now(),
	}

	for _, node := range nodes {
		url := fmt.Sprintf("http://%s/controller/startGracefulFailover", host)
		body := strings.NewReader(fmt.Sprintf("otpNode=ns_1@%s", strings.Split(node, ":")[0]))

		req, err := http.NewRequestWithContext(ctx, "POST", url, body)
		if err != nil {
			event.Status = "failed"
			return &event, err
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.SetBasicAuth(cfg.Username, cfg.Password)

		client := &http.Client{Timeout: 60 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			event.Status = "failed"
			return &event, fmt.Errorf("graceful failover %s: %w", node, err)
		}
		resp.Body.Close()

		if resp.StatusCode >= 400 {
			event.Status = "failed"
			return &event, fmt.Errorf("graceful failover %s: HTTP %d", node, resp.StatusCode)
		}
	}

	event.Status = "completed"
	event.Duration = time.Since(event.Timestamp).Seconds()

	m.mu.Lock()
	m.failoverHistory = append(m.failoverHistory, event)
	m.status.FailoverHistory = m.failoverHistory
	m.mu.Unlock()

	return &event, nil
}

// GetStatus returns the current failover status.
func (m *Manager) GetStatus() models.FailoverStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	status := m.status
	status.Timestamp = time.Now()
	return status
}

// GetHistory returns the failover event history.
func (m *Manager) GetHistory() []models.FailoverEvent {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]models.FailoverEvent, len(m.failoverHistory))
	copy(result, m.failoverHistory)
	return result
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
