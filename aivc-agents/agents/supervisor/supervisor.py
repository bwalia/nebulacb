"""Agent 3 -- Supervisor with specialist sub-agents.

The demo question: *"one assistant, many back-end systems -- how do you keep it coherent and
keep it safe?"*

Design decisions worth defending in a room:

  * **Routing is structured and cheap.** A small typed decision ("which specialists, what
    sub-task each") rather than a free-form conversation between agents. Agent-to-agent chat
    is where token budgets and debuggability both go to die.
  * **Specialists are narrower than the supervisor.** Each gets its own machine identity,
    its own scope set and only the tools it needs. The data analyst physically cannot touch
    the AP approval tool; the AP specialist cannot query the warehouse. Least privilege is
    enforced by the policy engine, not by prompt wording.
  * **The user's own permissions still bind.** A specialist's effective identity is the
    intersection of the caller's roles and the agent's scopes, so delegation can never be a
    privilege-escalation path -- the classic confused-deputy hole in multi-agent designs.
  * **Declining is a supported outcome.** Out-of-scope questions get a short honest answer,
    not a hallucinated one from whichever specialist scored highest.
  * **One budget for the whole fan-out.** The run's cost ledger is shared, so a supervisor
    that delegates three times cannot spend three times the cap.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any, Literal

from pydantic import BaseModel, Field

from aivc.agent.loop import AgentResult, ToolAgent
from aivc.llm.gateway import LLMGateway
from aivc.obs.run import RunContext
from aivc.security.identity import Principal
from aivc.store.checkpoint import CheckpointStore

from .tools import build_policy, build_tools
from .warehouse import Warehouse

ROUTE_MARKER = "AIVC_SUPERVISOR_ROUTE_V1"
SYNTH_MARKER = "AIVC_SUPERVISOR_SYNTH_V1"
DATA_MARKER = "AIVC_DATA_ANALYST_V1"
OPS_MARKER = "AIVC_AP_OPS_V1"

SpecialistName = Literal["policy_analyst", "data_analyst", "ap_operations"]


@dataclass(frozen=True)
class SpecialistSpec:
    name: str
    purpose: str
    tools: tuple[str, ...]
    scopes: frozenset[str]
    system_prompt: str


SPECIALISTS: dict[str, SpecialistSpec] = {
    "policy_analyst": SpecialistSpec(
        name="policy_analyst",
        purpose=(
            "Answers questions about company policy, controls and procedures from the indexed "
            "policy corpus, with citations. Use for 'what is our policy on...', thresholds, "
            "approval rules, obligations."
        ),
        tools=(),
        scopes=frozenset({"corpus:read"}),
        system_prompt="",  # delegates to the governed RAG agent
    ),
    "data_analyst": SpecialistSpec(
        name="data_analyst",
        purpose=(
            "Answers quantitative questions from the analytics warehouse: counts, totals, "
            "breakdowns and trends over invoices, exceptions and suppliers."
        ),
        tools=("get_warehouse_schema", "run_sql"),
        scopes=frozenset({"warehouse:read"}),
        system_prompt=f"""{DATA_MARKER}
You answer quantitative questions using the analytics warehouse.

Method, every time:
1. Call get_warehouse_schema first. Never guess a column name.
2. Write ONE read-only SELECT that answers the question. Aggregate in SQL, not in your head.
3. Read the returned rows and answer in at most three sentences, quoting the figures exactly
   and naming the columns they came from.

Constraints: SELECT only; a single statement; no DDL or DML. If the schema cannot answer the
question, say so plainly instead of approximating.""",
    ),
    "ap_operations": SpecialistSpec(
        name="ap_operations",
        purpose=(
            "Reports on the state of accounts-payable workflow runs: what is awaiting "
            "approval, why a specific invoice stopped, what the decision trail says."
        ),
        tools=("list_ap_exceptions", "get_ap_run", "submit_ap_approval"),
        scopes=frozenset({"ap:read"}),  # deliberately NOT ap:approve
        system_prompt=f"""{OPS_MARKER}
