"""Agent 3: supervisor with specialist sub-agents."""

from __future__ import annotations

from aivc.obs.run import RunContext
from aivc.store.checkpoint import CheckpointStore

from . import offline as _offline  # noqa: F401  (registers offline behaviour on import)
from .supervisor import SPECIALISTS, Route, SupervisorAgent, SupervisorResponse
from .warehouse import Warehouse

__all__ = [
    "SupervisorAgent",
    "SupervisorResponse",
    "Route",
    "SPECIALISTS",
    "Warehouse",
    "build_agent",
]


def build_agent(ctx: RunContext) -> SupervisorAgent:
    return SupervisorAgent(
        ctx,
        warehouse=Warehouse(ctx.settings.warehouse_db),
        store=CheckpointStore(ctx.settings.checkpoint_db),
    )
