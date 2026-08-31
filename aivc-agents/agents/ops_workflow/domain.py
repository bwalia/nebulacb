"""Domain model, stub systems of record, and the control thresholds from FIN-AP-090.

The stubs stand in for the client's ERP and supplier master. They are deliberately shaped
like the real integration -- an idempotent write that returns the prior result for a repeated
key, and a supplier record that can disagree with an invoice -- because those two behaviours
are what the workflow's correctness depends on. Swapping in SAP/NetSuite/Dynamics means
replacing this file, not the workflow.

The thresholds are transcribed from the policy document and carry the clause reference, so an
auditor can trace a machine decision back to the control it implements. They are configuration,
never model judgement.
"""

from __future__ import annotations

import json
import re
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any, Literal

from pydantic import BaseModel, Field

from aivc.config import get_settings

ExceptionCategory = Literal[
    "NONE",
    "PRICE_VARIANCE",
    "QUANTITY_VARIANCE",
    "NO_PO",
    "DUPLICATE_SUSPECT",
    "BANK_DETAIL_CHANGE",
    "SANCTIONS_HIT",
]

# --- control thresholds (FIN-AP-090) ---------------------------------------
STRAIGHT_THROUGH_LIMIT_GBP = 10_000.0        # FIN-AP-090 s2
AUTONOMOUS_VARIANCE_ABS_GBP = 500.0          # FIN-AP-090 s4
AUTONOMOUS_VARIANCE_PCT = 0.05               # FIN-AP-090 s4
PRICE_TOLERANCE_PCT = 0.05                   # POL-PROC-221 s5
PRICE_TOLERANCE_ABS_GBP = 100.0              # POL-PROC-221 s5
QUANTITY_TOLERANCE_PCT = 0.02                # POL-PROC-221 s5
NEVER_AUTONOMOUS: set[str] = {"NO_PO", "DUPLICATE_SUSPECT", "BANK_DETAIL_CHANGE", "SANCTIONS_HIT"}
ESCALATE_TO_CONTROLLER: set[str] = {"BANK_DETAIL_CHANGE", "SANCTIONS_HIT"}
POLICY_REFS = {
    "STRAIGHT_THROUGH_LIMIT_GBP": "FIN-AP-090 s2",
    "AUTONOMOUS_VARIANCE_ABS_GBP": "FIN-AP-090 s4",
    "AUTONOMOUS_VARIANCE_PCT": "FIN-AP-090 s4",
    "NEVER_AUTONOMOUS": "FIN-AP-090 s4",
    "ESCALATE_TO_CONTROLLER": "FIN-AP-090 s4",
    "TOLERANCES": "POL-PROC-221 s5",
}


class ExtractedInvoice(BaseModel):
    """What the extraction step must produce. Typed, because everything downstream is
    arithmetic and a string where a float belongs is a silent wrong answer."""

    invoice_number: str
    supplier_name: str
    po_reference: str | None = None
    quantity: float | None = None
    unit_price_gbp: float | None = None
    total_ex_vat_gbp: float
    remit_to_account: str | None = None
    payment_terms_days: int | None = None
    notes: str = Field(default="", description="anything unusual on the document")


@dataclass
class MatchResult:
    matched: bool
    po_reference: str | None
    price_variance_gbp: float = 0.0
    price_variance_pct: float = 0.0
    quantity_variance_units: float = 0.0
    quantity_variance_pct: float = 0.0
    bank_account_matches: bool = True
    reasons: list[str] = field(default_factory=list)

    def to_dict(self) -> dict[str, Any]:
        return {
            "matched": self.matched,
            "po_reference": self.po_reference,
            "price_variance_gbp": round(self.price_variance_gbp, 2),
            "price_variance_pct": round(self.price_variance_pct, 4),
            "quantity_variance_units": self.quantity_variance_units,
            "quantity_variance_pct": round(self.quantity_variance_pct, 4),
            "bank_account_matches": self.bank_account_matches,
            "reasons": self.reasons,
        }


_PRINTED_NUMBER_RE = re.compile(r"Invoice number:\s*([A-Z0-9-]+)", re.I)


def printed_invoice_number_of(document_text: str) -> str | None:
    match = _PRINTED_NUMBER_RE.search(document_text)
    return match.group(1) if match else None