You report on accounts-payable workflow runs.

Use list_ap_exceptions to see what is waiting, and get_ap_run for the decision trail of a
specific run. Summarise for a finance manager: what is blocked, why, and who needs to act.

You may not approve anything. If asked to approve, say that approval requires a named human
with the finance approval scope and stop.""",
    ),
}

ROUTE_SYSTEM = f"""{ROUTE_MARKER}
You are the router for an internal assistant at Northgate Industrial Group.

Specialists:
{chr(10).join(f"- {s.name}: {s.purpose}" for s in SPECIALISTS.values())}

Choose the minimum set of specialists that can answer the question -- usually one, at most
two when the question genuinely has a policy part and a data part. Give each a self-contained
sub-task; the specialist cannot see the original question.

If no specialist fits, return an empty delegations list. Declining is correct and expected.

Return JSON: {{"delegations": [{{"specialist": name, "task": str}}], "reasoning": str,
"confidence": 0..1}}
"""

SYNTH_SYSTEM = f"""{SYNTH_MARKER}
Combine the specialist findings into one answer for the person who asked.

Rules: use only what the specialists returned; keep every figure and citation exactly as
given; be brief; if the specialists disagree, say so rather than picking one.
"""


class Delegation(BaseModel):
    specialist: SpecialistName
    task: str


class Route(BaseModel):
    delegations: list[Delegation] = Field(default_factory=list, max_length=2)
    reasoning: str = ""
    confidence: float = Field(default=0.5, ge=0.0, le=1.0)


@dataclass
class SpecialistOutcome:
    specialist: str
    task: str
    answer: str
    stop_reason: str
    tools_used: list[str] = field(default_factory=list)
    denied_tools: list[str] = field(default_factory=list)
    citations: list[dict[str, Any]] = field(default_factory=list)
    error: str | None = None


@dataclass
class SupervisorResponse:
    question: str
    answer: str
    route: list[str]
    routing_reason: str
    declined: bool
    outcomes: list[SpecialistOutcome]
    cost_usd: float
    run_id: str
    trace_id: str
    latency_ms: float = 0.0

    def to_dict(self) -> dict[str, Any]:
        return {
            "question": self.question,
            "answer": self.answer,
            "route": self.route,
            "routing_reason": self.routing_reason,
            "declined": self.declined,
            "cost_usd": self.cost_usd,
            "run_id": self.run_id,
            "trace_id": self.trace_id,
            "latency_ms": self.latency_ms,
            "outcomes": [
                {
                    "specialist": o.specialist,
                    "task": o.task,
                    "answer": o.answer,
                    "stop_reason": o.stop_reason,
                    "tools_used": o.tools_used,
                    "denied_tools": o.denied_tools,
                    "citations": o.citations,
                    "error": o.error,
                }
                for o in self.outcomes
            ],
        }


DECLINE_TEXT = (
    "That is outside what this assistant covers. It can answer questions about company "
    "policy, about figures in the AP analytics warehouse, and about the status of invoice "
    "exception runs."
)


class SupervisorAgent:
    def __init__(
        self,
        ctx: RunContext,
        *,
        warehouse: Warehouse | None = None,
        store: CheckpointStore | None = None,
        rag_agent: Any | None = None,
    ):
        self.ctx = ctx
        self.gateway = LLMGateway.for_run(ctx)
        self.warehouse = warehouse or Warehouse()
        self.store = store or CheckpointStore(ctx.settings.checkpoint_db)
        self.tools = build_tools(self.warehouse, self.store)
        self.policy = build_policy()
        self._rag_agent = rag_agent

    # -- orchestration ------------------------------------------------------
    def handle(self, question: str) -> SupervisorResponse:
        with self.ctx.tracer.span(
            "agent.supervisor", kind="agent", question=question[:300],
            principal=self.ctx.principal.subject,
        ) as span:
            route = self.gateway.structured(
                system=ROUTE_SYSTEM, user=question, schema=Route, label="supervisor.route"
            )
            span.set(
                route=[d.specialist for d in route.delegations],
                routing_confidence=route.confidence,
            )

            if not route.delegations:
                span.set(outcome="declined")
                return self._response(question, DECLINE_TEXT, route, [], declined=True)

            outcomes = [self._delegate(d) for d in route.delegations]

            if len(outcomes) == 1 and outcomes[0].error is None:
                answer = outcomes[0].answer
            else:
                findings = "\n\n".join(
                    f"### {o.specialist}\nTask: {o.task}\nFinding: {o.answer or o.error}"
                    for o in outcomes
                )
                answer = self.gateway.ask(
                    SYNTH_SYSTEM,
                    f"QUESTION: {question}\n\nSPECIALIST FINDINGS:\n\n{findings}",
                    label="supervisor.synthesise",
                ).text

            span.set(outcome="answered", specialists=len(outcomes))
            return self._response(question, answer, route, outcomes, declined=False)

    def _delegate(self, delegation: Delegation) -> SpecialistOutcome:
        spec = SPECIALISTS[delegation.specialist]
        with self.ctx.tracer.span(
            f"delegate.{spec.name}", kind="agent", task=delegation.task[:200],
            scopes=sorted(spec.scopes),
        ) as span:
            try:
                if spec.name == "policy_analyst":
                    outcome = self._run_policy_analyst(delegation.task)
                else:
                    outcome = self._run_tool_specialist(spec, delegation.task)
            except Exception as exc:
                span.status = "error"
                return SpecialistOutcome(
                    spec.name, delegation.task, "", "error", error=f"{type(exc).__name__}: {exc}"
                )
            span.set(
                stop_reason=outcome.stop_reason,
                tools_used=outcome.tools_used,
                denied_tools=outcome.denied_tools,
            )
            return outcome

    def _run_policy_analyst(self, task: str) -> SpecialistOutcome:
        agent = self._rag_agent or self._build_rag_agent()
        response = agent.answer(task, self.ctx.principal)
        return SpecialistOutcome(
            specialist="policy_analyst",
            task=task,
            answer=response.answer,
            stop_reason="refused" if response.refused else "completed",
            citations=response.citations,
        )

    def _run_tool_specialist(self, spec: SpecialistSpec, task: str) -> SpecialistOutcome:
        agent = ToolAgent(
            name=spec.name,
            system_prompt=spec.system_prompt,
            gateway=self.gateway,
            tools=self.tools.subset(spec.tools),
            policy=self.policy,
            # The specialist's machine identity. Intersected with the caller's identity in
            # ToolAgent.effective_principal, so it can only ever narrow access.
            agent_principal=Principal.agent(
                f"agent:{spec.name}", self.ctx.principal.tenant, set(spec.scopes)
            ),
            max_steps=5,
        )
        result: AgentResult = agent.run(task)
        return SpecialistOutcome(
            specialist=spec.name,
            task=task,
            answer=result.output,
            stop_reason=result.stop_reason,
            tools_used=sorted({i.name for i in result.tool_calls if i.ok}),
            denied_tools=sorted({i.name for i in result.tool_calls if not i.decision.allowed}),
            error=result.error,
        )

    def _build_rag_agent(self) -> Any:
        from agents.governed_rag import build_agent  # noqa: PLC0415  (avoids import cycle)

        return build_agent(self.ctx)

    def _response(
        self,
        question: str,
        answer: str,
        route: Route,
        outcomes: list[SpecialistOutcome],
        *,
        declined: bool,
    ) -> SupervisorResponse:
        return SupervisorResponse(
            question=question,
            answer=answer,
            route=[o.specialist for o in outcomes],
            routing_reason=route.reasoning,
            declined=declined,
            outcomes=outcomes,
            cost_usd=round(self.ctx.ledger.total_usd, 6),
            run_id=self.ctx.run_id,
            trace_id=self.ctx.tracer.trace_id,
        )
