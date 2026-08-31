"""HTTP surface.

Thin by design: parse, build a RunContext, call the agent, return its result plus the run and
trace ids. All policy, budget and observability behaviour lives in the agent layer, so the
same guarantees hold whether an agent is reached over HTTP, from a queue worker, or from a
notebook.

Identity note: this reads the caller from headers because a POC sits behind whatever the
client already runs -- an API gateway, an OIDC proxy, a service mesh. `require_principal` is
the single function to replace with real token verification, and it is deliberately the only
place that knows how identity arrives.
"""

from __future__ import annotations

import time
from contextlib import asynccontextmanager
from datetime import date
from pathlib import Path
from typing import Any, Literal

from fastapi import Body, Depends, FastAPI, HTTPException
from fastapi.openapi.utils import get_openapi
from fastapi.responses import RedirectResponse
from fastapi.security import APIKeyHeader
from fastapi.staticfiles import StaticFiles
from pydantic import BaseModel, Field

from .config import bootstrap_provider, get_settings
from .obs.cost import BudgetExceeded
from .obs.run import RunContext
from .security.identity import Principal
from .store.checkpoint import CheckpointStore

DASHBOARD_DIR = Path(__file__).resolve().parent / "static" / "dashboard"

API_DESCRIPTION = """
Three production-shaped agents behind one HTTP surface. Uses **Ollama** on the LAN by
default (`AIVC_PROVIDER=ollama`) — set `AIVC_PROVIDER=offline` for CI or no-network runs.

## How to try every use case

1. Click **Authorize** (top right) and set the identity headers for the persona you want.
2. Open an endpoint, pick a named **Example** in the request body, click **Execute**.
3. For AP approvals: triage an invoice → copy `run_id` from **GET /v1/ap/queue** → approve
   with `approver` equal to the `X-User` you authorised as.

### Personas (paste into Authorize)

| Persona | X-User | X-Roles | X-Scopes | What it unlocks |
|---|---|---|---|---|
| Employee | `demo` | `employee` | `corpus:read` | Public policies; ACL refuse on HR docs |
| HR BP | `hr.bp` | `employee,hr` | `corpus:read` | Merit budget and other HR-restricted answers |
| Finance clerk | `ap.clerk` | `finance` | `ap:read` | AP triage + queue |
| Controller | `s.oyelaran` | `finance` | `ap:read` | Approve awaiting runs (`approver` must match) |
| Full assistant | `m.lindqvist` | `employee,finance` | `warehouse:read,ap:read,corpus:read` | Supervisor SQL + policy + AP status |
| Contractor | `contractor` | `employee` | `corpus:read` | Same questions, tools denied by least privilege |

### Suggested walkthrough

1. **GET /healthz** then **GET /readyz**
2. **POST /v1/policy/ask** — *Employee: merit budget (ACL refuse)* then switch Authorize to HR and
   run *HR: merit budget (answered + citations)*
3. **POST /v1/assistant/ask** — *Warehouse exceptions*, *Least privilege deny*, *Out of scope decline*
4. **POST /v1/ap/triage** — *INV-1001 auto-post* and *INV-1005 bank change (escalate)*
5. **GET /v1/ap/queue** → **POST /v1/ap/approve** → **GET /v1/runs/{run_id}**

Interactive docs: [/docs](/docs) · Workflow console: [/dashboard/](/dashboard/) · ReDoc: [/redoc](/redoc)
"""

TAGS_METADATA = [
    {"name": "health", "description": "Liveness and readiness probes."},
    {
        "name": "policy",
        "description": "Governed RAG over the policy corpus with ACL-aware retrieval and citations.",
    },
    {
        "name": "assistant",
        "description": "Supervisor that routes to specialists (policy, warehouse, AP ops) or declines.",
    },
    {
        "name": "ap",
        "description": "Durable invoice-exception workflow: triage, human approval, audit trail.",
    },
]


@asynccontextmanager
async def lifespan(_app: FastAPI):
    bootstrap_provider()
    yield


app = FastAPI(
    title="AI Value Creation — reference agents",
    version="0.3.0",
    description=API_DESCRIPTION,
    openapi_tags=TAGS_METADATA,
    lifespan=lifespan,
    swagger_ui_parameters={
        "persistAuthorization": True,
        "displayRequestDuration": True,
        "tryItOutEnabled": True,
        "filter": True,
        "docExpansion": "list",
    },
)


