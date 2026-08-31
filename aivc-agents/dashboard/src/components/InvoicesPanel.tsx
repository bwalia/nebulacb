import { useCallback, useEffect, useMemo, useState } from "react";
import { apiCall, type ApiResult } from "../api";
import { personaById } from "../personas";
import {
  PROCESS_STEPS,
  SCENARIO_LABELS,
  type CreditControllerSummary,
  type InvoiceSummary,
} from "../invoices";

type Props = {
  selectedInvoice: string;
  onSelectInvoice: (id: string) => void;
  onRunId: (runId: string | null) => void;
  autoRunning: boolean;
  setAutoRunning: (v: boolean) => void;
};

export function InvoicesPanel({
  selectedInvoice,
  onSelectInvoice,
  onRunId,
  autoRunning,
  setAutoRunning,
}: Props) {
  const clerk = personaById("clerk");
  const controller = personaById("controller");

  const [invoices, setInvoices] = useState<InvoiceSummary[]>([]);
  const [summary, setSummary] = useState<CreditControllerSummary | null>(null);
  const [detail, setDetail] = useState<{ document_text?: string } | null>(null);
  const [filter, setFilter] = useState<"all" | "untriaged" | "awaiting">("all");
  const [log, setLog] = useState<string[]>([]);
  const [busy, setBusy] = useState<string | null>(null);

  const pushLog = (line: string) => setLog((l) => [line, ...l].slice(0, 12));

  const refresh = useCallback(async () => {
    const [listRes, sumRes] = await Promise.all([
      apiCall("GET", "/v1/ap/invoices", clerk),
      apiCall("GET", "/v1/ap/credit-controller/summary", controller),
    ]);
    if (listRes.ok && listRes.body && typeof listRes.body === "object") {
      const body = listRes.body as { invoices: InvoiceSummary[] };
      setInvoices(body.invoices ?? []);
    }
    if (sumRes.ok && sumRes.body && typeof sumRes.body === "object") {
      setSummary(sumRes.body as CreditControllerSummary);
    }
  }, [clerk, controller]);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  useEffect(() => {
    if (!selectedInvoice) return;
    void (async () => {
      const res = await apiCall("GET", `/v1/ap/invoices/${selectedInvoice}`, clerk);
      if (res.ok && res.body && typeof res.body === "object") {
        setDetail(res.body as { document_text?: string });
      }
    })();
  }, [selectedInvoice, clerk]);

  const filtered = useMemo(() => {
    if (filter === "untriaged") return invoices.filter((i) => !i.workflow);
    if (filter === "awaiting") {
      return invoices.filter((i) => i.workflow?.status === "awaiting_approval");
    }
    return invoices;
  }, [invoices, filter]);

  const runGenerate = async (cadence: "weekly" | "monthly") => {
    setBusy(`generate-${cadence}`);
    pushLog(`Generating 10 ${cadence} invoices with AI…`);
    const res = await apiCall("POST", "/v1/ap/invoices/generate", clerk, {
      cadence,
      count: 10,
    });
    if (res.ok) {
      const body = res.body as { message?: string };
      pushLog(body.message ?? "Batch generated.");
      await refresh();
    } else {
      pushLog(`Generate failed: ${formatErr(res)}`);
    }
    setBusy(null);
  };

  const triageOne = async (invoiceId: string) => {
    setBusy(`triage-${invoiceId}`);
    pushLog(`Triaging ${invoiceId}…`);
    const res = await apiCall("POST", "/v1/ap/triage", clerk, { invoice_id: invoiceId });
    if (res.ok && res.body && typeof res.body === "object") {
      const body = res.body as { status?: string; run_id?: string };
      pushLog(`${invoiceId} → ${body.status ?? "done"}`);
      if (body.run_id) onRunId(body.run_id);
    } else {
      pushLog(`Triage ${invoiceId} failed: ${formatErr(res)}`);
    }
    await refresh();
    setBusy(null);
  };

  const triageUntriaged = async () => {
    const ids = invoices.filter((i) => !i.workflow).map((i) => i.invoice_id);
    if (!ids.length) {
      pushLog("No untriaged invoices.");
      return;
    }
    setAutoRunning(true);
    setBusy("batch");
    pushLog(`Credit controller batch: triaging ${ids.length} invoices…`);
    const res = await apiCall("POST", "/v1/ap/triage/batch", clerk, { invoice_ids: ids.slice(0, 15) });
    if (res.ok && res.body && typeof res.body === "object") {
      const body = res.body as { triaged?: number; awaiting_approval?: number; succeeded?: number };
      pushLog(
        `Batch done — ${body.triaged} triaged, ${body.awaiting_approval} awaiting approval, ${body.succeeded} posted.`,
      );
    } else {
      pushLog(`Batch triage failed: ${formatErr(res)}`);
    }
    await refresh();
    setBusy(null);
    setAutoRunning(false);
  };

  const runFullMonth = async () => {
    setAutoRunning(true);
    pushLog("Month-end credit controller run…");
    await runGenerate("monthly");
    await triageUntriaged();
    pushLog("Queue refreshed — approve exceptions in the AP workflow stage.");
    setAutoRunning(false);
  };

  return (
    <div className="invoices-panel">
      <section className="cc-hero">
        <div>
          <h3>Credit controller — {summary?.period ?? "August 2026"}</h3>
          <p>
            Realistic supplier invoices, AI-generated weekly/monthly batches, and the full AP
            exception workflow from intake to audit.
          </p>
        </div>
        <div className="cc-stats">
          <Stat label="Mailbox" value={String(summary?.mailbox.total_invoices ?? invoices.length)} />
          <Stat label="Untriaged" value={String(summary?.mailbox.untriaged ?? "—")} />
          <Stat label="Awaiting" value={String(summary?.queue.awaiting_approval ?? "—")} />
          <Stat
            label="Portfolio"
            value={`£${(summary?.mailbox.total_gbp ?? 0).toLocaleString("en-GB")}`}
          />
        </div>
      </section>

      <section className="process-rail" aria-label="AP process">
        {PROCESS_STEPS.map((step, i) => (
          <div key={step.id} className="process-step">
            <span className="process-num">{i + 1}</span>
            <strong>{step.label}</strong>
            <em>{step.detail}</em>
          </div>
        ))}
      </section>

      <div className="invoices-toolbar">
        <div className="filter-tabs">
          {(["all", "untriaged", "awaiting"] as const).map((f) => (
            <button
              key={f}
              type="button"
              className={`tab ${filter === f ? "is-active" : ""}`}
              onClick={() => setFilter(f)}
            >
              {f === "all" ? "All" : f === "untriaged" ? "Untriaged" : "Awaiting approval"}
            </button>
          ))}
        </div>
        <div className="toolbar-actions">
          <button
            type="button"
            className="btn ghost"
            disabled={!!busy || autoRunning}
            onClick={() => void refresh()}
          >
            Refresh
          </button>
          <button
            type="button"
            className="btn"
            disabled={!!busy || autoRunning}
            onClick={() => void runGenerate("weekly")}
          >
            {busy === "generate-weekly" ? "…" : "AI: 10 weekly"}
          </button>
          <button
            type="button"
            className="btn"
            disabled={!!busy || autoRunning}
            onClick={() => void runGenerate("monthly")}
          >
            {busy === "generate-monthly" ? "…" : "AI: 10 monthly"}
          </button>
          <button
            type="button"
            className="btn primary"
            disabled={!!busy || autoRunning}
            onClick={() => void triageUntriaged()}
          >
            {busy === "batch" ? "Triaging…" : "Triage untriaged"}
          </button>
          <button
            type="button"
            className="btn primary"
            disabled={!!busy || autoRunning}
            onClick={() => void runFullMonth()}
          >
            Full month-end run
          </button>
        </div>
      </div>

      <div className="invoices-layout">
        <div className="invoice-table-wrap">
          <table className="invoice-table">
            <thead>
              <tr>
                <th>Invoice</th>
                <th>Supplier</th>
                <th>Received</th>
                <th>Amount</th>
                <th>Scenario</th>
                <th>Status</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {filtered.map((inv) => (
                <tr
                  key={inv.invoice_id}
                  className={inv.invoice_id === selectedInvoice ? "is-selected" : ""}
                  onClick={() => onSelectInvoice(inv.invoice_id)}
                >
                  <td>
                    <code>{inv.invoice_id}</code>
                    {inv.source === "generated" && <span className="tag gen">AI</span>}
                  </td>
                  <td>{inv.supplier_name}</td>
                  <td>{inv.received_at}</td>
                  <td>{inv.amount_gbp != null ? `£${inv.amount_gbp.toLocaleString("en-GB")}` : "—"}</td>
                  <td>
                    <span className={`tag scen-${inv.scenario_hint.toLowerCase()}`}>
                      {SCENARIO_LABELS[inv.scenario_hint] ?? inv.scenario_hint}
                    </span>
                  </td>
                  <td>
                    {inv.workflow ? (
                      <span className={`status st-${inv.workflow.status}`}>{inv.workflow.status}</span>
                    ) : (
                      <span className="status st-new">new</span>
                    )}
                  </td>
                  <td>
                    <button
                      type="button"
                      className="btn sm"
                      disabled={!!busy || autoRunning}
                      onClick={(e) => {
                        e.stopPropagation();
                        void triageOne(inv.invoice_id);
                      }}
                    >
                      Triage
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>

        <aside className="invoice-detail">
          <h4>{selectedInvoice || "Select an invoice"}</h4>
          {detail?.document_text ? (
            <pre className="doc-preview">{detail.document_text}</pre>
          ) : (
            <p className="muted">Click a row to preview the supplier document.</p>
          )}
          {summary?.playbook && (
            <>
              <h4>Credit controller playbook</h4>
              <ol className="playbook">
                {summary.playbook.map((p) => (
                  <li key={p.step}>
                    <strong>{p.task}</strong>
                    <span>{p.detail}</span>
                  </li>
                ))}
              </ol>
            </>
          )}
        </aside>
      </div>

      {log.length > 0 && (
        <section className="activity-log">
          <h4>Activity</h4>
          <ul>
            {log.map((line, i) => (
              <li key={`${line}-${i}`}>{line}</li>
            ))}
          </ul>
        </section>
      )}
    </div>
  );
}

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <div className="cc-stat">
      <span>{label}</span>
      <strong>{value}</strong>
    </div>
  );
}

function formatErr(res: ApiResult): string {
  if (res.error) return res.error;
  if (typeof res.body === "object" && res.body && "detail" in res.body) {
    return String((res.body as { detail: unknown }).detail);
  }
  return `HTTP ${res.status}`;
}
