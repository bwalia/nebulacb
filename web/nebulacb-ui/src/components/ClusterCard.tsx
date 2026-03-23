import React from 'react';
import { ClusterMetrics } from '../types';

interface Props {
  cluster: ClusterMetrics;
  role: 'source' | 'target';
  upgrading?: boolean;
}

export const ClusterCard: React.FC<Props> = ({ cluster, role, upgrading }) => {
  const accentColor = role === 'source' ? '#00aaff' : '#aa88ff';
  const healthy = cluster.healthy;

  return (
    <div className="cluster-card" style={{ borderTopColor: accentColor }}>
      <div className="cluster-card-header">
        <div className="cluster-role" style={{ color: accentColor }}>
          {role === 'source' ? 'SOURCE' : 'TARGET'} CLUSTER
        </div>
        <div className="cluster-name">{cluster.cluster_name || role}</div>
        <div className={`cluster-health-badge ${healthy ? 'healthy' : 'unhealthy'}`}>
          {healthy ? 'HEALTHY' : 'DEGRADED'}
        </div>
      </div>

      <div className="cluster-version-bar">
        <span className="version-label">v{cluster.version || '—'}</span>
        {upgrading && <span className="upgrading-badge">UPGRADING</span>}
        {cluster.rebalance_state && cluster.rebalance_state !== 'none' && (
          <span className="rebalance-badge">REBALANCING</span>
        )}
      </div>

      {/* Key Metrics Grid */}
      <div className="cluster-metrics-grid">
        <MetricTile label="Documents" value={fmt(cluster.total_docs)} color={accentColor} large />
        <MetricTile label="Ops/sec" value={fmtNum(cluster.ops_per_sec)} color="#00ff88" />
        <MetricTile label="GET/sec" value={fmtNum(cluster.cmd_get_per_sec)} color="#00ff88" />
        <MetricTile label="SET/sec" value={fmtNum(cluster.cmd_set_per_sec)} color="#ffaa00" />
        <MetricTile
          label="Resident Ratio"
          value={`${(cluster.vb_active_resident_items_ratio || 0).toFixed(1)}%`}
          color={cluster.vb_active_resident_items_ratio > 90 ? '#00ff88' : '#ff4444'}
        />
        <MetricTile
          label="Disk Write Q"
          value={fmtNum(cluster.disk_write_queue)}
          color={cluster.disk_write_queue > 10000 ? '#ff4444' : '#00ff88'}
        />
        <MetricTile
          label="Cache Miss"
          value={fmtNum(cluster.ep_cache_miss_rate)}
          color={cluster.ep_cache_miss_rate > 10 ? '#ffaa00' : '#00ff88'}
        />
        <MetricTile
          label="Memory"
          value={`${((cluster.total_mem_used_mb / Math.max(cluster.total_mem_total_mb, 1)) * 100).toFixed(0)}%`}
          color={cluster.total_mem_used_mb / Math.max(cluster.total_mem_total_mb, 1) > 0.85 ? '#ff4444' : '#00ff88'}
        />
      </div>

      {/* Nodes */}
      <div className="cluster-nodes">
        <div className="nodes-header">Nodes ({cluster.nodes?.length || 0})</div>
        <div className="nodes-list">
          {cluster.nodes?.map((node, i) => (
            <div key={i} className="node-pill">
              <span
                className="node-dot"
                style={{
                  backgroundColor:
                    node.status === 'healthy' ? '#00ff88' :
                    node.status === 'warmup' ? '#ffaa00' : '#ff4444'
                }}
              />
              <span className="node-pill-host">{node.host?.split(':')[0] || `n${i}`}</span>
              <span className="node-pill-version">v{node.version?.split('-')[0]}</span>
              <span className="node-pill-cpu">{node.cpu_usage?.toFixed(0)}%</span>
            </div>
          ))}
        </div>
      </div>
    </div>
  );
};

const MetricTile: React.FC<{
  label: string; value: string; color: string; large?: boolean;
}> = ({ label, value, color, large }) => (
  <div className={`metric-tile ${large ? 'large' : ''}`}>
    <div className="metric-tile-value" style={{ color }}>{value}</div>
    <div className="metric-tile-label">{label}</div>
  </div>
);

function fmt(n: number): string {
  if (n >= 1_000_000) return (n / 1_000_000).toFixed(2) + 'M';
  if (n >= 1_000) return (n / 1_000).toFixed(1) + 'K';
  return String(n || 0);
}

function fmtNum(n: number): string {
  if (!n) return '0';
  if (n >= 1_000_000) return (n / 1_000_000).toFixed(1) + 'M';
  if (n >= 1_000) return (n / 1_000).toFixed(1) + 'K';
  return n.toFixed(0);
}
