import React, { useState, useEffect } from 'react';
import { getToken, apiCommand } from '../hooks/useWebSocket';
import { Command } from '../types';

function apiBase(): string {
  if (process.env.REACT_APP_API_URL) return process.env.REACT_APP_API_URL;
  return window.location.origin;
}

interface Runbook {
  id: string; name: string; description: string;
  category: string; steps: string[]; actions: string[]; risk: string;
}

const RISK_COLORS: Record<string, string> = {
  low: '#00ff88', medium: '#ffaa00', high: '#ff4444',
};

const CAT_ICONS: Record<string, string> = {
  upgrade: '\uD83D\uDE80', xdcr: '\uD83D\uDD01', migration: '\uD83D\uDCE6',
  failover: '\u26A0\uFE0F', backup: '\uD83D\uDCBE',
};

export function RunbooksPanel() {
  const [runbooks, setRunbooks] = useState<Runbook[]>([]);
  const [selected, setSelected] = useState<Runbook | null>(null);
  const [activeStep, setActiveStep] = useState(-1);
  const [stepResults, setStepResults] = useState<Record<number, string>>({});
  const [executing, setExecuting] = useState(false);

  useEffect(() => {
    const token = getToken();
    const headers: Record<string, string> = token ? { Authorization: `Bearer ${token}` } : {};
    fetch(`${apiBase()}/api/v1/runbooks`, { headers })
      .then(r => r.json())
      .then(data => setRunbooks(Array.isArray(data) ? data : []))
      .catch(() => {});
  }, []);

  const executeStep = async (rb: Runbook, stepIdx: number) => {
    if (stepIdx >= rb.actions.length) {
      setStepResults(prev => ({ ...prev, [stepIdx]: 'No automated action for this step. Complete manually.' }));
      setActiveStep(stepIdx);
      return;
    }
    setExecuting(true);
    setActiveStep(stepIdx);
    try {
      const result = await apiCommand({ action: rb.actions[stepIdx] } as Command);
      setStepResults(prev => ({ ...prev, [stepIdx]: result.message || 'Executed successfully' }));
    } catch (err: any) {
      setStepResults(prev => ({ ...prev, [stepIdx]: `Error: ${err.message}` }));
    }
    setExecuting(false);
  };

  const startRunbook = (rb: Runbook) => {
    setSelected(rb);
    setActiveStep(-1);
    setStepResults({});
  };

  return (
    <div className="logs-panel">
      <div className="rca-header">
        <div className="rca-title"><span className="ai-icon">&#x1F4CB;</span> Runbooks</div>
      </div>

      <div className="rca-content">
        {/* Runbook list */}
        <div className="rca-list">
          {runbooks.map(rb => (
            <div
              key={rb.id}
              className={`rca-item ${selected?.id === rb.id ? 'active' : ''}`}
              onClick={() => startRunbook(rb)}
            >
              <div className="rca-item-header">
                <span style={{ fontSize: '1.2rem' }}>{CAT_ICONS[rb.category] || '\uD83D\uDCCB'}</span>
                <span className="rca-step-risk" style={{ color: RISK_COLORS[rb.risk] || '#888',
                  background: `${RISK_COLORS[rb.risk] || '#888'}20` }}>{rb.risk} risk</span>
              </div>
              <div style={{ fontWeight: 700, fontSize: '0.85rem', marginTop: 4 }}>{rb.name}</div>
              <div style={{ fontSize: '0.72rem', color: '#8888aa', marginTop: 4 }}>{rb.description.substring(0, 80)}...</div>
              <div style={{ fontSize: '0.65rem', color: '#6666aa', marginTop: 4 }}>{rb.steps.length} steps | {rb.category}</div>
            </div>
          ))}
        </div>

        {/* Runbook execution */}
        {selected ? (
          <div className="rca-detail">
            <div className="rca-detail-header">
              <span style={{ fontSize: '1.3rem' }}>{CAT_ICONS[selected.category] || '\uD83D\uDCCB'}</span>
              <span style={{ fontWeight: 700, fontSize: '1rem' }}>{selected.name}</span>
              <span className="rca-step-risk" style={{ color: RISK_COLORS[selected.risk],
                background: `${RISK_COLORS[selected.risk]}20`, marginLeft: 'auto' }}>
                {selected.risk} risk
              </span>
            </div>
            <div style={{ fontSize: '0.8rem', color: '#8888aa', marginBottom: 16 }}>{selected.description}</div>

            {selected.steps.map((step, i) => (
              <div key={i} className={`runbook-step ${activeStep >= i ? 'active' : ''} ${stepResults[i] ? 'completed' : ''}`}>
                <div className="runbook-step-header">
                  <span className="rca-step-num">{i + 1}</span>
                  <span className="runbook-step-text">{step}</span>
                  <button
                    className="ai-quick-btn"
                    onClick={() => executeStep(selected, i)}
                    disabled={executing}
                    style={{ marginLeft: 'auto' }}
                  >
                    {executing && activeStep === i ? 'Running...' : i < selected.actions.length ? 'Execute' : 'Manual'}
                  </button>
                </div>
                {stepResults[i] && (
                  <div className={`runbook-step-result ${stepResults[i].startsWith('Error') ? 'error' : ''}`}>
                    {stepResults[i]}
                  </div>
                )}
              </div>
            ))}
          </div>
        ) : (
          <div className="rca-detail">
            <div className="ai-empty-state" style={{ padding: 40 }}>
              <div className="ai-empty-icon">&#x1F4CB;</div>
              <div className="ai-empty-title">Select a Runbook</div>
              <div className="ai-empty-text">
                Choose a pre-built workflow to automate common operations: upgrades, XDCR validation, disaster recovery simulation, and more.
              </div>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
