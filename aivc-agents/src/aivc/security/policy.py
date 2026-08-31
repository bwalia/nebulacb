"""Deny-by-default authorisation for tool calls.

The model proposes; the policy engine disposes. Every tool invocation is checked against
(principal, tool, arguments) before execution, and every decision is traced. Prompt
injection stops being an existential risk once the blast radius is bounded here rather than
by hoping the system prompt holds.
"""

from __future__ import annotations

import re
from dataclasses import dataclass, field
from typing import Any, Callable

from .identity import Principal


@dataclass(frozen=True, slots=True)
class Decision:
    allowed: bool
    reason: str
    rule: str = ""
    requires_approval: bool = False

    def raise_for_denial(self) -> None:
        if not self.allowed:
            raise PermissionDenied(self.reason)


class PermissionDenied(PermissionError):
    pass


ArgGuard = Callable[[Principal, dict[str, Any]], "Decision | None"]


@dataclass
class ToolRule:
    tool: str
    required_scopes: set[str] = field(default_factory=set)
    allowed_roles: set[str] = field(default_factory=set)  # empty = any role
    requires_approval: bool = False
    arg_guards: list[ArgGuard] = field(default_factory=list)
    rate_limit_per_run: int | None = None


class PolicyEngine:
    def __init__(self, rules: list[ToolRule] | None = None, default_allow: bool = False):
        self._rules = {r.tool: r for r in (rules or [])}
        self._default_allow = default_allow
        self._counts: dict[tuple[str, str], int] = {}

    def add(self, rule: ToolRule) -> "PolicyEngine":
        self._rules[rule.tool] = rule
        return self

    def authorize(
        self, principal: Principal, tool: str, args: dict[str, Any], run_id: str = ""
    ) -> Decision:
        rule = self._rules.get(tool)
        if rule is None:
            if self._default_allow:
                return Decision(True, "no rule; default-allow", rule="default")
            return Decision(False, f"tool '{tool}' is not permitted for this agent", rule="default-deny")

        missing = {s for s in rule.required_scopes if not principal.has_scope(s)}
        if missing:
            return Decision(
                False, f"principal lacks scope(s): {sorted(missing)}", rule=f"{tool}:scopes"
            )

        if rule.allowed_roles and not (rule.allowed_roles & set(principal.roles)):
            return Decision(
                False,
                f"principal roles {sorted(principal.roles)} not in {sorted(rule.allowed_roles)}",
                rule=f"{tool}:roles",
            )

        if rule.rate_limit_per_run is not None:
            key = (run_id, tool)
            if self._counts.get(key, 0) >= rule.rate_limit_per_run:
                return Decision(
                    False,
                    f"tool '{tool}' hit its per-run call limit ({rule.rate_limit_per_run})",
                    rule=f"{tool}:rate",
                )

        for guard in rule.arg_guards:
            decision = guard(principal, args)
            if decision is not None and not decision.allowed:
                return decision

        return Decision(True, "allowed", rule=tool, requires_approval=rule.requires_approval)

    def note_call(self, tool: str, run_id: str = "") -> None:
        key = (run_id, tool)
        self._counts[key] = self._counts.get(key, 0) + 1


# --- reusable argument guards ------------------------------------------------

_WRITE_SQL = re.compile(
    r"\b(insert|update|delete|drop|alter|create|truncate|grant|revoke|attach|copy|vacuum|pragma)\b",
    re.I,
)


_SQL_LITERAL = re.compile(r"'(?:[^']|'')*'|\"(?:[^\"]|\"\")*\"|--[^\n]*|/\*.*?\*/", re.S)


def read_only_sql(_: Principal, args: dict[str, Any]) -> Decision | None:
    """Structural check on a SQL string.

    Belt to the braces of a read-only database role -- never the only control, because a
    regex over SQL is a losing game in the general case. String literals and comments are
    stripped before the keyword scan, so `WHERE status = 'deleted'` is not mistaken for a
    DELETE; that false positive is the one that makes teams disable the guard entirely,
    which is worse than having it be imperfect.
    """
    sql = str(args.get("sql", ""))
    stripped = _SQL_LITERAL.sub(" ", sql)
    if ";" in stripped.strip().rstrip(";"):
        return Decision(False, "multiple statements are not allowed", rule="sql:single-statement")
    if _WRITE_SQL.search(stripped):
        return Decision(False, "only SELECT statements are permitted", rule="sql:read-only")
    if not re.match(r"^\s*(select|with)\b", stripped, re.I):
        return Decision(False, "query must begin with SELECT or WITH", rule="sql:read-only")
    return None


def max_value(field_name: str, limit: float) -> ArgGuard:
    def guard(_: Principal, args: dict[str, Any]) -> Decision | None:
        try:
            value = float(args.get(field_name, 0))
        except (TypeError, ValueError):
            return Decision(False, f"{field_name} is not numeric", rule=f"{field_name}:type")
        if value > limit:
            return Decision(
                False,
                f"{field_name}={value:,.2f} exceeds the autonomous limit of {limit:,.2f}",
                rule=f"{field_name}:limit",
                requires_approval=True,
            )
        return None

    return guard


def tenant_isolation(field_name: str = "tenant") -> ArgGuard:
    def guard(principal: Principal, args: dict[str, Any]) -> Decision | None:
        requested = args.get(field_name)
        if requested and requested != principal.tenant:
            return Decision(
                False,
                f"cross-tenant access denied ({principal.tenant} -> {requested})",
                rule="tenant:isolation",
            )
        return None

    return guard


def domain_allowlist(field_name: str, allowed: set[str]) -> ArgGuard:
    def guard(_: Principal, args: dict[str, Any]) -> Decision | None:
        target = str(args.get(field_name, ""))
        host = re.sub(r"^https?://", "", target).split("/")[0].lower()
        if host and not any(host == d or host.endswith("." + d) for d in allowed):
            return Decision(False, f"host '{host}' is not on the egress allowlist", rule="net:allowlist")
        return None

    return guard
