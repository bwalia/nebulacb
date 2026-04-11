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
	return o.StartWithParams(ctx, "", "")
}

// StartWithParams begins the upgrade, optionally overriding cluster name, target version, and namespace.
func (o *Orchestrator) StartWithParams(ctx context.Context, clusterName, targetVersion string) error {
	return o.StartWithFullParams(ctx, clusterName, targetVersion, "")
}

// StartWithFullParams begins the upgrade with full parameter overrides.
func (o *Orchestrator) StartWithFullParams(ctx context.Context, clusterName, targetVersion, namespace string) error {
	o.mu.Lock()
	if o.status.Phase != "pending" && o.status.Phase != "failed" && o.status.Phase != "aborted" && o.status.Phase != "completed" {
		o.mu.Unlock()
		return fmt.Errorf("upgrade already in progress: %s", o.status.Phase)
	}

	// Apply overrides
	if clusterName != "" {
		o.config.ClusterName = clusterName
	}
	if targetVersion != "" {
		o.config.TargetVersion = targetVersion
	}
	if namespace != "" {
		o.config.Namespace = namespace
		o.k8s.SetNamespace(namespace)
	}

	// Use a detached context so the upgrade outlives the HTTP request that started it
	ctx, o.cancel = context.WithCancel(context.Background())
	o.status = models.UpgradeStatus{
		Phase:         "pre-check",
		SourceVersion: o.config.SourceVersion,
		TargetVersion: o.config.TargetVersion,
		StartTime:     time.Now(),
	}
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

// Downgrade rolls back the cluster to the source version by re-patching the CouchbaseCluster CR.
// This triggers the Operator to do a rolling restart with the original image.
func (o *Orchestrator) Downgrade(ctx context.Context) error {
	o.mu.Lock()
	phase := o.status.Phase
	sourceVersion := o.config.SourceVersion
	clusterName := o.config.ClusterName
	o.mu.Unlock()

	if o.k8s == nil {
		return fmt.Errorf("Kubernetes client not available")
	}
	if sourceVersion == "" {
		return fmt.Errorf("source version not configured — cannot determine rollback target")
	}

	// Cancel any in-progress upgrade tracking
	if o.cancel != nil {
		o.cancel()
	}

	log.Printf("[Orchestrator] Downgrade requested: rolling back %s to v%s (was in phase: %s)", clusterName, sourceVersion, phase)

	// Patch the CouchbaseCluster CR back to the original version
	if err := o.k8s.PatchCouchbaseCluster(ctx, clusterName, sourceVersion); err != nil {
		o.setError("downgrade patch failed: %v", err)
		return fmt.Errorf("downgrade patch failed: %w", err)
	}

	// Reset status to track the downgrade as a new "upgrade" back to source
	o.mu.Lock()
	o.cancel = nil
	ctx, o.cancel = context.WithCancel(context.Background())
	o.status = models.UpgradeStatus{
		Phase:         "upgrading",
		SourceVersion: o.config.TargetVersion,
		TargetVersion: sourceVersion,
		StartTime:     time.Now(),
	}
	o.mu.Unlock()
	o.publishStatus()

	// Discover pods and track the rollback
	go func() {
		pods, err := o.k8s.GetPods(ctx, fmt.Sprintf("app=couchbase,couchbase_cluster=%s", clusterName))
		if err != nil {
			o.setError("downgrade: failed to list pods: %v", err)
			return
		}
		o.mu.Lock()
		o.status.NodesTotal = len(pods)
		o.mu.Unlock()

		// Temporarily swap target version for tracking
		origTarget := o.config.TargetVersion
		o.config.TargetVersion = sourceVersion
		o.trackRollingUpgrade(ctx, pods)
		o.config.TargetVersion = origTarget
	}()

	o.collector.AddAlert(models.Alert{
		ID:        fmt.Sprintf("downgrade-started-%d", time.Now().Unix()),
		Severity:  "warning",
		Category:  "upgrade",
		Title:     "Downgrade Initiated",
		Message:   fmt.Sprintf("Rolling back %s to v%s", clusterName, sourceVersion),
		Source:    "orchestrator",
		Timestamp: time.Now(),
	})

	return nil
}

// GetStatus returns the current upgrade status.
func (o *Orchestrator) GetStatus() models.UpgradeStatus {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.status
}

func (o *Orchestrator) run(ctx context.Context) {
	log.Printf("[Orchestrator] Starting upgrade %s -> %s", o.config.SourceVersion, o.config.TargetVersion)

	// Guard: k8s client required
	if o.k8s == nil {
		o.setError("Kubernetes client not available — configure KUBECONFIG to run upgrades")
		return
	}

	// Pre-check: verify cluster state
	if err := o.preCheck(ctx); err != nil {
		o.setError("pre-check failed: %v", err)
		return
	}

	// Discover nodes
	pods, err := o.k8s.GetPods(ctx, fmt.Sprintf("app=couchbase,couchbase_cluster=%s", o.config.ClusterName))
	if err != nil {
		o.setError("failed to list pods: %v", err)
		return
	}

	o.mu.Lock()
	o.status.NodesTotal = len(pods)
	o.status.Phase = "upgrading"
	o.mu.Unlock()
	o.publishStatus()

	// Trigger rolling upgrade by patching the CouchbaseCluster CR image
	log.Printf("[Orchestrator] Patching CouchbaseCluster %s to version %s", o.config.ClusterName, o.config.TargetVersion)
	if err := o.k8s.PatchCouchbaseCluster(ctx, o.config.ClusterName, o.config.TargetVersion); err != nil {
		if ctx.Err() != nil {
			return
		}
		o.setError("patch CouchbaseCluster failed: %v", err)
		return
	}

	// Track rolling upgrade progress
	o.trackRollingUpgrade(ctx, pods)
}

func (o *Orchestrator) preCheck(ctx context.Context) error {
	log.Println("[Orchestrator] Running pre-checks...")

	pods, err := o.k8s.GetPods(ctx, fmt.Sprintf("app=couchbase,couchbase_cluster=%s", o.config.ClusterName))
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
			pods, err := o.k8s.GetPods(ctx, fmt.Sprintf("app=couchbase,couchbase_cluster=%s", o.config.ClusterName))
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
