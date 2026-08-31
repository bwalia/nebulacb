"""Agent 2 -- Durable AP invoice-exception workflow.

The demo question this answers: *"can an agent take a real finance process end to end without
anyone losing sleep about it?"*

Shape of the answer:

  * The **model reads**; the **rules decide**. Extraction from a document is a language
    problem and the LLM does it. Whether an exception may be resolved without a human is a
    control question with a clause reference, and it is plain Python. Putting the control in
    the prompt is the mistake that makes these systems un-auditable.
  * Extraction output is validated against the source document before anything downstream
    trusts it -- a hallucinated total that no one checks is a wrong payment.
  * The approval gate is a durable suspend, not a blocking wait. The process can exit; the
    approver can take three days.
  * The ERP write is idempotent under a deterministic key, so replay after a crash cannot
    pay twice.
  * Every decision writes an audit record with the policy clause, the evidence and the
    identity of the decider -- FIN-AP-090 s6 requires exactly this of automated systems.
"""

from __future__ import annotations

import re
from typing import Any

from aivc.agent.durable import (
    PermanentStepError,
    RetryPolicy,
    Step,
    StepContext,
    Suspend,
    Workflow,
    WorkflowResult,
)
from aivc.llm.gateway import LLMGateway
from aivc.obs.run import RunContext
from aivc.store.checkpoint import CheckpointStore

from .domain import (
    POLICY_REFS,
    ErpStub,
    ExtractedInvoice,
    MatchResult,
    autonomy_decision,
    classify,
)

EXTRACTION_MARKER = "AIVC_AP_EXTRACT_V1"

EXTRACTION_SYSTEM = f"""{EXTRACTION_MARKER}
You extract structured fields from a supplier invoice document.

Rules:
- Copy values exactly as printed. Never compute, convert currency, or infer a missing value.
- If a field is absent from the document, return null. An absent purchase order reference is
  a meaningful business signal; guessing one destroys it.
- Put anything unusual about the document in `notes` (for example a stated change of bank
  details, a handwritten amendment, a missing PO).
Return JSON matching the schema only.
"""


