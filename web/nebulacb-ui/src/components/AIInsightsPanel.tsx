import React, { useState, useEffect } from 'react';
import { AIInsight } from '../types';
import { fetchAIInsights } from '../hooks/useWebSocket';

const SEVERITY_COLORS: Record<string, string> = {
  critical: '#ff4444',
  warning: '#ffaa00',
  info: '#00aaff',
};

const TYPE_LABELS: Record<string, string> = {
  cluster_health: 'Health',
  performance: 'Performance',
  troubleshoot: 'Troubleshoot',
  error: 'Error',
  logs: 'Logs',
  log_analysis: 'Logs',
  recommendation: 'Recommendation',
  root_cause: 'RCA',
};

export function AIInsightsPanel() {
  const [insights, setInsights] = useState<AIInsight[]>([]);
  const [loading, setLoading] = useState(false);
  const [expanded, setExpanded] = useState<string | null>(null);
  const [filterSeverity, setFilterSeverity] = useState('all');

  const loadInsights = async () => {
    setLoading(true);
    try {
      const data = await fetchAIInsights();
      setInsights(Array.isArray(data) ? data : []);
    } catch { /* ignore */ }
    setLoading(false);
  };

  useEffect(() => { loadInsights(); }, []);

  const filtered = insights.filter(i =>
    filterSeverity === 'all' || i.severity === filterSeverity
  ).reverse(); // newest first

  return (
    <div className="insights-panel">
      <div className="rca-header">
        <div className="rca-title">
          <span className="ai-icon">&#x1F4CA;</span> AI Insights History
        </div>
        <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
          <select value={filterSeverity} onChange={e => setFilterSeverity(e.target.value)} className="ai-select">
            <option value="all">All Severity</option>
            <option value="critical">Critical</option>
            <option value="warning">Warning</option>
            <option value="info">Info</option>
          </select>
          <button className="ai-quick-btn" onClick={loadInsights} disabled={loading}>
            {loading ? 'Loading...' : 'Refresh'}
          </button>
        </div>
      </div>

      <div className="insights-list">
        {filtered.length === 0 && (
          <div className="ai-empty-state" style={{ padding: 20 }}>
            <div className="ai-empty-text">No AI insights recorded yet. Use Ask AI or run an analysis to generate insights.</div>
          </div>
        )}
        {filtered.map(insight => (
          <div key={insight.id} className={`insight-item ${expanded === insight.id ? 'expanded' : ''}`}>
            <div className="insight-header" onClick={() => setExpanded(expanded === insight.id ? null : insight.id)}>
              <span className="insight-severity" style={{ color: SEVERITY_COLORS[insight.severity] || '#888' }}>
                {insight.severity?.toUpperCase()}
              </span>
              <span className="insight-type-badge">{TYPE_LABELS[insight.type] || insight.type}</span>
              <span className="insight-title">{insight.title}</span>
              {insight.cluster && <span className="insight-cluster">{insight.cluster}</span>}
              <span className="insight-confidence">{(insight.confidence * 100).toFixed(0)}%</span>
              <span className="insight-time">{new Date(insight.timestamp).toLocaleString()}</span>
              <span className="kb-expand">{expanded === insight.id ? '\u25B2' : '\u25BC'}</span>
            </div>
            {expanded === insight.id && (
              <div className="insight-body">
                <div className="insight-summary">{insight.summary}</div>
                {insight.details && insight.details !== insight.summary && (
                  <div className="insight-details">
                    <div className="kb-section-label">Details</div>
                    <div>{insight.details}</div>
                  </div>
                )}
                {insight.suggestions && insight.suggestions.length > 0 && (
                  <div className="insight-suggestions">
                    <div className="kb-section-label">Suggestions</div>
                    <ol>
                      {insight.suggestions.map((s, i) => <li key={i}>{s}</li>)}
                    </ol>
                  </div>
                )}
              </div>
            )}
          </div>
        ))}
      </div>
    </div>
  );
}
