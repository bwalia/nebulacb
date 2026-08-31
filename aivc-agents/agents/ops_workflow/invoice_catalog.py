"""Invoice catalog, AI batch generation, and credit-controller views.

Fixtures hold the seven demo exceptions. Generated invoices land in
``.state/ap_generated_invoices.json`` and merge into ``ErpStub`` at runtime.
"""

from __future__ import annotations

import json
import re
from datetime import date, timedelta
from pathlib import Path
from typing import Any, Literal

from pydantic import BaseModel, Field

from aivc.config import get_settings
from aivc.llm.gateway import LLMGateway
from aivc.obs.run import RunContext
from aivc.store.checkpoint import CheckpointStore

from .domain import ErpStub, printed_invoice_number_of

Cadence = Literal["weekly", "monthly"]

_TOTAL_RE = re.compile(r"Total excluding VAT:\s*GBP\s*([\d,]+\.\d{2})", re.I)
_PO_RE = re.compile(r"Purchase order:\s*(PO-\d+)", re.I)


class GeneratedBatch(BaseModel):
    batch_id: str
    cadence: Cadence
    count: int
    period_label: str
    invoice_ids: list[str]
    provider: str
    model: str


class GenerateRequest(BaseModel):
    cadence: Cadence = "monthly"
    count: int = Field(default=10, ge=1, le=20)
    period_start: date | None = None


def generated_store_path() -> Path:
    return get_settings().state_dir / "ap_generated_invoices.json"


def load_generated_invoices() -> dict[str, dict[str, Any]]:
    path = generated_store_path()
    if not path.is_file():
        return {}
    data = json.loads(path.read_text())
    return dict(data.get("invoices", {}))


def load_generated_batches() -> list[dict[str, Any]]:
    path = generated_store_path()
    if not path.is_file():
        return []
    data = json.loads(path.read_text())
    return list(data.get("batches", []))


def save_generated(invoices: dict[str, dict[str, Any]], batch: GeneratedBatch) -> None:
    path = generated_store_path()
    existing: dict[str, Any] = {"invoices": {}, "batches": []}
    if path.is_file():
        existing = json.loads(path.read_text())
    merged = {**existing.get("invoices", {}), **invoices}
    batches = list(existing.get("batches", []))
    batches.append(batch.model_dump())
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps({"invoices": merged, "batches": batches}, indent=2))


def _parse_amount(document_text: str) -> float | None:
    match = _TOTAL_RE.search(document_text)
    if not match:
        return None
    return float(match.group(1).replace(",", ""))


def _parse_po(document_text: str) -> str | None:
    match = _PO_RE.search(document_text)
    return match.group(1) if match else None


def _scenario_hint(invoice_id: str, document_text: str) -> str:
    text = document_text.upper()
    if "BANK DETAILS HAVE CHANGED" in text or "BANK DETAILS CHANGED" in text:
        return "BANK_DETAIL_CHANGE"
    if "NO PURCHASE ORDER" in text or "NO PO" in text:
        return "NO_PO"
    if "SECOND REQUEST" in text or invoice_id.endswith("-dup"):
        return "DUPLICATE_SUSPECT"
    if "at GBP" in document_text:
        # price drift vs PO is common in generated samples
        amounts = re.findall(r"at GBP\s*([\d,]+\.\d{2})", document_text)
        if len(amounts) >= 2 and amounts[0] != amounts[1]:
            return "PRICE_VARIANCE"
    if _parse_po(document_text) is None and "Purchase order:" not in document_text:
        return "NO_PO"
    if "11 units" in document_text or "11 pallets" in document_text:
        return "QUANTITY_VARIANCE"
    return "STRAIGHT_THROUGH"


def _run_status_for_invoice(store: CheckpointStore, invoice_id: str) -> dict[str, Any] | None:
    for run in store.list_runs(limit=200):
        if run.input.get("invoice_id") == invoice_id:
            return {
                "run_id": run.run_id,
                "status": run.status,
                "updated_at": run.updated_at,
            }
    return None


