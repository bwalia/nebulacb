"""The run context: one object threaded through everything an agent invocation touches.

Carries identity (who is this for), budget (how much may it spend), tracing (what happened)
and deadlines. Passing it explicitly rather than reading globals is what makes the same code
safe to run in a web request, a Celery worker and a test.
"""

from __future__ import annotations

import time
from dataclasses import dataclass, field
from typing import Any

from ..config import Settings, get_settings
from ..security.identity import Principal
from .cost import CostLedger
from .trace import ConsoleSink, JsonlSink, MemorySink, Tracer, new_id


class DeadlineExceeded(TimeoutError):
    pass


@dataclass
class RunContext:
    principal: Principal
    tracer: Tracer
    ledger: CostLedger
    settings: Settings
    run_id: str = field(default_factory=lambda: new_id("run_"))
    deadline_ts: float | None = None
    metadata: dict[str, Any] = field(default_factory=dict)
    memory_sink: MemorySink | None = None

    @classmethod
    def build(
        cls,
        principal: Principal,
        settings: Settings | None = None,
        *,
        budget_usd: float | None = None,
        timeout_s: float | None = None,
        capture_spans: bool = True,
        **metadata: Any,
    ) -> "RunContext":
        settings = settings or get_settings()
        sinks: list[Any] = [JsonlSink(settings.trace_file)]
        mem = MemorySink() if capture_spans else None
        if mem:
            sinks.append(mem)
        if settings.trace_console:
            sinks.append(ConsoleSink())
        if settings.otel_endpoint:  # pragma: no cover
            from .trace import OTelSink  # noqa: PLC0415

            sinks.append(OTelSink(settings.otel_endpoint, settings.service_name))
        return cls(
            principal=principal,
            tracer=Tracer(sinks),
            ledger=CostLedger(budget_usd=budget_usd if budget_usd is not None else settings.run_cost_budget_usd),
            settings=settings,
            deadline_ts=time.time() + timeout_s if timeout_s else None,
            metadata=metadata,
            memory_sink=mem,
        )

    def check_deadline(self) -> None:
        if self.deadline_ts and time.time() > self.deadline_ts:
            raise DeadlineExceeded(f"run {self.run_id} exceeded its wall-clock deadline")

    def checkpoint_guards(self) -> None:
        """One call that enforces every run-level limit. Invoke at each loop iteration."""
        self.check_deadline()
        self.ledger.check()

    def summary(self) -> dict[str, Any]:
        return {
            "run_id": self.run_id,
            "trace_id": self.tracer.trace_id,
            "principal": self.principal.subject,
            "tenant": self.principal.tenant,
            "cost": self.ledger.summary(),
            **({"spans": len(self.memory_sink.spans)} if self.memory_sink else {}),
        }
