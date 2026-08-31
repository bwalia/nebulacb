"""Offline behaviour for the supervisor and its tool-using specialists.

Three rules: keyword routing, a scripted analyst tool loop (schema -> SQL -> answer), and a
scripted ops loop. The SQL comes from a small template table rather than a model, which is
what makes the CI run deterministic -- and is, not incidentally, a pattern worth keeping in
production for the top twenty recurring questions, where a reviewed template beats generated
SQL on both cost and correctness.
"""

from __future__ import annotations

import json
import re
import uuid
from typing import Any

from aivc.llm.base import Completion, LLMRequest, ToolCall, Usage
from aivc.llm.offline import approx_tokens, register

from .supervisor import DATA_MARKER, OPS_MARKER, ROUTE_MARKER, SYNTH_MARKER

MODEL = "offline-deterministic-v1"

# --- routing ----------------------------------------------------------------
POLICY_TERMS = {
    "policy", "policies", "rule", "rules", "threshold", "approval", "approve", "allowed",
    "permitted", "must", "required", "warranty", "expense", "expenses", "procurement",
    "control", "controls", "notify", "breach", "mileage", "limit", "tender", "supplier",
    "obligation", "clause",
}
DATA_TERMS = {
    "how many", "count", "total", "sum", "average", "breakdown", "by category", "top",
    "trend", "value of", "worth", "largest", "most", "number of",
}
OPS_TERMS = {
    "awaiting", "waiting", "stuck", "blocked", "queue", "run", "status of", "workflow",
    "pending", "outstanding", "invoice inv-", "why did",
}
OUT_OF_SCOPE_TERMS = {
    "weather", "share price", "stock price", "recipe", "holiday booking", "joke",
    "football", "who won", "translate",
}


def _route(request: LLMRequest) -> str:
    q = request.last_user_text().lower()
    if any(t in q for t in OUT_OF_SCOPE_TERMS):
        return json.dumps(
            {"delegations": [], "reasoning": "no specialist covers this topic", "confidence": 0.9}
        )

    wants_data = any(t in q for t in DATA_TERMS)
    wants_ops = any(t in q for t in OPS_TERMS)
    wants_policy = any(re.search(rf"\b{re.escape(t)}\b", q) for t in POLICY_TERMS)

    delegations: list[dict[str, str]] = []
    # Ops questions are usually phrased with a quantity word too ("how many are waiting"),
    # so the more specific signal wins and data only fires when ops did not.
    if wants_ops:
        delegations.append({"specialist": "ap_operations", "task": request.last_user_text()})
    elif wants_data:
        delegations.append({"specialist": "data_analyst", "task": request.last_user_text()})
    if wants_policy and len(delegations) < 2:
        delegations.append({"specialist": "policy_analyst", "task": request.last_user_text()})
    if not delegations:
        delegations.append({"specialist": "data_analyst", "task": request.last_user_text()})

    return json.dumps(
        {
            "delegations": delegations[:2],
            "reasoning": "keyword routing over the specialist catalogue",
            "confidence": 0.8 if len(delegations) == 1 else 0.65,
        }
    )


# --- data analyst -----------------------------------------------------------
SQL_TEMPLATES: list[tuple[re.Pattern[str], str]] = [
    (
        re.compile(r"awaiting|outstanding|waiting|pending|approval", re.I),
        "SELECT category, count(*) AS invoices, round(sum(amount_gbp), 2) AS value_gbp "
        "FROM fct_invoice_exception WHERE status = 'awaiting_approval' "
        "GROUP BY category ORDER BY value_gbp DESC",
    ),
    (
        re.compile(r"supplier|vendor|spend", re.I),
        "SELECT s.supplier_name, count(*) AS exceptions, round(sum(f.amount_gbp), 2) AS value_gbp "
        "FROM fct_invoice_exception f JOIN dim_supplier s ON s.supplier_id = f.supplier_id "
        "GROUP BY s.supplier_name ORDER BY value_gbp DESC",
    ),
    (
        re.compile(r"categor|breakdown|type|how many", re.I),
        "SELECT category, count(*) AS invoices, round(sum(amount_gbp), 2) AS value_gbp "
        "FROM fct_invoice_exception GROUP BY category ORDER BY invoices DESC",
    ),
    (
        re.compile(r"automat|straight.?through|auto", re.I),
        "SELECT decision, count(*) AS invoices, round(sum(amount_gbp), 2) AS value_gbp "
        "FROM fct_invoice_exception GROUP BY decision ORDER BY invoices DESC",
    ),
]

