import React from 'react';
import { Command } from '../types';

interface Props {
  onCommand: (cmd: Command) => void;
}

const buttons = [
  { label: 'Start Load', action: 'start_load', icon: '▶', color: '#00ff88' },
  { label: 'Pause Load', action: 'pause_load', icon: '⏸', color: '#ffaa00' },
  { label: 'Stop Load', action: 'stop_load', icon: '⏹', color: '#ff4444' },
  { label: 'Start Upgrade', action: 'start_upgrade', icon: '🚀', color: '#00aaff' },
  { label: 'Abort Upgrade', action: 'abort_upgrade', icon: '🛑', color: '#ff4444' },
  { label: 'Restart XDCR', action: 'restart_xdcr', icon: '🔁', color: '#00aaff' },
  { label: 'Inject Failure', action: 'inject_failure', icon: '💥', color: '#ff6600' },
  { label: 'Full Audit', action: 'run_audit', icon: '🔍', color: '#aa88ff' },
];

export const ControlPanel: React.FC<Props> = ({ onCommand }) => {
  return (
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
            onClick={() => onCommand({ action: btn.action })}
          >
            <span className="btn-icon">{btn.icon}</span>
            <span className="btn-label">{btn.label}</span>
          </button>
        ))}
      </div>
    </div>
  );
};
