package models

import (
	"time"
)

// ClusterConfig represents a Couchbase cluster connection configuration.
type ClusterConfig struct {
	Name       string `json:"name" yaml:"name"`
	Host       string `json:"host" yaml:"host"`
	Username   string `json:"username" yaml:"username"`
	Password   string `json:"password" yaml:"password"`
	Bucket     string `json:"bucket" yaml:"bucket"`
	Scope      string `json:"scope,omitempty" yaml:"scope,omitempty"`
	Collection string `json:"collection,omitempty" yaml:"collection,omitempty"`
	Role       string `json:"role,omitempty" yaml:"role,omitempty"` // source, target, replica, standby
}

// Document represents a generated test document with tracking metadata.
type Document struct {
	ID         string    `json:"id"`
	SequenceID uint64    `json:"sequence_id"`
	Timestamp  time.Time `json:"timestamp"`
	Region     string    `json:"region"`
	Checksum   string    `json:"checksum"`
	Size       int       `json:"size"`
	Payload    []byte    `json:"payload"`
}

// StormConfig configures the Data Storm Generator.
type StormConfig struct {
	WritesPerSecond   int           `json:"writes_per_second" yaml:"writes_per_second"`
	ReadsPerSecond    int           `json:"reads_per_second" yaml:"reads_per_second"`
	DocSizeMin        int           `json:"doc_size_min" yaml:"doc_size_min"`
	DocSizeMax        int           `json:"doc_size_max" yaml:"doc_size_max"`
	Workers           int           `json:"workers" yaml:"workers"`
	Region            string        `json:"region" yaml:"region"`
	BurstEnabled      bool          `json:"burst_enabled" yaml:"burst_enabled"`
	BurstMultiplier   float64       `json:"burst_multiplier" yaml:"burst_multiplier"`
	BurstInterval     time.Duration `json:"burst_interval" yaml:"burst_interval"`
	BurstDuration     time.Duration `json:"burst_duration" yaml:"burst_duration"`
	HotKeyPercentage  float64       `json:"hot_key_percentage" yaml:"hot_key_percentage"`
	DeletePercentage  float64       `json:"delete_percentage" yaml:"delete_percentage"`
	UpdatePercentage  float64       `json:"update_percentage" yaml:"update_percentage"`
	KeyPrefix         string        `json:"key_prefix" yaml:"key_prefix"`
}

// UpgradeConfig configures the Upgrade Orchestrator.
type UpgradeConfig struct {
	SourceVersion  string        `json:"source_version" yaml:"source_version"`
	TargetVersion  string        `json:"target_version" yaml:"target_version"`
	Namespace      string        `json:"namespace" yaml:"namespace"`
	ClusterName    string        `json:"cluster_name" yaml:"cluster_name"`
	RollingDelay   time.Duration `json:"rolling_delay" yaml:"rolling_delay"`
	HelmRelease    string        `json:"helm_release" yaml:"helm_release"`
	HelmChart      string        `json:"helm_chart" yaml:"helm_chart"`
	HelmValues     string        `json:"helm_values" yaml:"helm_values"`
	MaxUnavailable int           `json:"max_unavailable" yaml:"max_unavailable"`
}

// XDCRConfig configures XDCR validation.
type XDCRConfig struct {
	SourceCluster    ClusterConfig `json:"source_cluster" yaml:"source_cluster"`
	TargetCluster    ClusterConfig `json:"target_cluster" yaml:"target_cluster"`
	ReplicationID    string        `json:"replication_id" yaml:"replication_id"`
	PollInterval     time.Duration `json:"poll_interval" yaml:"poll_interval"`
	GoxdcrDelayAlert time.Duration `json:"goxdcr_delay_alert" yaml:"goxdcr_delay_alert"`
}

// ValidatorConfig configures the Data Integrity Validator.
type ValidatorConfig struct {
	BatchSize     int           `json:"batch_size" yaml:"batch_size"`
	ScanInterval  time.Duration `json:"scan_interval" yaml:"scan_interval"`
	HashCheck     bool          `json:"hash_check" yaml:"hash_check"`
	SequenceCheck bool          `json:"sequence_check" yaml:"sequence_check"`
	KeySampling   float64       `json:"key_sampling" yaml:"key_sampling"`
}

// NodeStatus represents a single Couchbase node's state.
type NodeStatus struct {
	Name          string    `json:"name"`
	Host          string    `json:"host"`
	Status        string    `json:"status"` // healthy, upgrading, rebalancing, failed
	Version       string    `json:"version"`
	MemoryUsage   float64   `json:"memory_usage"`
	CPUUsage      float64   `json:"cpu_usage"`
	DiskUsage     float64   `json:"disk_usage"`
	LastHeartbeat time.Time `json:"last_heartbeat"`
}

