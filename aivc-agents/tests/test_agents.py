"""Agent behaviour: chunking, retrieval, RAG governance, durable execution, supervisor."""

from __future__ import annotations

import pytest

from aivc.agent.durable import (
    PermanentStepError,
    RetryPolicy,
    Step,
    StepContext,
    Suspend,
    TransientStepError,
    Workflow,
)
from aivc.obs.run import RunContext
from aivc.security.identity import Principal
from agents.governed_rag import build_agent as build_rag_agent, get_corpus
from agents.governed_rag.agent import GroundedAnswer, question_coverage
from agents.governed_rag.chunking import parse_front_matter, split_sections, window
from agents.governed_rag.retrieve import HybridRetriever, acl_predicate
from agents.ops_workflow import ErpStub, build_workflow
from agents.supervisor import build_agent as build_supervisor


# --- chunking ---------------------------------------------------------------
class TestChunking:
    def test_front_matter_is_parsed(self):
        meta, body = parse_front_matter("---\ntitle: X\nacl: [a, b]\n---\n# H\ntext\n")
        assert meta["title"] == "X" and meta["acl"] == ["a", "b"]
        assert body.startswith("# H")

    def test_malformed_front_matter_fails_loudly(self):
        with pytest.raises(ValueError):
            parse_front_matter("---\nthis line has no colon\n---\nbody\n")

    def test_sections_follow_the_heading_tree(self):
        sections = split_sections("# Doc\nintro\n\n## One\nalpha\n\n## Two\nbeta\n")
        names = [s for s, _ in sections]
        assert "One" in names and "Two" in names

    def test_window_overlaps_whole_sentences(self):
        text = " ".join(f"Sentence number {i} carries some content." for i in range(60))
        parts = window(text, max_tokens=40, overlap_tokens=10)
        assert len(parts) > 1
        # the tail of one chunk reappears at the head of the next
        assert parts[0].split(".")[-2].strip() in parts[1]


# --- retrieval and RAG governance -------------------------------------------
class TestGovernedRag:
    def test_acl_predicate_hides_restricted_chunks(self, settings):
        corpus = get_corpus(str(settings.corpus_dir), settings.embedding_dim)
        employee = acl_predicate(Principal.user("a", roles={"employee"}))
        hr = acl_predicate(Principal.user("b", roles={"employee", "hr"}))
        restricted = [c for c in corpus.chunks if c.doc_id == "HR-COMP-003"]
        assert restricted
        assert not any(employee(c) for c in restricted)
        assert all(hr(c) for c in restricted)

    def test_filtered_chunks_never_reach_the_ranking(self, settings, ctx):
        corpus = get_corpus(str(settings.corpus_dir), settings.embedding_dim)
        retriever = HybridRetriever(corpus)
        principal = Principal.user("a", roles={"employee"})
        result = retriever.retrieve("merit budget distribution guidance", principal, ctx)
        assert all("HR-COMP-003" not in h.chunk.id for h in result.hits)
        assert result.filtered_out > 0

    def test_lexical_retrieval_finds_an_exact_identifier(self, settings, ctx):
        corpus = get_corpus(str(settings.corpus_dir), settings.embedding_dim)
        retriever = HybridRetriever(corpus)
        result = retriever.retrieve(
            "S1 field failure response time", Principal.user("a", roles={"employee"}), ctx
        )
        assert any(h.chunk.doc_id == "OPS-WAR-045" for h in result.hits)

    def test_answer_is_grounded_and_cited(self, settings, employee):
        ctx = RunContext.build(employee, settings)
        response = build_rag_agent(ctx).answer(
            "What mileage rate applies to the first 10,000 business miles?", employee
        )
        assert not response.refused
        assert "45 pence" in response.answer
        assert response.citations and response.groundedness > 0.5
        assert not response.fabricated_citations

    def test_restricted_content_is_refused_for_the_wrong_role(self, settings):
        principal = Principal.user("a", tenant="northgate", roles={"employee"})
        ctx = RunContext.build(principal, settings)
        response = build_rag_agent(ctx).answer(
            "What is the Group merit budget for the 2026 cycle?", principal
        )
        assert response.refused
        assert "3.4" not in response.answer

    def test_same_question_answers_for_an_authorised_role(self, settings):
        principal = Principal.user("b", tenant="northgate", roles={"employee", "hr"})
        ctx = RunContext.build(principal, settings)
        response = build_rag_agent(ctx).answer(
            "What is the Group merit budget for the 2026 cycle?", principal
        )
        assert not response.refused and "3.4" in response.answer

    def test_fabricated_citations_are_stripped(self, settings, employee):
        ctx = RunContext.build(employee, settings)
        agent = build_rag_agent(ctx)
        hits = agent.retriever.retrieve("mileage", employee, ctx).hits
        real = hits[0].chunk.id
        verdict = agent._verify(
            "mileage rate",
            GroundedAnswer(
                answer=f"The rate is 45 pence [{real}] per HMRC [MADE-UP#9].",
                citations=[real, "MADE-UP#9"],
            ),
            hits,
        )
        assert verdict["fabricated"] == ["MADE-UP#9"]
        assert "MADE-UP#9" not in verdict["answer"]

    def test_question_coverage_separates_near_misses(self):
        high = question_coverage("price variance autonomous resolution limit",
                                 "autonomous resolution limits: price variance below GBP 500")
        low = question_coverage("price variance autonomous resolution limit",
                                "three-way match tolerances are 5 percent on price")
        assert high > low


