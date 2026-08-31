"""Offline behaviour for the AP workflow: deterministic invoice field extraction.

Regex extraction over a known document layout. On a real engagement this is what the LLM
earns its keep replacing -- supplier invoices are a long tail of layouts and a rules-based
extractor is a maintenance treadmill. Keeping a rules path here is still worth it: it makes
the workflow's control logic testable without a model in the loop, and on the subset of
suppliers with stable templates it is cheaper and more accurate than any model.
"""

from __future__ import annotations

import json
import re

from aivc.llm.base import LLMRequest
from aivc.llm.offline import register

from .workflow import EXTRACTION_MARKER

PATTERNS = {
    "invoice_number": re.compile(r"Invoice number:\s*([A-Z0-9-]+)", re.I),
    "po_reference": re.compile(r"Purchase order:\s*([A-Z0-9-]+)", re.I),
    "total": re.compile(r"Total excluding VAT:\s*GBP\s*([\d,]+\.?\d*)", re.I),
    "remit": re.compile(r"Remit to:\s*([A-Z0-9]+)", re.I),
    "terms": re.compile(r"Payment terms:\s*(\d+)\s*days", re.I),
    "qty_price": re.compile(r"Quantity:\s*([\d,]+)\s*units? at GBP\s*([\d,]+\.?\d*)", re.I),
}

NOTE_TRIGGERS = [
    (re.compile(r"bank details have changed", re.I), "document states the bank details have changed"),
    (re.compile(r"no purchase order reference", re.I), "no purchase order reference supplied"),
    (re.compile(r"handwritten|amended by hand", re.I), "document appears to be amended by hand"),
]


def _num(value: str | None) -> float | None:
    return float(value.replace(",", "")) if value else None


def _extract(request: LLMRequest) -> str:
    text = request.last_user_text()
    qty_price = PATTERNS["qty_price"].search(text)
    supplier_line = next((ln.strip() for ln in text.splitlines() if ln.strip()), "")
    if supplier_line.upper().startswith("INVOICE DOCUMENT"):
        supplier_line = next(
            (ln.strip() for ln in text.splitlines()[1:] if ln.strip()), ""
        )

    notes = [note for pattern, note in NOTE_TRIGGERS if pattern.search(text)]
    total = _num(PATTERNS["total"].search(text).group(1)) if PATTERNS["total"].search(text) else 0.0

    return json.dumps(
        {
            "invoice_number": _group(PATTERNS["invoice_number"], text) or "UNKNOWN",
            "supplier_name": supplier_line.title(),
            "po_reference": _group(PATTERNS["po_reference"], text),
            "quantity": _num(qty_price.group(1)) if qty_price else None,
            "unit_price_gbp": _num(qty_price.group(2)) if qty_price else None,
            "total_ex_vat_gbp": total,
            "remit_to_account": _group(PATTERNS["remit"], text),
            "payment_terms_days": int(_group(PATTERNS["terms"], text) or 0) or None,
            "notes": "; ".join(notes),
        }
    )


def _group(pattern: re.Pattern[str], text: str) -> str | None:
    match = pattern.search(text)
    return match.group(1) if match else None


def install() -> None:
    register("ops_workflow.extract", lambda req: EXTRACTION_MARKER in req.system, _extract, priority=10)


install()
