import React from 'react';
import {
  AreaChart, Area, XAxis, YAxis, Tooltip, ResponsiveContainer,
  LineChart, Line, ReferenceLine,
} from 'recharts';
import { DataLossProof } from '../types';

interface Props {
  proof: DataLossProof;
}

export const DataLossProofPanel: React.FC<Props> = ({ proof }) => {
  const isZeroLoss = proof.zero_loss_confirmed;
  const isConverged = proof.is_converged;
  const delta = proof.current_delta || 0;

  // Prepare chart data — last 120 samples
  const chartData = (proof.timeline || []).slice(-120).map((s, i) => ({
    t: i,
    source: s.source_docs,
    target: s.target_docs,
    delta: s.delta,
  }));

  const deltaChartData = (proof.timeline || []).slice(-120).map((s, i) => ({
    t: i,
    delta: Math.abs(s.delta),
  }));

  return (
    <div className="proof-panel">
      <div className="proof-header">
        <span className="proof-title">DATA LOSS PROOF</span>
        <div className={`proof-verdict ${isZeroLoss ? 'pass' : isConverged ? 'converging' : 'active'}`}>
          {isZeroLoss ? 'ZERO LOSS CONFIRMED' : isConverged ? 'CONVERGED' : 'MONITORING...'}
        </div>
      </div>

      {/* Big Numbers */}
      <div className="proof-big-numbers">
        <div className="proof-number source">
          <div className="proof-number-value">{(proof.current_source_docs || 0).toLocaleString()}</div>
          <div className="proof-number-label">Source Docs</div>
        </div>
        <div className="proof-delta-display">
          <div className={`proof-delta-value ${delta === 0 ? 'zero' : delta > 0 ? 'positive' : 'negative'}`}>
            {delta === 0 ? '=' : delta > 0 ? `+${delta.toLocaleString()}` : delta.toLocaleString()}
          </div>
          <div className="proof-delta-label">Delta</div>
        </div>
        <div className="proof-number target">
          <div className="proof-number-value">{(proof.current_target_docs || 0).toLocaleString()}</div>
          <div className="proof-number-label">Target Docs</div>
        </div>
      </div>

      {/* Doc Count Timeline Chart */}
      <div className="proof-chart-section">
        <div className="proof-chart-title">Document Count Timeline (Source vs Target)</div>
        <div className="proof-chart">
          <ResponsiveContainer width="100%" height={140}>
            <AreaChart data={chartData}>
              <defs>
                <linearGradient id="srcGrad" x1="0" y1="0" x2="0" y2="1">
                  <stop offset="5%" stopColor="#00aaff" stopOpacity={0.3} />
                  <stop offset="95%" stopColor="#00aaff" stopOpacity={0} />
                </linearGradient>
                <linearGradient id="tgtGrad" x1="0" y1="0" x2="0" y2="1">
                  <stop offset="5%" stopColor="#aa88ff" stopOpacity={0.3} />
                  <stop offset="95%" stopColor="#aa88ff" stopOpacity={0} />
                </linearGradient>
              </defs>
              <XAxis dataKey="t" hide />
              <YAxis hide />
              <Tooltip
                contentStyle={{ background: '#1a1a2e', border: '1px solid #333', color: '#fff', fontSize: '0.75rem' }}
                formatter={(val: any, name: any) => [Number(val).toLocaleString(), name === 'source' ? 'Source' : 'Target']}
              />
              <Area type="monotone" dataKey="source" stroke="#00aaff" fill="url(#srcGrad)" strokeWidth={2} dot={false} />
              <Area type="monotone" dataKey="target" stroke="#aa88ff" fill="url(#tgtGrad)" strokeWidth={2} dot={false} />
            </AreaChart>
          </ResponsiveContainer>
        </div>
      </div>

      {/* Delta Convergence Chart */}
      <div className="proof-chart-section">
        <div className="proof-chart-title">Delta Convergence (|Source - Target|)</div>
        <div className="proof-chart">
          <ResponsiveContainer width="100%" height={80}>
            <LineChart data={deltaChartData}>
              <XAxis dataKey="t" hide />
              <YAxis hide />
              <ReferenceLine y={0} stroke="#333" />
              <Tooltip
                contentStyle={{ background: '#1a1a2e', border: '1px solid #333', color: '#fff', fontSize: '0.75rem' }}
                formatter={(val: any) => [Number(val).toLocaleString(), 'Delta']}
              />
              <Line
                type="monotone" dataKey="delta"
                stroke={delta === 0 ? '#00ff88' : '#ffaa00'}
                strokeWidth={2} dot={false}
              />
            </LineChart>
          </ResponsiveContainer>
        </div>
      </div>

      {/* Proof Stats */}
      <div className="proof-stats">
        <div className="proof-stat">
          <span className="proof-stat-label">Max Delta Observed</span>
          <span className="proof-stat-value">{(proof.max_delta_observed || 0).toLocaleString()}</span>
        </div>
        <div className="proof-stat">
          <span className="proof-stat-label">Hash Samples Passed</span>
          <span className="proof-stat-value" style={{ color: '#00ff88' }}>{proof.hash_samples_passed || 0}</span>
        </div>
        <div className="proof-stat">
          <span className="proof-stat-label">Hash Samples Failed</span>
          <span className="proof-stat-value" style={{ color: proof.hash_samples_failed > 0 ? '#ff4444' : '#00ff88' }}>
            {proof.hash_samples_failed || 0}
          </span>
        </div>
        <div className="proof-stat">
          <span className="proof-stat-label">Monitoring Duration</span>
          <span className="proof-stat-value">{proof.monitoring_duration || '—'}</span>
        </div>
        {proof.converged_at && (
          <div className="proof-stat">
            <span className="proof-stat-label">Converged At</span>
            <span className="proof-stat-value">{new Date(proof.converged_at).toLocaleTimeString()}</span>
          </div>
        )}
      </div>
    </div>
  );
};
