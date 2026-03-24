import React, { useState, useEffect } from 'react';
import { Command, ClusterMetrics } from '../types';
import { getToken } from '../hooks/useWebSocket';

interface Props {
  onCommand: (cmd: Command) => void;
  clusters?: Record<string, ClusterMetrics>;
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
  { label: 'Stop Load', action: 'stop_load', icon: '\u23F9', color: '#ff4444', group: 'load' },
  { label: 'Start Upgrade', action: 'start_upgrade', icon: '\uD83D\uDE80', color: '#00aaff', group: 'upgrade' },
  { label: 'Abort Upgrade', action: 'abort_upgrade', icon: '\uD83D\uDED1', color: '#ff4444', group: 'upgrade' },
  { label: 'Restart XDCR', action: 'restart_xdcr', icon: '\uD83D\uDD01', color: '#00aaff', group: 'replication' },
  { label: 'Full Audit', action: 'run_audit', icon: '\uD83D\uDD0D', color: '#aa88ff', group: 'validation' },
  { label: 'Inject Failure', action: 'inject_failure', icon: '\uD83D\uDCA5', color: '#ff6600', group: 'chaos' },
  { label: 'AI Analyze', action: 'ai_analyze', icon: '\uD83E\uDDE0', color: '#ff88ff', group: 'ai' },
  { label: 'Backup', action: 'start_backup', icon: '\uD83D\uDCBE', color: '#00cc88', group: 'backup' },
  { label: 'Failover', action: 'manual_failover', icon: '\u26A0\uFE0F', color: '#ff8800', group: 'ha' },
];

function apiBase(): string {
  if (process.env.REACT_APP_API_URL) return process.env.REACT_APP_API_URL;
  return window.location.origin;
}

export const ControlPanel: React.FC<Props> = ({ onCommand, clusters }) => {
  const [showUpgradeModal, setShowUpgradeModal] = useState(false);
  const [showBackupModal, setShowBackupModal] = useState(false);
  const [showFailoverModal, setShowFailoverModal] = useState(false);
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

  const handleButtonClick = (action: string) => {
    if (action === 'start_upgrade') {
      setShowUpgradeModal(true);
    } else if (action === 'start_backup') {
      setShowBackupModal(true);
    } else if (action === 'manual_failover') {
      setShowFailoverModal(true);
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
    </>
  );
};
