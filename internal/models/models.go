package models

import (
	"time"
)

// ─── Cluster Configuration ──────────────────────────────────────────────────

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
	Region     string `json:"region,omitempty" yaml:"region,omitempty"`
	Zone       string `json:"zone,omitempty" yaml:"zone,omitempty"`
	Edition    string `json:"edition,omitempty" yaml:"edition,omitempty"`     // community, enterprise
	Platform   string `json:"platform,omitempty" yaml:"platform,omitempty"` // kubernetes, docker, native
	Namespace  string `json:"namespace,omitempty" yaml:"namespace,omitempty"` // k8s namespace for this cluster
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

// ─── Storm / Load Generation ────────────────────────────────────────────────

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

// ─── Upgrade Orchestration ──────────────────────────────────────────────────

// UpgradeConfig configures the Upgrade Orchestrator.
type UpgradeConfig struct {
	SourceVersion  string        `json:"source_version" yaml:"source_version"`
	TargetVersion  string        `json:"target_version" yaml:"target_version"`
	Namespace      string        `json:"namespace" yaml:"namespace"`
	ClusterName    string        `json:"cluster_name" yaml:"cluster_name"`
	Kubeconfig     string        `json:"kubeconfig" yaml:"kubeconfig"`
	RollingDelay   time.Duration `json:"rolling_delay" yaml:"rolling_delay"`
	HelmRelease    string        `json:"helm_release" yaml:"helm_release"`
	HelmChart      string        `json:"helm_chart" yaml:"helm_chart"`
	HelmValues     string        `json:"helm_values" yaml:"helm_values"`
	MaxUnavailable int           `json:"max_unavailable" yaml:"max_unavailable"`
}

// ─── XDCR Replication ───────────────────────────────────────────────────────

// XDCRConfig configures XDCR validation.
type XDCRConfig struct {
	SourceCluster    ClusterConfig `json:"source_cluster" yaml:"source_cluster"`
	TargetCluster    ClusterConfig `json:"target_cluster" yaml:"target_cluster"`
	ReplicationID    string        `json:"replication_id" yaml:"replication_id"`
	PollInterval     time.Duration `json:"poll_interval" yaml:"poll_interval"`
	GoxdcrDelayAlert time.Duration `json:"goxdcr_delay_alert" yaml:"goxdcr_delay_alert"`
}

// ─── Data Integrity Validation ──────────────────────────────────────────────

// ValidatorConfig configures the Data Integrity Validator.
type ValidatorConfig struct {
	BatchSize     int           `json:"batch_size" yaml:"batch_size"`
	ScanInterval  time.Duration `json:"scan_interval" yaml:"scan_interval"`
	HashCheck     bool          `json:"hash_check" yaml:"hash_check"`
	SequenceCheck bool          `json:"sequence_check" yaml:"sequence_check"`
	KeySampling   float64       `json:"key_sampling" yaml:"key_sampling"`
}

// ─── Node & Cluster Health ──────────────────────────────────────────────────

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
	Services      []string  `json:"services,omitempty"` // kv, n1ql, index, fts, analytics, eventing
	Region        string    `json:"region,omitempty"`
	Zone          string    `json:"zone,omitempty"`
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

// ─── XDCR Status ────────────────────────────────────────────────────────────

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

// ─── Data Integrity ─────────────────────────────────────────────────────────

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

// ─── Storm Metrics ──────────────────────────────────────────────────────────

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

// ─── Upgrade Status ─────────────────────────────────────────────────────────

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

// ─── Alerts ─────────────────────────────────────────────────────────────────

// Alert represents a system alert.
type Alert struct {
	ID        string    `json:"id"`
	Severity  string    `json:"severity"` // info, warning, critical
	Category  string    `json:"category"` // data_loss, replication, node_failure, upgrade, failover, backup, migration, ai_analysis
	Title     string    `json:"title"`
	Message   string    `json:"message"`
	Source    string    `json:"source"`
	Timestamp time.Time `json:"timestamp"`
	Resolved  bool      `json:"resolved"`
}

// ─── Cluster Metrics ────────────────────────────────────────────────────────

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
	Edition         string       `json:"edition"` // community, enterprise
	Platform        string       `json:"platform"` // kubernetes, docker, native
	Region          string       `json:"region,omitempty"`
	Zone            string       `json:"zone,omitempty"`
	Healthy         bool         `json:"healthy"`
	Timestamp       time.Time    `json:"timestamp"`
}

// ─── Data Loss Proof ────────────────────────────────────────────────────────

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

