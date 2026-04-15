import React, { useState, useEffect } from 'react';
import { Command, ClusterMetrics, XDCRStatus } from '../types';
import { getToken } from '../hooks/useWebSocket';

interface Props {
  onCommand: (cmd: Command) => void;
  clusters?: Record<string, ClusterMetrics>;
  xdcrStatus?: XDCRStatus;
}

const COUCHBASE_VERSIONS = [
  '7.6.10', '7.6.9', '7.6.8', '7.6.7', '7.6.6',
  '7.6.5', '7.6.4', '7.6.3', '7.6.2', '7.6.1', '7.6.0',
  '7.2.6', '7.2.5', '7.2.4', '7.2.3', '7.2.2', '7.2.1', '7.2.0',
  '7.1.6', '7.1.5', '7.1.4', '7.1.3', '7.1.2', '7.1.1', '7.1.0',
  '7.0.5', '7.0.4', '7.0.3', '7.0.2',
];

const buttons = [
  { label: 'Start Load', action: 'start_load', icon: '\u25B6', color: '#00ff88', group: 'load' },
  { label: 'Pause Load', action: 'pause_load', icon: '\u23F8', color: '#ffaa00', group: 'load' },
  { label: 'Resume Load', action: 'resume_load', icon: '\u25B6', color: '#00ff88', group: 'load' },
  { label: 'Stop Load', action: 'stop_load', icon: '\u23F9', color: '#ff4444', group: 'load' },
  { label: 'Start Upgrade', action: 'start_upgrade', icon: '\uD83D\uDE80', color: '#00aaff', group: 'upgrade' },
  { label: 'Abort Upgrade', action: 'abort_upgrade', icon: '\uD83D\uDED1', color: '#ff4444', group: 'upgrade' },
  { label: 'Downgrade', action: 'downgrade', icon: '\u23EA', color: '#ff6600', group: 'upgrade' },
  { label: 'Pause XDCR', action: 'pause_xdcr', icon: '\u23F8', color: '#ffaa00', group: 'replication' },
  { label: 'Resume XDCR', action: 'resume_xdcr', icon: '\u25B6', color: '#00ff88', group: 'replication' },
  { label: 'Stop XDCR', action: 'stop_xdcr', icon: '\u23F9', color: '#ff4444', group: 'replication' },
  { label: 'Restart XDCR', action: 'restart_xdcr', icon: '\uD83D\uDD01', color: '#00aaff', group: 'replication' },
  { label: 'XDCR Troubleshoot', action: 'xdcr_troubleshoot', icon: '\uD83D\uDEE0', color: '#ff88ff', group: 'replication' },
  { label: 'Full Audit', action: 'run_audit', icon: '\uD83D\uDD0D', color: '#aa88ff', group: 'validation' },
  { label: 'Inject Failure', action: 'inject_failure', icon: '\uD83D\uDCA5', color: '#ff6600', group: 'chaos' },
  { label: 'AI Analyze', action: 'ai_analyze', icon: '\uD83E\uDDE0', color: '#ff88ff', group: 'ai' },
  { label: 'Backup', action: 'start_backup', icon: '\uD83D\uDCBE', color: '#00cc88', group: 'backup' },
  { label: 'Restore', action: 'start_restore', icon: '\u21A9', color: '#00aaff', group: 'backup' },
  { label: 'Failover', action: 'manual_failover', icon: '\u26A0\uFE0F', color: '#ff8800', group: 'ha' },
];

function apiBase(): string {
  if (process.env.REACT_APP_API_URL) return process.env.REACT_APP_API_URL;
  return window.location.origin;
}

interface DiagnosticCheck {
  name: string;
  title: string;
  status: string;
  detail: string;
}

interface DiagnosticsResult {
  timestamp: string;
  replication_id: string;
  overall_status: string;
  checks: DiagnosticCheck[];
  delay_windows?: { start: string; end: string; duration: number; cause: string }[];
  current_status?: {
    state: string;
    changes_left: number;
    docs_processed: number;
    topology_change: boolean;
  };
}

