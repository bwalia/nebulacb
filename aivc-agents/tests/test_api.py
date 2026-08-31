"""HTTP surface: authentication, authorisation and the shape of the contract."""

from __future__ import annotations

import pytest
from fastapi.testclient import TestClient

from aivc.api import app


@pytest.fixture()
def client(settings):
    return TestClient(app)


def headers(user="a.user", roles="employee", scopes="corpus:read", tenant="northgate"):
    return {"X-User": user, "X-Roles": roles, "X-Scopes": scopes, "X-Tenant": tenant}


def finance_headers(user="ap.clerk", scopes="corpus:read,warehouse:read,ap:read"):
    return headers(user=user, roles="finance", scopes=scopes)


class TestHealth:
    def test_healthz(self, client):
        body = client.get("/healthz").json()
        assert body["status"] == "ok"

    def test_readyz_checks_dependencies(self, client):
        body = client.get("/readyz").json()
        assert body["checks"]["corpus"]["ok"] is True
        assert body["checks"]["corpus"]["chunks"] > 0

    def test_root_redirects_to_docs(self, client):
        response = client.get("/", follow_redirects=False)
        assert response.status_code in (307, 302)
        # Prefer the workflow console when the React build is present.
        assert response.headers["location"] in ("/docs", "/dashboard/")

    def test_openapi_exposes_use_case_examples_and_auth(self, client):
        schema = client.get("/openapi.json").json()
        schemes = schema["components"]["securitySchemes"]
        assert {"X-User", "X-Roles", "X-Scopes", "X-Tenant"} <= set(schemes)
        policy_examples = schema["paths"]["/v1/policy/ask"]["post"]["requestBody"]["content"][
            "application/json"
        ]["examples"]
        assert "merit_acl_refuse" in policy_examples
        assert "merit_hr_answer" in policy_examples
        triage_examples = schema["paths"]["/v1/ap/triage"]["post"]["requestBody"]["content"][
            "application/json"
        ]["examples"]
        assert "bank_change" in triage_examples
        assert client.get("/docs").status_code == 200

    def test_dashboard_is_served_when_built(self, client):
        from aivc.api import DASHBOARD_DIR

        if not (DASHBOARD_DIR / "index.html").is_file():
            return
        page = client.get("/dashboard/")
        assert page.status_code == 200
        assert b"Workflow console" in page.content or b"root" in page.content
        root = client.get("/", follow_redirects=False)
        assert root.headers["location"] == "/dashboard/"


class TestPolicyEndpoint:
    def test_requires_identity(self, client):
        assert client.post("/v1/policy/ask", json={"question": "What is the mileage rate?"}).status_code == 401

    def test_answers_with_citations(self, client):
        response = client.post(
            "/v1/policy/ask",
            json={"question": "What mileage rate applies to the first 10,000 business miles?"},
            headers=headers(),
        )
        body = response.json()
        assert response.status_code == 200
        assert body["refused"] is False
        assert body["citations"]
        assert "context" not in body  # retrieved text stays in the trace
        assert body["trace_id"]

    def test_acl_is_enforced_over_http(self, client):
        question = {"question": "What is the Group merit budget for the 2026 cycle?"}
        denied = client.post("/v1/policy/ask", json=question, headers=headers()).json()
        allowed = client.post(
            "/v1/policy/ask", json=question, headers=headers(roles="employee,hr")
        ).json()
        assert denied["refused"] is True and "3.4" not in denied["answer"]
        assert allowed["refused"] is False and "3.4" in allowed["answer"]

    def test_input_is_validated(self, client):
        assert client.post("/v1/policy/ask", json={"question": "x"}, headers=headers()).status_code == 422


class TestApEndpoints:
    def test_triage_requires_the_finance_role(self, client):
        response = client.post(
            "/v1/ap/triage", json={"invoice_id": "INV-1001"}, headers=headers(roles="employee")
        )
        assert response.status_code == 403

    def test_triage_and_queue(self, client):
        finance = headers(user="ap.clerk", roles="finance", scopes="ap:read")
        assert client.post("/v1/ap/triage", json={"invoice_id": "INV-1005"}, headers=finance).json()[
            "status"
        ] == "awaiting_approval"
        queue = client.get("/v1/ap/queue", headers=finance).json()
        assert any(r["invoice_id"] == "INV-1005" for r in queue["awaiting_approval"])

    def test_approver_must_be_the_authenticated_user(self, client):
        finance = headers(user="ap.clerk", roles="finance", scopes="ap:read")
        started = client.post(
            "/v1/ap/triage", json={"invoice_id": "INV-1003"}, headers=finance
        ).json()
        response = client.post(
            "/v1/ap/approve",
            json={"run_id": started["run_id"], "approved": True, "approver": "someone.else"},
            headers=finance,
        )
        assert response.status_code == 403

    def test_approval_completes_the_run(self, client):
        finance = headers(user="ap.clerk", roles="finance", scopes="ap:read")
        started = client.post(
            "/v1/ap/triage", json={"invoice_id": "INV-1004"}, headers=finance
        ).json()
        approved = client.post(
            "/v1/ap/approve",
            json={"run_id": started["run_id"], "approved": True, "approver": "ap.clerk"},
            headers=finance,
        ).json()
        assert approved["status"] == "succeeded"

    def test_cross_tenant_run_is_not_found(self, client):
        finance = headers(user="ap.clerk", roles="finance", scopes="ap:read")
        started = client.post(
            "/v1/ap/triage", json={"invoice_id": "INV-1006"}, headers=finance
        ).json()
        other = headers(user="x", roles="finance", tenant="other-co")
        assert client.get(f"/v1/runs/{started['run_id']}", headers=other).status_code == 404


class TestInvoicesEndpoint:
    def test_list_invoices_requires_finance(self, client):
        assert client.get("/v1/ap/invoices", headers=headers()).status_code == 403

    def test_list_invoices_returns_fixture_mailbox(self, client):
        body = client.get("/v1/ap/invoices", headers=finance_headers()).json()
        assert body["count"] >= 7
        ids = {i["invoice_id"] for i in body["invoices"]}
        assert "INV-1001" in ids
        assert body["invoices"][0]["supplier_name"]

    def test_invoice_detail(self, client):
        body = client.get("/v1/ap/invoices/INV-1005", headers=finance_headers()).json()
        assert "BANK DETAILS HAVE CHANGED" in body["document_text"]
        assert body["scenario_hint"] == "BANK_DETAIL_CHANGE"

    def test_generate_offline_batch(self, client, settings, tmp_path):
        from aivc.config import reset_settings

        reset_settings(
            provider="offline",
            state_dir=tmp_path / "state",
            checkpoint_db=tmp_path / "state" / "workflow.sqlite",
        )
        body = client.post(
            "/v1/ap/invoices/generate",
            json={"cadence": "monthly", "count": 3},
            headers=finance_headers(),
        ).json()
        assert body["batch"]["count"] == 3
        assert len(body["generated"]) == 3

    def test_credit_controller_summary(self, client):
        body = client.get("/v1/ap/credit-controller/summary", headers=finance_headers()).json()
        assert body["mailbox"]["total_invoices"] >= 7
        assert len(body["playbook"]) == 7


class TestAssistantEndpoint:
    def test_routes_and_reports_the_route(self, client):
        response = client.post(
            "/v1/assistant/ask",
            json={"question": "How many invoice exceptions do we have by category?"},
            headers=headers(roles="employee,finance", scopes="warehouse:read,ap:read,corpus:read"),
        ).json()
        assert response["route"] == ["data_analyst"]
        assert response["declined"] is False