// ClusterHealth represents overall cluster health.
type ClusterHealth struct {
	ClusterName     string       `json:"cluster_name"`
	Nodes           []NodeStatus `json:"nodes"`
	RebalanceState  string       `json:"rebalance_state"` // none, running, completed, failed
	UpgradeProgress float64      `json:"upgrade_progress"`
	TotalDocs       uint64       `json:"total_docs"`
	OpsPerSecond    float64      `json:"ops_per_second"`
	Timestamp       time.Time    `json:"timestamp"`
}

// XDCRStatus represents XDCR replication status.
type XDCRStatus struct {
	ReplicationID    string    `json:"replication_id"`
	State            string    `json:"state"` // Running, Paused, Restarting
	ChangesLeft      uint64    `json:"changes_left"`
	DocsProcessed    uint64    `json:"docs_processed"`
	ReplicationLag   float64   `json:"replication_lag_ms"`
	PipelineRestarts int       `json:"pipeline_restarts"`
	TopologyChange   bool      `json:"topology_change"`
	GoxdcrDelayStart time.Time `json:"goxdcr_delay_start,omitempty"`
	GoxdcrDelayEnd   time.Time `json:"goxdcr_delay_end,omitempty"`
	MutationQueue    uint64    `json:"mutation_queue"`
	Timestamp        time.Time `json:"timestamp"`
}

// IntegrityResult represents a data validation result.
type IntegrityResult struct {
	RunID           string    `json:"run_id"`
	SourceDocs      uint64    `json:"source_docs"`
	TargetDocs      uint64    `json:"target_docs"`
	MissingKeys     []string  `json:"missing_keys,omitempty"`
	MissingCount    uint64    `json:"missing_count"`
	ExtraKeys       []string  `json:"extra_keys,omitempty"`
	ExtraCount      uint64    `json:"extra_count"`
	HashMismatches  uint64    `json:"hash_mismatches"`
	MismatchPercent float64   `json:"mismatch_percent"`
	SequenceGaps    []uint64  `json:"sequence_gaps,omitempty"`
	Duration        float64   `json:"duration_seconds"`
	Timestamp       time.Time `json:"timestamp"`
	Status          string    `json:"status"` // pass, fail, running
}

// StormMetrics represents real-time load generation metrics.
type StormMetrics struct {
	WritesPerSec      float64   `json:"writes_per_sec"`
	ReadsPerSec       float64   `json:"reads_per_sec"`
	LatencyP50        float64   `json:"latency_p50_ms"`
	LatencyP95        float64   `json:"latency_p95_ms"`
	LatencyP99        float64   `json:"latency_p99_ms"`
	ActiveConnections int       `json:"active_connections"`
	TotalWritten      uint64    `json:"total_written"`
	TotalRead         uint64    `json:"total_read"`
	TotalErrors       uint64    `json:"total_errors"`
	Timestamp         time.Time `json:"timestamp"`
}

// UpgradeStatus represents upgrade progress.
type UpgradeStatus struct {
	Phase           string     `json:"phase"` // pending, pre-check, upgrading, rebalancing, completed, failed, aborted
	CurrentNode     string     `json:"current_node"`
	NodesCompleted  int        `json:"nodes_completed"`
	NodesTotal      int        `json:"nodes_total"`
	Progress        float64    `json:"progress"`
	StartTime       time.Time  `json:"start_time"`
	EstimatedEnd    time.Time  `json:"estimated_end,omitempty"`
	NodeDurations   []float64  `json:"node_durations_sec"`
	Availability    float64    `json:"availability_percent"`
	Errors          []string   `json:"errors,omitempty"`
	SourceVersion   string     `json:"source_version"`
	TargetVersion   string     `json:"target_version"`
	Timestamp       time.Time  `json:"timestamp"`
}

// Alert represents a system alert.
type Alert struct {
	ID        string    `json:"id"`
	Severity  string    `json:"severity"` // info, warning, critical
	Category  string    `json:"category"` // data_loss, replication, node_failure, upgrade
	Title     string    `json:"title"`
	Message   string    `json:"message"`
	Source    string    `json:"source"`
	Timestamp time.Time `json:"timestamp"`
	Resolved  bool      `json:"resolved"`
}

// ClusterMetrics holds deep per-cluster performance metrics from the Couchbase REST API.
type ClusterMetrics struct {
	ClusterName     string       `json:"cluster_name"`
	Nodes           []NodeStatus `json:"nodes"`
	RebalanceState  string       `json:"rebalance_state"`
	TotalDocs       uint64       `json:"total_docs"`
	TotalMemUsedMB  float64      `json:"total_mem_used_mb"`
	TotalMemTotalMB float64      `json:"total_mem_total_mb"`
	OpsPerSec       float64      `json:"ops_per_sec"`
	CmdGetPerSec    float64      `json:"cmd_get_per_sec"`
	CmdSetPerSec    float64      `json:"cmd_set_per_sec"`
	DiskWriteQueue  float64      `json:"disk_write_queue"`
	DiskReadRate    float64      `json:"ep_bg_fetched"`
	CacheMissRate   float64      `json:"ep_cache_miss_rate"`
	ResidentRatio   float64      `json:"vb_active_resident_items_ratio"`
	CurrConnections uint64       `json:"curr_connections"`
	Version         string       `json:"version"`
	Healthy         bool         `json:"healthy"`
	Timestamp       time.Time    `json:"timestamp"`
}