def summarize_invoice(
    invoice_id: str,
    inv: dict[str, Any],
    erp: ErpStub,
    *,
    run_status: dict[str, Any] | None = None,
    source: str = "fixture",
) -> dict[str, Any]:
    supplier = erp.get_supplier(inv["supplier_id"]) or {}
    amount = _parse_amount(inv["document_text"])
    po = _parse_po(inv["document_text"])
    printed = printed_invoice_number_of(inv["document_text"])
    return {
        "invoice_id": invoice_id,
        "supplier_id": inv["supplier_id"],
        "supplier_name": supplier.get("name", inv["supplier_id"]),
        "received_at": inv["received_at"],
        "amount_gbp": amount,
        "po_reference": po,
        "printed_number": printed,
        "scenario_hint": _scenario_hint(invoice_id, inv["document_text"]),
        "source": source,
        "workflow": run_status,
    }


def list_invoices(*, include_document: bool = False) -> dict[str, Any]:
    erp = ErpStub()
    store = CheckpointStore(get_settings().checkpoint_db)
    rows: list[dict[str, Any]] = []
    for invoice_id, inv in sorted(erp.invoices.items()):
        run_status = _run_status_for_invoice(store, invoice_id)
        source = "generated" if invoice_id.startswith("INV-2") else "fixture"
        row = summarize_invoice(invoice_id, inv, erp, run_status=run_status, source=source)
        if include_document:
            row["document_text"] = inv["document_text"]
        rows.append(row)
    return {
        "count": len(rows),
        "total_gbp": round(sum(r["amount_gbp"] or 0 for r in rows), 2),
        "invoices": rows,
        "batches": load_generated_batches(),
    }


def get_invoice_detail(invoice_id: str) -> dict[str, Any]:
    erp = ErpStub()
    inv = erp.get_invoice(invoice_id)
    store = CheckpointStore(get_settings().checkpoint_db)
    run_status = _run_status_for_invoice(store, invoice_id)
    source = "generated" if invoice_id.startswith("INV-2") else "fixture"
    summary = summarize_invoice(invoice_id, inv, erp, run_status=run_status, source=source)
    summary["document_text"] = inv["document_text"]
    if summary["po_reference"]:
        summary["purchase_order"] = erp.get_po(summary["po_reference"])
    summary["supplier"] = erp.get_supplier(inv["supplier_id"])
    return summary


def credit_controller_summary() -> dict[str, Any]:
    catalog = list_invoices()
    store = CheckpointStore(get_settings().checkpoint_db)
    awaiting = store.list_runs("awaiting_approval", limit=50)
    succeeded = [r for r in store.list_runs(limit=200) if r.status == "succeeded"]
    failed = [r for r in store.list_runs(limit=200) if r.status == "failed"]

    untriaged = [i for i in catalog["invoices"] if i["workflow"] is None]
    by_scenario: dict[str, int] = {}
    for inv in catalog["invoices"]:
        hint = inv["scenario_hint"]
        by_scenario[hint] = by_scenario.get(hint, 0) + 1

    return {
        "role": "credit_controller",
        "period": "August 2026 close",
        "mailbox": {
            "total_invoices": catalog["count"],
            "untriaged": len(untriaged),
            "total_gbp": catalog["total_gbp"],
        },
        "queue": {
            "awaiting_approval": len(awaiting),
            "items": [
                {
                    "run_id": r.run_id,
                    "invoice_id": r.input.get("invoice_id"),
                    "updated_at": r.updated_at,
                }
                for r in awaiting
            ],
        },
        "outcomes": {
            "posted": len(succeeded),
            "failed": len(failed),
        },
        "by_scenario": by_scenario,
        "playbook": [
            {"step": 1, "task": "Intake", "detail": "Review supplier invoices in the mailbox; generate weekly/monthly batches with AI if needed."},
            {"step": 2, "task": "Triage", "detail": "Run each invoice through the durable AP workflow (extract → match → classify → policy)."},
            {"step": 3, "task": "Queue", "detail": "Exceptions awaiting approval land on the controller queue with policy clause references."},
            {"step": 4, "task": "Approve / reject", "detail": "SoD enforced — approver must match X-User and cannot be the PO raiser."},
            {"step": 5, "task": "Escalate", "detail": "Bank detail changes and sanctions hits escalate to the Group Financial Controller."},
            {"step": 6, "task": "Post & audit", "detail": "Idempotent ERP posting; FIN-AP-090 s6 audit record per run."},
            {"step": 7, "task": "Month-end", "detail": "Reconcile posted payments, clear the queue, re-run supervisor 'what is stuck?' check."},
        ],
    }


