import React from 'react';
import { Alert } from '../types';

interface Props {
  alerts: Alert[];
}

const severityConfig: Record<string, { color: string; bg: string }> = {
  critical: { color: '#ff5d6e', bg: 'rgba(255,68,68,0.1)' },
  warning: { color: '#ffb43d', bg: 'rgba(255,170,0,0.1)' },
  info: { color: '#3fc8ff', bg: 'rgba(0,170,255,0.1)' },
};

export const AlertPanel: React.FC<Props> = ({ alerts }) => {
  const sortedAlerts = [...(alerts || [])].reverse().slice(0, 20);

  return (
    <div className="panel alert-panel">
      <div className="panel-header">
        <span className="panel-indicator red" />
        ALERTS
        {sortedAlerts.length > 0 && (
          <span className="alert-count">{sortedAlerts.length}</span>
        )}
      </div>
      <div className="panel-body alerts-scroll">
        {sortedAlerts.length === 0 && (
          <div className="empty-state">All systems nominal</div>
        )}
        {sortedAlerts.map((alert, i) => {
          const cfg = severityConfig[alert.severity] || severityConfig.info;
          return (
            <div
              key={alert.id || i}
              className="alert-item"
              style={{ borderLeftColor: cfg.color, backgroundColor: cfg.bg }}
            >
              <div className="alert-header">
                <span className="alert-severity" style={{ color: cfg.color }}>
                  {alert.severity?.toUpperCase()}
                </span>
                <span className="alert-category">{alert.category}</span>
                <span className="alert-time">
                  {alert.timestamp ? new Date(alert.timestamp).toLocaleTimeString() : ''}
                </span>
              </div>
              <div className="alert-title">{alert.title}</div>
              <div className="alert-message">{alert.message}</div>
            </div>
          );
        })}
      </div>
    </div>
  );
};