// DocCountSample is a single point in the doc-count timeline.
type DocCountSample struct {
	Timestamp  time.Time `json:"timestamp"`
	SourceDocs uint64    `json:"source_docs"`
	TargetDocs uint64    `json:"target_docs"`
	Delta      int64     `json:"delta"` // source - target
}

// DataLossProof tracks continuous proof that zero data loss has occurred.
type DataLossProof struct {
	Timeline             []DocCountSample `json:"timeline"`
	CurrentSourceDocs    uint64           `json:"current_source_docs"`
	CurrentTargetDocs    uint64           `json:"current_target_docs"`
	CurrentDelta         int64            `json:"current_delta"`
	MaxDeltaObserved     int64            `json:"max_delta_observed"`
	MaxDeltaTime         time.Time        `json:"max_delta_time,omitempty"`
	ConvergedAt          time.Time        `json:"converged_at,omitempty"`
	IsConverged          bool             `json:"is_converged"`
	ZeroLossConfirmed    bool             `json:"zero_loss_confirmed"`
	HashSamplesPassed    uint64           `json:"hash_samples_passed"`
	HashSamplesFailed    uint64           `json:"hash_samples_failed"`
	LastHashCheckTime    time.Time        `json:"last_hash_check_time,omitempty"`
	MonitoringDuration   string           `json:"monitoring_duration"`
	Timestamp            time.Time        `json:"timestamp"`
}

// DashboardState aggregates all metrics for the cockpit UI.
type DashboardState struct {
	// Multi-cluster: all connected clusters keyed by name
	Clusters        map[string]ClusterMetrics `json:"clusters"`
	// Backward compat: source/target shortcuts (point into Clusters map)
	SourceCluster   ClusterMetrics  `json:"source_cluster"`
	TargetCluster   ClusterMetrics  `json:"target_cluster"`
	XDCRStatus      XDCRStatus      `json:"xdcr_status"`
	DataLossProof   DataLossProof   `json:"data_loss_proof"`
	IntegrityResult IntegrityResult `json:"integrity_result"`
	StormMetrics    StormMetrics    `json:"storm_metrics"`
	UpgradeStatus   UpgradeStatus   `json:"upgrade_status"`
	Alerts          []Alert         `json:"alerts"`
	Timestamp       time.Time       `json:"timestamp"`
}

// Report represents a post-run report.
type Report struct {
	ID               string          `json:"id"`
	RunID            string          `json:"run_id"`
	Scenario         string          `json:"scenario"`
	StartTime        time.Time       `json:"start_time"`
	EndTime          time.Time       `json:"end_time"`
	UpgradeSuccess   bool            `json:"upgrade_success"`
	DataLossSummary  IntegrityResult `json:"data_loss_summary"`
	XDCRGapTimeline  []XDCRStatus    `json:"xdcr_gap_timeline"`
	RootCauseAnalysis string         `json:"root_cause_analysis"`
	Recommendations  []string        `json:"recommendations"`
}

// Scenario represents a test scenario configuration.
type Scenario struct {
	Name        string        `json:"name"`
	Description string        `json:"description"`
	Storm       StormConfig   `json:"storm"`
	Upgrade     UpgradeConfig `json:"upgrade,omitempty"`
	XDCR        XDCRConfig    `json:"xdcr,omitempty"`
	Validator   ValidatorConfig `json:"validator"`
	Duration    time.Duration `json:"duration"`
	Failures    []FailureSpec `json:"failures,omitempty"`
}

// FailureSpec defines a chaos/failure injection.
type FailureSpec struct {
	Type     string        `json:"type"` // kill_pod, network_delay, partition
	Target   string        `json:"target"`
	Delay    time.Duration `json:"delay"`
	Duration time.Duration `json:"duration"`
}

// WSMessage is a WebSocket message envelope.
type WSMessage struct {
	Type    string      `json:"type"` // dashboard_update, alert, command_response
	Payload interface{} `json:"payload"`
}

// Command represents a control command from the UI.
type Command struct {
	Action string            `json:"action"` // start_load, pause_load, start_upgrade, abort_upgrade, restart_xdcr, inject_failure, run_audit
	Params map[string]string `json:"params,omitempty"`
}
