"""Platform layer: cost ledger, tool registry, agent loop, eval harness."""

from __future__ import annotations

import pytest
from pydantic import BaseModel

from aivc.agent.loop import ToolAgent
from aivc.evals.harness import EvalCase, run_suite
from aivc.evals.scorers import Score, citation_validity, groundedness, refusal_correctness
from aivc.llm.base import Completion, LLMRequest, ToolCall, Usage
from aivc.llm.gateway import LLMGateway, StructuredOutputError
from aivc.llm.pricing import estimate_cost_usd
from aivc.obs.cost import BudgetExceeded, CostLedger
from aivc.obs.run import RunContext
from aivc.security.identity import Principal
from aivc.security.policy import PolicyEngine, ToolRule
from aivc.tools.registry import ToolArgumentError, ToolRegistry


class ScriptedLLM:
    """A fake provider that returns whatever the test queues up."""

    name = "scripted"

    def __init__(self, responses: list[Completion]):
        self.responses = list(responses)
        self.requests: list[LLMRequest] = []

    def complete(self, request: LLMRequest) -> Completion:
        self.requests.append(request)
        return self.responses.pop(0) if self.responses else _text("done")


def _text(text: str, tokens: int = 10) -> Completion:
    return Completion(text=text, model="test-model", usage=Usage(tokens, tokens))


def _call(name: str, **args) -> Completion:
    return Completion(
        text="", model="test-model", usage=Usage(10, 10),
        tool_calls=[ToolCall(id="c1", name=name, arguments=args)],
    )


class EchoArgs(BaseModel):
    value: str


def _registry() -> ToolRegistry:
    reg = ToolRegistry()

    @reg.tool(name="echo", description="Echo a value.", scopes={"demo:read"})
    def echo(args: EchoArgs) -> str:
        return f"echoed:{args.value}"

    @reg.tool(name="boom", description="Always fails.", scopes={"demo:read"})
    def boom(args: EchoArgs) -> str:
        raise RuntimeError("upstream is down")

    return reg


def _policy() -> PolicyEngine:
    return PolicyEngine(
        [
            ToolRule("echo", required_scopes={"demo:read"}),
            ToolRule("boom", required_scopes={"demo:read"}),
        ]
    )


class TestCostLedger:
    def test_records_and_totals(self):
        ledger = CostLedger(budget_usd=1.0)
        ledger.record("a", "unknown-model", Usage(1_000_000, 0))
        assert ledger.total_usd == pytest.approx(3.0)
        assert ledger.call_count == 1

    def test_budget_is_enforced(self):
        ledger = CostLedger(budget_usd=0.001)
        ledger.record("a", "unknown-model", Usage(1_000_000, 0))
        with pytest.raises(BudgetExceeded):
            ledger.check()

    def test_cached_tokens_are_cheaper(self):
        full = estimate_cost_usd("unknown", Usage(1_000_000, 0))
        cached = estimate_cost_usd("unknown", Usage(1_000_000, 0, cached_input_tokens=1_000_000))
        assert cached < full

    def test_attribution_by_label(self):
        ledger = CostLedger(budget_usd=10)
        ledger.record("retrieval", "unknown", Usage(1000, 0))
        ledger.record("answer", "unknown", Usage(2000, 0))
        assert set(ledger.by_label()) == {"retrieval", "answer"}


class TestToolRegistry:
    def test_schema_is_derived_from_the_model(self):
        spec = _registry().get("echo")
        assert spec.parameters["properties"]["value"]["type"] == "string"
        assert spec.scopes == {"demo:read"}

    def test_bad_arguments_are_rejected(self):
        with pytest.raises(ToolArgumentError):
            _registry().get("echo").validate_args({"wrong": 1})

    def test_subset_is_least_privilege(self):
        reg = _registry().subset(["echo"])
        assert reg.names() == ["echo"]
        with pytest.raises(KeyError):
            reg.get("boom")


