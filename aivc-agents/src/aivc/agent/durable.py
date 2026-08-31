"""Durable execution engine.

The properties a long-running business workflow needs, and how each is obtained here:

  crash safety      every step's output is committed before the next step starts; resume
                    replays from the checkpoint rather than from the beginning
  exactly-once side effects
                    a step marked non-idempotent is never re-executed after it succeeded,
                    and its external call carries a deterministic idempotency key
  human-in-the-loop a step raises Suspend; the run is persisted as awaiting_approval and the
                    process is free to exit. Resuming is a fresh call with the decision.
  bounded retries   per-step retry policy with exponential backoff and a permanent/transient
                    error split, so a validation failure fails fast and a 503 does not
  auditability      every transition is an event row with a timestamp

This is a deliberately small subset of what Temporal or LangGraph's checkpointer give you.
ADR-0003 covers when to stop using it and adopt one of them -- roughly, when you need timers,
signals, cross-service orchestration, or more than a few thousand concurrent runs.
"""

from __future__ import annotations

import hashlib
import random
import time
from dataclasses import dataclass, field
from typing import Any, Callable, Iterable

from ..obs.run import RunContext
from ..store.checkpoint import CheckpointStore, RunRecord


class Suspend(Exception):
    """Raised by a step to pause the run pending an external decision."""

    def __init__(self, reason: str, payload: dict[str, Any] | None = None):
        super().__init__(reason)
        self.reason = reason
        self.payload = payload or {}


class PermanentStepError(Exception):
    """Do not retry: bad input, failed validation, policy denial."""


class TransientStepError(Exception):
    """Retry: timeout, 5xx, lock contention."""


@dataclass
class RetryPolicy:
    attempts: int = 3
    base_delay_s: float = 0.2
    max_delay_s: float = 5.0
    jitter: bool = True

    def delay(self, attempt: int) -> float:
        d = min(self.base_delay_s * (2 ** (attempt - 1)), self.max_delay_s)
        return d * (random.random() if self.jitter else 1.0)


StepFn = Callable[["StepContext"], Any]


@dataclass
class Step:
    name: str
    fn: StepFn
    idempotent: bool = True
    retry: RetryPolicy = field(default_factory=RetryPolicy)
    description: str = ""


@dataclass
class StepContext:
    """What a step sees: the run, its input, prior step outputs, and the run context."""

    run: RunRecord
    state: dict[str, Any]
    ctx: RunContext
    store: CheckpointStore
    attempt: int = 1
    resume_payload: dict[str, Any] = field(default_factory=dict)

    def output_of(self, step_name: str) -> Any:
        return self.state.get(step_name)

    def idempotency_key(self, *parts: Any) -> str:
        """Deterministic key for an external write, stable across retries and resumes."""
        raw = "|".join([self.run.run_id, *[str(p) for p in parts]])
        return hashlib.sha256(raw.encode()).hexdigest()[:32]

    def emit(self, kind: str, payload: Any = None) -> None:
        self.store.append_event(self.run.run_id, kind, payload)


@dataclass
class WorkflowResult:
    run_id: str
    status: str  # succeeded | awaiting_approval | failed
    state: dict[str, Any]
    output: Any = None
    error: str | None = None
    suspended_reason: str | None = None
    suspended_payload: dict[str, Any] = field(default_factory=dict)
    steps_executed: list[str] = field(default_factory=list)
    steps_skipped: list[str] = field(default_factory=list)
    resumed: bool = False

    @property
    def completed(self) -> bool:
        return self.status == "succeeded"

    def to_dict(self) -> dict[str, Any]:
        return {
            "run_id": self.run_id,
            "status": self.status,
            "output": self.output,
            "error": self.error,
            "suspended_reason": self.suspended_reason,
            "suspended_payload": self.suspended_payload,
            "steps_executed": self.steps_executed,
            "steps_skipped": self.steps_skipped,
            "resumed": self.resumed,
        }


class SimulatedCrash(RuntimeError):
    """Raised by the `crash_after` test hook only. Never in a real run."""