// ─── Dashboard ──────────────────────────────────────────────────────────────

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
	// New: multi-region, HA, backup, migration, AI
	Regions         []RegionStatus    `json:"regions,omitempty"`
	FailoverStatus  *FailoverStatus   `json:"failover_status,omitempty"`
	BackupStatus    *BackupStatus     `json:"backup_status,omitempty"`
	MigrationStatus *MigrationStatus  `json:"migration_status,omitempty"`
	AIInsights      []AIInsight       `json:"ai_insights,omitempty"`
	Timestamp       time.Time         `json:"timestamp"`
}

// ─── Reports ────────────────────────────────────────────────────────────────

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

// ─── Scenarios ──────────────────────────────────────────────────────────────

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
	Type     string        `json:"type"` // kill_pod, network_delay, partition, kill_container
	Target   string        `json:"target"`
	Delay    time.Duration `json:"delay"`
	Duration time.Duration `json:"duration"`
}

// ─── WebSocket / Commands ───────────────────────────────────────────────────

// WSMessage is a WebSocket message envelope.
type WSMessage struct {
	Type    string      `json:"type"` // dashboard_update, alert, command_response
	Payload interface{} `json:"payload"`
}

// Command represents a control command from the UI.
type Command struct {
	Action string            `json:"action"`
	Params map[string]string `json:"params,omitempty"`
}

// ─── Multi-Region Management ────────────────────────────────────────────────

// RegionConfig defines a geographic region with its clusters.
type RegionConfig struct {
	Name       string   `json:"name" yaml:"name"`             // e.g. us-east-1, eu-west-1
	DisplayName string  `json:"display_name" yaml:"display_name"`
	Provider   string   `json:"provider,omitempty" yaml:"provider,omitempty"` // aws, gcp, azure, on-prem
	Clusters   []string `json:"clusters" yaml:"clusters"`     // cluster names in this region
	Primary    bool     `json:"primary" yaml:"primary"`       // is this the primary region
	Endpoint   string   `json:"endpoint,omitempty" yaml:"endpoint,omitempty"` // region-specific NebulaCB endpoint
}

// RegionStatus represents the current state of a region.
type RegionStatus struct {
	Name           string    `json:"name"`
	DisplayName    string    `json:"display_name"`
	Provider       string    `json:"provider"`
	Primary        bool      `json:"primary"`
	ClusterCount   int       `json:"cluster_count"`
	HealthyClusters int      `json:"healthy_clusters"`
	TotalDocs      uint64    `json:"total_docs"`
	OpsPerSec      float64   `json:"ops_per_sec"`
	ReplicationLag float64   `json:"replication_lag_ms"`
	Status         string    `json:"status"` // healthy, degraded, down
	Timestamp      time.Time `json:"timestamp"`
}

// RegionReplication represents cross-region replication status.
type RegionReplication struct {
	SourceRegion   string    `json:"source_region"`
	TargetRegion   string    `json:"target_region"`
	State          string    `json:"state"` // active, paused, failed
	ChangesLeft    uint64    `json:"changes_left"`
	ReplicationLag float64   `json:"replication_lag_ms"`
	Bandwidth      float64   `json:"bandwidth_mbps"`
	Timestamp      time.Time `json:"timestamp"`
}

// ─── High Availability & Failover ───────────────────────────────────────────

// FailoverConfig configures automatic failover behavior.
type FailoverConfig struct {
	Enabled            bool          `json:"enabled" yaml:"enabled"`
	AutoFailover       bool          `json:"auto_failover" yaml:"auto_failover"`
	FailoverTimeout    time.Duration `json:"failover_timeout" yaml:"failover_timeout"`         // time before triggering failover
	MaxAutoFailovers   int           `json:"max_auto_failovers" yaml:"max_auto_failovers"`     // max nodes to auto-failover
	PreferredRegion    string        `json:"preferred_region" yaml:"preferred_region"`          // region to failover to
	HealthCheckInterval time.Duration `json:"health_check_interval" yaml:"health_check_interval"`
	RecoveryMode       string        `json:"recovery_mode" yaml:"recovery_mode"`               // auto, manual
	PreserveData       bool          `json:"preserve_data" yaml:"preserve_data"`
}

