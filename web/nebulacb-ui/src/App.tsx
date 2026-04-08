import React, { useCallback, useEffect, useState } from 'react';
import { useWebSocket, apiCommand, checkAuthRequired, getToken, logout } from './hooks/useWebSocket';
import { ClusterCard } from './components/ClusterCard';
import { XDCRFlowPanel } from './components/XDCRFlowPanel';
import { DataLossProofPanel } from './components/DataLossProofPanel';
import { LoadPanel } from './components/LoadPanel';
import { AlertPanel } from './components/AlertPanel';
import { ControlPanel } from './components/ControlPanel';
import { LoginPage } from './components/LoginPage';
import { AskAIPanel } from './components/AskAIPanel';
import { RCAPanel } from './components/RCAPanel';
import { KnowledgeBasePanel } from './components/KnowledgeBasePanel';
import { AIInsightsPanel } from './components/AIInsightsPanel';
import {
  Command, ClusterMetrics, XDCRStatus, DataLossProof, StormMetrics, UpgradeStatus,
  RegionStatus, FailoverStatus, BackupStatus, MigrationStatus, AIInsight,
} from './types';
import './App.css';

type TabKey = 'dashboard' | 'ask-ai' | 'rca' | 'knowledge' | 'insights';

const emptyCluster: ClusterMetrics = {
  cluster_name: '', nodes: [], rebalance_state: 'none', total_docs: 0,
  total_mem_used_mb: 0, total_mem_total_mb: 0, ops_per_sec: 0,
  cmd_get_per_sec: 0, cmd_set_per_sec: 0, disk_write_queue: 0,
  ep_bg_fetched: 0, ep_cache_miss_rate: 0, vb_active_resident_items_ratio: 0,
  curr_connections: 0, version: '', healthy: false,
};

const emptyXDCR: XDCRStatus = {
  replication_id: '', state: 'Unknown', changes_left: 0, docs_processed: 0,
  replication_lag_ms: 0, pipeline_restarts: 0, topology_change: false,
  goxdcr_delay_start: '', goxdcr_delay_end: '', mutation_queue: 0,
};

const emptyProof: DataLossProof = {
  timeline: [], current_source_docs: 0, current_target_docs: 0, current_delta: 0,
  max_delta_observed: 0, max_delta_time: '', converged_at: '', is_converged: false,
  zero_loss_confirmed: false, hash_samples_passed: 0, hash_samples_failed: 0,
  last_hash_check_time: '', monitoring_duration: '',
};

const emptyStorm: StormMetrics = {
  writes_per_sec: 0, reads_per_sec: 0, latency_p50_ms: 0, latency_p95_ms: 0,
  latency_p99_ms: 0, active_connections: 0, total_written: 0, total_read: 0, total_errors: 0,
};

const emptyUpgrade: UpgradeStatus = {
  phase: 'pending', current_node: '', nodes_completed: 0, nodes_total: 0,
  progress: 0, source_version: '7.2.2', target_version: '7.6.0', errors: [],
};

// Keep last 60 storm snapshots for chart
const stormHistory: StormMetrics[] = [];

function App() {
  const [authChecked, setAuthChecked] = useState(false);
  const [authRequired, setAuthRequired] = useState(false);
  const [authenticated, setAuthenticated] = useState(false);

  useEffect(() => {
    checkAuthRequired().then((required) => {
      setAuthRequired(required);
      setAuthenticated(!required || !!getToken());
      setAuthChecked(true);
    }).catch(() => {
      setAuthChecked(true);
      setAuthenticated(true);
    });
  }, []);

  const handleLogout = async () => {
    await logout();
    setAuthenticated(false);
  };

  if (!authChecked) {
    return (
      <div className="app">
        <div className="loading-screen">
          <span className="logo-icon loading-logo">&#x25C8;</span>
          <div>Loading NebulaCB...</div>
        </div>
      </div>
    );
  }

  if (authRequired && !authenticated) {
    return <LoginPage onLogin={() => setAuthenticated(true)} />;
  }

  return <Dashboard onLogout={authRequired ? handleLogout : undefined} />;
}

