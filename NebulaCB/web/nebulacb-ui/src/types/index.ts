export interface NodeStatus {
  name: string;
  host: string;
  status: string;
  version: string;
  memory_usage: number;
  cpu_usage: number;
  disk_usage: number;
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
  timestamp: string;
}

export interface Command {
  action: string;
  params?: Record<string, string>;
}
