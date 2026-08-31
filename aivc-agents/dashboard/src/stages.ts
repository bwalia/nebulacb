export type ActionKind = "GET" | "POST";

export type WorkflowAction = {
  id: string;
  title: string;
  why: string;
  method: ActionKind;
  path: string | ((ctx: WorkflowContext) => string);
  personaId: string;
  body?: Record<string, unknown> | ((ctx: WorkflowContext) => Record<string, unknown>);
  expect?: string;
};

export type WorkflowStage = {
  id: string;
  label: string;
  subtitle: string;
  actions: WorkflowAction[];
};

export type WorkflowContext = {
  lastRunId: string | null;
  selectedInvoice: string;
};

export const INVOICES = [
  "INV-1001",
  "INV-1002",
  "INV-1003",
  "INV-1004",
  "INV-1005",
  "INV-1006",
  "INV-1007",
] as const;

export const STAGES: WorkflowStage[] = [
  {
    id: "probe",
    label: "Probe",
    subtitle: "Health before anything else",
    actions: [
      {
        id: "healthz",
        title: "Liveness",
        why: "Process is up — provider, model, version.",
        method: "GET",
        path: "/healthz",
        personaId: "employee",
        expect: "status: ok",
      },
      {
        id: "readyz",
        title: "Readiness",
        why: "Corpus and checkpoint store are reachable.",
        method: "GET",
        path: "/readyz",
        personaId: "employee",
        expect: "corpus chunks > 0",
      },
    ],
  },
  {
    id: "policy",
    label: "Policy",
    subtitle: "Governed RAG + ACL",
    actions: [
      {
        id: "merit-refuse",
        title: "Merit budget — employee refuse",
        why: "Same question, no HR role — refuse, no 3.4% leak.",
        method: "POST",
        path: "/v1/policy/ask",
        personaId: "employee",
        body: {
          question: "What is the Group merit budget for the 2026 compensation cycle?",
        },
        expect: "refused: true",
      },
      {
        id: "merit-hr",
        title: "Merit budget — HR answer",
        why: "HR BP sees the restricted chunk with citations.",
        method: "POST",
        path: "/v1/policy/ask",
        personaId: "hr",
        body: {
          question: "What is the Group merit budget for the 2026 compensation cycle?",
        },
        expect: "3.4% + HR-COMP-003",
      },
      {
        id: "mileage",
        title: "Mileage rate",
        why: "Public policy — citations for any employee.",
        method: "POST",
        path: "/v1/policy/ask",
        personaId: "employee",
        body: {
          question: "What mileage rate applies to the first 10,000 business miles?",
        },
        expect: "45 pence / mile",
      },
      {
        id: "out-of-corpus",
        title: "Out of corpus",
        why: "Unanswerable question produces a refusal, not a guess.",
        method: "POST",
        path: "/v1/policy/ask",
        personaId: "employee",
        body: {
          question: "How many weeks of paid parental leave do we offer?",
        },
        expect: "refused",
      },
      {
        id: "expense",
        title: "Expense threshold",
        why: "Approval threshold above GBP 2,000.",
        method: "POST",
        path: "/v1/policy/ask",
        personaId: "employee",
        body: {
          question: "What is the expense approval threshold for a claim over GBP 2,000?",
        },
        expect: "Finance Director",
      },
    ],
  },
  {
    id: "assistant",
    label: "Supervisor",
    subtitle: "Route, deny, or decline",
    actions: [
      {
        id: "warehouse",
        title: "Exceptions by category",
        why: "Routes to data_analyst + SQL.",
        method: "POST",
        path: "/v1/assistant/ask",
        personaId: "assistant",
        body: {
          question: "How many invoice exceptions do we have by category?",
        },
        expect: "route: data_analyst",
      },
      {
        id: "ap-stuck",
        title: "AP runs awaiting a human",
        why: "Routes to ap_operations.",
        method: "POST",
        path: "/v1/assistant/ask",
        personaId: "assistant",
        body: {
          question: "Which AP runs are stuck waiting for someone?",
        },
        expect: "route: ap_operations",
      },
      {
        id: "policy-via-sup",
        title: "Policy via supervisor",
        why: "Routes to policy_analyst with a citation.",
        method: "POST",
        path: "/v1/assistant/ask",
        personaId: "assistant",
        body: {
          question: "What is the expense approval threshold for a claim over GBP 2,000?",
        },
        expect: "route: policy_analyst",
      },
      {
        id: "least-priv",
        title: "Least privilege deny",
        why: "Contractor lacks warehouse:read — tools denied.",
        method: "POST",
        path: "/v1/assistant/ask",
        personaId: "contractor",
        body: {
          question: "How many invoice exceptions do we have by category?",
        },
        expect: "denied_tools present",
      },
      {
        id: "decline",
        title: "Out of scope decline",
        why: "Share price is outside the catalogue.",
        method: "POST",
        path: "/v1/assistant/ask",
        personaId: "assistant",
        body: {
          question: "What was the share price at close yesterday?",
        },
        expect: "declined: true",
      },
    ],
  },
  {
    id: "invoices",
    label: "Invoices",
    subtitle: "Mailbox, AI batches, credit controller",
    actions: [
      {
        id: "list-invoices",
        title: "List invoice mailbox",
        why: "All fixture + AI-generated invoices with scenario hints.",
        method: "GET",
        path: "/v1/ap/invoices",
        personaId: "clerk",
        expect: "invoices[] with amounts and workflow status",
      },
      {
        id: "cc-summary",
        title: "Credit controller summary",
        why: "Untriaged count, approval queue, month-end playbook.",
        method: "GET",
        path: "/v1/ap/credit-controller/summary",
        personaId: "controller",
        expect: "mailbox + playbook",
      },
      {
        id: "generate-monthly",
        title: "Generate 10 monthly samples (AI)",
        why: "Ollama drafts realistic invoice documents into the ERP stub.",
        method: "POST",
        path: "/v1/ap/invoices/generate",
        personaId: "clerk",
        body: { cadence: "monthly", count: 10 },
        expect: "batch + generated[]",
      },
      {
        id: "triage-batch",
        title: "Triage batch (untriaged ids)",
        why: "Credit controller runs the full workflow on each invoice.",
        method: "POST",
        path: "/v1/ap/triage/batch",
        personaId: "clerk",
        body: { invoice_ids: ["INV-1001", "INV-1002", "INV-1003"] },
        expect: "results[] with status per invoice",
      },
    ],
  },
  {
    id: "ap",
    label: "AP workflow",
    subtitle: "Triage → queue → approve → audit",
    actions: [
      {
        id: "triage",
        title: "Triage selected invoice",
        why: "Start the durable AP exception workflow.",
        method: "POST",
        path: "/v1/ap/triage",
        personaId: "clerk",
        body: (ctx) => ({ invoice_id: ctx.selectedInvoice }),
        expect: "succeeded | awaiting_approval",
      },
      {
        id: "queue",
        title: "List approval queue",
        why: "Copy a run_id for the next step.",
        method: "GET",
        path: "/v1/ap/queue",
        personaId: "clerk",
        expect: "awaiting_approval[]",
      },
      {
        id: "approve",
        title: "Approve awaiting run",
        why: "Controller resumes — approver must equal X-User.",
        method: "POST",
        path: "/v1/ap/approve",
        personaId: "controller",
        body: (ctx) => ({
          run_id: ctx.lastRunId ?? "wf_paste_from_queue",
          approved: true,
          approver: "s.oyelaran",
          note: "checked against the signed contract variation",
        }),
        expect: "status: succeeded",
      },
      {
        id: "reject",
        title: "Reject awaiting run",
        why: "Same identity rule; approved=false.",
        method: "POST",
        path: "/v1/ap/approve",
        personaId: "controller",
        body: (ctx) => ({
          run_id: ctx.lastRunId ?? "wf_paste_from_queue",
          approved: false,
          approver: "s.oyelaran",
          note: "variance not supported by contract",
        }),
        expect: "rejected / failed",
      },
      {
        id: "inspect",
        title: "Inspect run audit trail",
        why: "Steps and events for one run_id.",
        method: "GET",
        path: (ctx) => `/v1/runs/${ctx.lastRunId ?? "wf_unknown"}`,
        personaId: "clerk",
        expect: "steps + events",
      },
    ],
  },
];