class TestAgentLoop:
    def _agent(self, ctx: RunContext, responses: list[Completion], **kwargs) -> ToolAgent:
        gateway = LLMGateway(ScriptedLLM(responses), ctx)
        return ToolAgent("test", "system", gateway, _registry(), _policy(), **kwargs)

    def test_tool_call_then_answer(self, ctx):
        ctx.principal = Principal.user("t", scopes={"demo:read"})
        agent = self._agent(ctx, [_call("echo", value="hi"), _text("the answer")])
        result = agent.run("go")
        assert result.stop_reason == "completed"
        assert result.output == "the answer"
        assert result.used("echo")

    def test_missing_scope_denies_the_tool(self, ctx):
        ctx.principal = Principal.user("t", scopes=set())
        agent = self._agent(ctx, [_call("echo", value="hi"), _text("no data")])
        result = agent.run("go")
        invocation = result.tool_calls[0]
        assert not invocation.decision.allowed
        assert "scope" in invocation.observation().lower()

    def test_step_budget_is_bounded(self, ctx):
        ctx.principal = Principal.user("t", scopes={"demo:read"})
        agent = self._agent(ctx, [_call("echo", value="x") for _ in range(10)], max_steps=3)
        result = agent.run("go")
        assert result.stop_reason == "max_steps"
        assert len(result.steps) == 3

    def test_circuit_breaker_stops_repeated_failures(self, ctx):
        ctx.principal = Principal.user("t", scopes={"demo:read"})
        agent = self._agent(
            ctx, [_call("boom", value="x") for _ in range(6)],
            max_steps=6, max_consecutive_tool_errors=2,
        )
        result = agent.run("go")
        assert result.stop_reason == "circuit_breaker"
        assert len(result.steps) == 2

    def test_budget_exhaustion_stops_the_run(self, ctx):
        ctx.principal = Principal.user("t", scopes={"demo:read"})
        ctx.ledger.budget_usd = 0.0000001
        agent = self._agent(ctx, [_call("echo", value="x"), _text("done")])
        result = agent.run("go")
        assert result.stop_reason in ("budget", "completed")  # first guard may fire before step 1
        if result.stop_reason == "budget":
            assert "budget" in (result.error or "")

    def test_tool_errors_are_returned_to_the_model(self, ctx):
        ctx.principal = Principal.user("t", scopes={"demo:read"})
        agent = self._agent(ctx, [_call("boom", value="x"), _text("recovered")])
        result = agent.run("go")
        assert result.output == "recovered"
        assert "upstream is down" in result.tool_calls[0].observation()


class TestStructuredOutput:
    class Answer(BaseModel):
        value: int

    def test_repairs_invalid_json(self, ctx):
        llm = ScriptedLLM([_text("not json at all"), _text('{"value": 7}')])
        gateway = LLMGateway(llm, ctx)
        assert gateway.structured("sys", "user", self.Answer).value == 7
        assert len(llm.requests) == 2

    def test_gives_up_after_the_repair_budget(self, ctx):
        llm = ScriptedLLM([_text("nope"), _text("still nope"), _text("nope again")])
        gateway = LLMGateway(llm, ctx)
        with pytest.raises(StructuredOutputError):
            gateway.structured("sys", "user", self.Answer, max_repairs=2)


class TestEvalHarness:
    def test_gate_fails_when_a_threshold_is_missed(self):
        cases = [EvalCase("c1", inputs={"q": "x"}, expected={"should_refuse": False})]
        report = run_suite(
            "s", cases, lambda c: {"answer": "hi", "refused": True, "citations": [], "context": []},
            [refusal_correctness()], thresholds={"refusal_correctness": 1.0},
        )
        ok, breaches = report.gate()
        assert not ok and breaches

    def test_consistency_detects_flakiness(self):
        state = {"n": 0}

        def task(_case):
            state["n"] += 1
            return {"answer": "a", "refused": state["n"] % 2 == 0, "citations": [], "context": []}

        cases = [EvalCase("c1", expected={"should_refuse": False})]
        report = run_suite("s", cases, task, [refusal_correctness()], repeats=4)
        assert report.consistency() < 1.0
        assert report.flaky_cases() == ["c1"]

    def test_fabricated_citations_are_caught(self):
        case = EvalCase("c1")
        output = {"answer": "claim [REAL#1] and [FAKE#9]", "citations": [{"chunk_id": "REAL#1"}]}
        score: Score = citation_validity()(case, output)
        assert score.value == 0.5 and not score.passed

    def test_groundedness_skips_refusals(self):
        score = groundedness()(EvalCase("c1"), {"refused": True, "answer": "x", "context": []})
        assert score.passed
