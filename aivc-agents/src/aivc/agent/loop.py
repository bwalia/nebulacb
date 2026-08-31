"""The agent loop.

Framework-light on purpose (see ADR-0001). Roughly 150 lines of explicit control flow that
a client's own engineers can read on day one of handover, with the production concerns that
matter made visible rather than hidden behind a graph DSL:

  * a hard step ceiling, so a confused model cannot loop forever
  * budget and deadline checks between every step
  * deny-by-default authorisation on every tool call
  * tool failures fed back to the model as observations, but with a repeat-failure circuit
    breaker so it cannot thrash on the same broken call
  * every step traced with inputs, outputs, latency and cost
"""

from __future__ import annotations

import json
import time
from concurrent.futures import ThreadPoolExecutor, TimeoutError as FutureTimeout
from dataclasses import dataclass, field
from typing import Any, Callable

from ..llm.base import Completion, LLMRequest, Message, ToolCall
from ..llm.gateway import LLMGateway
from ..obs.cost import BudgetExceeded
from ..obs.run import DeadlineExceeded, RunContext
from ..security.identity import Principal
from ..security.policy import Decision, PolicyEngine
from ..tools.registry import ToolArgumentError, ToolRegistry

_EXECUTOR = ThreadPoolExecutor(max_workers=8, thread_name_prefix="aivc-tool")


@dataclass
class ToolInvocation:
    name: str
    arguments: dict[str, Any]
    decision: Decision
    result: Any = None
    error: str | None = None
    duration_ms: float = 0.0

    @property
    def ok(self) -> bool:
        return self.error is None and self.decision.allowed

    def observation(self) -> str:
        if not self.decision.allowed:
            return f"DENIED: {self.decision.reason}"
        if self.error:
            return f"ERROR: {self.error}"
        return _stringify(self.result)


@dataclass
class Step:
    index: int
    text: str
    invocations: list[ToolInvocation] = field(default_factory=list)


@dataclass
class AgentResult:
    output: str
    steps: list[Step]
    stop_reason: str  # completed | max_steps | budget | deadline | circuit_breaker | error
    messages: list[Message] = field(default_factory=list)
    error: str | None = None
    metadata: dict[str, Any] = field(default_factory=dict)

    @property
    def tool_calls(self) -> list[ToolInvocation]:
        return [inv for s in self.steps for inv in s.invocations]

    def used(self, tool: str) -> bool:
        return any(inv.name == tool and inv.ok for inv in self.tool_calls)