FALLBACK_SQL = (
    "SELECT invoice_id, category, decision, amount_gbp, status "
    "FROM fct_invoice_exception ORDER BY received_at DESC LIMIT 10"
)


def _tool_completion(name: str, arguments: dict[str, Any], request: LLMRequest) -> Completion:
    return Completion(
        text="",
        model=MODEL,
        usage=Usage(approx_tokens(request.all_text()), 20),
        tool_calls=[ToolCall(id=f"call_{uuid.uuid4().hex[:8]}", name=name, arguments=arguments)],
        stop_reason="tool_use",
    )


def _last_tool_result(request: LLMRequest, tool_name: str) -> str | None:
    for message in reversed(request.messages):
        if message.role == "tool" and message.name == tool_name:
            return message.content
    return None


def _data_analyst(request: LLMRequest) -> Completion | str:
    task = request.messages[0].content if request.messages else ""
    if _last_tool_result(request, "get_warehouse_schema") is None:
        return _tool_completion("get_warehouse_schema", {}, request)

    sql_result = _last_tool_result(request, "run_sql")
    if sql_result is None:
        sql = next((tpl for pattern, tpl in SQL_TEMPLATES if pattern.search(task)), FALLBACK_SQL)
        return _tool_completion(
            "run_sql", {"sql": sql, "reason": "template matched to the question"}, request
        )

    return _summarise_rows(sql_result)


def _summarise_rows(payload: str) -> str:
    if payload.startswith("DENIED:"):
        # The policy engine refused the call. Say so in the answer rather than dressing a
        # permission failure up as an empty result -- users act on "no data" very differently
        # from "you are not allowed to see this".
        return (
            "I cannot answer that: access to the analytics warehouse was denied for this "
            f"user. {payload.removeprefix('DENIED:').strip()}"
        )
    try:
        data = json.loads(payload)
    except (json.JSONDecodeError, TypeError):
        return f"The warehouse query did not return usable rows: {payload[:200]}"
    if payload.startswith("ERROR") or "rows" not in data:
        return f"The warehouse query failed: {payload[:200]}"
    rows = data["rows"]
    if not rows:
        return "The query returned no rows, so there is nothing matching that criteria."
    columns = list(rows[0].keys())
    lines = [
        ", ".join(f"{c}={r[c]}" for c in columns)
        for r in rows[:8]
    ]
    return (
        f"{data['row_count']} row(s) from the warehouse ({', '.join(columns)}): "
        + "; ".join(lines)
        + f". Query: {data['sql']}"
    )


# --- AP operations ----------------------------------------------------------
def _ap_ops(request: LLMRequest) -> Completion | str:
    listing = _last_tool_result(request, "list_ap_exceptions")
    if listing is None:
        return _tool_completion("list_ap_exceptions", {}, request)

    try:
        runs = json.loads(listing)
    except (json.JSONDecodeError, TypeError):
        return f"Could not read the exception queue: {listing[:200]}"
    if not runs:
        return "No AP workflow runs are currently awaiting a human decision."
    ids = ", ".join(f"{r['invoice_id']} (run {r['run_id'][:10]})" for r in runs[:8])
    return (
        f"{len(runs)} AP run(s) are awaiting a human decision: {ids}. "
        "Each needs a named approver with the finance approval scope; this assistant cannot "
        "approve them."
    )


# --- synthesis --------------------------------------------------------------
def _synthesise(request: LLMRequest) -> str:
    text = request.last_user_text()
    findings = re.split(r"\n### ", text.split("SPECIALIST FINDINGS:", 1)[-1].strip())
    parts = []
    for block in findings:
        block = block.strip().lstrip("#").strip()
        if not block:
            continue
        name, _, body = block.partition("\n")
        finding = body.split("Finding:", 1)[-1].strip()
        if finding:
            parts.append(f"{name.replace('_', ' ')}: {finding}")
    return " ".join(parts) if parts else "The specialists returned no findings."


def install() -> None:
    register("supervisor.route", lambda r: ROUTE_MARKER in r.system, _route, priority=10)
    register("supervisor.synth", lambda r: SYNTH_MARKER in r.system, _synthesise, priority=10)
    register("supervisor.data", lambda r: DATA_MARKER in r.system, _data_analyst, priority=10)
    register("supervisor.ops", lambda r: OPS_MARKER in r.system, _ap_ops, priority=10)


install()