# --- generation recipes -------------------------------------------------------
_RECIPES: list[dict[str, Any]] = [
    {
        "supplier_id": "SUP-204",
        "po": "PO-5001",
        "qty": 120,
        "unit": 40.00,
        "total": 4800.00,
        "desc": "Deep groove ball bearings 6205-2RS",
        "terms": 45,
        "flag": None,
    },
    {
        "supplier_id": "SUP-311",
        "po": "PO-5002",
        "qty": 400,
        "unit": 20.30,
        "total": 8120.00,
        "desc": "Rotary shaft seals",
        "terms": 45,
        "flag": "PRICE_VARIANCE",
    },
    {
        "supplier_id": "SUP-402",
        "po": "PO-5003",
        "qty": 250,
        "unit": 120.00,
        "total": 30000.00,
        "desc": "Ductile iron pump housings NG-7000",
        "terms": 45,
        "flag": None,
    },
    {
        "supplier_id": "SUP-509",
        "po": None,
        "qty": None,
        "unit": None,
        "total": 3150.00,
        "desc": "Ad hoc pallet haulage, Birmingham to Antwerp",
        "terms": 30,
        "flag": "NO_PO",
    },
    {
        "supplier_id": "SUP-204",
        "po": "PO-5001",
        "qty": 120,
        "unit": 40.00,
        "total": 4800.00,
        "desc": "Deep groove ball bearings — replenishment order",
        "terms": 45,
        "flag": "BANK_DETAIL_CHANGE",
        "bank": "GB11ABCD11223344556677",
    },
    {
        "supplier_id": "SUP-509",
        "po": "PO-5004",
        "qty": 11,
        "unit": 200.00,
        "total": 2200.00,
        "desc": "Pallet haulage — 11 pallets delivered",
        "terms": 30,
        "flag": "QUANTITY_VARIANCE",
    },
]


def _format_document(
    *,
    supplier_name: str,
    invoice_number: str,
    inv_date: date,
    recipe: dict[str, Any],
    bank: str,
) -> str:
    lines = [
        supplier_name.upper(),
        f"Invoice number: {invoice_number}",
        f"Date: {inv_date.day} {inv_date.strftime('%B %Y')}",
    ]
    if recipe["po"]:
        lines.append(f"Purchase order: {recipe['po']}")
    lines.append(f"Description: {recipe['desc']}")
    if recipe["qty"] and recipe["unit"]:
        lines.append(f"Quantity: {recipe['qty']} units at GBP {recipe['unit']:.2f} each")
    lines.append(f"Total excluding VAT: GBP {recipe['total']:,.2f}")
    if recipe.get("flag") == "BANK_DETAIL_CHANGE":
        lines.append("PLEASE NOTE OUR BANK DETAILS HAVE CHANGED.")
    lines.append(f"Remit to: {bank}")
    lines.append(f"Payment terms: {recipe['terms']} days")
    if recipe.get("flag") == "NO_PO":
        lines.append("No purchase order reference supplied.")
    return "\n".join(lines)


def _next_invoice_ids(count: int) -> list[str]:
    existing = set(ErpStub().invoices)
    start = 2001
    while f"INV-{start}" in existing:
        start += 1
    return [f"INV-{start + i}" for i in range(count)]


