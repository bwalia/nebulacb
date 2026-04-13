import React, { useEffect, useRef, useState } from 'react';
import { UpgradeStatus, XDCRStatus, ClusterMetrics, Alert } from '../types';

interface Props {
  upgrade: UpgradeStatus;
  xdcr: XDCRStatus;
  source: ClusterMetrics;
  alerts: Alert[];
}

interface TimelineEvent {
  id: string;
  ts: number;
  kind: 'phase' | 'node' | 'xdcr' | 'alert' | 'rebalance';
  icon: string;
  color: string;
  title: string;
  detail: string;
}

const PHASE_ORDER = ['pending', 'preparing', 'upgrading', 'rebalancing', 'verifying', 'completed'];

export const UpgradeTimeline: React.FC<Props> = ({ upgrade, xdcr, source, alerts }) => {
  const [events, setEvents] = useState<TimelineEvent[]>([]);
  const seenRef = useRef<Set<string>>(new Set());
  const lastPhaseRef = useRef<string>('');
  const lastXDCRStateRef = useRef<string>('');
  const lastRebalanceRef = useRef<string>('');
  const lastNodeRef = useRef<string>('');

  useEffect(() => {
    const newEvents: TimelineEvent[] = [];
    const now = Date.now();

    if (upgrade.phase && upgrade.phase !== lastPhaseRef.current) {
      lastPhaseRef.current = upgrade.phase;
      const id = `phase:${upgrade.phase}:${now}`;
      if (!seenRef.current.has(id)) {
        seenRef.current.add(id);
        newEvents.push({
          id, ts: now, kind: 'phase',
          icon: phaseIcon(upgrade.phase),
          color: phaseColor(upgrade.phase),
          title: `Phase: ${upgrade.phase.toUpperCase()}`,
          detail: `${upgrade.source_version} → ${upgrade.target_version} | ${upgrade.nodes_completed}/${upgrade.nodes_total} nodes`,
        });
      }
    }

    if (upgrade.current_node && upgrade.current_node !== lastNodeRef.current) {
      lastNodeRef.current = upgrade.current_node;
      const id = `node:${upgrade.current_node}:${now}`;
      if (!seenRef.current.has(id)) {
        seenRef.current.add(id);
        newEvents.push({
          id, ts: now, kind: 'node',
          icon: '\u25C8', color: '#00aaff',
          title: `Node upgrading: ${upgrade.current_node}`,
          detail: `Progress ${upgrade.progress.toFixed(0)}%`,
        });
      }
    }

    if (xdcr.state && xdcr.state !== lastXDCRStateRef.current) {
      const prev = lastXDCRStateRef.current;
      lastXDCRStateRef.current = xdcr.state;
      if (prev) {
        const id = `xdcr:${prev}->${xdcr.state}:${now}`;
        if (!seenRef.current.has(id)) {
          seenRef.current.add(id);
          newEvents.push({
            id, ts: now, kind: 'xdcr',
            icon: xdcrIcon(xdcr.state),
            color: xdcrColor(xdcr.state),
            title: `XDCR ${prev} → ${xdcr.state}`,
            detail: xdcr.topology_change ? 'Topology change detected' : `${xdcr.changes_left.toLocaleString()} changes pending`,
          });
        }
      } else {
        lastXDCRStateRef.current = xdcr.state;
      }
    }

    if (source.rebalance_state && source.rebalance_state !== lastRebalanceRef.current) {
      const prev = lastRebalanceRef.current;
      lastRebalanceRef.current = source.rebalance_state;
      if (prev || source.rebalance_state !== 'none') {
        const id = `rebalance:${source.rebalance_state}:${now}`;
        if (!seenRef.current.has(id)) {
          seenRef.current.add(id);
          newEvents.push({
            id, ts: now, kind: 'rebalance',
            icon: '\u21BB',
            color: source.rebalance_state === 'none' ? '#00ff88' : '#ffaa00',
            title: source.rebalance_state === 'none' ? 'Rebalance complete' : 'Rebalance started',
            detail: source.cluster_name || 'cluster',
          });
        }
      }
    }

    for (const a of alerts.slice(-5)) {
      if (!a.id || seenRef.current.has(`alert:${a.id}`)) continue;
      seenRef.current.add(`alert:${a.id}`);
      newEvents.push({
        id: `alert:${a.id}`,
        ts: a.timestamp ? new Date(a.timestamp).getTime() : now,
        kind: 'alert',
        icon: a.severity === 'critical' ? '\u26A0' : a.severity === 'warning' ? '!' : 'i',
        color: a.severity === 'critical' ? '#ff4444' : a.severity === 'warning' ? '#ffaa00' : '#00aaff',
        title: a.title || 'Alert',
        detail: a.message || '',
      });
    }

    if (newEvents.length === 0) return;
    setEvents((prev) => {
      const merged = [...prev, ...newEvents].sort((a, b) => a.ts - b.ts);
      return merged.slice(-40);
    });
  }, [upgrade, xdcr, source, alerts]);

  const phaseIdx = Math.max(0, PHASE_ORDER.indexOf(upgrade.phase));
  const phaseProgress = upgrade.phase === 'completed' ? 100 :
    upgrade.nodes_total > 0 ? (upgrade.nodes_completed / upgrade.nodes_total) * 100 :
    upgrade.progress || 0;

  return (
    <div className="cockpit-timeline">
      <div className="cockpit-timeline-header">
        <span className="panel-indicator" style={{ backgroundColor: phaseColor(upgrade.phase) }} />
        <span className="cockpit-timeline-title">UPGRADE TIMELINE</span>
        <span className="cockpit-timeline-versions">
          {upgrade.source_version} <span className="arrow">&rarr;</span> {upgrade.target_version}
        </span>
        <span className="cockpit-timeline-spacer" />
        <span className="cockpit-timeline-phase" style={{ color: phaseColor(upgrade.phase) }}>
          {(upgrade.phase || 'pending').toUpperCase()}
        </span>
        <span className="cockpit-timeline-progress-text">
          {upgrade.nodes_completed}/{upgrade.nodes_total || '—'} nodes &middot; {phaseProgress.toFixed(0)}%
        </span>
      </div>

      <div className="cockpit-phase-rail">
        {PHASE_ORDER.map((p, i) => {
          const isActive = i === phaseIdx;
          const isDone = i < phaseIdx || upgrade.phase === 'completed';
          return (
            <div key={p} className={`phase-step ${isActive ? 'active' : ''} ${isDone ? 'done' : ''}`}>
              <div className="phase-step-dot" />
              <div className="phase-step-label">{p}</div>
            </div>
          );
        })}
      </div>

      <div className="cockpit-progress-bar">
        <div className="cockpit-progress-fill" style={{
          width: `${phaseProgress}%`,
          background: `linear-gradient(90deg, ${phaseColor(upgrade.phase)}, ${phaseColor(upgrade.phase)}aa)`,
        }} />
      </div>

      <div className="cockpit-event-stream">
        {events.length === 0 && (
          <div className="cockpit-event-empty">No events yet — waiting for activity.</div>
        )}
        {events.slice(-12).reverse().map((e) => (
          <div key={e.id} className="cockpit-event" style={{ borderLeftColor: e.color }} title={e.detail}>
            <span className="cockpit-event-icon" style={{ color: e.color }}>{e.icon}</span>
            <span className="cockpit-event-title">{e.title}</span>
            <span className="cockpit-event-detail">{e.detail}</span>
            <span className="cockpit-event-time">{new Date(e.ts).toLocaleTimeString()}</span>
          </div>
        ))}
      </div>
    </div>
  );
};

function phaseColor(phase: string): string {
  switch (phase) {
    case 'completed': return '#00ff88';
    case 'failed': return '#ff4444';
    case 'rebalancing': return '#ffaa00';
    case 'upgrading': return '#00aaff';
    case 'verifying': return '#aa88ff';
    case 'preparing': return '#00cccc';
    default: return '#888';
  }
}

function phaseIcon(phase: string): string {
  switch (phase) {
    case 'completed': return '\u2713';
    case 'failed': return '\u2717';
    case 'rebalancing': return '\u21BB';
    case 'upgrading': return '\u25B2';
    case 'verifying': return '\u25C6';
    default: return '\u25CB';
  }
}

function xdcrIcon(state: string): string {
  switch (state) {
    case 'Running': return '\u25B6';
    case 'Paused': return '\u23F8';
    case 'Restarting': return '\u21BB';
    default: return '\u25CB';
  }
}

function xdcrColor(state: string): string {
  switch (state) {
    case 'Running': return '#00ff88';
    case 'Paused': return '#ffaa00';
    case 'Restarting': return '#00aaff';
    default: return '#888';
  }
}