class ToolAgent:
    def __init__(
        self,
        name: str,
        system_prompt: str,
        gateway: LLMGateway,
        tools: ToolRegistry,
        policy: PolicyEngine,
        *,
        agent_principal: Principal | None = None,
        max_steps: int | None = None,
        max_consecutive_tool_errors: int = 3,
        on_step: Callable[[Step], None] | None = None,
    ):
        self.name = name
        self.system_prompt = system_prompt
        self.gateway = gateway
        self.tools = tools
        self.policy = policy
        self.agent_principal = agent_principal
        self.max_steps = max_steps or gateway.ctx.settings.run_step_budget
        self.max_consecutive_tool_errors = max_consecutive_tool_errors
        self.on_step = on_step

    @property
    def ctx(self) -> RunContext:
        return self.gateway.ctx

    def effective_principal(self) -> Principal:
        """Caller identity narrowed by the agent's own scope set. Never wider than either."""
        if self.agent_principal is None:
            return self.ctx.principal
        return self.ctx.principal.intersect(self.agent_principal)

    def run(self, user_input: str, history: list[Message] | None = None) -> AgentResult:
        messages: list[Message] = list(history or []) + [Message("user", user_input)]
        steps: list[Step] = []
        principal = self.effective_principal()
        consecutive_errors = 0

        with self.ctx.tracer.span(
            f"agent.{self.name}", kind="agent", principal=principal.subject, tools=self.tools.names()
        ) as agent_span:
            for i in range(self.max_steps):
                try:
                    self.ctx.checkpoint_guards()
                except BudgetExceeded as exc:
                    return self._finish(steps, messages, "budget", agent_span, error=str(exc))
                except DeadlineExceeded as exc:
                    return self._finish(steps, messages, "deadline", agent_span, error=str(exc))

                completion = self.gateway.complete(
                    LLMRequest(
                        messages=messages,
                        system=self.system_prompt,
                        tools=self.tools.schemas(),
                        temperature=self.ctx.settings.temperature,
                        max_tokens=self.ctx.settings.max_output_tokens,
                    ),
                    label=f"{self.name}.step{i}",
                )
                step = Step(index=i, text=completion.text)

                if not completion.tool_calls:
                    steps.append(step)
                    if self.on_step:
                        self.on_step(step)
                    messages.append(Message("assistant", completion.text))
                    return self._finish(steps, messages, "completed", agent_span)

                messages.append(
                    Message("assistant", completion.text, tool_calls=completion.tool_calls)
                )
                for call in completion.tool_calls:
                    inv = self._invoke(call, principal)
                    step.invocations.append(inv)
                    messages.append(
                        Message("tool", inv.observation(), tool_call_id=call.id, name=call.name)
                    )

                steps.append(step)
                if self.on_step:
                    self.on_step(step)

                if all(not inv.ok for inv in step.invocations):
                    consecutive_errors += 1
                    if consecutive_errors >= self.max_consecutive_tool_errors:
                        return self._finish(
                            steps,
                            messages,
                            "circuit_breaker",
                            agent_span,
                            error=f"{consecutive_errors} consecutive failing tool steps",
                        )
                else:
                    consecutive_errors = 0

            return self._finish(steps, messages, "max_steps", agent_span)

    # -- internals ----------------------------------------------------------
    def _invoke(self, call: ToolCall, principal: Principal) -> ToolInvocation:
        with self.ctx.tracer.span(f"tool.{call.name}", kind="tool", args=call.arguments) as span:
            try:
                spec = self.tools.get(call.name)
            except KeyError as exc:
                span.set(outcome="unknown_tool")
                return ToolInvocation(call.name, call.arguments, Decision(True, "ok"), error=str(exc))

            decision = self.policy.authorize(principal, call.name, call.arguments, self.ctx.run_id)
            span.set(
                allowed=decision.allowed,
                policy_rule=decision.rule,
                policy_reason=decision.reason,
                side_effect=spec.side_effect,
            )
            if not decision.allowed:
                return ToolInvocation(call.name, call.arguments, decision)
            self.policy.note_call(call.name, self.ctx.run_id)

            start = time.perf_counter()
            try:
                args = spec.validate_args(call.arguments)
                timeout = spec.timeout_s or self.ctx.settings.tool_timeout_s
                future = _EXECUTOR.submit(spec.fn, spec.args_model(**args) if spec.args_model else args)
                result = future.result(timeout=timeout)
                inv = ToolInvocation(call.name, args, decision, result=result)
            except ToolArgumentError as exc:
                inv = ToolInvocation(call.name, call.arguments, decision, error=str(exc))
            except FutureTimeout:
                inv = ToolInvocation(
                    call.name, call.arguments, decision, error=f"tool timed out after {timeout}s"
                )
            except Exception as exc:
                inv = ToolInvocation(
                    call.name, call.arguments, decision, error=f"{type(exc).__name__}: {exc}"
                )
            inv.duration_ms = (time.perf_counter() - start) * 1000
            span.set(
                duration_ms=round(inv.duration_ms, 2),
                error=inv.error,
                result_preview=_stringify(inv.result)[:400] if inv.ok else None,
            )
            if inv.error:
                span.status = "error"
            return inv

    def _finish(
        self,
        steps: list[Step],
        messages: list[Message],
        stop_reason: str,
        span: Any,
        error: str | None = None,
    ) -> AgentResult:
        output = steps[-1].text if steps else ""
        span.set(
            stop_reason=stop_reason,
            steps=len(steps),
            tool_calls=sum(len(s.invocations) for s in steps),
            cost_usd=round(self.ctx.ledger.total_usd, 6),
        )
        if error:
            span.status = "error"
            span.error = error
        return AgentResult(output, steps, stop_reason, messages, error)


def _stringify(value: Any) -> str:
    if isinstance(value, str):
        return value
    try:
        return json.dumps(value, default=str, ensure_ascii=False)
    except (TypeError, ValueError):
        return str(value)


def completion_text(completion: Completion) -> str:
    return completion.text.strip()