def _period_dates(cadence: Cadence, count: int, period_start: date | None) -> tuple[list[date], str]:
    start = period_start or date(2026, 8, 1)
    if cadence == "weekly":
        step = timedelta(days=7 // max(count, 1) or 1)
        if count <= 7:
            step = timedelta(days=1)
        dates = [start + step * i for i in range(count)]
        label = f"Week of {start.isoformat()}"
    else:
        # spread across the calendar month
        step = max(1, 28 // count)
        dates = [start + timedelta(days=min(27, step * i)) for i in range(count)]
        label = start.strftime("%B %Y")
    return dates, label


def generate_offline_batch(req: GenerateRequest) -> tuple[dict[str, dict[str, Any]], GeneratedBatch]:
    erp = ErpStub()
    ids = _next_invoice_ids(req.count)
    dates, label = _period_dates(req.cadence, req.count, req.period_start)
    invoices: dict[str, dict[str, Any]] = {}
    for i, invoice_id in enumerate(ids):
        recipe = _RECIPES[i % len(_RECIPES)]
        supplier = erp.get_supplier(recipe["supplier_id"]) or {}
        bank = recipe.get("bank") or supplier.get("bank_account", "GB00BANK00000000000000")
        printed = invoice_id.replace("INV-", "GEN-")
        doc = _format_document(
            supplier_name=supplier.get("name", recipe["supplier_id"]),
            invoice_number=printed,
            inv_date=dates[i],
            recipe=recipe,
            bank=bank,
        )
        invoices[invoice_id] = {
            "supplier_id": recipe["supplier_id"],
            "received_at": dates[i].isoformat(),
            "document_text": doc,
        }
    batch = GeneratedBatch(
        batch_id=f"batch-{dates[0].isoformat()}-{req.cadence}",
        cadence=req.cadence,
        count=req.count,
        period_label=label,
        invoice_ids=ids,
        provider="offline",
        model="template",
    )
    return invoices, batch


def generate_ai_batch(req: GenerateRequest, ctx: RunContext) -> tuple[dict[str, dict[str, Any]], GeneratedBatch]:
    erp = ErpStub()
    ids = _next_invoice_ids(req.count)
    dates, label = _period_dates(req.cadence, req.count, req.period_start)
    recipes = [_RECIPES[i % len(_RECIPES)] for i in range(req.count)]
    supplier_lines = []
    for r in recipes:
        s = erp.get_supplier(r["supplier_id"]) or {}
        supplier_lines.append(f"- {r['supplier_id']}: {s.get('name', '?')} bank {s.get('bank_account', '?')}")
    prompt = f"""Generate {req.count} realistic UK B2B supplier invoice document texts as JSON.

Return {{"invoices": [{{"invoice_id": "...", "document_text": "..."}}, ...]}} with exactly these ids in order:
{', '.join(ids)}

Use these suppliers (match supplier_id in the recipe index):
{chr(10).join(supplier_lines)}

Recipes (one per invoice, same order):
{json.dumps(recipes, indent=2)}

Received dates (ISO): {[d.isoformat() for d in dates]}

Rules:
- Each document_text MUST include the exact total from its recipe as "Total excluding VAT: GBP X,XXX.XX"
- Include "Invoice number: GEN-XXXX" matching the invoice_id suffix
- Include "Purchase order: PO-XXXX" when recipe po is set
- For BANK_DETAIL_CHANGE flag include "PLEASE NOTE OUR BANK DETAILS HAVE CHANGED" and remit to GB11ABCD11223344556677
- For NO_PO omit purchase order line and add "No purchase order reference supplied."
- Copy amounts exactly — never round differently
"""
    gateway = LLMGateway.for_run(ctx)
    raw = gateway.structured(
        system="You write realistic supplier invoice documents for accounts payable testing.",
        user=prompt,
        schema=_GeneratedPayload,
        label="ap.generate_invoices",
    )
    invoices: dict[str, dict[str, Any]] = {}
    for i, item in enumerate(raw.invoices):
        invoice_id = ids[i] if i < len(ids) else item.invoice_id
        inv_date = dates[i] if i < len(dates) else dates[-1]
        invoices[invoice_id] = {
            "supplier_id": recipes[i]["supplier_id"],
            "received_at": inv_date.isoformat(),
            "document_text": item.document_text,
        }
    batch = GeneratedBatch(
        batch_id=f"batch-{dates[0].isoformat()}-{req.cadence}-ai",
        cadence=req.cadence,
        count=req.count,
        period_label=label,
        invoice_ids=list(invoices),
        provider=ctx.settings.provider,
        model=ctx.settings.model,
    )
    return invoices, batch


class _GeneratedItem(BaseModel):
    invoice_id: str
    document_text: str


class _GeneratedPayload(BaseModel):
    invoices: list[_GeneratedItem]


def generate_batch(req: GenerateRequest, ctx: RunContext) -> dict[str, Any]:
    if ctx.settings.provider == "offline":
        invoices, batch = generate_offline_batch(req)
    else:
        try:
            invoices, batch = generate_ai_batch(req, ctx)
        except Exception:
            invoices, batch = generate_offline_batch(req)
            batch = batch.model_copy(update={"provider": "fallback", "model": "template"})
    save_generated(invoices, batch)
    summaries = [
        summarize_invoice(iid, inv, ErpStub(), source="generated")
        for iid, inv in invoices.items()
    ]
    return {
        "batch": batch.model_dump(),
        "generated": summaries,
        "message": f"Generated {len(invoices)} {req.cadence} invoices for {batch.period_label}",
    }
