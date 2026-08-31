"""Tracing.

A run is a tree of spans. Every LLM call, tool call, retrieval and workflow step is a span
with inputs, outputs, token usage and cost attached. This is the artefact that makes an
agent debuggable in production and is the first thing a client's platform team asks for.

Sinks are pluggable: JSONL by default (works everywhere, greppable), console for demos,
OTLP when the client already runs an observability stack.
"""

from __future__ import annotations

import contextvars
import json
import threading
import time
import uuid
from contextlib import contextmanager
from dataclasses import asdict, dataclass, field
from pathlib import Path
from typing import Any, Iterator, Protocol

_current_span: contextvars.ContextVar["Span | None"] = contextvars.ContextVar(
    "aivc_current_span", default=None
)


def new_id(prefix: str = "") -> str:
    return f"{prefix}{uuid.uuid4().hex[:16]}"


@dataclass
class Span:
    name: str
    trace_id: str
    span_id: str = field(default_factory=lambda: new_id())
    parent_id: str | None = None
    kind: str = "internal"  # internal | llm | tool | retrieval | workflow_step | agent
    start_ts: float = field(default_factory=time.time)
    end_ts: float | None = None
    status: str = "ok"  # ok | error
    error: str | None = None
    attributes: dict[str, Any] = field(default_factory=dict)

    @property
    def duration_ms(self) -> float:
        return ((self.end_ts or time.time()) - self.start_ts) * 1000

    def set(self, **attrs: Any) -> "Span":
        self.attributes.update(attrs)
        return self

    def to_dict(self) -> dict[str, Any]:
        d = asdict(self)
        d["duration_ms"] = round(self.duration_ms, 2)
        return d


class SpanSink(Protocol):
    def emit(self, span: Span) -> None:  # pragma: no cover - protocol
        ...


class MemorySink:
    def __init__(self) -> None:
        self.spans: list[Span] = []

    def emit(self, span: Span) -> None:
        self.spans.append(span)

    def by_kind(self, kind: str) -> list[Span]:
        return [s for s in self.spans if s.kind == kind]

    def clear(self) -> None:
        self.spans.clear()


class JsonlSink:
    def __init__(self, path: str | Path, max_value_chars: int = 2000):
        self.path = Path(path)
        self.path.parent.mkdir(parents=True, exist_ok=True)
        self._lock = threading.Lock()
        self._max = max_value_chars

    def emit(self, span: Span) -> None:
        d = span.to_dict()
        d["attributes"] = {k: _truncate(v, self._max) for k, v in d["attributes"].items()}
        line = json.dumps(d, default=str)
        with self._lock, self.path.open("a") as fh:
            fh.write(line + "\n")


class ConsoleSink:
    def __init__(self, indent: bool = True):
        self.indent = indent
        self._depth: dict[str, int] = {}

    def emit(self, span: Span) -> None:
        depth = self._depth.get(span.parent_id or "", 0) if self.indent else 0
        self._depth[span.span_id] = depth + 1
        mark = "x" if span.status == "error" else "-"
        pad = "  " * depth
        extra = ""
        if span.kind == "llm":
            extra = f" [{span.attributes.get('tokens_in', 0)}->{span.attributes.get('tokens_out', 0)}tok ${span.attributes.get('cost_usd', 0):.4f}]"
        print(f"{pad}{mark} {span.name} ({span.duration_ms:.0f}ms){extra}")


class OTelSink:  # pragma: no cover - requires optional deps + a collector
    """Bridge to an existing OpenTelemetry stack.

    Most portfolio companies already have one (Datadog, Grafana, Honeycomb). Emitting into
    it beats standing up a bespoke LLM-observability tool nobody will own after handover.
    """

    def __init__(self, endpoint: str, service_name: str = "aivc-agents"):
        from opentelemetry import trace as ot  # noqa: PLC0415
        from opentelemetry.exporter.otlp.proto.http.trace_exporter import (  # noqa: PLC0415
            OTLPSpanExporter,
        )
        from opentelemetry.sdk.resources import Resource  # noqa: PLC0415
        from opentelemetry.sdk.trace import TracerProvider  # noqa: PLC0415
        from opentelemetry.sdk.trace.export import BatchSpanProcessor  # noqa: PLC0415

        provider = TracerProvider(resource=Resource.create({"service.name": service_name}))
        provider.add_span_processor(BatchSpanProcessor(OTLPSpanExporter(endpoint=endpoint)))
        self._tracer = ot.get_tracer_provider().get_tracer(service_name) if ot else None
        ot.set_tracer_provider(provider)
        self._tracer = provider.get_tracer(service_name)

    def emit(self, span: Span) -> None:
        s = self._tracer.start_span(span.name, start_time=int(span.start_ts * 1e9))
        for k, v in span.attributes.items():
            s.set_attribute(f"aivc.{k}", v if isinstance(v, (str, int, float, bool)) else str(v))
        s.set_attribute("aivc.kind", span.kind)
        if span.status == "error":
            s.set_attribute("error", True)
            s.set_attribute("error.message", span.error or "")
        s.end(end_time=int((span.end_ts or time.time()) * 1e9))


class Tracer:
    def __init__(self, sinks: list[SpanSink] | None = None, trace_id: str | None = None):
        self.sinks = sinks or []
        self.trace_id = trace_id or new_id("tr_")

    @contextmanager
    def span(self, name: str, kind: str = "internal", **attributes: Any) -> Iterator[Span]:
        parent = _current_span.get()
        span = Span(
            name=name,
            trace_id=self.trace_id,
            parent_id=parent.span_id if parent else None,
            kind=kind,
            attributes=dict(attributes),
        )
        token = _current_span.set(span)
        try:
            yield span
        except Exception as exc:
            span.status = "error"
            span.error = f"{type(exc).__name__}: {exc}"
            raise
        finally:
            _current_span.reset(token)
            span.end_ts = time.time()
            for sink in self.sinks:
                try:
                    sink.emit(span)
                except Exception:  # observability must never break the run
                    pass

    def event(self, name: str, kind: str = "internal", **attributes: Any) -> None:
        with self.span(name, kind, **attributes):
            pass


def _truncate(value: Any, limit: int) -> Any:
    if isinstance(value, str) and len(value) > limit:
        return value[:limit] + f"...<truncated {len(value) - limit} chars>"
    return value