# --- durable execution ------------------------------------------------------
class TestDurableExecution:
    def test_completed_steps_are_not_re_executed(self, store, ctx):
        calls: list[str] = []

        def one(sc: StepContext) -> dict:
            calls.append("one")
            return {"ok": True}

        def two(sc: StepContext) -> dict:
            calls.append("two")
            raise TransientStepError("flaky")

        workflow = Workflow(
            "t", [Step("one", one), Step("two", two, retry=RetryPolicy(attempts=1))], store
        )
        first = workflow.start({"x": 1}, ctx)
        assert first.status == "failed" and calls == ["one", "two"]

        calls.clear()
        workflow.steps[1] = Step("two", lambda sc: {"ok": True})
        second = workflow.resume(first.run_id, ctx)
        assert second.status == "succeeded"
        assert calls == []  # step one came from the checkpoint
        assert second.steps_skipped == ["one"]

    def test_suspend_and_resume_with_a_decision(self, store, ctx):
        def gate(sc: StepContext) -> dict:
            if not sc.resume_payload:
                raise Suspend("needs a human", {"amount": 900})
            return {"approved": sc.resume_payload["approved"]}

        workflow = Workflow("t", [Step("gate", gate)], store)
        suspended = workflow.start({}, ctx)
        assert suspended.status == "awaiting_approval"
        assert suspended.suspended_payload["amount"] == 900

        resumed = workflow.resume(suspended.run_id, ctx, {"approved": True})
        assert resumed.status == "succeeded"

    def test_transient_errors_retry_and_permanent_ones_do_not(self, store, ctx):
        attempts = {"n": 0}

        def flaky(sc: StepContext) -> dict:
            attempts["n"] += 1
            if attempts["n"] < 3:
                raise TransientStepError("503")
            return {"ok": True}

        workflow = Workflow(
            "t", [Step("flaky", flaky, retry=RetryPolicy(attempts=3, base_delay_s=0))], store
        )
        assert workflow.start({}, ctx).status == "succeeded"
        assert attempts["n"] == 3

        permanent = {"n": 0}

        def bad(sc: StepContext) -> dict:
            permanent["n"] += 1
            raise PermanentStepError("invalid input")

        wf2 = Workflow("t2", [Step("bad", bad, retry=RetryPolicy(attempts=3, base_delay_s=0))], store)
        assert wf2.start({}, ctx).status == "failed"
        assert permanent["n"] == 1  # not retried

    def test_idempotency_key_suppresses_a_duplicate_run(self, store, ctx):
        runs = {"n": 0}

        def once(sc: StepContext) -> dict:
            runs["n"] += 1
            return {"n": runs["n"]}

        workflow = Workflow("t", [Step("once", once)], store)
        a = workflow.start({"id": 1}, ctx, idempotency_key="k1")
        b = workflow.start({"id": 1}, ctx, idempotency_key="k1")
        assert a.run_id == b.run_id and runs["n"] == 1 and b.resumed


