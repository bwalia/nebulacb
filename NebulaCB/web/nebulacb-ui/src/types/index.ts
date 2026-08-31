export interface NodeStatus {
  name: string;
  host: string;
  status: string;
  version: string;
  memory_usage: number;
  cpu_usage: number;
  disk_usage: number;
  services?: string[];
  region?: string;
  zone?: string;
}

export interface ClusterMetrics {
  cluster_name: string;
  nodes: NodeStatus[];
  rebalance_state: string;
  total_docs: number;
  total_mem_used_mb: number;
  total_mem_total_mb: number;
  ops_per_sec: number;
  cmd_get_per_sec: number;
  cmd_set_per_sec: number;
  disk_write_queue: number;
  ep_bg_fetched: number;
  ep_cache_miss_rate: number;
  vb_active_resident_items_ratio: number;
  curr_connections: number;
  version: string;
  edition?: string;
  platform?: string;
  region?: string;
  zone?: string;
  healthy: boolean;
}

export interface XDCRStatus {
  replication_id: string;
  state: string;
  changes_left: number;
  docs_processed: number;
  replication_lag_ms: number;
  pipeline_restarts: number;
  topology_change: boolean;
  goxdcr_delay_start: string;
  goxdcr_delay_end: string;
  mutation_queue: number;
}

export interface DocCountSample {
  timestamp: string;
  source_docs: number;
  target_docs: number;
  delta: number;
}

export interface DataLossProof {
  timeline: DocCountSample[];
  current_source_docs: number;
  current_target_docs: number;
  current_delta: number;
  max_delta_observed: number;
  max_delta_time: string;
  converged_at: string;
  is_converged: boolean;
  zero_loss_confirmed: boolean;
  hash_samples_passed: number;
  hash_samples_failed: number;
  last_hash_check_time: string;
  monitoring_duration: string;
}

export interface IntegrityResult {
  run_id: string;
  source_docs: number;
  target_docs: number;
  missing_count: number;
  extra_count: number;
  hash_mismatches: number;
  mismatch_percent: number;
  sequence_gaps: number[];
  duration_seconds: number;
  status: string;
}

export interface StormMetrics {
  writes_per_sec: number;
  reads_per_sec: number;
  latency_p50_ms: number;
  latency_p95_ms: number;
  latency_p99_ms: number;
  active_connections: number;
  total_written: number;
  total_read: number;
  total_errors: number;
}

export interface UpgradeStatus {
  phase: string;
  current_node: string;
  nodes_completed: number;
  nodes_total: number;
  progress: number;
  source_version: string;
  target_version: string;
  errors: string[];
}

export interface Alert {
  id: string;
  severity: string;
  category: string;
  title: string;
  message: string;
  source: string;
  timestamp: string;
  resolved: boolean;
}

// ─── Multi-Region ───────────────────────────────────────────────────────────

export interface RegionStatus {
  name: string;
  display_name: string;
  provider: string;
  primary: boolean;
  cluster_count: number;
  healthy_clusters: number;
  total_docs: number;
  ops_per_sec: number;
  replication_lag_ms: number;
  status: string;
}

// ─── HA & Failover ──────────────────────────────────────────────────────────

export interface FailoverStatus {
  mode: string;
  primary_cluster: string;
  standby_cluster: string;
  auto_failover_enabled: boolean;
  failover_history?: FailoverEvent[];
  last_health_check: string;
  cluster_states: Record<string, string>;
}

export interface FailoverEvent {
  id: string;
  type: string;
  source_cluster: string;
  target_cluster: string;
  reason: string;
  nodes_affected?: string[];
  duration_seconds: number;
  data_loss: boolean;
  status: string;
  timestamp: string;
}

// ─── Backup & Restore ───────────────────────────────────────────────────────

export interface BackupStatus {
  last_backup?: BackupInfo;
  active_backup?: BackupInfo;
  next_scheduled?: string;
  recent_backups?: BackupInfo[];
  active_restore?: RestoreInfo;
  repository_size: string;
  healthy: boolean;
}

export interface BackupInfo {
  id: string;
  cluster_name: string;
  type: string;
  status: string;
  size: string;
  duration_seconds: number;
  buckets: string[];
  start_time: string;
  end_time?: string;
  error?: string;
  mode?: string;
  docs_exported?: number;
  bytes_exported?: number;
  path?: string;
}

export interface RestoreInfo {
  id: string;
  backup_id: string;
  target_cluster: string;
  status: string;
  progress: number;
  buckets: string[];
  start_time: string;
  error?: string;
  mode?: string;
  docs_restored?: number;
  errors?: number;
}

// ─── Data Migration ─────────────────────────────────────────────────────────

export interface MigrationStatus {
  id: string;
  source_cluster: string;
  target_cluster: string;
  source_bucket: string;
  target_bucket: string;
  status: string;
  total_docs: number;
  migrated_docs: number;
  failed_docs: number;
  progress: number;
  bytes_transferred: number;
  docs_per_sec: number;
  start_time: string;
  estimated_end?: string;
  errors?: string[];
}

// ─── AI Analysis ────────────────────────────────────────────────────────────

export interface AIInsight {
  id: string;
  type: string;
  severity: string;
  title: string;
  summary: string;
  details?: string;
  suggestions?: string[];
  related_logs?: string[];
  cluster?: string;
  confidence: number;
  timestamp: string;
}

// ─── AI Chat & RCA ─────────────────────────────────────────────────────────

export interface AIChatMessage {
  id: string;
  role: 'user' | 'assistant';
  content: string;
  timestamp: string;
  type?: string;
  tokens_used?: number;
  duration?: number;
}

export interface RCAReport {
  id: string;
  timestamp: string;
  cluster: string;
  severity: 'critical' | 'high' | 'medium' | 'low' | 'info';
  category: string;
  root_cause: string;
  evidence: string[];
  remediation: RemediationStep[];
  status: 'pending' | 'analyzing' | 'complete' | 'action_taken';
  confidence: number;
}

export interface RemediationStep {
  order: number;
  action: string;
  description: string;
  command?: string;
  risk: 'low' | 'medium' | 'high';
}

export interface KnowledgeBaseEntry {
  id: string;
  category: string;
  title: string;
  description: string;
  symptoms: string[];
  solution: string;
  commands?: string[];
  severity: string;
  tags: string[];
}

// ─── Dashboard State ────────────────────────────────────────────────────────

export interface DashboardState {
  clusters: Record<string, ClusterMetrics>;
  source_cluster: ClusterMetrics;
  target_cluster: ClusterMetrics;
  xdcr_status: XDCRStatus;
  data_loss_proof: DataLossProof;
  integrity_result: IntegrityResult;
  storm_metrics: StormMetrics;
  upgrade_status: UpgradeStatus;
  alerts: Alert[];
  regions?: RegionStatus[];
  failover_status?: FailoverStatus;
  backup_status?: BackupStatus;
  migration_status?: MigrationStatus;
  ai_insights?: AIInsight[];
  timestamp: string;
}

export interface Command {
  action: string;
  params?: Record<string, string>;
}
