"""Tools and the policy that binds them.

The point of this file is the pairing: every tool is declared with the scope it needs, and
the policy grants each specialist only the scopes its job requires. A prompt-injected
instruction to "run this UPDATE" reaches a policy engine that has never heard of an UPDATE
scope for this principal, and dies there rather than in the model's judgement.
"""

from __future__ import annotations

from typing import Any

from pydantic import BaseModel, Field

from aivc.security.policy import PolicyEngine, ToolRule, read_only_sql
from aivc.store.checkpoint import CheckpointStore
from aivc.tools.registry import ToolRegistry

from .warehouse import Warehouse

registry = ToolRegistry()


# --- warehouse -------------------------------------------------------------
class SqlArgs(BaseModel):
    sql: str = Field(description="A single read-only SELECT statement")
    reason: str = Field(default="", description="Why this query answers the question")


class Empty(BaseModel):
    pass


class RunIdArgs(BaseModel):
    run_id: str


class ApprovalArgs(BaseModel):
    run_id: str
    approved: bool
    approver: str
    note: str = ""


def build_tools(warehouse: Warehouse, store: CheckpointStore) -> ToolRegistry:
    """Bind tools to concrete dependencies. Built per request so a tool can never reach a
    resource from another tenant's context."""
    reg = ToolRegistry()

    @reg.tool(
        name="get_warehouse_schema",
        description="Return the documented schema of the analytics views available to you.",
        scopes={"warehouse:read"},
        side_effect="read",
    )
    def get_warehouse_schema(_: Empty) -> str:
        return warehouse.schema()

    @reg.tool(
        name="run_sql",
        description=(
            "Run one read-only SELECT against the analytics warehouse. Returns at most 200 "
            "rows together with the SQL executed."
        ),
        scopes={"warehouse:read"},
        side_effect="read",
        timeout_s=15.0,
    )
    def run_sql(args: SqlArgs) -> dict[str, Any]:
        return warehouse.query(args.sql)

    @reg.tool(
        name="list_ap_exceptions",
        description="List AP invoice runs currently awaiting a human decision.",
        scopes={"ap:read"},
        side_effect="read",
    )
    def list_ap_exceptions(_: Empty) -> list[dict[str, Any]]:
        return [
            {
                "run_id": r.run_id,
                "invoice_id": r.input.get("invoice_id"),
                "status": r.status,
                "updated_at": r.updated_at,
            }
            for r in store.list_runs("awaiting_approval", limit=25)
        ]

    @reg.tool(
        name="get_ap_run",
        description="Fetch the full decision trail for one AP workflow run.",
        scopes={"ap:read"},
        side_effect="read",
    )
    def get_ap_run(args: RunIdArgs) -> dict[str, Any]:
        run = store.get_run(args.run_id)
        if run is None:
            raise KeyError(f"no run {args.run_id}")
        return {
            "run_id": run.run_id,
            "status": run.status,
            "invoice_id": run.input.get("invoice_id"),
            "steps": [
                {"name": s.name, "status": s.status, "output": s.output} for s in store.steps(run.run_id)
            ],
        }

    @reg.tool(
        name="submit_ap_approval",
        description="Record a human approval decision against a suspended AP workflow run.",
        scopes={"ap:approve"},
        side_effect="write",
        idempotent=False,
    )
    def submit_ap_approval(args: ApprovalArgs) -> dict[str, Any]:
        # Intentionally does not resolve the run here. The specialist agent may only stage a
        # decision; committing it goes through the workflow's own approval path, where the
        # segregation-of-duties check lives. Two agents, one control boundary.
        store.append_event(
            args.run_id,
            "approval_staged_by_agent",
            {"approved": args.approved, "approver": args.approver, "note": args.note},
        )
        return {"staged": True, "run_id": args.run_id, "requires_human_commit": True}

    return reg


def build_policy() -> PolicyEngine:
    """Deny by default; grant the minimum each tool needs.

    Note `run_sql` carries both a scope requirement and a structural guard, and the tool
    underneath opens a read-only connection. Three independent controls, because the SQL
    string itself is attacker-influenced text.
    """
    return PolicyEngine(
        [
            ToolRule("get_warehouse_schema", required_scopes={"warehouse:read"}),
            ToolRule(
                "run_sql",
                required_scopes={"warehouse:read"},
                arg_guards=[read_only_sql],
                rate_limit_per_run=6,
            ),
            ToolRule("list_ap_exceptions", required_scopes={"ap:read"}),
            ToolRule("get_ap_run", required_scopes={"ap:read"}),
            ToolRule(
                "submit_ap_approval",
                required_scopes={"ap:approve"},
                allowed_roles={"finance", "exec"},
                requires_approval=True,
                rate_limit_per_run=2,
            ),
        ],
        default_allow=False,
    )