class ErpStub:
    """Stands in for the client's ERP. Two behaviours that matter are real: idempotent
    posting, and a supplier master that the invoice can contradict."""

    def __init__(self, fixtures_path: Path | None = None, *, include_generated: bool = True):
        path = fixtures_path or (get_settings().data_dir / "ap" / "fixtures.json")
        data = json.loads(Path(path).read_text())
        self.suppliers: dict[str, dict[str, Any]] = data["suppliers"]
        self.purchase_orders: dict[str, dict[str, Any]] = data["purchase_orders"]
        self.invoices: dict[str, dict[str, Any]] = dict(data["invoices"])
        if include_generated:
            from .invoice_catalog import load_generated_invoices  # noqa: PLC0415

            self.invoices.update(load_generated_invoices())
        self._posted: dict[str, dict[str, Any]] = {}
        self.post_calls = 0

    # -- reads --------------------------------------------------------------
    def get_invoice(self, invoice_id: str) -> dict[str, Any]:
        if invoice_id not in self.invoices:
            raise KeyError(f"invoice {invoice_id} not found in ERP")
        return {"invoice_id": invoice_id, **self.invoices[invoice_id]}

    def get_po(self, po_ref: str | None) -> dict[str, Any] | None:
        return self.purchase_orders.get(po_ref) if po_ref else None

    def get_supplier(self, supplier_id: str) -> dict[str, Any] | None:
        return self.suppliers.get(supplier_id)

    def find_duplicates(
        self,
        supplier_id: str,
        total_gbp: float,
        printed_invoice_number: str,
        received_at: str,
        exclude_erp_id: str,
    ) -> list[str]:
        """FIN-AP-090 s3: same supplier, amount and *printed* invoice number seen before.

        Two details that matter and are easy to get wrong. It matches on the number printed
        on the document, not the ERP's own id -- a resubmitted invoice arrives with a new ERP
        id and the same supplier reference, which is exactly the case worth catching. And it
        only looks at invoices received *earlier*, so the first arrival processes cleanly and
        the resubmission is the one flagged, rather than both blocking each other.
        """
        out = []
        for erp_id, inv in self.invoices.items():
            if erp_id == exclude_erp_id or inv["supplier_id"] != supplier_id:
                continue
            if inv["received_at"] > received_at:
                continue
            if (
                printed_invoice_number_of(inv["document_text"]) == printed_invoice_number
                and f"{total_gbp:,.2f}" in inv["document_text"]
            ):
                out.append(erp_id)
        return sorted(out)

    # -- writes -------------------------------------------------------------
    def post_payment(self, idempotency_key: str, payload: dict[str, Any]) -> dict[str, Any]:
        """Idempotent by key. A replayed step returns the original receipt and posts nothing."""
        if idempotency_key in self._posted:
            return {**self._posted[idempotency_key], "duplicate_suppressed": True}
        self.post_calls += 1
        receipt = {
            "payment_id": f"PAY-{len(self._posted) + 9001}",
            "idempotency_key": idempotency_key,
            "posted": True,
            **payload,
        }
        self._posted[idempotency_key] = receipt
        return receipt

    @property
    def payments(self) -> list[dict[str, Any]]:
        return list(self._posted.values())


def classify(
    extracted: ExtractedInvoice, match: MatchResult, supplier: dict[str, Any] | None,
    duplicates: list[str],
) -> tuple[ExceptionCategory, str]:
    """Deterministic exception classification.

    Not an LLM decision. The categories are defined by a control document, the inputs are
    numbers, and the outcome is auditable -- three good reasons to keep the model out of it.
    The model's job in this workflow is reading the document, not deciding the control.
    """
    if not match.bank_account_matches:
        return "BANK_DETAIL_CHANGE", "remit-to account differs from the supplier master record"
    if duplicates:
        return "DUPLICATE_SUSPECT", f"possible duplicate of {', '.join(duplicates)}"
    if not match.po_reference or not match.matched:
        return "NO_PO", "no purchase order reference could be matched"
    if abs(match.price_variance_gbp) > PRICE_TOLERANCE_ABS_GBP or abs(
        match.price_variance_pct
    ) > PRICE_TOLERANCE_PCT:
        return (
            "PRICE_VARIANCE",
            f"invoice price differs from PO by GBP {match.price_variance_gbp:,.2f} "
            f"({match.price_variance_pct:.1%})",
        )
    if abs(match.quantity_variance_pct) > QUANTITY_TOLERANCE_PCT:
        return (
            "QUANTITY_VARIANCE",
            f"invoiced quantity differs from goods received by "
            f"{match.quantity_variance_units:g} units ({match.quantity_variance_pct:.1%})",
        )
    return "NONE", "three-way match within tolerance"


def autonomy_decision(
    category: ExceptionCategory,
    extracted: ExtractedInvoice,
    match: MatchResult,
    supplier: dict[str, Any] | None,
) -> dict[str, Any]:
    """Apply FIN-AP-090 s4. Returns the decision plus the clause it rests on."""
    total = extracted.total_ex_vat_gbp

    if category == "NONE":
        if total < STRAIGHT_THROUGH_LIMIT_GBP:
            return _d("auto_post", "clean three-way match below the straight-through limit",
                      "FIN-AP-090 s2")
        return _d("require_approval",
                  f"clean match but GBP {total:,.2f} is at or above the straight-through "
                  f"limit of GBP {STRAIGHT_THROUGH_LIMIT_GBP:,.0f}", "FIN-AP-090 s2")

    if category in ESCALATE_TO_CONTROLLER:
        return _d("escalate", f"{category} must be escalated to the Group Financial Controller",
                  "FIN-AP-090 s4", escalate_to="group_financial_controller")

    if category in NEVER_AUTONOMOUS:
        return _d("require_approval", f"{category} may never be resolved autonomously",
                  "FIN-AP-090 s4")

    variance_abs = abs(
        match.price_variance_gbp
        if category == "PRICE_VARIANCE"
        else match.quantity_variance_units * (extracted.unit_price_gbp or 0.0)
    )
    variance_pct = abs(variance_abs / total) if total else 1.0
    recent = (supplier or {}).get("exceptions_last_90d", 0)

    failed = []
    if variance_abs >= AUTONOMOUS_VARIANCE_ABS_GBP:
        failed.append(f"variance GBP {variance_abs:,.2f} is not below GBP {AUTONOMOUS_VARIANCE_ABS_GBP:,.0f}")
    if variance_pct >= AUTONOMOUS_VARIANCE_PCT:
        failed.append(f"variance {variance_pct:.1%} is not below {AUTONOMOUS_VARIANCE_PCT:.0%}")
    if recent:
        failed.append(f"supplier has {recent} exception(s) in the preceding 90 days")

    if failed:
        return _d("require_approval", "; ".join(failed), "FIN-AP-090 s4")
    return _d(
        "auto_resolve",
        f"{category} of GBP {variance_abs:,.2f} ({variance_pct:.1%}) is within the autonomous "
        "resolution limit and the supplier has no recent exceptions",
        "FIN-AP-090 s4",
    )


def _d(action: str, rationale: str, policy_ref: str, **extra: Any) -> dict[str, Any]:
    return {"action": action, "rationale": rationale, "policy_ref": policy_ref, **extra}