// FailoverStatus represents the current HA/failover state.
type FailoverStatus struct {
	Mode               string            `json:"mode"` // active-active, active-passive, multi-region
	PrimaryCluster     string            `json:"primary_cluster"`
	StandbyCluster     string            `json:"standby_cluster"`
	AutoFailoverEnabled bool             `json:"auto_failover_enabled"`
	FailoverHistory    []FailoverEvent   `json:"failover_history,omitempty"`
	LastHealthCheck    time.Time         `json:"last_health_check"`
	ClusterStates      map[string]string `json:"cluster_states"` // cluster_name -> active/standby/failed/recovering
	Timestamp          time.Time         `json:"timestamp"`
}

// FailoverEvent records a failover occurrence.
type FailoverEvent struct {
	ID             string    `json:"id"`
	Type           string    `json:"type"` // auto, manual, graceful
	SourceCluster  string    `json:"source_cluster"`
	TargetCluster  string    `json:"target_cluster"`
	Reason         string    `json:"reason"`
	NodesAffected  []string  `json:"nodes_affected,omitempty"`
	Duration       float64   `json:"duration_seconds"`
	DataLoss       bool      `json:"data_loss"`
	Status         string    `json:"status"` // completed, failed, in_progress
	Timestamp      time.Time `json:"timestamp"`
}

// ─── Backup & Restore ───────────────────────────────────────────────────────

// BackupConfig configures backup management.
type BackupConfig struct {
	Enabled       bool          `json:"enabled" yaml:"enabled"`
	Schedule      string        `json:"schedule" yaml:"schedule"`           // cron expression
	Repository    string        `json:"repository" yaml:"repository"`       // backup storage path/URI
	RetentionDays int           `json:"retention_days" yaml:"retention_days"`
	Compression   bool          `json:"compression" yaml:"compression"`
	Encryption    bool          `json:"encryption" yaml:"encryption"`
	FullBackupInterval string   `json:"full_backup_interval" yaml:"full_backup_interval"` // e.g. "7d"
	Timeout       time.Duration `json:"timeout" yaml:"timeout"`
}

// BackupStatus represents current backup state.
type BackupStatus struct {
	LastBackup     *BackupInfo   `json:"last_backup,omitempty"`
	NextScheduled  time.Time     `json:"next_scheduled,omitempty"`
	RecentBackups  []BackupInfo  `json:"recent_backups,omitempty"`
	ActiveRestore  *RestoreInfo  `json:"active_restore,omitempty"`
	RepositorySize string        `json:"repository_size"`
	Healthy        bool          `json:"healthy"`
	Timestamp      time.Time     `json:"timestamp"`
}

// BackupInfo represents a single backup record.
type BackupInfo struct {
	ID          string    `json:"id"`
	ClusterName string    `json:"cluster_name"`
	Type        string    `json:"type"` // full, incremental, differential
	Status      string    `json:"status"` // running, completed, failed
	Size        string    `json:"size"`
	Duration    float64   `json:"duration_seconds"`
	Buckets     []string  `json:"buckets"`
	StartTime   time.Time `json:"start_time"`
	EndTime     time.Time `json:"end_time,omitempty"`
	Repository  string    `json:"repository"`
	Error       string    `json:"error,omitempty"`
}

// RestoreInfo represents an active restore operation.
type RestoreInfo struct {
	ID            string    `json:"id"`
	BackupID      string    `json:"backup_id"`
	TargetCluster string    `json:"target_cluster"`
	Status        string    `json:"status"` // running, completed, failed
	Progress      float64   `json:"progress"`
	Buckets       []string  `json:"buckets"`
	StartTime     time.Time `json:"start_time"`
	Error         string    `json:"error,omitempty"`
}

// ─── Data Migration ─────────────────────────────────────────────────────────

// MigrationConfig configures data migration.
type MigrationConfig struct {
	Workers        int           `json:"workers" yaml:"workers"`
	BatchSize      int           `json:"batch_size" yaml:"batch_size"`
	Timeout        time.Duration `json:"timeout" yaml:"timeout"`
	RetryAttempts  int           `json:"retry_attempts" yaml:"retry_attempts"`
	ValidateAfter  bool          `json:"validate_after" yaml:"validate_after"`
	TransformRules []TransformRule `json:"transform_rules,omitempty" yaml:"transform_rules,omitempty"`
}

// TransformRule defines a data transformation during migration.
type TransformRule struct {
	Type      string `json:"type"`   // rename_field, convert_type, filter, add_field
	Field     string `json:"field"`
	NewField  string `json:"new_field,omitempty"`
	Value     string `json:"value,omitempty"`
	Condition string `json:"condition,omitempty"`
}

