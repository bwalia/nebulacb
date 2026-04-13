import React, { useState, useEffect, useRef } from 'react';
import { getToken } from '../hooks/useWebSocket';

function apiBase(): string {
  if (process.env.REACT_APP_API_URL) return process.env.REACT_APP_API_URL;
  return window.location.origin;
}

function authHeaders(): Record<string, string> {
  const token = getToken();
  if (token) return { Authorization: `Bearer ${token}` };
  return {};
}

interface Props {
  namespaces: string[];
}

export function K8sLogsPanel({ namespaces }: Props) {
  const [pods, setPods] = useState<{ name: string; status: string }[]>([]);
  const [selectedPod, setSelectedPod] = useState('');
  const [selectedNs, setSelectedNs] = useState(namespaces[0] || '');
  const [logLines, setLogLines] = useState<string[]>([]);
  const [tailCount, setTailCount] = useState(200);
  const [loading, setLoading] = useState(false);
  const [autoRefresh, setAutoRefresh] = useState(false);
  const [filter, setFilter] = useState('');
  const scrollRef = useRef<HTMLDivElement>(null);
  const intervalRef = useRef<NodeJS.Timeout>(undefined);

  useEffect(() => {
    if (!selectedNs) return;
    fetch(`${apiBase()}/api/v1/k8s/pods?namespace=${selectedNs}`, { headers: authHeaders() })
      .then(r => r.json())
      .then(data => {
        const p = Array.isArray(data) ? data : [];
        setPods(p);
        if (p.length > 0 && !selectedPod) setSelectedPod(p[0].name);
      })
      .catch(() => setPods([]));
  }, [selectedNs]); // eslint-disable-line react-hooks/exhaustive-deps

  const fetchLogs = async () => {
    if (!selectedPod || !selectedNs) return;
    setLoading(true);
    try {
      const resp = await fetch(
        `${apiBase()}/api/v1/k8s/logs?pod=${selectedPod}&namespace=${selectedNs}&tail=${tailCount}`,
        { headers: authHeaders() }
      );
      const data = await resp.json();
      setLogLines(data.lines || []);
      if (scrollRef.current) {
        setTimeout(() => { scrollRef.current!.scrollTop = scrollRef.current!.scrollHeight; }, 100);
      }
    } catch { setLogLines(['Error fetching logs']); }
    setLoading(false);
  };

  useEffect(() => { if (selectedPod) fetchLogs(); }, [selectedPod, tailCount]); // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    if (autoRefresh) {
      intervalRef.current = setInterval(fetchLogs, 5000);
    } else {
      clearInterval(intervalRef.current);
    }
    return () => clearInterval(intervalRef.current);
  }, [autoRefresh, selectedPod, selectedNs, tailCount]); // eslint-disable-line react-hooks/exhaustive-deps

  const filteredLines = filter
    ? logLines.filter(l => l.toLowerCase().includes(filter.toLowerCase()))
    : logLines;

  const getSeverityClass = (line: string): string => {
    const lower = line.toLowerCase();
    if (lower.includes('error') || lower.includes('fatal') || lower.includes('panic')) return 'log-error';
    if (lower.includes('warn')) return 'log-warn';
    if (lower.includes('info')) return 'log-info';
    return '';
  };

  return (
    <div className="logs-panel">
      <div className="rca-header">
        <div className="rca-title"><span className="ai-icon">&#x1F4DC;</span> Pod Logs (kubectl)</div>
        <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
          <label className="auto-refresh-label">
            <input type="checkbox" checked={autoRefresh} onChange={e => setAutoRefresh(e.target.checked)} />
            Auto (5s)
          </label>
          <button className="ai-quick-btn" onClick={fetchLogs} disabled={loading}>
            {loading ? 'Loading...' : 'Refresh'}
          </button>
        </div>
      </div>
      <div className="logs-controls">
        <select value={selectedNs} onChange={e => { setSelectedNs(e.target.value); setSelectedPod(''); }} className="ai-select">
          {namespaces.map(ns => <option key={ns} value={ns}>{ns}</option>)}
        </select>
        <select value={selectedPod} onChange={e => setSelectedPod(e.target.value)} className="ai-select" style={{ flex: 1 }}>
          {pods.map(p => (
            <option key={p.name} value={p.name}>{p.name} ({p.status})</option>
          ))}
        </select>
        <select value={tailCount} onChange={e => setTailCount(Number(e.target.value))} className="ai-select">
          <option value={50}>50 lines</option>
          <option value={100}>100 lines</option>
          <option value={200}>200 lines</option>
          <option value={500}>500 lines</option>
          <option value={1000}>1000 lines</option>
        </select>
        <input
          type="text" value={filter} onChange={e => setFilter(e.target.value)}
          placeholder="Filter logs..." className="ai-chat-input" style={{ flex: 1, padding: '6px 10px', fontSize: '0.75rem' }}
        />
      </div>
      <div className="logs-output" ref={scrollRef}>
        {filteredLines.length === 0 && (
          <div className="ai-empty-state" style={{ padding: 20 }}>
            <div className="ai-empty-text">{loading ? 'Loading logs...' : 'No logs available. Select a pod and click Refresh.'}</div>
          </div>
        )}
        {filteredLines.map((line, i) => (
          <div key={i} className={`log-line ${getSeverityClass(line)}`}>{line}</div>
        ))}
      </div>
    </div>
  );
}