class InvoiceExceptionWorkflow:
    def __init__(
        self,
        store: CheckpointStore,
        erp: ErpStub | None = None,
        *,
        fail_after_step: str | None = None,
    ):
        self.erp = erp or ErpStub()
        self.workflow = Workflow(
            "ap_invoice_exception",
            [
                Step("fetch_invoice", self._fetch, description="read the invoice from the ERP"),
                Step(
                    "extract_fields",
                    self._extract,
                    retry=RetryPolicy(attempts=2),
                    description="LLM structured extraction, validated against the document",
                ),
                Step("three_way_match", self._match, description="PO / GRN / invoice reconciliation"),
                Step("classify_exception", self._classify, description="deterministic control classification"),
                Step("policy_decision", self._decide, description="apply FIN-AP-090 autonomy limits"),
                Step("approval_gate", self._approval, description="durable suspend for a human decision"),
                Step(
                    "post_to_erp",
                    self._post,
                    idempotent=False,
                    description="idempotent payment posting",
                ),
                Step("audit_record", self._audit, description="FIN-AP-090 s6 audit evidence"),
            ],
            store,
            # Demo/test hook only; see Workflow.crash_after.
            crash_after=fail_after_step,
        )

    # -- public API ---------------------------------------------------------
    def start(self, invoice_id: str, ctx: RunContext) -> WorkflowResult:
        return self.workflow.start(
            {"invoice_id": invoice_id},
            ctx,
            # One run per invoice. A redelivered queue message is then a no-op, not a
            # second payment.
            idempotency_key=f"invoice:{invoice_id}",
        )

    def resume(
        self, run_id: str, ctx: RunContext, *, approved: bool, approver: str, note: str = ""
    ) -> WorkflowResult:
        return self.workflow.resume(
            run_id, ctx, {"approved": approved, "approver": approver, "note": note}
        )

    # -- steps --------------------------------------------------------------
    def _fetch(self, sc: StepContext) -> dict[str, Any]:
        invoice_id = sc.run.input["invoice_id"]
        try:
            invoice = self.erp.get_invoice(invoice_id)
        except KeyError as exc:
            raise PermanentStepError(str(exc)) from exc
        sc.emit("invoice_fetched", {"invoice_id": invoice_id})
        return invoice

    def _extract(self, sc: StepContext) -> dict[str, Any]:
        invoice = sc.output_of("fetch_invoice")
        gateway = LLMGateway.for_run(sc.ctx)
        extracted = gateway.structured(
            system=EXTRACTION_SYSTEM,
            user=f"INVOICE DOCUMENT:\n\n{invoice['document_text']}",
            schema=ExtractedInvoice,
            label="ap.extract",
        )

        # Validate before trusting. The total drives every downstream control, so it must
        # appear verbatim in the source document -- this is the cheapest possible defence
        # against a fluent extraction error, and it costs nothing per invoice.
        text = invoice["document_text"]
        if not _amount_present(extracted.total_ex_vat_gbp, text):
            raise PermanentStepError(
                f"extracted total GBP {extracted.total_ex_vat_gbp:,.2f} does not appear in the "
                "invoice document; refusing to proceed on unverified figures"
            )
        if extracted.po_reference and extracted.po_reference not in text:
            raise PermanentStepError(
                f"extracted PO reference {extracted.po_reference} does not appear in the document"
            )
        return extracted.model_dump()

    def _match(self, sc: StepContext) -> dict[str, Any]:
        invoice = sc.output_of("fetch_invoice")
        extracted = ExtractedInvoice(**sc.output_of("extract_fields"))
        po = self.erp.get_po(extracted.po_reference)
        supplier = self.erp.get_supplier(invoice["supplier_id"])

        if po is None:
            result = MatchResult(
                matched=False,
                po_reference=extracted.po_reference,
                bank_account_matches=(
                    supplier is None
                    or extracted.remit_to_account is None
                    or extracted.remit_to_account == supplier["bank_account"]
                ),
                reasons=["no matching purchase order"],
            )
            return result.to_dict()

        price_variance = extracted.total_ex_vat_gbp - po["line_total_gbp"]
        qty_variance = (extracted.quantity or 0) - po["goods_received_qty"]
        result = MatchResult(
            matched=True,
            po_reference=extracted.po_reference,
            price_variance_gbp=price_variance,
            price_variance_pct=price_variance / po["line_total_gbp"] if po["line_total_gbp"] else 0.0,
            quantity_variance_units=qty_variance,
            quantity_variance_pct=qty_variance / po["goods_received_qty"]
            if po["goods_received_qty"]
            else 0.0,
            bank_account_matches=(
                supplier is None
                or extracted.remit_to_account is None
                or extracted.remit_to_account == supplier["bank_account"]
            ),
            reasons=[],
        )
        if not result.bank_account_matches:
            result.reasons.append("remit-to account differs from supplier master")
        return result.to_dict()

    def _classify(self, sc: StepContext) -> dict[str, Any]:
        invoice = sc.output_of("fetch_invoice")
        extracted = ExtractedInvoice(**sc.output_of("extract_fields"))
        match = MatchResult(**{**sc.output_of("three_way_match"), "reasons": []})
        supplier = self.erp.get_supplier(invoice["supplier_id"])
        duplicates = self.erp.find_duplicates(
            invoice["supplier_id"],
            extracted.total_ex_vat_gbp,
            extracted.invoice_number,
            invoice["received_at"],
            invoice["invoice_id"],
        )
        category, reason = classify(extracted, match, supplier, duplicates)
        sc.emit("classified", {"category": category, "reason": reason})
        return {"category": category, "reason": reason, "duplicates": duplicates}

    def _decide(self, sc: StepContext) -> dict[str, Any]:
        invoice = sc.output_of("fetch_invoice")
        extracted = ExtractedInvoice(**sc.output_of("extract_fields"))
        match = MatchResult(**{**sc.output_of("three_way_match"), "reasons": []})
        supplier = self.erp.get_supplier(invoice["supplier_id"])
        category = sc.output_of("classify_exception")["category"]
        decision = autonomy_decision(category, extracted, match, supplier)
        sc.emit("decided", decision)
        return decision

    def _approval(self, sc: StepContext) -> dict[str, Any]:
        decision = sc.output_of("policy_decision")
        if decision["action"] in ("auto_post", "auto_resolve"):
            return {"required": False, "approved": True, "approver": "system:policy_engine"}

        payload = sc.resume_payload
        if not payload:
            # Durable pause. Nothing is held in memory; the run can sit here for days.
            raise Suspend(
                decision["rationale"],
                {
                    "invoice_id": sc.run.input["invoice_id"],
                    "action": decision["action"],
                    "policy_ref": decision["policy_ref"],
                    "escalate_to": decision.get("escalate_to", "budget_holder"),
                    "category": sc.output_of("classify_exception")["category"],
                },
            )

        if not payload.get("approved"):
            sc.emit("rejected", payload)
            return {
                "required": True,
                "approved": False,
                "approver": payload.get("approver", "unknown"),
                "note": payload.get("note", ""),
            }

        # Segregation of duties, FIN-AP-090 s5: the approver may not be the budget holder who
        # raised the PO. Checked here rather than trusted upstream.
        po = self.erp.get_po(ExtractedInvoice(**sc.output_of("extract_fields")).po_reference)
        approver = payload.get("approver", "unknown")
        if po and approver == po.get("budget_holder"):
            raise PermanentStepError(
                f"segregation of duties: {approver} raised the purchase order and may not "
                "approve the exception (FIN-AP-090 s5)"
            )
        sc.emit("approved", {"approver": approver})
        return {
            "required": True,
            "approved": True,
            "approver": approver,
            "note": payload.get("note", ""),
        }

    def _post(self, sc: StepContext) -> dict[str, Any]:
        approval = sc.output_of("approval_gate")
        extracted = ExtractedInvoice(**sc.output_of("extract_fields"))
        if not approval["approved"]:
            return {"posted": False, "reason": "exception rejected by approver"}
        key = sc.idempotency_key("post", extracted.invoice_number)
        receipt = self.erp.post_payment(
            key,
            {
                "invoice_number": extracted.invoice_number,
                "amount_gbp": extracted.total_ex_vat_gbp,
                "po_reference": extracted.po_reference,
                "approved_by": approval["approver"],
            },
        )
        sc.emit("posted", {"payment_id": receipt["payment_id"]})
        return receipt

    def _audit(self, sc: StepContext) -> dict[str, Any]:
        extracted = ExtractedInvoice(**sc.output_of("extract_fields"))
        classification = sc.output_of("classify_exception")
        decision = sc.output_of("policy_decision")
        approval = sc.output_of("approval_gate")
        posting = sc.output_of("post_to_erp")
        record = {
            "invoice_id": sc.run.input["invoice_id"],
            "invoice_number": extracted.invoice_number,
            "amount_gbp": extracted.total_ex_vat_gbp,
            "category": classification["category"],
            "category_reason": classification["reason"],
            "decision": decision["action"],
            "decision_rationale": decision["rationale"],
            "policy_ref": decision["policy_ref"],
            "policy_refs": POLICY_REFS,
            "approval_required": approval["required"],
            "approved_by": approval["approver"],
            "posted": bool(posting.get("posted")),
            "payment_id": posting.get("payment_id"),
            "decided_by": f"agent:ap_invoice_exception@{sc.ctx.settings.model}",
            "run_id": sc.run.run_id,
            "trace_id": sc.ctx.tracer.trace_id,
            "cost_usd": round(sc.ctx.ledger.total_usd, 6),
        }
        sc.emit("audit_recorded", {"invoice_id": record["invoice_id"]})
        return record

    # -- helpers ------------------------------------------------------------
def _amount_present(amount: float, text: str) -> bool:
    """Does the extracted total actually appear on the document, in any common format?"""
    candidates = {
        f"{amount:,.2f}",
        f"{amount:.2f}",
        f"{amount:,.0f}" if amount == int(amount) else f"{amount:,.2f}",
    }
    normalised = re.sub(r"[  ]", "", text)
    return any(re.sub(r"[  ]", "", c) in normalised for c in candidates)