export const ControlPanel: React.FC<Props> = ({ onCommand, clusters, xdcrStatus }) => {
  const [showUpgradeModal, setShowUpgradeModal] = useState(false);
  const [showDowngradeModal, setShowDowngradeModal] = useState(false);
  const [showBackupModal, setShowBackupModal] = useState(false);
  const [showRestoreModal, setShowRestoreModal] = useState(false);
  const [availableBackups, setAvailableBackups] = useState<Array<{ id: string; cluster_name: string; size: string; mode?: string; docs_exported?: number }>>([]);
  const [selectedBackupID, setSelectedBackupID] = useState('');
  const [restoreTarget, setRestoreTarget] = useState('');
  const [showFailoverModal, setShowFailoverModal] = useState(false);
  const [showXDCRTroubleshoot, setShowXDCRTroubleshoot] = useState(false);
  const [diagnostics, setDiagnostics] = useState<DiagnosticsResult | null>(null);
  const [diagLoading, setDiagLoading] = useState(false);
  const [selectedCluster, setSelectedCluster] = useState('');
  const [selectedVersion, setSelectedVersion] = useState(COUCHBASE_VERSIONS[0]);
  const [selectedNamespace, setSelectedNamespace] = useState('couchbase');
  const [failoverSource, setFailoverSource] = useState('');
  const [failoverTarget, setFailoverTarget] = useState('');
  const [fetchedClusters, setFetchedClusters] = useState<Record<string, ClusterMetrics>>({});

  // Fetch clusters from REST API when any modal opens
  useEffect(() => {
    if (!showUpgradeModal && !showBackupModal && !showFailoverModal) return;
    const headers: Record<string, string> = { 'Content-Type': 'application/json' };
    const token = getToken();
    if (token) headers['Authorization'] = `Bearer ${token}`;

    fetch(`${apiBase()}/api/v1/dashboard`, { headers })
      .then(r => r.json())
      .then(data => {
        const c = data.clusters || {};
        setFetchedClusters(c);
        const keys = Object.keys(c);
        if (keys.length > 0 && !selectedCluster) {
          setSelectedCluster(keys[0]);
        }
      })
      .catch(() => {});
  }, [showUpgradeModal, showBackupModal, showFailoverModal]); // eslint-disable-line react-hooks/exhaustive-deps

  const effectiveClusters = Object.keys(fetchedClusters).length > 0 ? fetchedClusters : (clusters || {});
  const clusterList = Object.entries(effectiveClusters);

  const fetchDiagnostics = () => {
    setDiagLoading(true);
    const headers: Record<string, string> = { 'Content-Type': 'application/json' };
    const token = getToken();
    if (token) headers['Authorization'] = `Bearer ${token}`;

    fetch(`${apiBase()}/api/v1/xdcr/diagnostics`, { headers })
      .then(r => r.json())
      .then(data => {
        setDiagnostics(data as DiagnosticsResult);
        setDiagLoading(false);
      })
      .catch(() => setDiagLoading(false));
  };

  const handleButtonClick = (action: string) => {
    if (action === 'start_upgrade') {
      setShowUpgradeModal(true);
    } else if (action === 'downgrade') {
      setShowDowngradeModal(true);
    } else if (action === 'start_backup') {
      setShowBackupModal(true);
    } else if (action === 'start_restore') {
      setShowRestoreModal(true);
      const headers: Record<string, string> = { 'Content-Type': 'application/json' };
      const token = getToken();
      if (token) headers['Authorization'] = `Bearer ${token}`;
      fetch(`${apiBase()}/api/v1/backup/list`, { headers })
        .then(r => r.json())
        .then((list: Array<{ id: string; cluster_name: string; size: string; mode?: string; docs_exported?: number; status?: string }>) => {
          const completed = (list || []).filter((b) => !b.status || b.status === 'completed');
          setAvailableBackups(completed);
          if (completed.length > 0) setSelectedBackupID(completed[completed.length - 1].id);
        })
        .catch(() => setAvailableBackups([]));
    } else if (action === 'manual_failover') {
      setShowFailoverModal(true);
    } else if (action === 'xdcr_troubleshoot') {
      setShowXDCRTroubleshoot(true);
      fetchDiagnostics();
    } else if (action === 'inject_failure') {
      onCommand({ action, params: { type: 'xdcr_partition', target: 'replication' } });
    } else {
      onCommand({ action });
    }
  };

  const handleUpgradeSubmit = () => {
    if (!selectedCluster || !selectedVersion || !selectedNamespace) return;
    onCommand({
      action: 'start_upgrade',
      params: {
        cluster_name: selectedCluster,
        target_version: selectedVersion,
        namespace: selectedNamespace,
      },
    });
    setShowUpgradeModal(false);
  };

  const handleBackupSubmit = () => {
    if (!selectedCluster) return;
    onCommand({
      action: 'start_backup',
      params: { cluster_name: selectedCluster },
    });
    setShowBackupModal(false);
  };

  const handleRestoreSubmit = () => {
    if (!selectedBackupID || !restoreTarget) return;
    onCommand({
      action: 'start_restore',
      params: { backup_id: selectedBackupID, target_cluster: restoreTarget },
    });
    setShowRestoreModal(false);
  };

  const handleFailoverSubmit = () => {
    if (!failoverSource || !failoverTarget) return;
    onCommand({
      action: 'manual_failover',
      params: { source_cluster: failoverSource, target_cluster: failoverTarget },
    });
    setShowFailoverModal(false);
  };

  const currentVersion = selectedCluster && effectiveClusters[selectedCluster]?.version
    ? effectiveClusters[selectedCluster].version
    : '';

  return (
    <>
      <div className="control-panel">
        <div className="panel-header">
          <span className="panel-indicator" style={{ backgroundColor: '#aa88ff' }} />
          MISSION CONTROL
        </div>
        <div className="control-buttons">
          {buttons.map((btn) => (
            <button
              key={btn.action}
              className="control-btn"
              style={{ '--btn-color': btn.color } as React.CSSProperties}
              onClick={() => handleButtonClick(btn.action)}
            >
              <span className="btn-icon">{btn.icon}</span>
              <span className="btn-label">{btn.label}</span>
            </button>
          ))}
        </div>
      </div>

      {/* Upgrade Modal */}
      {showUpgradeModal && (
        <div className="modal-overlay" onClick={() => setShowUpgradeModal(false)}>
          <div className="modal-dialog" onClick={(e) => e.stopPropagation()}>
            <div className="modal-header">
              <span className="modal-icon">{'\uD83D\uDE80'}</span>
              Start Cluster Upgrade
            </div>
            <div className="modal-body">
              <div className="modal-field">
                <label className="modal-label">Cluster</label>
                {clusterList.length === 0 ? (
                  <div className="modal-warning">Loading clusters...</div>
                ) : (
                  <select
                    className="modal-select"
                    value={selectedCluster}
                    onChange={(e) => setSelectedCluster(e.target.value)}
                  >
                    <option value="">-- Select cluster --</option>
                    {clusterList.map(([name, cm]) => (
                      <option key={name} value={name}>
                        {name} {cm.version ? `(v${cm.version})` : ''} {cm.edition ? `[${cm.edition}]` : ''}
                      </option>
                    ))}
                  </select>
                )}
              </div>

              {selectedCluster && currentVersion && (
                <div className="modal-current-version">
                  Current version: <span className="version-tag">{currentVersion}</span>
                  {effectiveClusters[selectedCluster]?.edition && (
                    <span className="edition-tag" style={{
                      marginLeft: 8,
                      color: effectiveClusters[selectedCluster]?.edition === 'enterprise' ? '#00ff88' : '#ffaa00'
                    }}>
                      {effectiveClusters[selectedCluster]?.edition?.toUpperCase()}
                    </span>
                  )}
                </div>
              )}

              <div className="modal-field">
                <label className="modal-label">Target Version</label>
                <select
                  className="modal-select"
                  value={selectedVersion}
                  onChange={(e) => setSelectedVersion(e.target.value)}
                >
                  {COUCHBASE_VERSIONS.map((v) => (
                    <option key={v} value={v}>
                      Couchbase Server {v}
                      {v === currentVersion ? ' (current)' : ''}
                    </option>
                  ))}
                </select>
              </div>

              <div className="modal-field">
                <label className="modal-label">Kubernetes Namespace</label>
                <input
                  className="modal-select"
                  type="text"
                  value={selectedNamespace}
                  onChange={(e) => setSelectedNamespace(e.target.value)}
                  placeholder="e.g. couchbase"
                />
              </div>

              {selectedVersion && selectedVersion === currentVersion && (
                <div className="modal-warning">
                  Target version is the same as the current version.
                </div>
              )}
            </div>
            <div className="modal-footer">
              <button
                className="modal-btn modal-btn-cancel"
                onClick={() => setShowUpgradeModal(false)}
              >
                Cancel
              </button>
              <button
                className="modal-btn modal-btn-confirm"
                disabled={!selectedCluster || !selectedVersion || !selectedNamespace || selectedVersion === currentVersion}
                onClick={handleUpgradeSubmit}
              >
                Upgrade {selectedCluster || '...'} to {selectedVersion}
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Backup Modal */}
      {showBackupModal && (
        <div className="modal-overlay" onClick={() => setShowBackupModal(false)}>
          <div className="modal-dialog" onClick={(e) => e.stopPropagation()}>
            <div className="modal-header">
              <span className="modal-icon">{'\uD83D\uDCBE'}</span>
              Start Cluster Backup
            </div>
            <div className="modal-body">
              <div className="modal-field">
                <label className="modal-label">Cluster to Backup</label>
                {clusterList.length === 0 ? (
                  <div className="modal-warning">Loading clusters...</div>
                ) : (
                  <select
                    className="modal-select"
                    value={selectedCluster}
                    onChange={(e) => setSelectedCluster(e.target.value)}
                  >
                    <option value="">-- Select cluster --</option>
                    {clusterList.map(([name, cm]) => (
                      <option key={name} value={name}>
                        {name} ({cm.total_docs?.toLocaleString()} docs)
                      </option>
                    ))}
                  </select>
                )}
              </div>
            </div>
            <div className="modal-footer">
              <button className="modal-btn modal-btn-cancel" onClick={() => setShowBackupModal(false)}>
                Cancel
              </button>
              <button
                className="modal-btn modal-btn-confirm"
                disabled={!selectedCluster}
                onClick={handleBackupSubmit}
              >
                Start Backup
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Restore Modal */}
      {showRestoreModal && (
        <div className="modal-overlay" onClick={() => setShowRestoreModal(false)}>
          <div className="modal-dialog" onClick={(e) => e.stopPropagation()}>
            <div className="modal-header">
              <span className="modal-icon">{'\u21A9'}</span>
              Restore From Backup
            </div>
            <div className="modal-body">
              <div className="modal-field">
                <label className="modal-label">Backup to Restore</label>
                {availableBackups.length === 0 ? (
                  <div className="modal-warning">No completed backups available.</div>
                ) : (
                  <select
                    className="modal-select"
                    value={selectedBackupID}
                    onChange={(e) => setSelectedBackupID(e.target.value)}
                  >
                    <option value="">-- Select backup --</option>
                    {availableBackups.map((b) => (
                      <option key={b.id} value={b.id}>
                        {b.id} — {b.cluster_name}
                        {b.docs_exported ? ` (${b.docs_exported.toLocaleString()} docs, ${b.size})` : b.size ? ` (${b.size})` : ''}
                        {b.mode === 'ce-sdk' ? ' [CE]' : b.mode === 'ee-cbbackupmgr' ? ' [EE]' : ''}
                      </option>
                    ))}
                  </select>
                )}
              </div>
              <div className="modal-field">
                <label className="modal-label">Target Cluster</label>
                {clusterList.length === 0 ? (
                  <div className="modal-warning">Loading clusters...</div>
                ) : (
                  <select
                    className="modal-select"
                    value={restoreTarget}
                    onChange={(e) => setRestoreTarget(e.target.value)}
                  >
                    <option value="">-- Select target --</option>
                    {clusterList.map(([name, cm]) => (
                      <option key={name} value={name}>
                        {name} ({cm.total_docs?.toLocaleString()} docs)
                      </option>
                    ))}
                  </select>
                )}
              </div>
              <div className="modal-warning" style={{ marginTop: 8, fontSize: 11 }}>
                Restore will upsert all documents from the backup into the target cluster's bucket. Existing keys with the same ID will be overwritten.
              </div>
            </div>
            <div className="modal-footer">
              <button className="modal-btn modal-btn-cancel" onClick={() => setShowRestoreModal(false)}>
                Cancel
              </button>
              <button
                className="modal-btn modal-btn-confirm"
                disabled={!selectedBackupID || !restoreTarget}
                onClick={handleRestoreSubmit}
              >
                Start Restore
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Failover Modal */}
      {showFailoverModal && (
        <div className="modal-overlay" onClick={() => setShowFailoverModal(false)}>
          <div className="modal-dialog" onClick={(e) => e.stopPropagation()}>
            <div className="modal-header">
              <span className="modal-icon">{'\u26A0\uFE0F'}</span>
              Manual Failover
            </div>
            <div className="modal-body">
              <div className="modal-warning" style={{ marginBottom: 12 }}>
                This will promote the target cluster and mark the source as failed.
              </div>
              <div className="modal-field">
                <label className="modal-label">Source (failing) Cluster</label>
                <select
                  className="modal-select"
                  value={failoverSource}
                  onChange={(e) => setFailoverSource(e.target.value)}
                >
                  <option value="">-- Select source --</option>
                  {clusterList.map(([name]) => (
                    <option key={name} value={name}>{name}</option>
                  ))}
                </select>
              </div>
              <div className="modal-field">
                <label className="modal-label">Target (promote) Cluster</label>
                <select
                  className="modal-select"
                  value={failoverTarget}
                  onChange={(e) => setFailoverTarget(e.target.value)}
                >
                  <option value="">-- Select target --</option>
                  {clusterList.filter(([name]) => name !== failoverSource).map(([name]) => (
                    <option key={name} value={name}>{name}</option>
                  ))}
                </select>
              </div>
            </div>
            <div className="modal-footer">
              <button className="modal-btn modal-btn-cancel" onClick={() => setShowFailoverModal(false)}>
                Cancel
              </button>
              <button
                className="modal-btn modal-btn-confirm"
                style={{ backgroundColor: '#ff4444' }}
                disabled={!failoverSource || !failoverTarget}
                onClick={handleFailoverSubmit}
              >
                Execute Failover
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Downgrade Confirmation Modal */}
      {showDowngradeModal && (
        <div className="modal-overlay" onClick={() => setShowDowngradeModal(false)}>
          <div className="modal-dialog" onClick={(e) => e.stopPropagation()}>
            <div className="modal-header">
              <span className="modal-icon">{'\u23EA'}</span>
              Downgrade — Roll Back to Previous Version
            </div>
            <div className="modal-body">
              <div className="modal-warning" style={{ marginBottom: 12 }}>
                This will re-patch the CouchbaseCluster CR to the original image version,
                triggering the Operator to do a rolling restart with the previous version.
                This is a destructive operation that may cause temporary service disruption.
              </div>
              <div className="modal-current-version">
                The cluster will be rolled back from the upgrade target to the
                <span className="version-tag"> source version</span> configured in NebulaCB.
              </div>
              <div className="modal-warning" style={{ marginTop: 12, color: '#ff4444', borderColor: 'rgba(255,68,68,0.3)', background: 'rgba(255,68,68,0.08)' }}>
                Downgrading across major versions is not always supported by Couchbase.
                Ensure compatibility before proceeding.
              </div>
            </div>
            <div className="modal-footer">
              <button
                className="modal-btn modal-btn-cancel"
                onClick={() => setShowDowngradeModal(false)}
              >Cancel</button>
              <button
                className="modal-btn modal-btn-confirm"
                style={{ background: '#ff6600' }}
                onClick={() => {
                  onCommand({ action: 'downgrade' });
                  setShowDowngradeModal(false);
                }}
              >Confirm Downgrade</button>
            </div>
          </div>
        </div>
      )}

      {/* XDCR Troubleshoot Modal */}
      {showXDCRTroubleshoot && (
        <div className="modal-overlay" onClick={() => setShowXDCRTroubleshoot(false)}>
          <div className="modal-dialog" style={{ maxWidth: 700 }} onClick={(e) => e.stopPropagation()}>
            <div className="modal-header">
              <span className="modal-icon">{'\uD83D\uDEE0'}</span>
              XDCR Troubleshoot &amp; Diagnostics
            </div>
            <div className="modal-body" style={{ maxHeight: 500, overflowY: 'auto' }}>

              {/* Live XDCR Status */}
              {xdcrStatus && (
                <div style={{ marginBottom: 16 }}>
                  <div className="modal-label" style={{ marginBottom: 8, fontSize: 13, fontWeight: 600 }}>LIVE XDCR STATUS</div>
                  <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr 1fr', gap: 8 }}>
                    <div style={{
                      background: 'rgba(0,0,0,0.3)', padding: '8px 12px', borderRadius: 6,
                      borderLeft: `3px solid ${xdcrStatus.state === 'Running' ? '#00ff88' : xdcrStatus.state === 'Paused' ? '#ffaa00' : '#ff4444'}`
                    }}>
                      <div style={{ color: '#888', fontSize: 10, textTransform: 'uppercase' }}>State</div>
                      <div style={{ color: xdcrStatus.state === 'Running' ? '#00ff88' : xdcrStatus.state === 'Paused' ? '#ffaa00' : '#ff4444', fontWeight: 700 }}>
                        {xdcrStatus.state}
                      </div>
                    </div>
                    <div style={{ background: 'rgba(0,0,0,0.3)', padding: '8px 12px', borderRadius: 6, borderLeft: '3px solid #00aaff' }}>
                      <div style={{ color: '#888', fontSize: 10, textTransform: 'uppercase' }}>Changes Left</div>
                      <div style={{ color: '#fff', fontWeight: 700 }}>{(xdcrStatus.changes_left || 0).toLocaleString()}</div>
                    </div>
                    <div style={{ background: 'rgba(0,0,0,0.3)', padding: '8px 12px', borderRadius: 6, borderLeft: '3px solid #aa88ff' }}>
                      <div style={{ color: '#888', fontSize: 10, textTransform: 'uppercase' }}>Docs Processed</div>
                      <div style={{ color: '#fff', fontWeight: 700 }}>{(xdcrStatus.docs_processed || 0).toLocaleString()}</div>
                    </div>
                  </div>
                  <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr 1fr', gap: 8, marginTop: 8 }}>
                    <div style={{ background: 'rgba(0,0,0,0.3)', padding: '8px 12px', borderRadius: 6, borderLeft: '3px solid #00aaff' }}>
                      <div style={{ color: '#888', fontSize: 10, textTransform: 'uppercase' }}>Replication Lag</div>
                      <div style={{ color: '#fff', fontWeight: 700 }}>{xdcrStatus.replication_lag_ms?.toFixed(1) || 0}ms</div>
                    </div>
                    <div style={{ background: 'rgba(0,0,0,0.3)', padding: '8px 12px', borderRadius: 6, borderLeft: '3px solid #ffaa00' }}>
                      <div style={{ color: '#888', fontSize: 10, textTransform: 'uppercase' }}>Topology Change</div>
                      <div style={{ color: xdcrStatus.topology_change ? '#ffaa00' : '#00ff88', fontWeight: 700 }}>
                        {xdcrStatus.topology_change ? 'ACTIVE' : 'None'}
                      </div>
                    </div>
                    <div style={{ background: 'rgba(0,0,0,0.3)', padding: '8px 12px', borderRadius: 6, borderLeft: '3px solid #ff6600' }}>
                      <div style={{ color: '#888', fontSize: 10, textTransform: 'uppercase' }}>Pipeline Restarts</div>
                      <div style={{ color: '#fff', fontWeight: 700 }}>{xdcrStatus.pipeline_restarts || 0}</div>
                    </div>
                  </div>
                </div>
              )}

              {/* Diagnostic Checks */}
              <div style={{ marginBottom: 16 }}>
                <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 8 }}>
                  <div className="modal-label" style={{ fontSize: 13, fontWeight: 600, margin: 0 }}>DIAGNOSTIC CHECKS</div>
                  <button
                    className="control-btn"
                    style={{ '--btn-color': '#00aaff', padding: '4px 12px', fontSize: 11, minWidth: 'auto' } as React.CSSProperties}
                    onClick={fetchDiagnostics}
                    disabled={diagLoading}
                  >
                    {diagLoading ? 'Running...' : 'Re-run Diagnostics'}
                  </button>
                </div>

                {diagLoading && !diagnostics && (
                  <div style={{ color: '#888', textAlign: 'center', padding: 20 }}>Running diagnostics...</div>
                )}

                {diagnostics && (
                  <>
                    <div style={{
                      background: diagnostics.overall_status === 'healthy' ? 'rgba(0,255,136,0.1)' :
                                  diagnostics.overall_status === 'warning' ? 'rgba(255,170,0,0.1)' : 'rgba(255,68,68,0.1)',
                      border: `1px solid ${diagnostics.overall_status === 'healthy' ? '#00ff88' :
                               diagnostics.overall_status === 'warning' ? '#ffaa00' : '#ff4444'}`,
                      borderRadius: 6, padding: '8px 12px', marginBottom: 10, textAlign: 'center'
                    }}>
                      <span style={{
                        color: diagnostics.overall_status === 'healthy' ? '#00ff88' :
                               diagnostics.overall_status === 'warning' ? '#ffaa00' : '#ff4444',
                        fontWeight: 700, textTransform: 'uppercase'
                      }}>
                        {diagnostics.overall_status === 'healthy' ? '\u2713 ALL CHECKS PASSED' :
                         diagnostics.overall_status === 'warning' ? '\u26A0 WARNINGS DETECTED' : '\u2717 CRITICAL ISSUES FOUND'}
                      </span>
                    </div>

                    {diagnostics.checks.map((check, i) => (
                      <div key={i} style={{
                        background: 'rgba(0,0,0,0.3)', borderRadius: 6, padding: '8px 12px', marginBottom: 6,
                        borderLeft: `3px solid ${check.status === 'ok' ? '#00ff88' : check.status === 'warning' ? '#ffaa00' : '#ff4444'}`
                      }}>
                        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
                          <span style={{ color: '#ccc', fontWeight: 600, fontSize: 12 }}>{check.title}</span>
                          <span style={{
                            color: check.status === 'ok' ? '#00ff88' : check.status === 'warning' ? '#ffaa00' : '#ff4444',
                            fontSize: 11, fontWeight: 700, textTransform: 'uppercase'
                          }}>
                            {check.status === 'ok' ? '\u2713 OK' : check.status === 'warning' ? '\u26A0 WARN' : '\u2717 CRIT'}
                          </span>
                        </div>
                        <div style={{ color: '#999', fontSize: 11, marginTop: 4 }}>{check.detail}</div>
                      </div>
                    ))}
                  </>
                )}
              </div>

              {/* GOXDCR Delay History */}
              {diagnostics?.delay_windows && diagnostics.delay_windows.length > 0 && (
                <div style={{ marginBottom: 16 }}>
                  <div className="modal-label" style={{ marginBottom: 8, fontSize: 13, fontWeight: 600 }}>GOXDCR DELAY HISTORY</div>
                  {diagnostics.delay_windows.map((dw, i) => (
                    <div key={i} style={{
                      background: 'rgba(255,170,0,0.1)', borderRadius: 6, padding: '6px 12px', marginBottom: 4,
                      borderLeft: '3px solid #ffaa00', fontSize: 11
                    }}>
                      <span style={{ color: '#ffaa00' }}>{dw.cause}</span>
                      <span style={{ color: '#888', marginLeft: 8 }}>
                        {new Date(dw.start).toLocaleTimeString()}
                        {dw.end ? ` — ${new Date(dw.end).toLocaleTimeString()}` : ' — ongoing'}
                      </span>
                      {dw.duration > 0 && (
                        <span style={{ color: '#ccc', marginLeft: 8 }}>
                          ({(dw.duration / 1e9).toFixed(0)}s)
                        </span>
                      )}
                    </div>
                  ))}
                </div>
              )}

              {/* Quick Actions */}
              <div>
                <div className="modal-label" style={{ marginBottom: 8, fontSize: 13, fontWeight: 600 }}>QUICK ACTIONS</div>
                <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 8 }}>
                  <button
                    className="control-btn"
                    style={{ '--btn-color': '#ffaa00' } as React.CSSProperties}
                    onClick={() => { onCommand({ action: 'pause_xdcr' }); setTimeout(fetchDiagnostics, 1000); }}
                  >
                    <span className="btn-icon">{'\u23F8'}</span>
                    <span className="btn-label">Pause XDCR</span>
                  </button>
                  <button
                    className="control-btn"
                    style={{ '--btn-color': '#00ff88' } as React.CSSProperties}
                    onClick={() => { onCommand({ action: 'resume_xdcr' }); setTimeout(fetchDiagnostics, 1000); }}
                  >
                    <span className="btn-icon">{'\u25B6'}</span>
                    <span className="btn-label">Resume XDCR</span>
                  </button>
                  <button
                    className="control-btn"
                    style={{ '--btn-color': '#00aaff' } as React.CSSProperties}
                    onClick={() => { onCommand({ action: 'restart_xdcr' }); setTimeout(fetchDiagnostics, 1000); }}
                  >
                    <span className="btn-icon">{'\uD83D\uDD01'}</span>
                    <span className="btn-label">Restart Pipeline</span>
                  </button>
                  <button
                    className="control-btn"
                    style={{ '--btn-color': '#ff4444' } as React.CSSProperties}
                    onClick={() => { onCommand({ action: 'stop_xdcr' }); setTimeout(fetchDiagnostics, 1000); }}
                  >
                    <span className="btn-icon">{'\u23F9'}</span>
                    <span className="btn-label">Stop XDCR</span>
                  </button>
                  <button
                    className="control-btn"
                    style={{ '--btn-color': '#aa88ff' } as React.CSSProperties}
                    onClick={() => onCommand({ action: 'run_audit' })}
                  >
                    <span className="btn-icon">{'\uD83D\uDD0D'}</span>
                    <span className="btn-label">Run Data Audit</span>
                  </button>
                  <button
                    className="control-btn"
                    style={{ '--btn-color': '#ff6600' } as React.CSSProperties}
                    onClick={() => onCommand({ action: 'inject_failure', params: { type: 'xdcr_partition', target: 'replication' } })}
                  >
                    <span className="btn-icon">{'\uD83D\uDCA5'}</span>
                    <span className="btn-label">Inject XDCR Failure</span>
                  </button>
                </div>
              </div>
            </div>
            <div className="modal-footer">
              <button className="modal-btn modal-btn-cancel" onClick={() => setShowXDCRTroubleshoot(false)}>
                Close
              </button>
            </div>
          </div>
        </div>
      )}
    </>
  );
};
