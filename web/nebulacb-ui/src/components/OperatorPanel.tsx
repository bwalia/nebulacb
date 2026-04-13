import React, { useState, useEffect } from 'react';
import { getToken } from '../hooks/useWebSocket';

function apiBase(): string {
  if (process.env.REACT_APP_API_URL) return process.env.REACT_APP_API_URL;
  return window.location.origin;
}

interface OperatorStatus {
  cluster_name: string; namespace: string;
  desired_image: string; current_image: string;
  desired_nodes: number; current_nodes: number;
  phase: string; conditions: string[];
  drifted: boolean;
  servers: { name: string; size: number; services: string[] }[];
  error?: string;
}

export function OperatorPanel() {
  const [statuses, setStatuses] = useState<OperatorStatus[]>([]);
  const [loading, setLoading] = useState(false);
  const [recs, setRecs] = useState<{ severity: string; cluster: string; title: string; message: string }[]>([]);

  const fetchAll = async () => {
    setLoading(true);
    const token = getToken();
    const headers: Record<string, string> = token ? { Authorization: `Bearer ${token}` } : {};
    try {
      const [opResp, recResp] = await Promise.all([
        fetch(`${apiBase()}/api/v1/k8s/operator`, { headers }),
        fetch(`${apiBase()}/api/v1/recommendations`, { headers }),
      ]);
      const opData = await opResp.json();
      setStatuses(Array.isArray(opData) ? opData : []);
      const recData = await recResp.json();
      setRecs(Array.isArray(recData) ? recData : []);
    } catch { }
    setLoading(false);
  };

  useEffect(() => { fetchAll(); }, []);

  const downloadDiagnostics = () => {
    const token = getToken();
    const url = `${apiBase()}/api/v1/diagnostics`;
    // Create a hidden link to trigger download
    const a = document.createElement('a');
    a.href = url + (token ? `?token=${encodeURIComponent(token)}` : '');
    a.download = 'nebulacb-diagnostics.json';
    a.click();
  };

  return (
    <div className="logs-panel">
      <div className="rca-header">
        <div className="rca-title"><span className="ai-icon">&#x2638;&#xFE0F;</span> Operator &amp; Recommendations</div>
        <div style={{ display: 'flex', gap: 8 }}>
          <button className="ai-quick-btn" onClick={downloadDiagnostics}>Export Diagnostics</button>
          <button className="ai-quick-btn" onClick={fetchAll} disabled={loading}>
            {loading ? 'Loading...' : 'Refresh'}
          </button>
        </div>
      </div>

      {/* Operator Status Cards */}
      <div style={{ padding: 16, display: 'flex', gap: 16, flexWrap: 'wrap' }}>
        {statuses.length === 0 && !loading && (
          <div className="ai-empty-state" style={{ padding: 20, width: '100%' }}>
            <div className="ai-empty-text">No Kubernetes clusters configured or operator not reachable.</div>
          </div>
        )}
        {statuses.map((s, i) => (
          <div key={i} className="operator-card">
            <div className="operator-card-header">
              <span className="operator-name">{s.cluster_name || 'Unknown'}</span>
              <span className={`operator-drift ${s.drifted ? 'drifted' : 'synced'}`}>
                {s.drifted ? 'DRIFTED' : 'IN SYNC'}
              </span>
            </div>
            {s.error ? (
              <div style={{ color: '#ff4444', fontSize: '0.8rem' }}>{s.error}</div>
            ) : (
              <>
                <div className="operator-row">
                  <span className="operator-label">Namespace</span>
                  <span>{s.namespace}</span>
                </div>
                <div className="operator-row">
                  <span className="operator-label">Desired Image</span>
                  <span style={{ color: '#00aaff' }}>{s.desired_image}</span>
                </div>
                <div className="operator-row">
                  <span className="operator-label">Current Image</span>
                  <span style={{ color: s.drifted ? '#ffaa00' : '#00ff88' }}>{s.current_image || 'unknown'}</span>
                </div>
                <div className="operator-row">
                  <span className="operator-label">Nodes</span>
                  <span style={{ color: s.desired_nodes !== s.current_nodes ? '#ffaa00' : '#00ff88' }}>
                    {s.current_nodes}/{s.desired_nodes}
                  </span>
                </div>
                <div className="operator-row">
                  <span className="operator-label">Phase</span>
                  <span>{s.phase || 'unknown'}</span>
                </div>
                {s.servers && s.servers.length > 0 && (
                  <div className="operator-servers">
                    {s.servers.map((sg, j) => (
                      <div key={j} className="operator-server-group">
                        <span style={{ fontWeight: 600 }}>{sg.name}</span>
                        <span style={{ color: '#8888aa' }}> x{sg.size}</span>
                        <span style={{ color: '#00aaff', fontSize: '0.7rem', marginLeft: 6 }}>{sg.services?.join(', ')}</span>
                      </div>
                    ))}
                  </div>
                )}
              </>
            )}
          </div>
        ))}
      </div>

      {/* Smart Recommendations */}
      <div style={{ padding: '0 16px 16px' }}>
        <div className="rca-section-title" style={{ marginBottom: 8 }}>SMART RECOMMENDATIONS</div>
        {recs.map((r, i) => (
          <div key={i} className={`rec-item rec-${r.severity}`}>
            <span className="rec-severity">{r.severity.toUpperCase()}</span>
            <span className="rec-title">{r.title}</span>
            <div className="rec-message">{r.message}</div>
          </div>
        ))}
      </div>
    </div>
  );
}
