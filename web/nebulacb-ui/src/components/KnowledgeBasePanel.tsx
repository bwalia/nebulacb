import React, { useState, useEffect } from 'react';
import { KnowledgeBaseEntry } from '../types';
import { fetchKnowledgeBase } from '../hooks/useWebSocket';

const CATEGORIES = [
  'All', 'XDCR', 'Upgrade', 'Performance', 'Failover', 'Backup', 'Data Integrity', 'Configuration',
];

const SEVERITY_COLORS: Record<string, string> = {
  critical: '#ff4444',
  high: '#ff6644',
  warning: '#ffaa00',
  info: '#00aaff',
};

export function KnowledgeBasePanel() {
  const [entries, setEntries] = useState<KnowledgeBaseEntry[]>([]);
  const [filter, setFilter] = useState('All');
  const [search, setSearch] = useState('');
  const [expanded, setExpanded] = useState<string | null>(null);

  useEffect(() => {
    fetchKnowledgeBase().then(data => {
      setEntries(Array.isArray(data) ? data : []);
    }).catch(() => {});
  }, []);

  const filtered = entries.filter(e => {
    const matchCat = filter === 'All' || e.category.toLowerCase().includes(filter.toLowerCase());
    const matchSearch = !search ||
      e.title.toLowerCase().includes(search.toLowerCase()) ||
      e.description.toLowerCase().includes(search.toLowerCase()) ||
      e.tags?.some(t => t.toLowerCase().includes(search.toLowerCase()));
    return matchCat && matchSearch;
  });

  return (
    <div className="kb-panel">
      <div className="kb-header">
        <div className="rca-title">
          <span className="ai-icon">&#x1F4DA;</span> Knowledge Base
        </div>
        <span className="kb-count">{filtered.length} articles</span>
      </div>

      {/* Search + filter */}
      <div className="kb-controls">
        <input
          type="text"
          value={search}
          onChange={e => setSearch(e.target.value)}
          placeholder="Search knowledge base..."
          className="ai-chat-input"
          style={{ flex: 1 }}
        />
        <div className="kb-category-tabs">
          {CATEGORIES.map(c => (
            <button
              key={c}
              className={`kb-cat-btn ${filter === c ? 'active' : ''}`}
              onClick={() => setFilter(c)}
            >
              {c}
            </button>
          ))}
        </div>
      </div>

      {/* Entries */}
      <div className="kb-entries">
        {filtered.length === 0 && (
          <div className="ai-empty-state" style={{ padding: 20 }}>
            <div className="ai-empty-text">
              {entries.length === 0
                ? 'Knowledge base is loading... AI will populate common Couchbase issues and solutions.'
                : 'No matching articles found.'}
            </div>
          </div>
        )}
        {filtered.map(entry => (
          <div key={entry.id} className={`kb-entry ${expanded === entry.id ? 'expanded' : ''}`}>
            <div className="kb-entry-header" onClick={() => setExpanded(expanded === entry.id ? null : entry.id)}>
              <span className="kb-severity" style={{ color: SEVERITY_COLORS[entry.severity] || '#888' }}>
                {entry.severity?.toUpperCase()}
              </span>
              <span className="kb-entry-title">{entry.title}</span>
              <span className="kb-entry-cat">{entry.category}</span>
              <span className="kb-expand">{expanded === entry.id ? '\u25B2' : '\u25BC'}</span>
            </div>
            {expanded === entry.id && (
              <div className="kb-entry-body">
                <div className="kb-section">
                  <div className="kb-section-label">Description</div>
                  <div>{entry.description}</div>
                </div>
                {entry.symptoms?.length > 0 && (
                  <div className="kb-section">
                    <div className="kb-section-label">Symptoms</div>
                    <ul className="kb-symptoms">
                      {entry.symptoms.map((s, i) => <li key={i}>{s}</li>)}
                    </ul>
                  </div>
                )}
                <div className="kb-section">
                  <div className="kb-section-label">Solution</div>
                  <div className="kb-solution">{entry.solution}</div>
                </div>
                {entry.commands && entry.commands.length > 0 && (
                  <div className="kb-section">
                    <div className="kb-section-label">Commands</div>
                    {entry.commands.map((cmd, i) => (
                      <code key={i} className="rca-step-cmd">{cmd}</code>
                    ))}
                  </div>
                )}
                {entry.tags?.length > 0 && (
                  <div className="kb-tags">
                    {entry.tags.map(t => <span key={t} className="kb-tag">{t}</span>)}
                  </div>
                )}
              </div>
            )}
          </div>
        ))}
      </div>
    </div>
  );
}
