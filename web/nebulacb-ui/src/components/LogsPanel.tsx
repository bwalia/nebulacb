import React, { useEffect, useMemo, useRef, useState } from 'react';
import { Alert, XDCRStatus, UpgradeStatus, ClusterMetrics } from '../types';

interface Props {
  alerts: Alert[];
  xdcr: XDCRStatus;
  upgrade: UpgradeStatus;
  clusters: Record<string, ClusterMetrics>;
}

interface LogLine {
  id: string;
  ts: number;
  level: 'error' | 'warn' | 'info' | 'debug';
  source: string;
  message: string;
}

type Severity = 'all' | 'error' | 'warn' | 'info';

const MAX_LINES = 200;

export const LogsPanel: React.FC<Props> = ({ alerts, xdcr, upgrade, clusters }) => {
  const [lines, setLines] = useState<LogLine[]>([]);
  const [filter, setFilter] = useState<Severity>('all');
  const [sourceFilter, setSourceFilter] = useState<string>('all');
  const [paused, setPaused] = useState(false);
  const seenRef = useRef<Set<string>>(new Set());
  const tailRef = useRef<HTMLDivElement>(null);
  const lastXDCRRef = useRef<string>('');
  const lastPhaseRef = useRef<string>('');
  const lastTopologyRef = useRef<boolean>(false);

  useEffect(() => {
    if (paused) return;
    const additions: LogLine[] = [];
    const now = Date.now();

    for (const a of alerts) {
      const id = `alert:${a.id}`;
      if (!a.id || seenRef.current.has(id)) continue;
      seenRef.current.add(id);
      additions.push({
        id, ts: a.timestamp ? new Date(a.timestamp).getTime() : now,
        level: a.severity === 'critical' ? 'error' : a.severity === 'warning' ? 'warn' : 'info',
        source: a.source || a.category || 'system',
        message: `${a.title}${a.message ? ' — ' + a.message : ''}`,
      });
    }

    if (xdcr.state && xdcr.state !== lastXDCRRef.current) {
      const prev = lastXDCRRef.current;
      lastXDCRRef.current = xdcr.state;
      if (prev) {
        const id = `xdcr-state:${prev}->${xdcr.state}:${now}`;
        if (!seenRef.current.has(id)) {
          seenRef.current.add(id);
          additions.push({
            id, ts: now,
            level: xdcr.state === 'Paused' ? 'warn' : 'info',
            source: 'goxdcr',
            message: `replication state transition: ${prev} -> ${xdcr.state} (changes_left=${xdcr.changes_left})`,
          });
        }
      }
    }

    if (xdcr.topology_change !== lastTopologyRef.current) {
      lastTopologyRef.current = xdcr.topology_change;
      if (xdcr.topology_change) {
        const id = `topology:${now}`;
        seenRef.current.add(id);
        additions.push({
          id, ts: now, level: 'warn', source: 'goxdcr',
          message: 'topology change detected — pipeline will restart after delay',
        });
      }
    }

    if (upgrade.phase && upgrade.phase !== lastPhaseRef.current) {
      const prev = lastPhaseRef.current;
      lastPhaseRef.current = upgrade.phase;
      if (prev) {
        const id = `upgrade-phase:${upgrade.phase}:${now}`;
        if (!seenRef.current.has(id)) {
          seenRef.current.add(id);
          additions.push({
            id, ts: now,
            level: upgrade.phase === 'failed' ? 'error' : 'info',
            source: 'ns_server',
            message: `upgrade phase: ${prev} -> ${upgrade.phase} (${upgrade.nodes_completed}/${upgrade.nodes_total} nodes)`,
          });
        }
      }
    }

    if (additions.length === 0) return;
    setLines((prev) => {
      const merged = [...prev, ...additions].sort((a, b) => a.ts - b.ts);
      return merged.slice(-MAX_LINES);
    });
  }, [alerts, xdcr, upgrade, paused]);

  useEffect(() => {
    if (paused || !tailRef.current) return;
    tailRef.current.scrollTop = tailRef.current.scrollHeight;
  }, [lines, paused]);

  const sources = useMemo(() => {
    const s = new Set<string>();
    lines.forEach((l) => s.add(l.source));
    Object.keys(clusters).forEach((c) => s.add(c));
    return Array.from(s).sort();
  }, [lines, clusters]);

  const filtered = lines.filter((l) => {
    if (filter !== 'all' && l.level !== filter) return false;
    if (sourceFilter !== 'all' && l.source !== sourceFilter) return false;
    return true;
  });

  return (
    <div className="cockpit-logs-panel">
      <div className="panel-header">
        <span className="panel-indicator" style={{ backgroundColor: '#00cccc' }} />
        LIVE LOGS
        <span className="cockpit-logs-count">{filtered.length}</span>
        <span className="cockpit-timeline-spacer" />
        <button
          className={`cockpit-logs-pill ${paused ? 'paused' : ''}`}
          onClick={() => setPaused((p) => !p)}
          title={paused ? 'Resume tail' : 'Pause tail'}
        >
          {paused ? 'PAUSED' : 'LIVE'}
        </button>
      </div>

      <div className="cockpit-logs-filters">
        <div className="cockpit-logs-filter-group">
          {(['all', 'error', 'warn', 'info'] as Severity[]).map((s) => (
            <button
              key={s}
              className={`cockpit-logs-filter ${filter === s ? 'active' : ''} sev-${s}`}
              onClick={() => setFilter(s)}
            >
              {s.toUpperCase()}
            </button>
          ))}
        </div>
        <select
          className="cockpit-logs-source"
          value={sourceFilter}
          onChange={(e) => setSourceFilter(e.target.value)}
        >
          <option value="all">all sources</option>
          {sources.map((s) => (
            <option key={s} value={s}>{s}</option>
          ))}
        </select>
      </div>

      <div className="cockpit-logs-tail" ref={tailRef}>
        {filtered.length === 0 && (
          <div className="cockpit-event-empty">awaiting log events...</div>
        )}
        {filtered.map((l) => (
          <div key={l.id} className={`cockpit-log-line lvl-${l.level}`}>
            <span className="cockpit-log-ts">{new Date(l.ts).toLocaleTimeString()}</span>
            <span className="cockpit-log-level">{l.level.toUpperCase()}</span>
            <span className="cockpit-log-source">{l.source}</span>
            <span className="cockpit-log-msg">{highlight(l.message)}</span>
          </div>
        ))}
      </div>
    </div>
  );
};

function highlight(msg: string): React.ReactNode {
  const patterns = [
    { re: /(topology change detected)/i, cls: 'hl-warn' },
    { re: /(connection closed)/i, cls: 'hl-warn' },
    { re: /(error|failed|critical)/i, cls: 'hl-error' },
    { re: /(rebalance complete|completed)/i, cls: 'hl-good' },
  ];
  for (const p of patterns) {
    const m = msg.match(p.re);
    if (m) {
      const idx = msg.indexOf(m[0]);
      return (
        <>
          {msg.slice(0, idx)}
          <span className={p.cls}>{m[0]}</span>
          {msg.slice(idx + m[0].length)}
        </>
      );
    }
  }
  return msg;
}
