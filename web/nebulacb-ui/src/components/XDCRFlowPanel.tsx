import React, { useEffect, useState } from 'react';
import { XDCRStatus } from '../types';

interface Props {
  xdcr: XDCRStatus;
  sourceDocs: number;
  targetDocs: number;
}

const stateColor: Record<string, string> = {
  Running: '#00ff88',
  Paused: '#ffaa00',
  Restarting: '#00aaff',
  Unknown: '#888',
};

export const XDCRFlowPanel: React.FC<Props> = ({ xdcr, sourceDocs, targetDocs }) => {
  const [delayTimer, setDelayTimer] = useState('');

  useEffect(() => {
    if (!xdcr.goxdcr_delay_start || xdcr.goxdcr_delay_end) {
      setDelayTimer('');
      return;
    }
    const interval = setInterval(() => {
      const start = new Date(xdcr.goxdcr_delay_start).getTime();
      const elapsed = Date.now() - start;
      const remaining = Math.max(0, 5 * 60 * 1000 - elapsed);
      const mins = Math.floor(remaining / 60000);
      const secs = Math.floor((remaining % 60000) / 1000);
      setDelayTimer(`${mins}:${secs.toString().padStart(2, '0')}`);
    }, 1000);
    return () => clearInterval(interval);
  }, [xdcr.goxdcr_delay_start, xdcr.goxdcr_delay_end]);

  const color = stateColor[xdcr.state] || '#888';
  const isActive = xdcr.state === 'Running';

  return (
    <div className="xdcr-flow-panel">
      <div className="xdcr-flow-header">
        <span className="xdcr-flow-title">XDCR REPLICATION PIPELINE</span>
        <span className="xdcr-state-badge" style={{ color, borderColor: color }}>
          {xdcr.state || 'Unknown'}
        </span>
      </div>

      {/* Visual Flow */}
      <div className="xdcr-flow-visual">
        <div className="flow-endpoint source">
          <div className="flow-endpoint-label">SOURCE</div>
          <div className="flow-endpoint-docs">{(sourceDocs || 0).toLocaleString()}</div>
          <div className="flow-endpoint-sublabel">documents</div>
        </div>

        <div className="flow-pipe">
          <div className={`flow-arrows ${isActive ? 'active' : 'paused'}`}>
            <span className="flow-arrow">&raquo;</span>
            <span className="flow-arrow">&raquo;</span>
            <span className="flow-arrow">&raquo;</span>
          </div>
          <div className="flow-pipe-stats">
            <span>{(xdcr.docs_processed || 0).toLocaleString()} processed</span>
          </div>
          {xdcr.changes_left > 0 && (
            <div className="flow-pipe-queue" style={{
              color: xdcr.changes_left > 10000 ? '#ff4444' : xdcr.changes_left > 1000 ? '#ffaa00' : '#00ff88'
            }}>
              {xdcr.changes_left.toLocaleString()} pending
            </div>
          )}
        </div>

        <div className="flow-endpoint target">
          <div className="flow-endpoint-label">TARGET</div>
          <div className="flow-endpoint-docs">{(targetDocs || 0).toLocaleString()}</div>
          <div className="flow-endpoint-sublabel">documents</div>
        </div>
      </div>

      {/* XDCR Metrics Row */}
      <div className="xdcr-metrics-row">
        <div className="xdcr-metric">
          <div className="xdcr-metric-value">{xdcr.replication_lag_ms?.toFixed(1) || 0}ms</div>
          <div className="xdcr-metric-label">Replication Lag</div>
        </div>
        <div className="xdcr-metric">
          <div className="xdcr-metric-value">{xdcr.pipeline_restarts || 0}</div>
          <div className="xdcr-metric-label">Pipeline Restarts</div>
        </div>
        <div className="xdcr-metric">
          <div className="xdcr-metric-value" style={{ color: xdcr.topology_change ? '#ffaa00' : '#00ff88' }}>
            {xdcr.topology_change ? 'YES' : 'NO'}
          </div>
          <div className="xdcr-metric-label">Topology Change</div>
        </div>
        <div className="xdcr-metric">
          <div className="xdcr-metric-value">{(xdcr.mutation_queue || 0).toLocaleString()}</div>
          <div className="xdcr-metric-label">Mutation Queue</div>
        </div>
      </div>

      {/* GOXDCR Delay Timer */}
      {delayTimer && (
        <div className="goxdcr-delay-banner">
          <div className="goxdcr-delay-icon">&#x26A0;</div>
          <div className="goxdcr-delay-info">
            <div className="goxdcr-delay-title">GOXDCR DELAY ACTIVE</div>
            <div className="goxdcr-delay-sub">Pipeline paused due to topology change (~5 min expected)</div>
          </div>
          <div className="goxdcr-delay-countdown">{delayTimer}</div>
        </div>
      )}
    </div>
  );
};
