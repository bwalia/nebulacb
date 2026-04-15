package backup

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/balinderwalia/nebulacb/internal/metrics"
	"github.com/balinderwalia/nebulacb/internal/models"
)

// Manager handles backup and restore operations for Couchbase clusters.
type Manager struct {
	config    models.BackupConfig
	clusters  map[string]models.ClusterConfig
	collector *metrics.Collector

	mu            sync.RWMutex
	status        models.BackupStatus
	activeBackup  *models.BackupInfo
	activeRestore *models.RestoreInfo
	backups       []models.BackupInfo
}

// NewManager creates a new backup manager.
func NewManager(cfg models.BackupConfig, clusters map[string]models.ClusterConfig, collector *metrics.Collector) *Manager {
	return &Manager{
		config:    cfg,
		clusters:  clusters,
		collector: collector,
		backups:   make([]models.BackupInfo, 0),
		status: models.BackupStatus{
			Healthy: true,
		},
	}
}

// StartBackup initiates a backup for a cluster.
func (m *Manager) StartBackup(ctx context.Context, clusterName, backupType string, buckets []string) (*models.BackupInfo, error) {
	m.mu.Lock()
	if m.activeBackup != nil && m.activeBackup.Status == "running" {
		m.mu.Unlock()
		return nil, fmt.Errorf("backup already in progress for %s", m.activeBackup.ClusterName)
	}

	cfg, ok := m.clusters[clusterName]
	if !ok {
		m.mu.Unlock()
		return nil, fmt.Errorf("cluster %s not found", clusterName)
	}

	if len(buckets) == 0 {
		buckets = []string{cfg.Bucket}
	}

	backup := &models.BackupInfo{
		ID:          fmt.Sprintf("backup-%s-%d", clusterName, time.Now().UnixNano()),
		ClusterName: clusterName,
		Type:        backupType,
		Status:      "running",
		Buckets:     buckets,
		StartTime:   time.Now(),
		Repository:  m.config.Repository,
	}
	m.activeBackup = backup
	m.mu.Unlock()

	// Detach from the caller's context (typically an HTTP request context that
	// is cancelled as soon as the response is written) so the long-running
	// backup is not aborted the moment the API replies.
	runCtx, cancel := context.WithTimeout(context.Background(), 6*time.Hour)
	go func() {
		defer cancel()
		m.runBackup(runCtx, cfg, backup)
	}()

	log.Printf("[Backup] Started %s backup for cluster %s", backupType, clusterName)
	return backup, nil
}

func (m *Manager) runBackup(ctx context.Context, cfg models.ClusterConfig, backup *models.BackupInfo) {
	defer func() {
		m.mu.Lock()
		m.activeBackup = nil
		m.backups = append(m.backups, *backup)
		if len(m.backups) > 50 {
			m.backups = m.backups[len(m.backups)-50:]
		}
		m.status.LastBackup = backup
		m.status.RecentBackups = m.backups
		m.mu.Unlock()
	}()

	host := extractHost(cfg.Host)

	// Use cbbackupmgr if available, otherwise fall back to REST API
	if m.hasCBBackupMgr() {
		err := m.runCBBackupMgr(ctx, host, cfg, backup)
		if err != nil {
			backup.Status = "failed"
			backup.Error = err.Error()
			backup.EndTime = time.Now()
			backup.Duration = time.Since(backup.StartTime).Seconds()
			m.collector.AddAlert(models.Alert{
				ID:       fmt.Sprintf("backup-fail-%d", time.Now().Unix()),
				Severity: "critical",
				Category: "backup",
				Title:    "Backup Failed",
				Message:  fmt.Sprintf("Backup of %s failed: %s", backup.ClusterName, err.Error()),
				Source:   "backup",
				Timestamp: time.Now(),
			})
			return
		}
	} else {
		// REST-based backup via bucket export
		err := m.runRESTBackup(ctx, host, cfg, backup)
		if err != nil {
			backup.Status = "failed"
			backup.Error = err.Error()
			backup.EndTime = time.Now()
			backup.Duration = time.Since(backup.StartTime).Seconds()
			return
		}
	}

	backup.Status = "completed"
	backup.EndTime = time.Now()
	backup.Duration = time.Since(backup.StartTime).Seconds()

	log.Printf("[Backup] Completed %s backup of %s in %.1fs", backup.Type, backup.ClusterName, backup.Duration)
	m.collector.AddAlert(models.Alert{
		ID:        fmt.Sprintf("backup-ok-%d", time.Now().Unix()),
		Severity:  "info",
		Category:  "backup",
		Title:     "Backup Completed",
		Message:   fmt.Sprintf("%s backup of %s completed in %.1fs", backup.Type, backup.ClusterName, backup.Duration),
		Source:    "backup",
		Timestamp: time.Now(),
	})
}

func (m *Manager) hasCBBackupMgr() bool {
	_, err := exec.LookPath("cbbackupmgr")
	return err == nil
}