# --- identity (Swagger Authorize) -------------------------------------------
# Distinct scheme_name values so Swagger shows four Authorize fields, not one.
_x_user = APIKeyHeader(
    name="X-User",
    scheme_name="X-User",
    auto_error=False,
    description="Authenticated subject. Required. Example: `demo`, `hr.bp`, `ap.clerk`, `s.oyelaran`.",
)
_x_roles = APIKeyHeader(
    name="X-Roles",
    scheme_name="X-Roles",
    auto_error=False,
    description="Comma-separated roles. Examples: `employee`, `employee,hr`, `finance`.",
)
_x_scopes = APIKeyHeader(
    name="X-Scopes",
    scheme_name="X-Scopes",
    auto_error=False,
    description="Comma-separated scopes. Examples: `corpus:read`, `warehouse:read,ap:read,corpus:read`.",
)
_x_tenant = APIKeyHeader(
    name="X-Tenant",
    scheme_name="X-Tenant",
    auto_error=False,
    description="Tenant id. Demo corpus uses `northgate`.",
)


def require_principal(
    x_user: str | None = Depends(_x_user),
    x_roles: str | None = Depends(_x_roles),
    x_scopes: str | None = Depends(_x_scopes),
    x_tenant: str | None = Depends(_x_tenant),
) -> Principal:
    """POC identity. REPLACE THIS before any deployment that leaves a laptop.

    Trusting headers is safe only where an upstream gateway strips and re-sets them. The
    production version verifies a JWT and derives roles from the client's IdP -- everything
    downstream is unchanged, because it only ever sees a Principal.
    """
    if not x_user:
        raise HTTPException(401, "X-User header is required")
    return Principal.user(
        x_user,
        tenant=x_tenant or "default",
        roles=set(filter(None, (x_roles or "").split(","))),
        scopes=set(filter(None, (x_scopes or "").split(","))),
    )


# --- schemas ----------------------------------------------------------------
class AskRequest(BaseModel):
    question: str = Field(
        min_length=3,
        max_length=2000,
        description="Natural-language question for policy RAG or the supervisor.",
        examples=["What mileage rate applies to the first 10,000 business miles?"],
    )


class TriageRequest(BaseModel):
    invoice_id: str = Field(
        description="Demo invoice id INV-1001 … INV-1007. Re-triage is idempotent.",
        examples=["INV-1005"],
    )


class ApprovalRequest(BaseModel):
    run_id: str = Field(description="Workflow run id from triage or the AP queue.", examples=["wf_…"])
    approved: bool = Field(description="True to continue; False to reject.", examples=[True])
    approver: str = Field(
        description="Must equal the authorised X-User (FIN-AP-090 s6).",
        examples=["s.oyelaran"],
    )
    note: str = Field(default="", description="Optional decision note retained on the audit trail.")


class GenerateInvoicesRequest(BaseModel):
    cadence: Literal["weekly", "monthly"] = Field(
        default="monthly",
        description="Spread generated invoices across a week or calendar month.",
    )
    count: int = Field(default=10, ge=1, le=20, description="Number of sample invoices (default 10).")
    period_start: date | None = Field(
        default=None,
        description="ISO start date for the batch (defaults to 2026-08-01).",
    )


class TriageBatchRequest(BaseModel):
    invoice_ids: list[str] = Field(
        min_length=1,
        max_length=25,
        description="Invoice ids to triage in sequence (credit-controller batch run).",
    )


POLICY_EXAMPLES = {
    "mileage": {
        "summary": "Mileage rate (any employee)",
        "description": "Authorize as Employee (`demo` / `employee` / `corpus:read`). Expect citations.",
        "value": {"question": "What mileage rate applies to the first 10,000 business miles?"},
    },
    "merit_acl_refuse": {
        "summary": "Employee: merit budget (ACL refuse)",
        "description": "Authorize as Employee. Same question as HR case — must refuse, no 3.4% leak.",
        "value": {"question": "What is the Group merit budget for the 2026 compensation cycle?"},
    },
    "merit_hr_answer": {
        "summary": "HR: merit budget (answered + citations)",
        "description": "Authorize as HR BP (`hr.bp` / `employee,hr` / `corpus:read`). Expect 3.4% + HR-COMP-003.",
        "value": {"question": "What is the Group merit budget for the 2026 compensation cycle?"},
    },
    "out_of_corpus": {
        "summary": "Out of corpus (refuse, do not invent)",
        "description": "Authorize as Employee. Parental leave is not in the indexed corpus.",
        "value": {"question": "How many weeks of paid parental leave do we offer?"},
    },
    "expense_threshold": {
        "summary": "Expense approval threshold",
        "description": "Authorize as Employee. Expect GBP 2,000 Finance Director threshold.",
        "value": {"question": "What is the expense approval threshold for a claim over GBP 2,000?"},
    },
}

