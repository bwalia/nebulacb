import React from 'react';
import { AreaChart, Area, XAxis, YAxis, Tooltip, ResponsiveContainer } from 'recharts';
import { StormMetrics } from '../types';

interface Props {
  metrics: StormMetrics;
  history: StormMetrics[];
}

export const LoadPanel: React.FC<Props> = ({ metrics, history }) => {
  const chartData = history.map((m, i) => ({
    t: i,
    writes: m.writes_per_sec,
    reads: m.reads_per_sec,
  }));

  return (
    <div className="panel">
      <div className="panel-header">
        <span className="panel-indicator amber" />
        LOAD METRICS
      </div>
      <div className="panel-body">
        <div className="metrics-grid">
          <div className="metric-card">
            <div className="metric-card-label">Writes/sec</div>
            <div className="metric-card-value green">{metrics.writes_per_sec?.toFixed(0) || 0}</div>
          </div>
          <div className="metric-card">
            <div className="metric-card-label">Reads/sec</div>
            <div className="metric-card-value blue">{metrics.reads_per_sec?.toFixed(0) || 0}</div>
          </div>
          <div className="metric-card">
            <div className="metric-card-label">P95 Latency</div>
            <div className="metric-card-value amber">{metrics.latency_p95_ms?.toFixed(1) || 0}ms</div>
          </div>
          <div className="metric-card">
            <div className="metric-card-label">P99 Latency</div>
            <div className="metric-card-value" style={{
              color: (metrics.latency_p99_ms || 0) > 100 ? '#ff4444' : '#ffaa00'
            }}>
              {metrics.latency_p99_ms?.toFixed(1) || 0}ms
            </div>
          </div>
        </div>

        <div className="chart-container">
          <ResponsiveContainer width="100%" height={120}>
            <AreaChart data={chartData}>
              <defs>
                <linearGradient id="writeGrad" x1="0" y1="0" x2="0" y2="1">
                  <stop offset="5%" stopColor="#00ff88" stopOpacity={0.3} />
                  <stop offset="95%" stopColor="#00ff88" stopOpacity={0} />
                </linearGradient>
                <linearGradient id="readGrad" x1="0" y1="0" x2="0" y2="1">
                  <stop offset="5%" stopColor="#00aaff" stopOpacity={0.3} />
                  <stop offset="95%" stopColor="#00aaff" stopOpacity={0} />
                </linearGradient>
              </defs>
              <XAxis dataKey="t" hide />
              <YAxis hide />
              <Tooltip
                contentStyle={{ background: '#1a1a2e', border: '1px solid #333', color: '#fff' }}
              />
              <Area type="monotone" dataKey="writes" stroke="#00ff88" fill="url(#writeGrad)" strokeWidth={2} />
              <Area type="monotone" dataKey="reads" stroke="#00aaff" fill="url(#readGrad)" strokeWidth={2} />
            </AreaChart>
          </ResponsiveContainer>
        </div>

        <div className="metric-row">
          <span className="metric-label">Total Written</span>
          <span className="metric-value">{(metrics.total_written || 0).toLocaleString()}</span>
        </div>
        <div className="metric-row">
          <span className="metric-label">Total Read</span>
          <span className="metric-value">{(metrics.total_read || 0).toLocaleString()}</span>
        </div>
        <div className="metric-row">
          <span className="metric-label">Errors</span>
          <span className="metric-value" style={{
            color: (metrics.total_errors || 0) > 0 ? '#ff4444' : '#00ff88'
          }}>
            {(metrics.total_errors || 0).toLocaleString()}
          </span>
        </div>
        <div className="metric-row">
          <span className="metric-label">Connections</span>
          <span className="metric-value">{metrics.active_connections || 0}</span>
        </div>
      </div>
    </div>
  );
};