function Dashboard({ onLogout }: { onLogout?: () => void }) {
  const { state, connected } = useWebSocket();
  const [activeTab, setActiveTab] = useState<TabKey>('dashboard');

  if (state?.storm_metrics) {
    stormHistory.push(state.storm_metrics);
    if (stormHistory.length > 60) stormHistory.splice(0, stormHistory.length - 60);
  }

  const handleCommand = useCallback(async (cmd: Command) => {
    try {
      const result = await apiCommand(cmd);
      if (result.status === 'error') {
        alert(`Command failed: ${result.message}`);
      }
    } catch (e) {
      alert(`Command failed: ${e}`);
    }
  }, []);

  const clusters = state?.clusters || {};
  const source = state?.source_cluster || emptyCluster;
  const target = state?.target_cluster || emptyCluster;
  const xdcr = state?.xdcr_status || emptyXDCR;
  const proof = state?.data_loss_proof || emptyProof;
  const storm = state?.storm_metrics || emptyStorm;
  const upgrade = state?.upgrade_status || emptyUpgrade;
  const alerts = state?.alerts || [];
  const regions = state?.regions || [];
  const failoverStatus = state?.failover_status;
  const backupStatus = state?.backup_status;
  const migrationStatus = state?.migration_status;
  const aiInsights = state?.ai_insights || [];
  const upgrading = upgrade.phase === 'upgrading' || upgrade.phase === 'rebalancing';

  // Build ordered cluster list
  const clusterEntries = Object.entries(clusters);
  const sortedClusters = [
    ...clusterEntries.filter(([, c]) => c.cluster_name === source.cluster_name && source.cluster_name),
    ...clusterEntries.filter(([, c]) => c.cluster_name === target.cluster_name && target.cluster_name && c.cluster_name !== source.cluster_name),
    ...clusterEntries.filter(([, c]) => c.cluster_name !== source.cluster_name && c.cluster_name !== target.cluster_name),
  ];
  const seen = new Set<string>();
  const uniqueClusters = sortedClusters.filter(([name]) => {
    if (seen.has(name)) return false;
    seen.add(name);
    return true;
  });
  const clusterCount = uniqueClusters.length || 2;

  return (
    <div className="app">
      {/* Header */}
      <header className="header">
        <div className="header-left">
          <span className="logo-icon">&#x25C8;</span>
          <span className="logo-text">NebulaCB</span>
          <span className="tagline">Complete Couchbase Management Platform</span>
        </div>
        <div className="header-right">
          <span className={`conn-badge ${connected ? 'live' : 'off'}`}>
            {connected ? 'LIVE' : 'OFFLINE'}
          </span>
          {clusterCount > 0 && (
            <span className="cluster-count-badge">{clusterCount} CLUSTERS</span>
          )}
          {regions.length > 0 && (
            <span className="cluster-count-badge" style={{ background: '#1a4a1a', color: '#00ff88' }}>
              {regions.length} REGIONS
            </span>
          )}
          {upgrading && <span className="upgrade-badge-header">UPGRADE IN PROGRESS</span>}
          {migrationStatus && migrationStatus.status === 'running' && (
            <span className="upgrade-badge-header" style={{ background: '#1a2a4a' }}>
              MIGRATING {migrationStatus.progress.toFixed(0)}%
            </span>
          )}
          <span className="header-time">
            {state?.timestamp ? new Date(state.timestamp).toLocaleTimeString() : '--:--:--'}
          </span>
          {onLogout && (
            <button className="logout-btn" onClick={onLogout} title="Sign out">
              Logout
            </button>
          )}
        </div>
      </header>

      {/* Tab Navigation */}
      <nav className="tab-nav">
        {([
          ['dashboard', 'Dashboard', '\u25C8'],
          ['ask-ai', 'Ask AI', '\uD83E\uDD16'],
          ['rca', 'RCA', '\uD83D\uDD0D'],
          ['knowledge', 'Knowledge Base', '\uD83D\uDCDA'],
          ['insights', 'AI Insights', '\uD83D\uDCCA'],
        ] as [TabKey, string, string][]).map(([key, label, icon]) => (
          <button
            key={key}
            className={`tab-btn ${activeTab === key ? 'active' : ''}`}
            onClick={() => setActiveTab(key)}
          >
            <span className="tab-icon">{icon}</span>
            <span className="tab-label">{label}</span>
          </button>
        ))}
      </nav>

      <main className="main-layout">
        {/* AI Tabs */}
        {activeTab === 'ask-ai' && <AskAIPanel clusters={clusters} />}
        {activeTab === 'rca' && <RCAPanel clusters={clusters} />}
        {activeTab === 'knowledge' && <KnowledgeBasePanel />}
        {activeTab === 'insights' && <AIInsightsPanel />}

        {/* Dashboard Tab */}
        {activeTab === 'dashboard' && <>
        {/* Region Bar (if multi-region) */}
        {regions.length > 0 && (
          <section className="region-bar">
            <div className="panel-header">
              <span className="panel-indicator" style={{ backgroundColor: '#00cc88' }} />
              REGIONS
            </div>
            <div className="region-cards">
              {regions.map((r: RegionStatus) => (
                <div key={r.name} className={`region-card ${r.status}`}>
                  <div className="region-name">
                    {r.display_name || r.name}
                    {r.primary && <span className="primary-badge">PRIMARY</span>}
                  </div>
                  <div className="region-meta">
                    {r.provider && <span className="region-provider">{r.provider}</span>}
                    <span className={`region-status-dot ${r.status}`} />
                    {r.cluster_count} clusters ({r.healthy_clusters} healthy)
                  </div>
                  <div className="region-stats">
                    <span>{r.total_docs?.toLocaleString()} docs</span>
                    <span>{r.ops_per_sec?.toFixed(0)} ops/s</span>
                  </div>
                </div>
              ))}
            </div>
          </section>
        )}

        {/* Row 1: All Connected Clusters */}
        <section className="clusters-row" style={{
          gridTemplateColumns: `repeat(${Math.min(clusterCount, 3)}, 1fr)`
        }}>
          {uniqueClusters.length > 0 ? (
            uniqueClusters.map(([name, cm]) => (
              <ClusterCard
                key={name}
                cluster={cm}
                role={name === source.cluster_name ? 'source' : name === target.cluster_name ? 'target' : 'source'}
                upgrading={upgrading && name === source.cluster_name}
              />
            ))
          ) : (
            <>
              <ClusterCard cluster={source} role="source" upgrading={upgrading} />
              <ClusterCard cluster={target} role="target" />
            </>
          )}
        </section>

        {/* Row 2: XDCR Flow between clusters */}
        <section className="xdcr-row">
          <XDCRFlowPanel xdcr={xdcr} sourceDocs={source.total_docs} targetDocs={target.total_docs} />
        </section>

        {/* Row 3: Data Loss Proof + Load + Alerts + New Panels */}
        <section className="proof-row">
          <div className="proof-col">
            <DataLossProofPanel proof={proof} />
          </div>
          <div className="side-col">
            {/* Upgrade Progress */}
            {upgrade.phase !== 'pending' && (
              <div className="upgrade-mini-panel">
                <div className="panel-header">
                  <span className="panel-indicator" style={{
                    backgroundColor: upgrade.phase === 'completed' ? '#00ff88' :
                      upgrade.phase === 'failed' ? '#ff4444' : '#00aaff'
                  }} />
                  UPGRADE: {upgrade.source_version} &rarr; {upgrade.target_version}
                </div>
                <div className="panel-body">
                  <div className="upgrade-phase" style={{
                    color: upgrade.phase === 'completed' ? '#00ff88' :
                      upgrade.phase === 'failed' ? '#ff4444' : '#00aaff'
                  }}>
                    {upgrade.phase.toUpperCase()}
                  </div>
                  <div className="progress-bar">
                    <div className="progress-fill" style={{ width: `${upgrade.progress}%` }} />
                  </div>
                  <div className="upgrade-nodes">
                    {upgrade.nodes_completed}/{upgrade.nodes_total} nodes
                    {upgrade.current_node && <span> | Current: {upgrade.current_node}</span>}
                  </div>
                </div>
              </div>
            )}

            {/* Migration Progress */}
            {migrationStatus && migrationStatus.status === 'running' && (
              <div className="upgrade-mini-panel">
                <div className="panel-header">
                  <span className="panel-indicator" style={{ backgroundColor: '#00aaff' }} />
                  MIGRATION: {migrationStatus.source_cluster} &rarr; {migrationStatus.target_cluster}
                </div>
                <div className="panel-body">
                  <div className="upgrade-phase" style={{ color: '#00aaff' }}>
                    {migrationStatus.status.toUpperCase()} ({migrationStatus.progress.toFixed(1)}%)
                  </div>
                  <div className="progress-bar">
                    <div className="progress-fill" style={{ width: `${migrationStatus.progress}%` }} />
                  </div>
                  <div className="upgrade-nodes">
                    {migrationStatus.migrated_docs?.toLocaleString()}/{migrationStatus.total_docs?.toLocaleString()} docs
                    | {migrationStatus.docs_per_sec?.toFixed(0)} docs/s
                    {migrationStatus.failed_docs > 0 && (
                      <span style={{ color: '#ff4444' }}> | {migrationStatus.failed_docs} failed</span>
                    )}
                  </div>
                </div>
              </div>
            )}

            {/* HA / Failover Status */}
            {failoverStatus && failoverStatus.auto_failover_enabled && (
              <div className="upgrade-mini-panel">
                <div className="panel-header">
                  <span className="panel-indicator" style={{ backgroundColor: '#ff8800' }} />
                  HA / FAILOVER
                </div>
                <div className="panel-body" style={{ fontSize: 11 }}>
                  <div>Mode: <span style={{ color: '#00aaff' }}>{failoverStatus.mode}</span></div>
                  {failoverStatus.primary_cluster && (
                    <div>Primary: <span style={{ color: '#00ff88' }}>{failoverStatus.primary_cluster}</span></div>
                  )}
                  {failoverStatus.cluster_states && Object.entries(failoverStatus.cluster_states).map(([name, st]) => (
                    <div key={name} style={{ color: st === 'active' ? '#00ff88' : st === 'failed' ? '#ff4444' : '#aaa' }}>
                      {name}: {st}
                    </div>
                  ))}
                </div>
              </div>
            )}

            {/* Backup Status */}
            {backupStatus && backupStatus.last_backup && (
              <div className="upgrade-mini-panel">
                <div className="panel-header">
                  <span className="panel-indicator" style={{ backgroundColor: '#00cc88' }} />
                  BACKUP
                </div>
                <div className="panel-body" style={{ fontSize: 11 }}>
                  <div>Last: <span style={{ color: backupStatus.last_backup.status === 'completed' ? '#00ff88' : '#ff4444' }}>
                    {backupStatus.last_backup.status}
                  </span> ({backupStatus.last_backup.cluster_name})</div>
                  {backupStatus.last_backup.duration_seconds > 0 && (
                    <div>Duration: {backupStatus.last_backup.duration_seconds.toFixed(1)}s</div>
                  )}
                </div>
              </div>
            )}

            {/* AI Insights */}
            {aiInsights.length > 0 && (
              <div className="upgrade-mini-panel">
                <div className="panel-header">
                  <span className="panel-indicator" style={{ backgroundColor: '#ff88ff' }} />
                  AI INSIGHTS ({aiInsights.length})
                </div>
                <div className="panel-body" style={{ fontSize: 11, maxHeight: 120, overflowY: 'auto' }}>
                  {aiInsights.slice(-3).reverse().map((insight: AIInsight) => (
                    <div key={insight.id} style={{
                      padding: '4px 0',
                      borderBottom: '1px solid #333',
                      color: insight.severity === 'critical' ? '#ff4444' :
                        insight.severity === 'warning' ? '#ffaa00' : '#aaa'
                    }}>
                      <div style={{ fontWeight: 'bold' }}>{insight.title}</div>
                      <div>{insight.summary?.substring(0, 100)}...</div>
                      {insight.suggestions && insight.suggestions.length > 0 && (
                        <div style={{ color: '#00aaff', fontSize: 10 }}>
                          Suggestion: {insight.suggestions[0]}
                        </div>
                      )}
                    </div>
                  ))}
                </div>
              </div>
            )}

            <LoadPanel metrics={storm} history={stormHistory} />
            <AlertPanel alerts={alerts} />
          </div>
        </section>

        {/* Control Bar */}
        <ControlPanel
          onCommand={handleCommand}
          clusters={clusters}
          xdcrStatus={state?.xdcr_status}
        />
        </>}
      </main>

      <footer className="footer">
        <span>NebulaCB v1.0</span>
        <span>The Complete Couchbase Management Platform — Upgrade · Migrate · Monitor · Protect</span>
      </footer>
    </div>
  );
}

export default App;