ASSISTANT_EXAMPLES = {
    "warehouse": {
        "summary": "Warehouse exceptions by category",
        "description": "Authorize as Full assistant. Routes to data_analyst + run_sql.",
        "value": {"question": "How many invoice exceptions do we have by category?"},
    },
    "ap_stuck": {
        "summary": "AP runs awaiting a human",
        "description": "Authorize as Full assistant. Routes to ap_operations.",
        "value": {"question": "Which AP runs are stuck waiting for someone?"},
    },
    "policy_via_supervisor": {
        "summary": "Policy via supervisor",
        "description": "Authorize as Full assistant. Routes to policy_analyst with a citation.",
        "value": {"question": "What is the expense approval threshold for a claim over GBP 2,000?"},
    },
    "least_privilege": {
        "summary": "Least privilege deny",
        "description": "Authorize as Contractor (`contractor` / `employee` / `corpus:read`). Tools denied.",
        "value": {"question": "How many invoice exceptions do we have by category?"},
    },
    "decline": {
        "summary": "Out of scope decline",
        "description": "Authorize as Full assistant. No specialist covers share price.",
        "value": {"question": "What was the share price at close yesterday?"},
    },
}

TRIAGE_EXAMPLES = {
    "auto_post": {
        "summary": "INV-1001 auto-post (straight-through)",
        "description": "Authorize as Finance clerk. Often succeeds with no human.",
        "value": {"invoice_id": "INV-1001"},
    },
    "auto_resolve": {
        "summary": "INV-1002 price variance (auto_resolve)",
        "description": "Authorize as Finance clerk. Small variance cleared by policy.",
        "value": {"invoice_id": "INV-1002"},
    },
    "require_approval": {
        "summary": "INV-1003 price variance (require_approval)",
        "description": "Authorize as Finance clerk. Then approve as Controller with matching X-User.",
        "value": {"invoice_id": "INV-1003"},
    },
    "no_po": {
        "summary": "INV-1004 no PO (require_approval)",
        "description": "Authorize as Finance clerk. Appears on /v1/ap/queue until approved.",
        "value": {"invoice_id": "INV-1004"},
    },
    "bank_change": {
        "summary": "INV-1005 bank detail change (escalate)",
        "description": "Authorize as Finance clerk. Always escalated — never autonomous.",
        "value": {"invoice_id": "INV-1005"},
    },
    "quantity": {
        "summary": "INV-1006 quantity variance (require_approval)",
        "description": "Authorize as Finance clerk. SoD applies if the PO raiser tries to approve.",
        "value": {"invoice_id": "INV-1006"},
    },
    "duplicate": {
        "summary": "INV-1007 duplicate suspect (require_approval)",
        "description": "Authorize as Finance clerk.",
        "value": {"invoice_id": "INV-1007"},
    },
}

APPROVE_EXAMPLES = {
    "approve": {
        "summary": "Approve awaiting run",
        "description": (
            "Authorize as Controller (`s.oyelaran` / `finance`). "
            "Paste run_id from GET /v1/ap/queue. approver MUST equal X-User."
        ),
        "value": {
            "run_id": "wf_paste_from_queue",
            "approved": True,
            "approver": "s.oyelaran",
            "note": "checked against the signed contract variation",
        },
    },
    "reject": {
        "summary": "Reject awaiting run",
        "description": "Authorize as Controller. Same identity rule on approver.",
        "value": {
            "run_id": "wf_paste_from_queue",
            "approved": False,
            "approver": "s.oyelaran",
            "note": "variance not supported by contract",
        },
    },
}


def custom_openapi() -> dict[str, Any]:
    if app.openapi_schema:
        return app.openapi_schema
    schema = get_openapi(
        title=app.title,
        version=app.version,
        description=app.description,
        routes=app.routes,
        tags=TAGS_METADATA,
    )
    schema["servers"] = [
        {"url": "http://localhost:8000", "description": "Local `make serve`"},
        {"url": "/", "description": "This host"},
    ]
    # Keep Authorize values across Try it out; schemes already registered via APIKeyHeader.
    schema.setdefault("components", {}).setdefault("securitySchemes", {})
    app.openapi_schema = schema
    return app.openapi_schema


