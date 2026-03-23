package orchestrator

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/balinderwalia/nebulacb/internal/metrics"
	"github.com/balinderwalia/nebulacb/internal/models"
	"github.com/balinderwalia/nebulacb/pkg/kubernetes"
)

// Orchestrator manages Couchbase cluster upgrades.
type Orchestrator struct {
	config    models.UpgradeConfig
	k8s       *kubernetes.K8sClient
	collector *metrics.Collector

	mu     sync.RWMutex
	status models.UpgradeStatus
	cancel context.CancelFunc
}

// NewOrchestrator creates a new Upgrade Orchestrator.
func NewOrchestrator(cfg models.UpgradeConfig, k8s *kubernetes.K8sClient, collector *metrics.Collector) *Orchestrator {
	return &Orchestrator{
		config:    cfg,
		k8s:       k8s,
		collector: collector,
		status: models.UpgradeStatus{
			Phase:         "pending",
			SourceVersion: cfg.SourceVersion,
			TargetVersion: cfg.TargetVersion,
		},
	}
}

// Start begins the upgrade process.
func (o *Orchestrator) Start(ctx context.Context) error {
	o.mu.Lock()
	if o.status.Phase != "pending" && o.status.Phase != "failed" && o.status.Phase != "aborted" {
		o.mu.Unlock()
		return fmt.Errorf("upgrade already in progress: %s", o.status.Phase)
	}
	ctx, o.cancel = context.WithCancel(ctx)
	o.status.Phase = "pre-check"
	o.status.StartTime = time.Now()
	o.status.Errors = nil
	o.mu.Unlock()

	go o.run(ctx)
	return nil
}

// Abort cancels the current upgrade.
func (o *Orchestrator) Abort() {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.cancel != nil {
		o.cancel()
	}
	o.status.Phase = "aborted"
	o.publishStatus()
	log.Println("[Orchestrator] Upgrade aborted")
}

// GetStatus returns the current upgrade status.
func (o *Orchestrator) GetStatus() models.UpgradeStatus {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.status
}

func (o *Orchestrator) run(ctx context.Context) {
	log.Printf("[Orchestrator] Starting upgrade %s -> %s", o.config.SourceVersion, o.config.TargetVersion)

	// Pre-check: verify cluster state
	if err := o.preCheck(ctx); err != nil {
		o.setError("pre-check failed: %v", err)
		return
	}

	// Discover nodes
	pods, err := o.k8s.GetPods(ctx, fmt.Sprintf("app=couchbase,cluster=%s", o.config.ClusterName))
	if err != nil {
		o.setError("failed to list pods: %v", err)
		return
	}

	o.mu.Lock()
	o.status.NodesTotal = len(pods)
	o.status.Phase = "upgrading"
	o.mu.Unlock()
	o.publishStatus()

	// Execute Helm upgrade
	setValues := map[string]string{
		"couchbaseCluster.version": o.config.TargetVersion,
	}

	if err := o.k8s.HelmUpgrade(ctx, o.config.HelmRelease, o.config.HelmChart, o.config.HelmValues, setValues); err != nil {
		// Check for abort
		if ctx.Err() != nil {
			return
		}
		o.setError("helm upgrade failed: %v", err)
		return
	}

	// Track rolling upgrade progress
	o.trackRollingUpgrade(ctx, pods)
}

func (o *Orchestrator) preCheck(ctx context.Context) error {
	log.Println("[Orchestrator] Running pre-checks...")

	pods, err := o.k8s.GetPods(ctx, fmt.Sprintf("app=couchbase,cluster=%s", o.config.ClusterName))
	if err != nil {
		return fmt.Errorf("cannot list pods: %w", err)
	}

	for _, pod := range pods {
		if !pod.Ready {
			return fmt.Errorf("pod %s is not ready (status: %s)", pod.Name, pod.Status)
		}
	}

	log.Printf("[Orchestrator] Pre-check passed: %d nodes ready", len(pods))
	return nil
}

func (o *Orchestrator) trackRollingUpgrade(ctx context.Context, originalPods []kubernetes.PodInfo) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pods, err := o.k8s.GetPods(ctx, fmt.Sprintf("app=couchbase,cluster=%s", o.config.ClusterName))
			if err != nil {
				continue
			}

			upgraded := 0
			var currentNode string
			for _, pod := range pods {
				if pod.Image != "" && containsVersion(pod.Image, o.config.TargetVersion) {
					upgraded++
				} else if !pod.Ready {
					currentNode = pod.Name
				}
			}

			o.mu.Lock()
			o.status.NodesCompleted = upgraded
			o.status.CurrentNode = currentNode
			o.status.Progress = float64(upgraded) / float64(o.status.NodesTotal) * 100

			if upgraded == o.status.NodesTotal {
				o.status.Phase = "completed"
				o.status.Progress = 100
				duration := time.Since(o.status.StartTime).Seconds()
				log.Printf("[Orchestrator] Upgrade completed in %.0fs", duration)
			}
			o.mu.Unlock()
			o.publishStatus()

			if o.status.Phase == "completed" {
				// Emit success alert
				o.collector.AddAlert(models.Alert{
					ID:        fmt.Sprintf("upgrade-complete-%d", time.Now().Unix()),
					Severity:  "info",
					Category:  "upgrade",
					Title:     "Upgrade Completed",
					Message:   fmt.Sprintf("Cluster upgraded to %s successfully", o.config.TargetVersion),
					Source:    "orchestrator",
					Timestamp: time.Now(),
				})
				return
			}
		}
	}
}

func (o *Orchestrator) publishStatus() {
	o.collector.SetUpgradeStatus(o.status)
}

func (o *Orchestrator) setError(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	log.Printf("[Orchestrator] ERROR: %s", msg)

	o.mu.Lock()
	o.status.Phase = "failed"
	o.status.Errors = append(o.status.Errors, msg)
	o.mu.Unlock()
	o.publishStatus()

	o.collector.AddAlert(models.Alert{
		ID:        fmt.Sprintf("upgrade-error-%d", time.Now().Unix()),
		Severity:  "critical",
		Category:  "upgrade",
		Title:     "Upgrade Failed",
		Message:   msg,
		Source:    "orchestrator",
		Timestamp: time.Now(),
	})
}

func containsVersion(image, version string) bool {
	return len(image) > 0 && len(version) > 0 && contains(image, version)
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
