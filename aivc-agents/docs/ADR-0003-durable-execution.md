# ADR-0003 — A small durable-execution engine for POC workflows, with a stated exit to Temporal

**Status:** accepted · **Date:** 2026-08

## Context

The AP workflow calls an LLM, reconciles against an ERP, may wait hours or days for a human
approval, and then moves money. Three properties are non-negotiable before it touches a real
ledger:

1. A crash, deploy or pod eviction costs one step, not the run.
2. A step with an external side effect executes exactly once, even across retries and resumes.
3. Waiting for a human does not mean holding a process open.

An in-memory `for` loop over steps gives none of these. Temporal gives all three and much
more, at the cost of a cluster, workers, a new SDK and a mental model — roughly a week of an
engagement spent on infrastructure before anything is demonstrable.

## Decision

Implement a minimal durable engine (`aivc/agent/durable.py`) over a checkpoint store
(`aivc/store/checkpoint.py`), with SQLite in WAL mode and `synchronous=FULL` — a checkpoint
that is not durable is a lie.

The properties and how each is obtained:

| Property | Mechanism |
|---|---|
| Crash safety | each step's output is committed before the next starts; resume replays state from checkpoints |
| Exactly-once side effects | a succeeded step is never re-executed; the external call carries a deterministic idempotency key derived from `(run_id, business key)` |
| Duplicate event suppression | a run-level idempotency key (`invoice:INV-1002`); a redelivered queue message returns the original outcome instead of paying twice |
| Human-in-the-loop | a step raises `Suspend`; the run persists as `awaiting_approval` and the process is free to exit; `resume(run_id, decision)` continues |
| Bounded retries | per-step policy, exponential backoff with jitter, and a permanent/transient error split so a validation failure fails fast and a 503 does not |
| Operator recovery | a failed run is resumable after a fix; completed steps are skipped |
| Auditability | every transition is an event row with a timestamp; the final step writes an audit record naming the decision, the clause, the evidence and the decider |

Unclassified exceptions are treated as **permanent**, not transient. Retrying an unknown error
against a non-idempotent step is how duplicate payments happen; a step that wants a retry must
say so by raising `TransientStepError`.

## Consequences

**Good.** Zero infrastructure — the demo runs anywhere and CI needs no services. The whole
engine is ~250 readable lines, so the client's team can reason about failure modes on day one.
The schema is portable SQL; moving to Postgres is a connection change.

**Bad, and named rather than discovered later.**

- No durable timers. "Escalate if not approved within 48 hours" needs an external scheduler.
- No signals or queries into a running workflow beyond `resume`.
- No cross-service orchestration or compensation (sagas).
- No worker fleet, no visibility UI, no built-in rate limiting across runs.
- Concurrency is bounded by SQLite. Fine for hundreds of runs a day; not for thousands
  concurrently.
- Step outputs are JSON-serialised into the checkpoint. Large payloads belong in object
  storage with a reference in the checkpoint, not inline.

## When to migrate

Adopt Temporal (or the client's existing workflow engine) when any of these is true:

- the workflow needs timers, escalation on elapsed time, or signals from other systems
- more than a few hundred concurrent runs, or a worker fleet worth managing
- the workflow spans services and needs compensation on failure
- operations want a UI to inspect, retry and terminate runs without an engineer
- the client already runs Temporal, Step Functions, Camunda or similar — use theirs

The migration is contained: `Step` functions are already pure `(StepContext) -> output`, which
is the shape of a Temporal activity. The engine is replaced; the step bodies, the domain rules
and the eval suite are not.

## Alternatives considered

**Temporal now.** The right destination, wrong week. Revisit at the first production hardening
milestone, or immediately if the client already runs it.

**Celery with a result backend.** Retries and a queue, but no durable state machine, no
resume-from-step, and no human-in-the-loop primitive. We would end up building this engine on
top of it anyway.

**LangGraph checkpointer.** Real durable execution, and a good fit if the graph topology were
the hard part. Here the hard parts are idempotency and audit evidence, which are ours to
implement either way — and ADR-0001 already declines the framework dependency for the POC.

**Event sourcing onto Kafka.** Correct at scale and appropriate for a company that already
runs it. Disproportionate for a first deployment, and it moves the audit problem rather than
solving it.