app.openapi = custom_openapi  # type: ignore[method-assign]


# --- routes -----------------------------------------------------------------
@app.get("/", include_in_schema=False)
def root() -> RedirectResponse:
    if (DASHBOARD_DIR / "index.html").is_file():
        return RedirectResponse(url="/dashboard/")
    return RedirectResponse(url="/docs")


@app.get("/healthz", tags=["health"], summary="Liveness", operation_id="healthz")
def healthz() -> dict[str, Any]:
    """Process is up. Does not prove the corpus or checkpoint store are reachable."""
    s = get_settings()
    return {"status": "ok", "provider": s.provider, "model": s.model, "version": app.version}


@app.get("/readyz", tags=["health"], summary="Readiness", operation_id="readyz")
def readyz() -> dict[str, Any]:
    """Readiness means the dependencies an agent needs are actually reachable."""
    from agents.governed_rag import get_corpus  # noqa: PLC0415

    s = get_settings()
    checks: dict[str, Any] = {}
    try:
        corpus = get_corpus(str(s.corpus_dir), s.embedding_dim)
        checks["corpus"] = {"ok": True, "chunks": len(corpus.chunks)}
    except Exception as exc:
        checks["corpus"] = {"ok": False, "error": str(exc)}
    try:
        CheckpointStore(s.checkpoint_db).list_runs(limit=1)
        checks["checkpoints"] = {"ok": True}
    except Exception as exc:
        checks["checkpoints"] = {"ok": False, "error": str(exc)}
    ready = all(c["ok"] for c in checks.values())
    if not ready:
        raise HTTPException(503, checks)
    return {"status": "ready", "checks": checks}


@app.get(
    "/v1/ap/invoices",
    tags=["ap"],
    summary="List supplier invoices",
    operation_id="ap_list_invoices",
    responses={401: {"description": "Missing X-User"}, 403: {"description": "Finance role required"}},
)
def ap_list_invoices(
    principal: Principal = Depends(require_principal),
    include_document: bool = False,
) -> dict[str, Any]:
    """Mailbox view — fixture demos plus AI-generated batches."""
    if "finance" not in principal.roles:
        raise HTTPException(403, "invoice list requires the finance role")
    from agents.ops_workflow.invoice_catalog import list_invoices  # noqa: PLC0415

    return list_invoices(include_document=include_document)


@app.get(
    "/v1/ap/invoices/{invoice_id}",
    tags=["ap"],
    summary="Invoice detail with document text",
    operation_id="ap_get_invoice",
    responses={401: {"description": "Missing X-User"}, 404: {"description": "Unknown invoice"}},
)
def ap_get_invoice(invoice_id: str, principal: Principal = Depends(require_principal)) -> dict[str, Any]:
    if "finance" not in principal.roles:
        raise HTTPException(403, "invoice detail requires the finance role")
    from agents.ops_workflow.invoice_catalog import get_invoice_detail  # noqa: PLC0415

    try:
        return get_invoice_detail(invoice_id)
    except KeyError as exc:
        raise HTTPException(404, str(exc)) from exc


@app.post(
    "/v1/ap/invoices/generate",
    tags=["ap"],
    summary="Generate sample invoices with AI",
    operation_id="ap_generate_invoices",
    responses={401: {"description": "Missing X-User"}, 403: {"description": "Finance role required"}},
)
def ap_generate_invoices(
    body: GenerateInvoicesRequest = Body(
        openapi_examples={
            "monthly_10": {
                "summary": "10 monthly samples (August 2026)",
                "value": {"cadence": "monthly", "count": 10},
            },
            "weekly_10": {
                "summary": "10 weekly samples",
                "value": {"cadence": "weekly", "count": 10},
            },
        }
    ),
    principal: Principal = Depends(require_principal),
) -> dict[str, Any]:
    """Uses Ollama to draft realistic invoice documents; merges into the ERP stub."""
    if "finance" not in principal.roles:
        raise HTTPException(403, "invoice generation requires the finance role")
    from agents.ops_workflow.invoice_catalog import GenerateRequest, generate_batch  # noqa: PLC0415

    ctx = RunContext.build(principal, timeout_s=180)
    req = GenerateRequest(
        cadence=body.cadence,
        count=body.count,
        period_start=body.period_start,
    )
    return generate_batch(req, ctx)


