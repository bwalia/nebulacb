"""Agent 2: durable AP invoice-exception workflow."""

from __future__ import annotations

from aivc.config import get_settings
from aivc.store.checkpoint import CheckpointStore

from . import offline as _offline  # noqa: F401  (registers offline behaviour on import)
from .domain import ErpStub, ExtractedInvoice
from .workflow import InvoiceExceptionWorkflow

__all__ = ["InvoiceExceptionWorkflow", "ErpStub", "ExtractedInvoice", "build_workflow"]


def build_workflow(
    store: CheckpointStore | None = None,
    erp: ErpStub | None = None,
    *,
    fail_after_step: str | None = None,
) -> InvoiceExceptionWorkflow:
    s = get_settings()
    return InvoiceExceptionWorkflow(
        store or CheckpointStore(s.checkpoint_db), erp, fail_after_step=fail_after_step
    )
