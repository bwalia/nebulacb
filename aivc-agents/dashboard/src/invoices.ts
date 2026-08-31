export type InvoiceSummary = {
  invoice_id: string;
  supplier_name: string;
  received_at: string;
  amount_gbp: number | null;
  po_reference: string | null;
  scenario_hint: string;
  source: string;
  workflow: { run_id: string; status: string; updated_at: string } | null;
};

export type CreditControllerSummary = {
  role: string;
  period: string;
  mailbox: { total_invoices: number; untriaged: number; total_gbp: number };
  queue: { awaiting_approval: number; items: { run_id: string; invoice_id: string }[] };
  outcomes: { posted: number; failed: number };
  by_scenario: Record<string, number>;
  playbook: { step: number; task: string; detail: string }[];
};

export const PROCESS_STEPS = [
  { id: "intake", label: "Intake", detail: "Invoices arrive in the mailbox (EDI, scan, or AI batch)." },
  { id: "extract", label: "Extract", detail: "LLM reads the document; totals validated verbatim." },
  { id: "match", label: "3-way match", detail: "PO, goods receipt, and invoice reconciled." },
  { id: "classify", label: "Classify", detail: "Deterministic exception category (FIN-AP-090)." },
  { id: "policy", label: "Policy", detail: "Autonomy limits — straight-through vs human." },
  { id: "approve", label: "Approve", detail: "Credit controller / GFC decision with SoD." },
  { id: "post", label: "Post", detail: "Idempotent ERP payment." },
  { id: "audit", label: "Audit", detail: "Clause, evidence, and identity on the trail." },
] as const;

export const SCENARIO_LABELS: Record<string, string> = {
  STRAIGHT_THROUGH: "Straight-through",
  PRICE_VARIANCE: "Price variance",
  QUANTITY_VARIANCE: "Qty variance",
  NO_PO: "No PO",
  DUPLICATE_SUSPECT: "Duplicate",
  BANK_DETAIL_CHANGE: "Bank change",
};