@app.get(
    "/v1/ap/credit-controller/summary",
    tags=["ap"],
    summary="Credit controller dashboard",
    operation_id="ap_credit_controller_summary",
    responses={401: {"description": "Missing X-User"}, 403: {"description": "Finance role required"}},
)
def ap_credit_controller_summary(principal: Principal = Depends(require_principal)) -> dict[str, Any]:
    """Mailbox counts, approval queue, month-end playbook."""
    if "finance" not in principal.roles and "exec" not in principal.roles:
        raise HTTPException(403, "credit controller summary requires finance or exec role")
    from agents.ops_workflow.invoice_catalog import credit_controller_summary  # noqa: PLC0415

    return credit_controller_summary()


@app.post(
    "/v1/ap/triage/batch",
    tags=["ap"],
    summary="Triage multiple invoices",
    operation_id="ap_triage_batch",
    responses={401: {"description": "Missing X-User"}, 403: {"description": "Finance role required"}},
)
def ap_triage_batch(
    body: TriageBatchRequest,
    principal: Principal = Depends(require_principal),
) -> dict[str, Any]:
    """Run the durable workflow for each invoice — typical credit-controller intake step."""
    from agents.ops_workflow import build_workflow  # noqa: PLC0415

    if "finance" not in principal.roles:
        raise HTTPException(403, "AP triage requires the finance role")
    ctx = RunContext.build(principal, timeout_s=180)
    results = []
    for invoice_id in body.invoice_ids:
        try:
            result = build_workflow().start(invoice_id, ctx)
            results.append({**result.to_dict(), "invoice_id": invoice_id})
        except Exception as exc:
            results.append({"invoice_id": invoice_id, "status": "error", "error": str(exc)})
    awaiting = sum(1 for r in results if r.get("status") == "awaiting_approval")
    succeeded = sum(1 for r in results if r.get("status") == "succeeded")
    return {
        "triaged": len(results),
        "awaiting_approval": awaiting,
        "succeeded": succeeded,
        "results": results,
        "trace_id": ctx.tracer.trace_id,
        "cost": ctx.ledger.summary(),
    }


@app.post(
    "/v1/policy/ask",
    tags=["policy"],
    summary="Ask a policy question (governed RAG)",
    operation_id="policy_ask",
    responses={401: {"description": "Missing X-User"}, 429: {"description": "Budget exceeded"}},
)
def policy_ask(
    body: AskRequest = Body(openapi_examples=POLICY_EXAMPLES),
    principal: Principal = Depends(require_principal),
) -> dict[str, Any]:
    """ACL-aware retrieval with verified citations. Refusal is a first-class outcome."""
    from agents.governed_rag import build_agent  # noqa: PLC0415

    started = time.perf_counter()
    ctx = RunContext.build(principal, timeout_s=60)
    try:
        response = build_agent(ctx).answer(body.question, principal)
    except BudgetExceeded as exc:
        raise HTTPException(429, str(exc)) from exc
    payload = response.to_dict()
    payload["latency_ms"] = round((time.perf_counter() - started) * 1000, 1)
    payload.pop("context", None)  # retrieved text is in the trace, not the API response
    return payload


@app.post(
    "/v1/assistant/ask",
    tags=["assistant"],
    summary="Ask the supervisor",
    operation_id="assistant_ask",
    responses={401: {"description": "Missing X-User"}, 429: {"description": "Budget exceeded"}},
)
def assistant_ask(
    body: AskRequest = Body(openapi_examples=ASSISTANT_EXAMPLES),
    principal: Principal = Depends(require_principal),
) -> dict[str, Any]:
    """Routes to a specialist, enforces least privilege at the tool boundary, or declines."""
    from agents.supervisor import build_agent  # noqa: PLC0415

    started = time.perf_counter()
    ctx = RunContext.build(principal, timeout_s=120)
    try:
        response = build_agent(ctx).handle(body.question)
    except BudgetExceeded as exc:
        raise HTTPException(429, str(exc)) from exc
    payload = response.to_dict()
    payload["latency_ms"] = round((time.perf_counter() - started) * 1000, 1)
    return payload


