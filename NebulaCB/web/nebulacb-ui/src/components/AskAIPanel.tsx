import React, { useState, useRef, useEffect } from 'react';
import { AIChatMessage, ClusterMetrics } from '../types';
import { aiAnalyze } from '../hooks/useWebSocket';

interface Props {
  clusters: Record<string, ClusterMetrics>;
}

const QUICK_PROMPTS = [
  { label: 'Health Check', question: 'Analyze the current health of all clusters and report any issues', type: 'cluster_health' },
  { label: 'XDCR Status', question: 'Check the XDCR replication status and identify any lag or pipeline issues', type: 'troubleshoot' },
  { label: 'Upgrade Readiness', question: 'Is this cluster ready for a rolling upgrade? Check for any blockers', type: 'troubleshoot' },
  { label: 'Performance', question: 'Analyze current performance metrics and identify bottlenecks', type: 'performance' },
  { label: 'Backup Strategy', question: 'Review the current backup configuration and suggest improvements', type: 'troubleshoot' },
  { label: 'Failover Plan', question: 'Evaluate the failover configuration and suggest improvements for HA', type: 'troubleshoot' },
];

export function AskAIPanel({ clusters }: Props) {
  const [messages, setMessages] = useState<AIChatMessage[]>([]);
  const [input, setInput] = useState('');
  const [loading, setLoading] = useState(false);
  const [selectedCluster, setSelectedCluster] = useState('');
  const [analysisType, setAnalysisType] = useState('troubleshoot');
  const scrollRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (scrollRef.current) {
      scrollRef.current.scrollTop = scrollRef.current.scrollHeight;
    }
  }, [messages]);

  const handleSend = async (question?: string, type?: string) => {
    const q = question || input.trim();
    if (!q) return;

    const userMsg: AIChatMessage = {
      id: `user-${Date.now()}`,
      role: 'user',
      content: q,
      timestamp: new Date().toISOString(),
      type: type || analysisType,
    };
    setMessages(prev => [...prev, userMsg]);
    setInput('');
    setLoading(true);

    try {
      const result = await aiAnalyze({
        type: type || analysisType,
        question: q,
        cluster_name: selectedCluster || undefined,
        context: buildClusterContext(),
      });

      const aiMsg: AIChatMessage = {
        id: result.insight.id,
        role: 'assistant',
        content: formatAIResponse(result.insight, result.raw_reply),
        timestamp: new Date().toISOString(),
        type: result.insight.type,
        tokens_used: result.tokens_used,
        duration: result.duration_seconds,
      };
      setMessages(prev => [...prev, aiMsg]);
    } catch (err: any) {
      const errMsg: AIChatMessage = {
        id: `err-${Date.now()}`,
        role: 'assistant',
        content: `Error: ${err.message || 'AI analysis failed. Check that AI is enabled in config.json with a valid API key.'}`,
        timestamp: new Date().toISOString(),
      };
      setMessages(prev => [...prev, errMsg]);
    } finally {
      setLoading(false);
    }
  };

  const buildClusterContext = (): string => {
    const parts: string[] = [];
    Object.entries(clusters).forEach(([name, cm]) => {
      parts.push(`Cluster ${name}: ${cm.healthy ? 'healthy' : 'UNHEALTHY'}, ` +
        `nodes=${cm.nodes?.length || 0}, docs=${cm.total_docs}, ops/s=${cm.ops_per_sec?.toFixed(0)}, ` +
        `version=${cm.version}, edition=${cm.edition || 'unknown'}, rebalance=${cm.rebalance_state}`);
    });
    return parts.join('\n');
  };

  const formatAIResponse = (insight: any, rawReply: string): string => {
    if (insight.title && insight.summary) {
      let response = `**${insight.title}**\n\n${insight.summary}`;
      if (insight.suggestions?.length > 0) {
        response += '\n\n**Recommendations:**\n' + insight.suggestions.map((s: string, i: number) => `${i + 1}. ${s}`).join('\n');
      }
      if (insight.details && insight.details !== insight.summary) {
        response += '\n\n**Details:**\n' + insight.details;
      }
      return response;
    }
    return rawReply || 'No response received.';
  };

  const clusterNames = Object.keys(clusters);

  return (
    <div className="ai-chat-panel">
      <div className="ai-chat-header">
        <div className="ai-chat-title">
          <span className="ai-icon">&#x1F916;</span> Ask AI
        </div>
        <div className="ai-chat-controls">
          <select value={analysisType} onChange={e => setAnalysisType(e.target.value)} className="ai-select">
            <option value="troubleshoot">Troubleshoot</option>
            <option value="cluster_health">Health Check</option>
            <option value="performance">Performance</option>
            <option value="error">Error Analysis</option>
            <option value="logs">Log Analysis</option>
          </select>
          <select value={selectedCluster} onChange={e => setSelectedCluster(e.target.value)} className="ai-select">
            <option value="">All Clusters</option>
            {clusterNames.map(n => <option key={n} value={n}>{n}</option>)}
          </select>
        </div>
      </div>

      {/* Quick prompts */}
      <div className="ai-quick-prompts">
        {QUICK_PROMPTS.map(p => (
          <button key={p.label} className="ai-quick-btn" onClick={() => handleSend(p.question, p.type)} disabled={loading}>
            {p.label}
          </button>
        ))}
      </div>

      {/* Chat messages */}
      <div className="ai-chat-messages" ref={scrollRef}>
        {messages.length === 0 && (
          <div className="ai-empty-state">
            <div className="ai-empty-icon">&#x1F916;</div>
            <div className="ai-empty-title">NebulaCB AI Assistant</div>
            <div className="ai-empty-text">
              Ask about cluster health, XDCR issues, upgrade planning, performance tuning,
              backup strategies, or any Couchbase management question.
            </div>
          </div>
        )}
        {messages.map(msg => (
          <div key={msg.id} className={`ai-msg ai-msg-${msg.role}`}>
            <div className="ai-msg-header">
              <span className="ai-msg-role">{msg.role === 'user' ? 'You' : 'AI'}</span>
              {msg.tokens_used && <span className="ai-msg-meta">{msg.tokens_used} tokens</span>}
              {msg.duration && <span className="ai-msg-meta">{msg.duration.toFixed(1)}s</span>}
            </div>
            <div className="ai-msg-content">
              {msg.content.split('\n').map((line, i) => {
                if (line.startsWith('**') && line.endsWith('**')) {
                  return <div key={i} className="ai-msg-heading">{line.replace(/\*\*/g, '')}</div>;
                }
                if (line.match(/^\d+\.\s/)) {
                  return <div key={i} className="ai-msg-list-item">{line}</div>;
                }
                return <div key={i}>{line || '\u00A0'}</div>;
              })}
            </div>
          </div>
        ))}
        {loading && (
          <div className="ai-msg ai-msg-assistant">
            <div className="ai-msg-header"><span className="ai-msg-role">AI</span></div>
            <div className="ai-typing">
              <span className="ai-typing-dot" />
              <span className="ai-typing-dot" />
              <span className="ai-typing-dot" />
            </div>
          </div>
        )}
      </div>

      {/* Input */}
      <div className="ai-chat-input-row">
        <input
          type="text"
          value={input}
          onChange={e => setInput(e.target.value)}
          onKeyDown={e => e.key === 'Enter' && !e.shiftKey && handleSend()}
          placeholder='Ask: "Why is XDCR lag increasing?" or "Check upgrade readiness"'
          className="ai-chat-input"
          disabled={loading}
        />
        <button onClick={() => handleSend()} disabled={loading || !input.trim()} className="ai-send-btn">
          {loading ? 'Thinking...' : 'Send'}
        </button>
      </div>
    </div>
  );
}
