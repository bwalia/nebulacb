"""Security controls. These are the tests that should fail loudest."""

from __future__ import annotations

import pytest

from aivc.security.identity import Principal
from aivc.security.policy import (
    PolicyEngine,
    ToolRule,
    domain_allowlist,
    max_value,
    read_only_sql,
    tenant_isolation,
)
from aivc.security.redaction import Redactor


class TestPolicyEngine:
    def test_unknown_tool_is_denied_by_default(self):
        engine = PolicyEngine([])
        decision = engine.authorize(Principal.user("a", scopes={"*"}), "anything", {})
        assert not decision.allowed
        assert decision.rule == "default-deny"

    def test_missing_scope_is_denied(self):
        engine = PolicyEngine([ToolRule("run_sql", required_scopes={"warehouse:read"})])
        assert not engine.authorize(Principal.user("a"), "run_sql", {}).allowed
        assert engine.authorize(
            Principal.user("a", scopes={"warehouse:read"}), "run_sql", {}
        ).allowed

    def test_role_restriction(self):
        engine = PolicyEngine([ToolRule("approve", allowed_roles={"finance"})])
        assert not engine.authorize(Principal.user("a", roles={"employee"}), "approve", {}).allowed
        assert engine.authorize(Principal.user("a", roles={"finance"}), "approve", {}).allowed

    def test_rate_limit_is_per_run(self):
        engine = PolicyEngine([ToolRule("run_sql", rate_limit_per_run=2)])
        p = Principal.user("a")
        for _ in range(2):
            assert engine.authorize(p, "run_sql", {}, "run-1").allowed
            engine.note_call("run_sql", "run-1")
        assert not engine.authorize(p, "run_sql", {}, "run-1").allowed
        # a different run starts with a fresh allowance
        assert engine.authorize(p, "run_sql", {}, "run-2").allowed

    @pytest.mark.parametrize(
        "sql",
        [
            "DELETE FROM invoices",
            "update dim_supplier set x=1",
            "SELECT 1; DROP TABLE t",
            "DROP TABLE t",
            "insert into t values (1)",
            "PRAGMA table_info(t)",
            "ATTACH DATABASE '/etc/passwd' AS p",
        ],
    )
    def test_write_sql_is_rejected(self, sql):
        assert read_only_sql(Principal.user("a"), {"sql": sql}) is not None

    @pytest.mark.parametrize(
        "sql",
        ["SELECT 1", "select a from b where c = 'delete'", "WITH x AS (SELECT 1) SELECT * FROM x"],
    )
    def test_read_sql_is_allowed(self, sql):
        assert read_only_sql(Principal.user("a"), {"sql": sql}) is None

    def test_value_limit_requires_approval(self):
        guard = max_value("amount_gbp", 500)
        assert guard(Principal.user("a"), {"amount_gbp": 400}) is None
        decision = guard(Principal.user("a"), {"amount_gbp": 900})
        assert decision is not None and not decision.allowed and decision.requires_approval

    def test_tenant_isolation(self):
        guard = tenant_isolation()
        p = Principal.user("a", tenant="acme")
        assert guard(p, {"tenant": "acme"}) is None
        assert guard(p, {"tenant": "other"}).allowed is False

    def test_domain_allowlist(self):
        guard = domain_allowlist("url", {"northgate.example"})
        assert guard(Principal.user("a"), {"url": "https://api.northgate.example/x"}) is None
        assert guard(Principal.user("a"), {"url": "https://evil.test/x"}).allowed is False


class TestIdentity:
    def test_agent_identity_can_only_narrow(self):
        user = Principal.user("sid", roles={"employee", "finance"}, scopes={"a", "b", "c"})
        agent = Principal.agent("agent:analyst", scopes={"a", "z"})
        effective = user.intersect(agent)
        assert effective.scopes == {"a"}
        assert "z" not in effective.scopes  # the agent cannot grant itself a scope
        assert effective.kind == "agent"

    def test_wildcard_agent_does_not_widen_user(self):
        user = Principal.user("sid", scopes={"a"})
        agent = Principal.agent("agent:x", scopes={"*"})
        assert user.intersect(agent).scopes == {"a"}


class TestRedaction:
    def test_round_trip(self):
        text = "Email a.smith@northgate.example about IBAN GB29NWBK60161331926819 today"
        result = Redactor().redact(text)
        assert "a.smith@northgate.example" not in result.text
        assert "GB29NWBK60161331926819" not in result.text
        assert Redactor.restore(result.text, result.mapping) == text

    def test_same_value_gets_one_placeholder(self):
        result = Redactor().redact("x@y.com and again x@y.com")
        assert result.counts["EMAIL"] == 1
        assert result.text.count("[EMAIL_1]") == 2

    def test_card_number_requires_luhn(self):
        # a valid test card number redacts; a random 16-digit string does not
        valid = Redactor().redact("card 4111111111111111")
        assert "4111111111111111" not in valid.text
        invalid = Redactor().redact("ref 1234567812345678")
        assert "1234567812345678" in invalid.text

    def test_api_keys_are_caught(self):
        result = Redactor().redact("token sk-abcdefghijklmnopqrstuvwxyz012345")
        assert "sk-abcdefghijklmnop" not in result.text
