import { useCallback, useMemo, useState } from "react";
import { apiCall, pickRunId, type ApiResult } from "./api";
import { PERSONAS, personaById, type Persona } from "./personas";
import { INVOICES, STAGES, type WorkflowAction, type WorkflowContext } from "./stages";
import { InvoicesPanel } from "./components/InvoicesPanel";

type ActionState = {
  status: "idle" | "running" | "done" | "error";
  result?: ApiResult;
};

function resolvePath(action: WorkflowAction, ctx: WorkflowContext): string {
  return typeof action.path === "function" ? action.path(ctx) : action.path;
}

function resolveBody(
  action: WorkflowAction,
  ctx: WorkflowContext,
): Record<string, unknown> | undefined {
  if (action.body === undefined) return undefined;
  return typeof action.body === "function" ? action.body(ctx) : action.body;
}

export default function App() {
  const [stageIdx, setStageIdx] = useState(0);
  const [personaOverride, setPersonaOverride] = useState<string | null>(null);
  const [invoice, setInvoice] = useState<string>(INVOICES[4]);
  const [lastRunId, setLastRunId] = useState<string | null>(null);
  const [states, setStates] = useState<Record<string, ActionState>>({});
  const [autoRunning, setAutoRunning] = useState(false);

  const stage = STAGES[stageIdx];
  const ctx: WorkflowContext = useMemo(
    () => ({ lastRunId, selectedInvoice: invoice }),
    [lastRunId, invoice],
  );

  const completedInStage = stage.actions.filter((a) => states[a.id]?.status === "done").length;
  const stageDone = completedInStage === stage.actions.length;

  const execute = useCallback(
    async (
      action: WorkflowAction,
      liveCtx: WorkflowContext,
      override: string | null,
    ): Promise<{ result: ApiResult; runId: string | null }> => {
      const persona: Persona = personaById(override ?? action.personaId);
      setStates((s) => ({ ...s, [action.id]: { status: "running" } }));
      const path = resolvePath(action, liveCtx);
      const body = resolveBody(action, liveCtx);
      const result = await apiCall(action.method, path, persona, body);
      const runId = pickRunId(result.body);
      setStates((s) => ({
        ...s,
        [action.id]: { status: result.ok ? "done" : "error", result },
      }));
      return { result, runId };
    },
    [],
  );

  const runAction = async (action: WorkflowAction) => {
    const { runId } = await execute(action, ctx, personaOverride);
    if (runId) setLastRunId(runId);
  };

  const runStage = async () => {
    setAutoRunning(true);
    let runId = lastRunId;
    for (const action of stage.actions) {
      const live: WorkflowContext = { lastRunId: runId, selectedInvoice: invoice };
      const out = await execute(action, live, personaOverride);
      if (out.runId) {
        runId = out.runId;
        setLastRunId(out.runId);
      }
    }
    setAutoRunning(false);
  };

  const runAll = async () => {
    setAutoRunning(true);
    let runId = lastRunId;
    for (let i = 0; i < STAGES.length; i++) {
      setStageIdx(i);
      for (const action of STAGES[i].actions) {
        const live: WorkflowContext = { lastRunId: runId, selectedInvoice: invoice };
        const out = await execute(action, live, personaOverride);
        if (out.runId) {
          runId = out.runId;
          setLastRunId(out.runId);
        }
      }
    }
    setAutoRunning(false);
  };

  const reset = () => {
    setStates({});
    setLastRunId(null);
    setStageIdx(0);
    setPersonaOverride(null);
  };

  return (
    <div className="shell">
      <header className="top">
        <div className="brand-block">
          <p className="eyebrow">AI Value Creation</p>
          <h1>Workflow console</h1>
          <p className="lede">
            Walk every HTTP action against the live agents on this host — probe, policy ACL,
            supervisor routing, durable AP.
          </p>
        </div>
        <div className="top-actions">
          <a className="ghost-link" href="/docs" target="_blank" rel="noreferrer">
            Swagger
          </a>
          <button type="button" className="btn ghost" onClick={reset} disabled={autoRunning}>
            Reset
          </button>
          <button type="button" className="btn primary" onClick={() => void runAll()} disabled={autoRunning}>
            {autoRunning ? "Running…" : "Run all stages"}
          </button>
        </div>
      </header>

      <div className="layout">
        <aside className="rail" aria-label="Workflow stages">
          <ol className="spine">
            {STAGES.map((s, i) => {
              const done = s.actions.every((a) => states[a.id]?.status === "done");
              const active = i === stageIdx;
              const hasError = s.actions.some((a) => states[a.id]?.status === "error");
              return (
                <li key={s.id}>
                  <button
                    type="button"
                    className={[
                      "spine-node",
                      active ? "is-active" : "",
                      done ? "is-done" : "",
                      hasError ? "is-error" : "",
                    ].join(" ")}
                    onClick={() => setStageIdx(i)}
                  >
                    <span className="spine-index">{String(i + 1).padStart(2, "0")}</span>
                    <span className="spine-copy">
                      <strong>{s.label}</strong>
                      <em>{s.subtitle}</em>
                    </span>
                  </button>
                </li>
              );
            })}
          </ol>
        </aside>

        <main className="stage">
          <div className="stage-head">
            <div>
              <p className="stage-kicker">
                Stage {stageIdx + 1} of {STAGES.length}
              </p>
              <h2>{stage.label}</h2>
              <p className="stage-sub">{stage.subtitle}</p>
            </div>
            <div className="stage-meta">
              <span>
                {completedInStage}/{stage.actions.length} actions
              </span>
              <button
                type="button"
                className="btn primary"
                onClick={() => void runStage()}
                disabled={autoRunning}
              >
                Run this stage
              </button>
            </div>
          </div>

          {stage.id === "invoices" && (
            <InvoicesPanel
              selectedInvoice={invoice}
              onSelectInvoice={setInvoice}
              onRunId={setLastRunId}
              autoRunning={autoRunning}
              setAutoRunning={setAutoRunning}
            />
          )}

          {stage.id === "ap" && (
            <div className="ap-controls">
              <label>
                Invoice
                <select value={invoice} onChange={(e) => setInvoice(e.target.value)}>
                  {INVOICES.map((id) => (
                    <option key={id} value={id}>
                      {id}
                    </option>
                  ))}
                </select>
              </label>
              <label>
                Last run_id
                <input
                  value={lastRunId ?? ""}
                  onChange={(e) => setLastRunId(e.target.value || null)}
                  placeholder="filled from triage / queue"
                  spellCheck={false}
                />
              </label>
            </div>
          )}

          <ul className="actions">
            {stage.actions.map((action) => {
              const st = states[action.id] ?? { status: "idle" as const };
              const persona = personaById(personaOverride ?? action.personaId);
              return (
                <li key={action.id} className={`action is-${st.status}`}>
                  <div className="action-main">
                    <div className="action-title-row">
                      <span className={`verb verb-${action.method.toLowerCase()}`}>
                        {action.method}
                      </span>
                      <code className="path">{resolvePath(action, ctx)}</code>
                    </div>
                    <h3>{action.title}</h3>
                    <p>{action.why}</p>
                    <p className="expect">Expect: {action.expect}</p>
                    <p className="persona-chip">
                      as <strong>{persona.label}</strong> · {persona.user} · {persona.roles}
                    </p>
                  </div>
                  <div className="action-side">
                    <button
                      type="button"
                      className="btn"
                      disabled={autoRunning || st.status === "running"}
                      onClick={() => void runAction(action)}
                    >
                      {st.status === "running" ? "…" : "Execute"}
                    </button>
                    {st.result && (
                      <span className={`badge ${st.result.ok ? "ok" : "bad"}`}>
                        {st.result.status || "ERR"} · {st.result.latencyMs}ms
                      </span>
                    )}
                  </div>
                  {st.result && <ResponsePanel result={st.result} />}
                </li>
              );
            })}
          </ul>

          <nav className="stage-nav">
            <button
              type="button"
              className="btn ghost"
              disabled={stageIdx === 0}
              onClick={() => setStageIdx((i) => i - 1)}
            >
              Previous
            </button>
            <button
              type="button"
              className="btn primary"
              disabled={stageIdx >= STAGES.length - 1}
              onClick={() => setStageIdx((i) => i + 1)}
            >
              {stageDone ? "Next stage →" : "Skip to next →"}
            </button>
          </nav>
        </main>

        <aside className="personas" aria-label="Identity">
          <h2>Persona</h2>
          <p className="persona-help">
            Default follows each action. Override to force a different caller on every execute.
          </p>
          <button
            type="button"
            className={`persona ${personaOverride === null ? "is-selected" : ""}`}
            onClick={() => setPersonaOverride(null)}
          >
            <strong>Per-action default</strong>
            <span>Use the identity each step recommends</span>
          </button>
          {PERSONAS.map((p) => (
            <button
              key={p.id}
              type="button"
              className={`persona ${personaOverride === p.id ? "is-selected" : ""}`}
              onClick={() => setPersonaOverride(p.id)}
            >
              <strong>{p.label}</strong>
              <span>
                {p.user} · {p.roles}
              </span>
              <em>{p.blurb}</em>
            </button>
          ))}
        </aside>
      </div>
    </div>
  );
}

function ResponsePanel({ result }: { result: ApiResult }) {
  const text =
    result.error ??
    (typeof result.body === "string" ? result.body : JSON.stringify(result.body, null, 2));
  return (
    <details className="response" open>
      <summary>Response</summary>
      <pre>{text}</pre>
    </details>
  );
}
