import { useEffect, useRef, useCallback, useState } from 'react';
import { DashboardState, Command, AIInsight, RCAReport, KnowledgeBaseEntry } from '../types';

function wsUrl(): string {
  if (process.env.REACT_APP_WS_URL) return process.env.REACT_APP_WS_URL;
  const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
  return `${proto}//${window.location.host}/ws`;
}

// ─── Auth helpers ────────────────────────────────────────────────────────────

export function getToken(): string {
  return sessionStorage.getItem('nebulacb_token') || '';
}

export function setToken(token: string) {
  sessionStorage.setItem('nebulacb_token', token);
}

export function clearToken() {
  sessionStorage.removeItem('nebulacb_token');
}

function authHeaders(): Record<string, string> {
  const token = getToken();
  if (token) {
    return { 'Content-Type': 'application/json', Authorization: `Bearer ${token}` };
  }
  return { 'Content-Type': 'application/json' };
}

// ─── Auth API ────────────────────────────────────────────────────────────────

export async function checkAuthRequired(): Promise<boolean> {
  const resp = await fetch(`${apiBase()}/api/v1/auth/check`);
  const data = await resp.json();
  return data.auth_enabled === true;
}

export async function login(username: string, password: string): Promise<{ token: string; error?: string }> {
  const resp = await fetch(`${apiBase()}/api/v1/login`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username, password }),
  });
  const data = await resp.json();
  if (resp.ok && data.token) {
    setToken(data.token);
    return { token: data.token };
  }
  return { token: '', error: data.error || 'Login failed' };
}

export async function logout() {
  await fetch(`${apiBase()}/api/v1/logout`, { method: 'POST', headers: authHeaders() }).catch(() => {});
  clearToken();
}

// ─── WebSocket hook ──────────────────────────────────────────────────────────

export function useWebSocket() {
  const ws = useRef<WebSocket | null>(null);
  const [state, setState] = useState<DashboardState | null>(null);
  const [connected, setConnected] = useState(false);
  const reconnectTimeout = useRef<NodeJS.Timeout>(undefined);

  const connect = useCallback(() => {
    try {
      const token = getToken();
      const url = token ? `${wsUrl()}?token=${encodeURIComponent(token)}` : wsUrl();
      ws.current = new WebSocket(url);

      ws.current.onopen = () => {
        setConnected(true);
      };

      ws.current.onmessage = (event) => {
        try {
          const msg = JSON.parse(event.data);
          if (msg.type === 'dashboard_update') {
            setState(msg.payload);
          }
        } catch (e) {
          // ignore parse errors
        }
      };

      ws.current.onclose = () => {
        setConnected(false);
        reconnectTimeout.current = setTimeout(connect, 3000);
      };

      ws.current.onerror = () => {
        ws.current?.close();
      };
    } catch (e) {
      reconnectTimeout.current = setTimeout(connect, 3000);
    }
  }, []);

  const sendCommand = useCallback((cmd: Command) => {
    if (ws.current?.readyState === WebSocket.OPEN) {
      ws.current.send(JSON.stringify(cmd));
    }
  }, []);

  const reconnect = useCallback(() => {
    clearTimeout(reconnectTimeout.current);
    if (ws.current) {
      ws.current.onclose = null; // prevent auto-reconnect loop
      ws.current.close();
      ws.current = null;
    }
    setConnected(false);
    // Force a REST fetch first for instant data, then re-establish WebSocket
    fetch(`${apiBase()}/api/v1/dashboard`, { headers: authHeaders() })
      .then(r => { if (r.ok) return r.json(); throw new Error('fetch failed'); })
      .then(data => setState(data))
      .catch(() => {});
    setTimeout(connect, 300);
  }, [connect]);

  useEffect(() => {
    connect();
    return () => {
      clearTimeout(reconnectTimeout.current);
      ws.current?.close();
    };
  }, [connect]);

  return { state, connected, sendCommand, reconnect };
}

// ─── REST API helpers ────────────────────────────────────────────────────────

function apiBase(): string {
  if (process.env.REACT_APP_API_URL) return process.env.REACT_APP_API_URL;
  return window.location.origin;
}

export async function apiCommand(cmd: Command): Promise<Record<string, string>> {
  const resp = await fetch(`${apiBase()}/api/v1/command`, {
    method: 'POST',
    headers: authHeaders(),
    body: JSON.stringify(cmd),
  });
  if (resp.status === 401) {
    clearToken();
    window.location.reload();
  }
  return resp.json();
}

export async function fetchDashboard(): Promise<DashboardState> {
  const resp = await fetch(`${apiBase()}/api/v1/dashboard`, { headers: authHeaders() });
  if (resp.status === 401) {
    clearToken();
    window.location.reload();
  }
  return resp.json();
}

// ─── AI API helpers ─────────────────────────────────────────────────────────

export async function aiAnalyze(req: {
  type: string;
  context?: string;
  cluster_name?: string;
  logs?: string[];
  error_msg?: string;
  metrics_json?: string;
  question?: string;
}): Promise<{ insight: AIInsight; raw_reply: string; tokens_used: number; duration_seconds: number }> {
  const resp = await fetch(`${apiBase()}/api/v1/ai/analyze`, {
    method: 'POST',
    headers: authHeaders(),
    body: JSON.stringify(req),
  });
  if (resp.status === 401) { clearToken(); window.location.reload(); }
  if (!resp.ok) {
    const err = await resp.json().catch(() => ({ error: 'AI analysis failed' }));
    throw new Error(err.error || `HTTP ${resp.status}`);
  }
  return resp.json();
}

export async function aiAutoAnalyze(): Promise<{ insight: AIInsight; raw_reply: string }> {
  const resp = await fetch(`${apiBase()}/api/v1/ai/auto-analyze`, {
    method: 'POST',
    headers: authHeaders(),
  });
  if (resp.status === 401) { clearToken(); window.location.reload(); }
  if (!resp.ok) {
    const err = await resp.json().catch(() => ({ error: 'Auto-analyze failed' }));
    throw new Error(err.error || `HTTP ${resp.status}`);
  }
  return resp.json();
}

export async function fetchAIInsights(): Promise<AIInsight[]> {
  const resp = await fetch(`${apiBase()}/api/v1/ai/insights`, { headers: authHeaders() });
  if (resp.status === 401) { clearToken(); window.location.reload(); }
  return resp.json();
}

export async function fetchRCAReports(): Promise<RCAReport[]> {
  const resp = await fetch(`${apiBase()}/api/v1/ai/rca`, { headers: authHeaders() });
  if (resp.status === 401) { clearToken(); window.location.reload(); }
  if (!resp.ok) return [];
  return resp.json();
}

export async function triggerRCA(cluster: string, category: string): Promise<RCAReport> {
  const resp = await fetch(`${apiBase()}/api/v1/ai/rca`, {
    method: 'POST',
    headers: authHeaders(),
    body: JSON.stringify({ cluster, category }),
  });
  if (resp.status === 401) { clearToken(); window.location.reload(); }
  if (!resp.ok) {
    const err = await resp.json().catch(() => ({ error: 'RCA failed' }));
    throw new Error(err.error || `HTTP ${resp.status}`);
  }
  return resp.json();
}

export async function fetchKnowledgeBase(): Promise<KnowledgeBaseEntry[]> {
  const resp = await fetch(`${apiBase()}/api/v1/ai/knowledge`, { headers: authHeaders() });
  if (resp.status === 401) { clearToken(); window.location.reload(); }
  if (!resp.ok) return [];
  return resp.json();
}