class Workflow:
    def __init__(
        self,
        name: str,
        steps: Iterable[Step],
        store: CheckpointStore,
        *,
        crash_after: str | None = None,
    ):
        self.name = name
        self.steps = list(steps)
        self.store = store
        # Demo/test hook: kill the process immediately *after* the named step commits its
        # checkpoint. That is the interesting failure -- the work is done and durable, and
        # resume must not redo it. Crashing before the commit only proves retry works.
        self.crash_after = crash_after

    def start(
        self,
        payload: dict[str, Any],
        ctx: RunContext,
        *,
        idempotency_key: str | None = None,
    ) -> WorkflowResult:
        run = self.store.start_run(
            self.name,
            payload,
            idempotency_key=idempotency_key,
            principal=ctx.principal.subject,
            tenant=ctx.principal.tenant,
        )
        if run.resumed and run.status in ("succeeded", "failed"):
            # Replay protection: the same business event arriving twice returns the original
            # outcome instead of re-running the side effects.
            self.store.append_event(run.run_id, "duplicate_start_ignored", {"status": run.status})
            return WorkflowResult(
                run.run_id, run.status, self._state(run.run_id), run.output, run.error, resumed=True
            )
        self.store.append_event(run.run_id, "started", {"workflow": self.name})
        return self._execute(run, ctx)

    def resume(
        self, run_id: str, ctx: RunContext, payload: dict[str, Any] | None = None
    ) -> WorkflowResult:
        run = self.store.get_run(run_id)
        if run is None:
            raise KeyError(f"unknown run {run_id}")
        if run.status == "succeeded":
            return WorkflowResult(run_id, run.status, self._state(run_id), run.output, run.error)
        # A failed run is resumable: completed steps are skipped and execution restarts at
        # the first step without a successful checkpoint. This is the operator's recovery
        # path after a bug fix or an upstream outage.
        self.store.append_event(run_id, "resumed", payload)
        self.store.update_run(run_id, status="running")
        run.status = "running"
        return self._execute(run, ctx, resume_payload=payload or {})

    # -- internals ----------------------------------------------------------
    def _execute(
        self, run: RunRecord, ctx: RunContext, resume_payload: dict[str, Any] | None = None
    ) -> WorkflowResult:
        state = self._state(run.run_id)
        executed: list[str] = []
        skipped: list[str] = []
        resume_payload = resume_payload or {}

        with ctx.tracer.span(
            f"workflow.{self.name}", kind="workflow_step", run_id=run.run_id
        ) as wf_span:
            for step in self.steps:
                record = self.store.get_step(run.run_id, step.name)
                if record and record.status == "succeeded":
                    state[step.name] = record.output
                    skipped.append(step.name)
                    continue

                step_ctx = StepContext(
                    run=run, state=state, ctx=ctx, store=self.store, resume_payload=resume_payload
                )
                try:
                    output = self._run_step(step, step_ctx, ctx)
                except Suspend as suspend:
                    self.store.record_step(run.run_id, step.name, "suspended", error=suspend.reason)
                    self.store.update_run(
                        run.run_id, status="awaiting_approval", cursor_step=step.name
                    )
                    self.store.append_event(
                        run.run_id, "suspended", {"step": step.name, "reason": suspend.reason,
                                                  **suspend.payload}
                    )
                    wf_span.set(outcome="awaiting_approval", suspended_at=step.name)
                    return WorkflowResult(
                        run.run_id, "awaiting_approval", state, steps_executed=executed,
                        steps_skipped=skipped, suspended_reason=suspend.reason,
                        suspended_payload=suspend.payload, resumed=bool(resume_payload),
                    )
                except Exception as exc:
                    error = f"{type(exc).__name__}: {exc}"
                    self.store.record_step(run.run_id, step.name, "failed", error=error)
                    self.store.update_run(
                        run.run_id, status="failed", error=error, cursor_step=step.name
                    )
                    self.store.append_event(run.run_id, "failed", {"step": step.name, "error": error})
                    wf_span.status = "error"
                    wf_span.set(outcome="failed", failed_at=step.name)
                    return WorkflowResult(
                        run.run_id, "failed", state, error=error, steps_executed=executed,
                        steps_skipped=skipped, resumed=bool(resume_payload),
                    )

                state[step.name] = output
                executed.append(step.name)
                self.store.record_step(run.run_id, step.name, "succeeded", output=output)

                if self.crash_after == step.name:
                    self.crash_after = None  # crash once, so a resume can complete
                    error = f"SimulatedCrash: process died after '{step.name}' committed"
                    self.store.update_run(run.run_id, status="failed", error=error,
                                          cursor_step=step.name)
                    self.store.append_event(run.run_id, "crashed", {"step": step.name})
                    wf_span.status = "error"
                    wf_span.set(outcome="crashed", failed_at=step.name)
                    return WorkflowResult(
                        run.run_id, "failed", state, error=error, steps_executed=executed,
                        steps_skipped=skipped,
                    )

            final = state.get(self.steps[-1].name) if self.steps else None
            self.store.update_run(run.run_id, status="succeeded", output={"result": final})
            self.store.append_event(run.run_id, "succeeded", {"steps": len(executed)})
            wf_span.set(outcome="succeeded", executed=len(executed), skipped=len(skipped))
            return WorkflowResult(
                run.run_id, "succeeded", state, final, steps_executed=executed,
                steps_skipped=skipped, resumed=bool(resume_payload),
            )

    def _run_step(self, step: Step, step_ctx: StepContext, ctx: RunContext) -> Any:
        last: Exception | None = None
        for attempt in range(1, step.retry.attempts + 1):
            step_ctx.attempt = attempt
            ctx.checkpoint_guards()
            with ctx.tracer.span(
                f"step.{step.name}", kind="workflow_step", attempt=attempt,
                idempotent=step.idempotent,
            ) as span:
                try:
                    result = step.fn(step_ctx)
                    span.set(outcome="ok")
                    return result
                except (Suspend, PermanentStepError):
                    raise
                except TransientStepError as exc:
                    last = exc
                    span.status = "error"
                    span.set(outcome="transient_error", error=str(exc))
                    if attempt < step.retry.attempts:
                        time.sleep(step.retry.delay(attempt))
                        continue
                    raise
                except Exception as exc:
                    # Unclassified failures are treated as permanent. Retrying an unknown
                    # error against a non-idempotent step is how duplicate payments happen.
                    span.status = "error"
                    span.set(outcome="error", error=str(exc))
                    raise PermanentStepError(f"{type(exc).__name__}: {exc}") from exc
        raise PermanentStepError(str(last))

    def _state(self, run_id: str) -> dict[str, Any]:
        return {s.name: s.output for s in self.store.steps(run_id) if s.status == "succeeded"}
