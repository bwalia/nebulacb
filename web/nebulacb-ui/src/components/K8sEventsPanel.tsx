import React, { useState, useEffect } from 'react';
import { getToken } from '../hooks/useWebSocket';

function apiBase(): string {
  if (process.env.REACT_APP_API_URL) return process.env.REACT_APP_API_URL;
  return window.location.origin;
}

interface K8sEvent {
  namespace: string; name: string; kind: string; reason: string;
  message: string; type: string; count: number;
  first_seen: string; last_seen: string; object: string;
}

interface Props { namespaces: string[]; }

export function K8sEventsPanel({ namespaces }: Props) {
  const [events, setEvents] = useState<K8sEvent[]>([]);
  const [selectedNs, setSelectedNs] = useState(namespaces[0] || '');
  const [loading, setLoading] = useState(false);
  const [filterType, setFilterType] = useState('all');

  const fetchEvents = async () => {
    if (!selectedNs) return;
    setLoading(true);
    try {
      const token = getToken();
      const headers: Record<string, string> = token ? { Authorization: `Bearer ${token}` } : {};
      const resp = await fetch(`${apiBase()}/api/v1/k8s/events?namespace=${selectedNs}`, { headers });
      const data = await resp.json();
      setEvents(Array.isArray(data) ? data : []);
    } catch { setEvents([]); }
    setLoading(false);
  };

  useEffect(() => { fetchEvents(); }, [selectedNs]); // eslint-disable-line react-hooks/exhaustive-deps

  const filtered = events.filter(e => filterType === 'all' || e.type === filterType).reverse();

  return (
    <div className="logs-panel">
      <div className="rca-header">
        <div className="rca-title"><span className="ai-icon">&#x26A1;</span> Kubernetes Events</div>
        <button className="ai-quick-btn" onClick={fetchEvents} disabled={loading}>
          {loading ? 'Loading...' : 'Refresh'}
        </button>
      </div>
      <div className="logs-controls">
        <select value={selectedNs} onChange={e => setSelectedNs(e.target.value)} className="ai-select">
          {namespaces.map(ns => <option key={ns} value={ns}>{ns}</option>)}
        </select>
        <select value={filterType} onChange={e => setFilterType(e.target.value)} className="ai-select">
          <option value="all">All Events</option>
          <option value="Warning">Warnings Only</option>
          <option value="Normal">Normal Only</option>
        </select>
        <span style={{ fontSize: '0.75rem', color: '#8888aa' }}>{filtered.length} events</span>
      </div>
      <div className="events-list">
        {filtered.length === 0 && (
          <div className="ai-empty-state" style={{ padding: 20 }}>
            <div className="ai-empty-text">No events in this namespace.</div>
          </div>
        )}
        {filtered.map((ev, i) => (
          <div key={i} className={`event-item event-${ev.type?.toLowerCase()}`}>
            <div className="event-header">
              <span className={`event-type ${ev.type === 'Warning' ? 'event-warn' : 'event-normal'}`}>
                {ev.type}
              </span>
              <span className="event-reason">{ev.reason}</span>
              <span className="event-object">{ev.object}</span>
              {ev.count > 1 && <span className="event-count">x{ev.count}</span>}
              <span className="event-time">{ev.last_seen ? new Date(ev.last_seen).toLocaleTimeString() : ''}</span>
            </div>
            <div className="event-message">{ev.message}</div>
          </div>
        ))}
      </div>
    </div>
  );
}