@app.post(
    "/v1/ap/triage",
    tags=["ap"],
    summary="Start AP invoice triage",
    operation_id="ap_triage",
    responses={
        401: {"description": "Missing X-User"},
        403: {"description": "Finance role required"},
    },
)
def ap_triage(
    body: TriageRequest = Body(openapi_examples=TRIAGE_EXAMPLES),
    principal: Principal = Depends(require_principal),
) -> dict[str, Any]:
    """Checkpointed workflow. May succeed, await approval, or fail (e.g. segregation of duties)."""
    from agents.ops_workflow import build_workflow  # noqa: PLC0415

    if "finance" not in principal.roles:
        raise HTTPException(403, "AP triage requires the finance role")
    ctx = RunContext.build(principal, timeout_s=120)
    result = build_workflow().start(body.invoice_id, ctx)
    return {**result.to_dict(), "trace_id": ctx.tracer.trace_id, "cost": ctx.ledger.summary()}


@app.post(
    "/v1/ap/approve",
    tags=["ap"],
    summary="Approve or reject an awaiting AP run",
    operation_id="ap_approve",
    responses={
        401: {"description": "Missing X-User"},
        403: {"description": "Role or approver mismatch"},
    },
)
def ap_approve(
    body: ApprovalRequest = Body(openapi_examples=APPROVE_EXAMPLES),
    principal: Principal = Depends(require_principal),
) -> dict[str, Any]:
    """Resumes a suspended run. `approver` must match the authenticated `X-User`."""
    from agents.ops_workflow import build_workflow  # noqa: PLC0415

    if "finance" not in principal.roles and "exec" not in principal.roles:
        raise HTTPException(403, "approval requires the finance or exec role")
    if body.approver != principal.subject:
        # An approval must be attributable to the human who made it, not to whoever the
        # request body names. FIN-AP-090 s6 requires the identity of the decision maker.
        raise HTTPException(403, "approver must match the authenticated user")
    ctx = RunContext.build(principal, timeout_s=120)
    result = build_workflow().resume(
        body.run_id, ctx, approved=body.approved, approver=body.approver, note=body.note
    )
    return {**result.to_dict(), "trace_id": ctx.tracer.trace_id}


@app.get(
    "/v1/ap/queue",
    tags=["ap"],
    summary="List AP runs awaiting approval",
    operation_id="ap_queue",
    responses={401: {"description": "Missing X-User"}, 403: {"description": "Finance role required"}},
)
def ap_queue(principal: Principal = Depends(require_principal)) -> dict[str, Any]:
    """Copy a `run_id` from here into **POST /v1/ap/approve**."""
    if "finance" not in principal.roles:
        raise HTTPException(403, "the AP queue requires the finance role")
    store = CheckpointStore(get_settings().checkpoint_db)
    return {
        "awaiting_approval": [
            {
                "run_id": r.run_id,
                "invoice_id": r.input.get("invoice_id"),
                "cursor_step": r.cursor_step,
                "updated_at": r.updated_at,
            }
            for r in store.list_runs("awaiting_approval", limit=50)
        ]
    }


@app.get(
    "/v1/runs/{run_id}",
    tags=["ap"],
    summary="Inspect a workflow run",
    operation_id="get_run",
    responses={401: {"description": "Missing X-User"}, 404: {"description": "Unknown or other-tenant run"}},
)
def get_run(run_id: str, principal: Principal = Depends(require_principal)) -> dict[str, Any]:
    """Steps and audit events for one run. Cross-tenant lookups return 404 (not 403)."""
    store = CheckpointStore(get_settings().checkpoint_db)
    run = store.get_run(run_id)
    if run is None:
        raise HTTPException(404, "run not found")
    if run.tenant and run.tenant != principal.tenant:
        raise HTTPException(404, "run not found")  # not 403: do not confirm it exists
    return {
        "run_id": run.run_id,
        "workflow": run.workflow,
        "status": run.status,
        "steps": [
            {"name": s.name, "status": s.status, "attempt": s.attempt, "error": s.error}
            for s in store.steps(run_id)
        ],
        "events": list(store.events(run_id)),
    }


# Mount last so API routes take precedence. Built by `make dashboard`.
if (DASHBOARD_DIR / "index.html").is_file():
    app.mount(
        "/dashboard",
        StaticFiles(directory=str(DASHBOARD_DIR), html=True),
        name="dashboard",
    )