// MigrationStatus represents the current state of a migration.
type MigrationStatus struct {
	ID               string    `json:"id"`
	SourceCluster    string    `json:"source_cluster"`
	TargetCluster    string    `json:"target_cluster"`
	SourceBucket     string    `json:"source_bucket"`
	TargetBucket     string    `json:"target_bucket"`
	Status           string    `json:"status"` // pending, running, paused, completed, failed
	TotalDocs        uint64    `json:"total_docs"`
	MigratedDocs     uint64    `json:"migrated_docs"`
	FailedDocs       uint64    `json:"failed_docs"`
	Progress         float64   `json:"progress"`
	BytesTransferred uint64    `json:"bytes_transferred"`
	Rate             float64   `json:"docs_per_sec"`
	StartTime        time.Time `json:"start_time"`
	EstimatedEnd     time.Time `json:"estimated_end,omitempty"`
	Errors           []string  `json:"errors,omitempty"`
	Timestamp        time.Time `json:"timestamp"`
}

// MigrationRequest initiates a new migration.
type MigrationRequest struct {
	SourceCluster  string          `json:"source_cluster"`
	TargetCluster  string          `json:"target_cluster"`
	SourceBucket   string          `json:"source_bucket"`
	TargetBucket   string          `json:"target_bucket"`
	Filter         string          `json:"filter,omitempty"`         // N1QL WHERE clause
	TransformRules []TransformRule `json:"transform_rules,omitempty"`
	DryRun         bool            `json:"dry_run"`
}

// ─── AI Analysis & Troubleshooting ──────────────────────────────────────────

// AIConfig configures AI-powered analysis.
type AIConfig struct {
	Enabled      bool   `json:"enabled" yaml:"enabled"`
	Provider     string `json:"provider" yaml:"provider"`         // anthropic, openai, ollama, custom
	Model        string `json:"model" yaml:"model"`               // claude-sonnet-4-20250514, gpt-4, llama3, etc.
	APIKey       string `json:"api_key" yaml:"api_key"`
	APIEndpoint  string `json:"api_endpoint" yaml:"api_endpoint"` // custom endpoint for ollama/vllm
	MaxTokens    int    `json:"max_tokens" yaml:"max_tokens"`
	AutoAnalyze  bool   `json:"auto_analyze" yaml:"auto_analyze"` // auto-analyze on critical alerts
	AnalyzeInterval time.Duration `json:"analyze_interval" yaml:"analyze_interval"`
}

// AIInsight represents an AI-generated analysis result.
type AIInsight struct {
	ID          string    `json:"id"`
	Type        string    `json:"type"` // log_analysis, performance, troubleshoot, recommendation, root_cause
	Severity    string    `json:"severity"` // info, warning, critical
	Title       string    `json:"title"`
	Summary     string    `json:"summary"`
	Details     string    `json:"details"`
	Suggestions []string  `json:"suggestions,omitempty"`
	RelatedLogs []string  `json:"related_logs,omitempty"`
	Cluster     string    `json:"cluster,omitempty"`
	Confidence  float64   `json:"confidence"` // 0.0-1.0
	Timestamp   time.Time `json:"timestamp"`
}

// AIAnalysisRequest represents a request to analyze logs/errors.
type AIAnalysisRequest struct {
	Type        string   `json:"type"` // logs, error, performance, cluster_health, troubleshoot
	Context     string   `json:"context"`
	ClusterName string   `json:"cluster_name,omitempty"`
	Logs        []string `json:"logs,omitempty"`
	ErrorMsg    string   `json:"error_msg,omitempty"`
	MetricsJSON string   `json:"metrics_json,omitempty"`
	Question    string   `json:"question,omitempty"` // freeform question about the cluster
}

// AIAnalysisResponse represents the AI's response.
type AIAnalysisResponse struct {
	Insight  AIInsight `json:"insight"`
	RawReply string    `json:"raw_reply,omitempty"`
	TokensUsed int     `json:"tokens_used"`
	Duration float64   `json:"duration_seconds"`
}

// ─── Docker Management ──────────────────────────────────────────────────────

// DockerConfig configures Docker-based cluster management.
type DockerConfig struct {
	Enabled     bool   `json:"enabled" yaml:"enabled"`
	Host        string `json:"host" yaml:"host"`               // docker daemon host (unix:///var/run/docker.sock)
	Network     string `json:"network" yaml:"network"`         // docker network name
	ComposeFile string `json:"compose_file" yaml:"compose_file"`
}

