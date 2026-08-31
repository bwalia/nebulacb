import React, { useCallback, useEffect, useState } from 'react';
import { useWebSocket, apiCommand, checkAuthRequired, getToken, logout } from './hooks/useWebSocket';
import { ClusterCard } from './components/ClusterCard';
import { XDCRFlowPanel } from './components/XDCRFlowPanel';
import { DataLossProofPanel } from './components/DataLossProofPanel';
import { LoadPanel } from './components/LoadPanel';
import { AlertPanel } from './components/AlertPanel';
import { ControlPanel } from './components/ControlPanel';
import { LoginPage } from './components/LoginPage';
import { Command, ClusterMetrics, XDCRStatus, DataLossProof, StormMetrics, UpgradeStatus } from './types';
import './App.css';

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
      setAuthenticated(true); // If can't reach server, show dashboard (will fail gracefully)
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

  if (state?.storm_metrics) {
    stormHistory.push(state.storm_metrics);
    if (stormHistory.length > 60) stormHistory.splice(0, stormHistory.length - 60);
  }

  const handleCommand = useCallback(async (cmd: Command) => {
    try {
      await apiCommand(cmd);
    } catch (e) {
      console.error(`[CMD] ${cmd.action} failed:`, e);
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
  const upgrading = upgrade.phase === 'upgrading' || upgrade.phase === 'rebalancing';

  // Build ordered cluster list: source first, target second, then extras
  const clusterEntries = Object.entries(clusters);
  const sortedClusters = [
    ...clusterEntries.filter(([, c]) => c.cluster_name === source.cluster_name && source.cluster_name),
    ...clusterEntries.filter(([, c]) => c.cluster_name === target.cluster_name && target.cluster_name && c.cluster_name !== source.cluster_name),
    ...clusterEntries.filter(([, c]) => c.cluster_name !== source.cluster_name && c.cluster_name !== target.cluster_name),
  ];
  // Deduplicate
  const seen = new Set<string>();
  const uniqueClusters = sortedClusters.filter(([name]) => {
    if (seen.has(name)) return false;
    seen.add(name);
    return true;
  });
  const clusterCount = uniqueClusters.length || 2; // fallback for source/target

  return (
    <div className="app">
      {/* Header */}
      <header className="header">
        <div className="header-left">
          <span className="logo-icon">&#x25C8;</span>
          <span className="logo-text">NebulaCB</span>
          <span className="tagline">Migration &amp; Upgrade Mission Control</span>
        </div>
        <div className="header-right">
          <span className={`conn-badge ${connected ? 'live' : 'off'}`}>
            {connected ? 'LIVE' : 'OFFLINE'}
          </span>
          {clusterCount > 0 && (
            <span className="cluster-count-badge">{clusterCount} CLUSTERS</span>
          )}
          {upgrading && <span className="upgrade-badge-header">UPGRADE IN PROGRESS</span>}
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

      <main className="main-layout">
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

        {/* Row 3: Data Loss Proof + Load + Alerts */}
        <section className="proof-row">
          <div className="proof-col">
            <DataLossProofPanel proof={proof} />
          </div>
          <div className="side-col">
            {/* Upgrade Progress (if active) */}
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

            <LoadPanel metrics={storm} history={stormHistory} />
            <AlertPanel alerts={alerts} />
          </div>
        </section>

        {/* Control Bar */}
        <ControlPanel onCommand={handleCommand} />
      </main>

      <footer className="footer">
        <span>NebulaCB v0.1.0</span>
        <span>Upgrade Fearlessly. Validate Everything. Lose Nothing.</span>
      </footer>
    </div>
  );
}

export default App;