func (m *Manager) runCBBackupMgr(ctx context.Context, host string, cfg models.ClusterConfig, backup *models.BackupInfo) error {
	repo := m.config.Repository
	if repo == "" {
		repo = "/tmp/nebulacb-backups"
	}

	// Configure repository
	configArgs := []string{"config", "--archive", repo, "--repo", backup.ID}
	cmd := exec.CommandContext(ctx, "cbbackupmgr", configArgs...)
	if out, err := cmd.CombinedOutput(); err != nil {
		log.Printf("[Backup] cbbackupmgr config: %s", string(out))
		// Repository may already exist, continue
	}

	// Run backup
	backupArgs := []string{
		"backup", "--archive", repo, "--repo", backup.ID,
		"--cluster", fmt.Sprintf("couchbase://%s", strings.Split(host, ":")[0]),
		"--username", cfg.Username, "--password", cfg.Password,
	}
	if m.config.Compression {
		backupArgs = append(backupArgs, "--value-compression", "compressed")
	}

	cmd = exec.CommandContext(ctx, "cbbackupmgr", backupArgs...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("cbbackupmgr backup: %s: %w", string(out), err)
	}

	log.Printf("[Backup] cbbackupmgr output: %s", string(out))
	return nil
}

func (m *Manager) runRESTBackup(ctx context.Context, host string, cfg models.ClusterConfig, backup *models.BackupInfo) error {
	// Trigger backup via REST API (Enterprise only)
	url := fmt.Sprintf("http://%s/controller/startBackup", host)
	req, err := http.NewRequestWithContext(ctx, "POST", url, nil)
	if err != nil {
		return err
	}
	req.SetBasicAuth(cfg.Username, cfg.Password)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		// Fallback: use cbbackupmgr if REST not available (community edition)
		return fmt.Errorf("REST backup not available (may be Community Edition): %w", err)
	}
	defer resp.Body.Close()
	io.ReadAll(resp.Body)

	if resp.StatusCode >= 400 {
		return fmt.Errorf("backup API returned HTTP %d", resp.StatusCode)
	}
	return nil
}

// StartRestore initiates a restore operation.
func (m *Manager) StartRestore(ctx context.Context, backupID, targetCluster string, buckets []string) (*models.RestoreInfo, error) {
	m.mu.Lock()
	if m.activeRestore != nil && m.activeRestore.Status == "running" {
		m.mu.Unlock()
		return nil, fmt.Errorf("restore already in progress")
	}

	cfg, ok := m.clusters[targetCluster]
	if !ok {
		m.mu.Unlock()
		return nil, fmt.Errorf("target cluster %s not found", targetCluster)
	}

	restore := &models.RestoreInfo{
		ID:            fmt.Sprintf("restore-%d", time.Now().UnixNano()),
		BackupID:      backupID,
		TargetCluster: targetCluster,
		Status:        "running",
		Progress:      0,
		Buckets:       buckets,
		StartTime:     time.Now(),
	}
	m.activeRestore = restore
	m.status.ActiveRestore = restore
	m.mu.Unlock()

	// Detach from the caller's context so the restore outlives the HTTP request.
	runCtx, cancel := context.WithTimeout(context.Background(), 6*time.Hour)
	go func() {
		defer cancel()
		m.runRestore(runCtx, cfg, restore)
	}()

	log.Printf("[Backup] Started restore of %s to cluster %s", backupID, targetCluster)
	return restore, nil
}

func (m *Manager) runRestore(ctx context.Context, cfg models.ClusterConfig, restore *models.RestoreInfo) {
	defer func() {
		m.mu.Lock()
		m.activeRestore = nil
		m.status.ActiveRestore = nil
		m.mu.Unlock()
	}()

	host := extractHost(cfg.Host)
	repo := m.config.Repository
	if repo == "" {
		repo = "/tmp/nebulacb-backups"
	}

	if m.hasCBBackupMgr() {
		args := []string{
			"restore", "--archive", repo, "--repo", restore.BackupID,
			"--cluster", fmt.Sprintf("couchbase://%s", strings.Split(host, ":")[0]),
			"--username", cfg.Username, "--password", cfg.Password,
			"--force-updates",
		}

		cmd := exec.CommandContext(ctx, "cbbackupmgr", args...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			restore.Status = "failed"
			restore.Error = fmt.Sprintf("cbbackupmgr restore: %s: %v", string(out), err)
			return
		}
		log.Printf("[Backup] cbbackupmgr restore output: %s", string(out))
	}

	restore.Status = "completed"
	restore.Progress = 100

	m.collector.AddAlert(models.Alert{
		ID:        fmt.Sprintf("restore-ok-%d", time.Now().Unix()),
		Severity:  "info",
		Category:  "backup",
		Title:     "Restore Completed",
		Message:   fmt.Sprintf("Restore of %s to %s completed", restore.BackupID, restore.TargetCluster),
		Source:    "backup",
		Timestamp: time.Now(),
	})
}

// GetStatus returns the current backup status.
func (m *Manager) GetStatus() models.BackupStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	status := m.status
	status.Timestamp = time.Now()
	return status
}

// GetBackups returns all backup records.
func (m *Manager) GetBackups() []models.BackupInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]models.BackupInfo, len(m.backups))
	copy(result, m.backups)
	return result
}

// ListRepositoryBackups lists backups in the repository via cbbackupmgr.
func (m *Manager) ListRepositoryBackups(ctx context.Context) ([]models.BackupInfo, error) {
	repo := m.config.Repository
	if repo == "" {
		repo = "/tmp/nebulacb-backups"
	}

	if !m.hasCBBackupMgr() {
		return m.GetBackups(), nil
	}

	args := []string{"list", "--archive", repo, "--json"}
	cmd := exec.CommandContext(ctx, "cbbackupmgr", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("cbbackupmgr list: %s: %w", string(out), err)
	}

	var result []models.BackupInfo
	if err := json.Unmarshal(out, &result); err != nil {
		// Fallback to in-memory list
		return m.GetBackups(), nil
	}
	return result, nil
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