# --- AP workflow ------------------------------------------------------------
class TestApWorkflow:
    @pytest.mark.parametrize(
        "invoice_id,category,action",
        [
            ("INV-1001", "NONE", "auto_post"),
            ("INV-1002", "PRICE_VARIANCE", "auto_resolve"),
            ("INV-1003", "PRICE_VARIANCE", "require_approval"),
            ("INV-1004", "NO_PO", "require_approval"),
            ("INV-1005", "BANK_DETAIL_CHANGE", "escalate"),
            ("INV-1007", "DUPLICATE_SUSPECT", "require_approval"),
        ],
    )
    def test_control_decisions(self, store, settings, invoice_id, category, action):
        principal = Principal.user("ap", tenant="northgate", roles={"finance"})
        workflow = build_workflow(store, ErpStub())
        result = workflow.start(invoice_id, RunContext.build(principal, settings))
        assert result.state["classify_exception"]["category"] == category
        assert result.state["policy_decision"]["action"] == action

    def test_no_payment_without_approval(self, store, settings):
        principal = Principal.user("ap", tenant="northgate", roles={"finance"})
        erp = ErpStub()
        workflow = build_workflow(store, erp)
        result = workflow.start("INV-1005", RunContext.build(principal, settings))
        assert result.status == "awaiting_approval"
        assert erp.post_calls == 0

    def test_crash_then_resume_pays_exactly_once(self, store, settings):
        principal = Principal.user("ap", tenant="northgate", roles={"finance"})
        erp = ErpStub()
        crashed = build_workflow(store, erp, fail_after_step="post_to_erp").start(
            "INV-1002", RunContext.build(principal, settings)
        )
        assert crashed.status == "failed"
        assert erp.post_calls == 1
        recovered = build_workflow(store, erp).resume(
            crashed.run_id, RunContext.build(principal, settings), approved=True, approver="x"
        )
        assert recovered.status == "succeeded"
        assert erp.post_calls == 1  # the committed step was never re-run

    def test_segregation_of_duties(self, store, settings):
        principal = Principal.user("ap", tenant="northgate", roles={"finance"})
        workflow = build_workflow(store, ErpStub())
        suspended = workflow.start("INV-1006", RunContext.build(principal, settings))
        result = workflow.resume(
            suspended.run_id, RunContext.build(principal, settings),
            approved=True, approver="a.deniz",  # raised PO-5004
        )
        assert result.status == "failed" and "segregation of duties" in (result.error or "")

    def test_extraction_is_validated_against_the_document(self, store, settings, monkeypatch):
        """A hallucinated total must stop the run, not become a payment."""
        from aivc.llm import gateway as gateway_module

        principal = Principal.user("ap", tenant="northgate", roles={"finance"})
        original = gateway_module.LLMGateway.structured

        def lying_structured(self, system, user, schema, **kwargs):
            result = original(self, system, user, schema, **kwargs)
            result.total_ex_vat_gbp = 999_999.0
            return result

        monkeypatch.setattr(gateway_module.LLMGateway, "structured", lying_structured)
        result = build_workflow(store, ErpStub()).start(
            "INV-1001", RunContext.build(principal, settings)
        )
        assert result.status == "failed"
        assert "does not appear in the" in (result.error or "")


# --- supervisor -------------------------------------------------------------
class TestSupervisor:
    def _principal(self, scopes: set[str], roles: set[str] | None = None) -> Principal:
        return Principal.user(
            "u", tenant="northgate", roles=roles or {"employee", "finance"}, scopes=scopes
        )

    def test_routes_data_questions_to_the_analyst(self, settings):
        principal = self._principal({"warehouse:read"})
        response = build_supervisor(RunContext.build(principal, settings)).handle(
            "How many invoice exceptions do we have by category?"
        )
        assert response.route == ["data_analyst"]
        assert "PRICE_VARIANCE" in response.answer

    def test_routes_policy_questions_to_the_analyst(self, settings):
        principal = self._principal({"corpus:read"})
        response = build_supervisor(RunContext.build(principal, settings)).handle(
            "What is the expense approval threshold for a claim over GBP 2,000?"
        )
        assert response.route == ["policy_analyst"]
        assert "Finance Director" in response.answer

    def test_declines_out_of_scope(self, settings):
        principal = self._principal({"warehouse:read", "ap:read", "corpus:read"})
        response = build_supervisor(RunContext.build(principal, settings)).handle(
            "What was the share price at close yesterday?"
        )
        assert response.declined and response.route == []

    def test_specialist_cannot_exceed_the_callers_scopes(self, settings):
        principal = self._principal({"corpus:read"})  # no warehouse:read
        response = build_supervisor(RunContext.build(principal, settings)).handle(
            "How many invoice exceptions do we have by category?"
        )
        denied = {t for o in response.outcomes for t in o.denied_tools}
        assert denied == {"get_warehouse_schema", "run_sql"}
        assert "PRICE_VARIANCE" not in response.answer

    def test_specialists_only_see_their_own_tools(self, settings):
        from agents.supervisor.supervisor import SPECIALISTS

        assert "run_sql" not in SPECIALISTS["ap_operations"].tools
        assert "submit_ap_approval" not in SPECIALISTS["data_analyst"].tools
        # the AP specialist is not granted the approval scope even though it lists the tool
        assert "ap:approve" not in SPECIALISTS["ap_operations"].scopes