// ContainerInfo represents a Docker container running Couchbase.
type ContainerInfo struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Image       string            `json:"image"`
	Status      string            `json:"status"` // running, stopped, paused, restarting
	State       string            `json:"state"`
	Ports       map[string]string `json:"ports"` // container_port -> host_port
	ClusterName string            `json:"cluster_name"`
	Created     time.Time         `json:"created"`
	MemoryUsage float64           `json:"memory_usage_mb"`
	CPUPercent  float64           `json:"cpu_percent"`
	Network     string            `json:"network"`
}

// ─── Index & Query Management ───────────────────────────────────────────────

// IndexInfo represents a Couchbase index.
type IndexInfo struct {
	Name         string    `json:"name"`
	Bucket       string    `json:"bucket"`
	Scope        string    `json:"scope,omitempty"`
	Collection   string    `json:"collection,omitempty"`
	Type         string    `json:"type"` // gsi, view, fts
	State        string    `json:"state"` // building, online, deferred, error
	KeyExpression string   `json:"key_expression"`
	Replicas     int       `json:"replicas"`
	ItemCount    uint64    `json:"item_count"`
	DataSize     string    `json:"data_size"`
	ScanRate     float64   `json:"scan_rate"`
	Timestamp    time.Time `json:"timestamp"`
}

// QueryInfo represents a slow or active query.
type QueryInfo struct {
	ID           string    `json:"id"`
	Statement    string    `json:"statement"`
	State        string    `json:"state"` // running, completed, cancelled
	ElapsedTime  string    `json:"elapsed_time"`
	ResultCount  uint64    `json:"result_count"`
	ErrorCount   uint64    `json:"error_count"`
	Cluster      string    `json:"cluster"`
	Timestamp    time.Time `json:"timestamp"`
}

// ─── Bucket Management ──────────────────────────────────────────────────────

// BucketInfo represents a Couchbase bucket.
type BucketInfo struct {
	Name          string  `json:"name"`
	Type          string  `json:"type"` // membase, memcached, ephemeral
	RamQuotaMB    uint64  `json:"ram_quota_mb"`
	RamUsedMB     float64 `json:"ram_used_mb"`
	DiskUsedMB    float64 `json:"disk_used_mb"`
	ItemCount     uint64  `json:"item_count"`
	OpsPerSec     float64 `json:"ops_per_sec"`
	Replicas      int     `json:"replicas"`
	EvictionPolicy string `json:"eviction_policy"`
	ConflictResolution string `json:"conflict_resolution"`
	FlushEnabled  bool    `json:"flush_enabled"`
	CompressionMode string `json:"compression_mode"`
	MaxTTL        int     `json:"max_ttl"`
}

// ─── User / RBAC Management ────────────────────────────────────────────────

// CBUser represents a Couchbase user.
type CBUser struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Domain   string   `json:"domain"` // local, external
	Roles    []CBRole `json:"roles"`
	Groups   []string `json:"groups,omitempty"`
}

// CBRole represents a Couchbase role assignment.
type CBRole struct {
	Role       string `json:"role"`
	Bucket     string `json:"bucket_name,omitempty"`
	Scope      string `json:"scope_name,omitempty"`
	Collection string `json:"collection_name,omitempty"`
}

// ─── Edition Detection ──────────────────────────────────────────────────────

// EditionInfo captures Couchbase edition details.
type EditionInfo struct {
	Edition       string   `json:"edition"` // community, enterprise
	Version       string   `json:"version"`
	Build         string   `json:"build"`
	Enterprise    bool     `json:"enterprise"`
	Features      []string `json:"features"` // available features based on edition
	LicenseValid  bool     `json:"license_valid"`
	ExpiryDate    string   `json:"expiry_date,omitempty"`
}

// ─── Cluster Topology ───────────────────────────────────────────────────────

// ClusterTopology represents the full topology of a managed cluster.
type ClusterTopology struct {
	ClusterName  string       `json:"cluster_name"`
	Edition      EditionInfo  `json:"edition"`
	Platform     string       `json:"platform"` // kubernetes, docker, native
	Region       string       `json:"region"`
	Zone         string       `json:"zone"`
	Nodes        []NodeStatus `json:"nodes"`
	Buckets      []BucketInfo `json:"buckets"`
	Indexes      []IndexInfo  `json:"indexes"`
	Services     map[string]int `json:"services"` // service_name -> node_count
	XDCRLinks    []XDCRStatus `json:"xdcr_links,omitempty"`
	Timestamp    time.Time    `json:"timestamp"`
}
