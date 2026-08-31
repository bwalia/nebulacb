# ADR-0001 — Build the agent loop directly rather than on an orchestration framework

**Status:** accepted · **Date:** 2026-08 · **Applies to:** POC and first production deployment
of an engagement

## Context

Every engagement starts with the same choice: LangGraph, PydanticAI, CrewAI, Temporal +
an SDK, or plain Python. The decision is usually made on the consultant's familiarity, which
is the wrong criterion, because the code outlives the consultant by design — a 3–12 week
engagement ends with a handover to a team that has to own what is left behind.

The constraints that actually matter here:

- The client's engineers must be able to read and change the agent within days, not weeks.
- The interesting behaviour is in the guardrails — step ceilings, budget enforcement, tool
  authorisation, circuit breakers. Those must be visible in review.
- We deploy across many companies with different platform standards; a heavyweight runtime
  dependency is a negotiation with each one's platform team.
- The POC has to run with no network egress and no vendor account.

## Decision

Implement the tool-calling loop and the durable workflow engine directly (`aivc/agent/`),
against a provider-neutral `LLMClient` interface. Use libraries for what they are genuinely
good at — pydantic for schemas and validation, FastAPI for HTTP, numpy for vector maths —
and not for control flow.

Concretely, the loop is roughly 150 lines and makes these explicit rather than inherited:

- a hard step ceiling
- budget and deadline checks between every step
- deny-by-default authorisation on every tool call, with the decision traced
- tool failures returned to the model as observations, behind a repeat-failure circuit breaker
- a span per step with inputs, outputs, latency, tokens and cost

## Consequences

**Good.** No framework upgrade treadmill mid-engagement. Handover is reading one file, not
learning a DSL. Debugging is a stack trace rather than a graph visualisation. The guardrails
are auditable because they are in the code path a reviewer already has open. The behaviour is
identical across the four provider adapters.

**Bad.** We hand-rolled things frameworks give away: streaming, parallel tool execution,
sub-graph composition, retries as a declarative policy, human-in-the-loop primitives, and a
UI for inspecting runs. `aivc/agent/durable.py` is a deliberately small subset of what
Temporal does properly — no timers, no signals, no cross-service orchestration, no worker
fleet management.

**Mitigated by.** The `LLMClient` boundary means a framework can be adopted *underneath* an
agent later without changing the agent's contract. `ToolRegistry` emits standard JSON tool
schemas, so tools port to any framework unchanged.

## When to revisit

Adopt a framework when any of these becomes true — this is the trigger list, not a vague
"if it gets complex":

| Signal | Adopt |
|---|---|
| Workflows need durable timers, external signals, or cross-service sagas | Temporal (or the client's existing workflow engine) |
| More than ~10 concurrent long-running workflow shapes, or a worker fleet to manage | Temporal |
| Genuine graph topology — branches, joins, cycles with shared state across many nodes | LangGraph |
| The client's platform team has already standardised on one | theirs; the argument is not worth the goodwill |
| We need streaming token output through several agent hops | a framework with first-class streaming |

## Alternatives considered

**LangGraph.** Good durable-execution and checkpointing story, and a real answer for graph
topologies. Rejected for the POC because the state-machine abstraction obscures the guardrails
we most want a reviewer and a client engineer to see, and the dependency surface is a
conversation with every platform team.

**PydanticAI.** Pleasant typed-agent ergonomics, close to how we already use pydantic.
Rejected because it solves the part that was never hard here (structured output — 60 lines in
`gateway.structured`) and not the parts that were (policy, budget, durability).

**Temporal.** The correct answer for the workflow agent at production scale, and the stated
migration target in ADR-0003. Rejected for a POC because standing up a Temporal cluster is
week one of an engagement spent on infrastructure rather than on a demonstrable business
outcome.
