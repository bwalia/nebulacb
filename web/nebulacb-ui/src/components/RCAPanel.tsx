import React, { useState, useEffect, useCallback } from 'react';
import { RCAReport, ClusterMetrics } from '../types';
import { fetchRCAReports, triggerRCA } from '../hooks/useWebSocket';

interface Props {
  clusters: Record<string, ClusterMetrics>;
}

const RCA_CATEGORIES = [
  { value: 'xdcr', label: 'XDCR Replication' },
  { value: 'upgrade', label: 'Upgrade Issues' },
  { value: 'performance', label: 'Performance' },
  { value: 'failover', label: 'Failover / HA' },
  { value: 'backup', label: 'Backup & Restore' },
  { value: 'data_loss', label: 'Data Integrity' },
  { value: 'cluster_health', label: 'Cluster Health' },
];

const SEVERITY_COLORS: Record<string, string> = {
  critical: '#ff4444',
  high: '#ff6644',
  medium: '#ffaa00',
  low: '#00aaff',
  info: '#888',
};

export function RCAPanel({ clusters }: Props) {
  const [reports, setReports] = useState<RCAReport[]>([]);
  const [loading, setLoading] = useState(false);
  const [triggerLoading, setTriggerLoading] = useState(false);
  const [selectedReport, setSelectedReport] = useState<RCAReport | null>(null);
  const [newCluster, setNewCluster] = useState('');
  const [newCategory, setNewCategory] = useState('cluster_health');

  const loadReports = useCallback(async () => {
    setLoading(true);
    try {
      const data = await fetchRCAReports();
      setReports(Array.isArray(data) ? data : []);
    } catch { /* ignore */ }
    setLoading(false);
  }, []);

  useEffect(() => { loadReports(); }, [loadReports]);

  const handleTrigger = async () => {
    setTriggerLoading(true);
    try {
      const report = await triggerRCA(newCluster, newCategory);
      setReports(prev => [report, ...prev]);
      setSelectedReport(report);
    } catch (err: any) {
      alert(`RCA failed: ${err.message}`);
    }
    setTriggerLoading(false);
  };

  const clusterNames = Object.keys(clusters);

  return (
    <div className="rca-panel">
      <div className="rca-header">
        <div className="rca-title">
          <span className="ai-icon">&#x1F50D;</span> Root Cause Analysis
        </div>
        <button className="ai-quick-btn" onClick={loadReports} disabled={loading}>
          {loading ? 'Loading...' : 'Refresh'}
        </button>
      </div>

      {/* Trigger new RCA */}
      <div className="rca-trigger">
        <select value={newCluster} onChange={e => setNewCluster(e.target.value)} className="ai-select">
          <option value="">All Clusters</option>
          {clusterNames.map(n => <option key={n} value={n}>{n}</option>)}
        </select>
        <select value={newCategory} onChange={e => setNewCategory(e.target.value)} className="ai-select">
          {RCA_CATEGORIES.map(c => <option key={c.value} value={c.value}>{c.label}</option>)}
        </select>
        <button className="ai-send-btn" onClick={handleTrigger} disabled={triggerLoading}>
          {triggerLoading ? 'Analyzing...' : 'Run RCA'}
        </button>
      </div>

      {/* Report list + detail */}
      <div className="rca-content">
        <div className="rca-list">
          {reports.length === 0 && !loading && (
            <div className="ai-empty-state" style={{ padding: 20 }}>
              <div className="ai-empty-text">No RCA reports yet. Run an analysis to get started.</div>
            </div>
          )}
          {reports.map(r => (
            <div
              key={r.id}
              className={`rca-item ${selectedReport?.id === r.id ? 'active' : ''}`}
              onClick={() => setSelectedReport(r)}
            >
              <div className="rca-item-header">
                <span className="rca-severity" style={{ color: SEVERITY_COLORS[r.severity] || '#888' }}>
                  {r.severity?.toUpperCase()}
                </span>
                <span className="rca-confidence">{(r.confidence * 100).toFixed(0)}%</span>
              </div>
              <div className="rca-item-category">{r.category}</div>
              <div className="rca-item-cause">{r.root_cause?.substring(0, 80)}...</div>
              <div className="rca-item-meta">
                {r.cluster && <span>{r.cluster}</span>}
                <span>{new Date(r.timestamp).toLocaleTimeString()}</span>
              </div>
            </div>
          ))}
        </div>

        {/* Detail view */}
        {selectedReport && (
          <div className="rca-detail">
            <div className="rca-detail-header">
              <span className="rca-severity-badge" style={{
                color: '#fff',
                background: SEVERITY_COLORS[selectedReport.severity] || '#888',
              }}>
                {selectedReport.severity?.toUpperCase()}
              </span>
              <span className="rca-detail-id">{selectedReport.id}</span>
              <span className="rca-detail-status">{selectedReport.status}</span>
            </div>

            <div className="rca-section">
              <div className="rca-section-title">Root Cause</div>
              <div className="rca-section-body">{selectedReport.root_cause}</div>
            </div>

            {selectedReport.evidence?.length > 0 && (
              <div className="rca-section">
                <div className="rca-section-title">Evidence</div>
                {selectedReport.evidence.map((e, i) => (
                  <div key={i} className="rca-evidence-item">{e}</div>
                ))}
              </div>
            )}

            {selectedReport.remediation?.length > 0 && (
              <div className="rca-section">
                <div className="rca-section-title">Remediation Steps</div>
                {selectedReport.remediation.map((step, i) => (
                  <div key={i} className="rca-remediation-step">
                    <div className="rca-step-header">
                      <span className="rca-step-num">{step.order}</span>
                      <span className="rca-step-action">{step.action}</span>
                      <span className={`rca-step-risk risk-${step.risk}`}>{step.risk} risk</span>
                    </div>
                    <div className="rca-step-desc">{step.description}</div>
                    {step.command && (
                      <code className="rca-step-cmd">{step.command}</code>
                    )}
                  </div>
                ))}
              </div>
            )}
          </div>
        )}
      </div>
    </div>
  );
}
