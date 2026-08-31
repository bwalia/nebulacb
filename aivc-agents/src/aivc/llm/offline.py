"""Deterministic offline provider.

Why this exists: a client-facing POC has to run on a locked-down laptop in a meeting room
with no egress and no keys, and CI has to be free and reproducible. This provider gives
byte-identical outputs for identical inputs, so eval regressions are real regressions and
not model drift.

It is NOT a model. It is a rule table plus extractive fallbacks. Agent modules register
their own rules at import time (see `agents/*/offline.py`), which keeps the demo honest:
if an agent's prompt contract changes, its rule has to change too.
"""

from __future__ import annotations

import hashlib
import json
import re
import time
from dataclasses import dataclass
from typing import Any, Callable

from .base import Completion, LLMRequest, Usage

Matcher = Callable[[LLMRequest], bool]
Producer = Callable[[LLMRequest], "str | Completion"]


@dataclass(slots=True)
class Rule:
    name: str
    match: Matcher
    produce: Producer
    priority: int = 100


_RULES: list[Rule] = []


def register(name: str, match: Matcher, produce: Producer, priority: int = 100) -> None:
    """Register an offline behaviour. Later registrations of the same name replace earlier."""
    global _RULES
    _RULES = [r for r in _RULES if r.name != name]
    _RULES.append(Rule(name, match, produce, priority))
    _RULES.sort(key=lambda r: r.priority)


def registered_rules() -> list[str]:
    return [r.name for r in _RULES]


def marker(token: str) -> Matcher:
    """Match on a marker string the agent puts in its system prompt."""

    def _m(req: LLMRequest) -> bool:
        return token in req.system or token in req.all_text()

    return _m


def approx_tokens(text: str) -> int:
    return max(1, len(text) // 4)


class OfflineLLM:
    """Zero-cost, zero-network, deterministic stand-in for a hosted model."""

    name = "offline"

    def __init__(self, model: str = "offline-deterministic-v1", latency_ms: float = 4.0):
        self.model = model
        self._latency_ms = latency_ms
        self.calls: list[LLMRequest] = []

    def complete(self, request: LLMRequest) -> Completion:
        start = time.perf_counter()
        self.calls.append(request)
        result: str | Completion | None = None
        rule_name = "fallback"
        for rule in _RULES:
            try:
                hit = rule.match(request)
            except Exception:  # a broken matcher must not take the run down
                hit = False
            if hit:
                result = rule.produce(request)
                rule_name = rule.name
                break
        if result is None:
            result = self._fallback(request)

        if isinstance(result, Completion):
            completion = result
        else:
            completion = Completion(
                text=result,
                model=self.model,
                usage=Usage(
                    input_tokens=approx_tokens(request.all_text()),
                    output_tokens=approx_tokens(result),
                ),
            )
        completion.latency_ms = (time.perf_counter() - start) * 1000 + self._latency_ms
        completion.raw = {"provider": "offline", "rule": rule_name}
        return completion

    # -- fallbacks ---------------------------------------------------------
    def _fallback(self, request: LLMRequest) -> str:
        if request.response_schema:
            return json.dumps(stub_from_schema(request.response_schema, request))
        return extractive_summary(request.last_user_text())


def stub_from_schema(schema: dict[str, Any], request: LLMRequest | None = None) -> Any:
    """Build the smallest instance that validates against a JSON schema.

    Enough to keep a structured-output path exercised end to end offline.
    """
    t = schema.get("type")
    if "enum" in schema:
        return schema["enum"][0]
    if t == "object":
        props = schema.get("properties", {})
        required = schema.get("required", list(props))
        return {k: stub_from_schema(v, request) for k, v in props.items() if k in required}
    if t == "array":
        item = schema.get("items")
        return [stub_from_schema(item, request)] if item and schema.get("minItems") else []
    if t == "integer":
        return int(schema.get("minimum", 0))
    if t == "number":
        return float(schema.get("minimum", 0.0))
    if t == "boolean":
        return False
    if t == "string":
        return schema.get("default", "")
    return None


_SENT_SPLIT = re.compile(r"(?<=[.!?])\s+")


def extractive_summary(text: str, max_sentences: int = 3) -> str:
    """Cheap, deterministic 'summary': the highest term-overlap sentences, original order."""
    sentences = [s.strip() for s in _SENT_SPLIT.split(text) if s.strip()]
    if len(sentences) <= max_sentences:
        return " ".join(sentences)
    vocab: dict[str, int] = {}
    for s in sentences:
        for w in _words(s):
            vocab[w] = vocab.get(w, 0) + 1
    scored = [(sum(vocab.get(w, 0) for w in _words(s)) / (len(_words(s)) or 1), i) for i, s in enumerate(sentences)]
    keep = sorted(i for _, i in sorted(scored, reverse=True)[:max_sentences])
    return " ".join(sentences[i] for i in keep)


def _words(text: str) -> list[str]:
    return re.findall(r"[a-z0-9']+", text.lower())


def stable_choice(seed_text: str, options: list[str]) -> str:
    """Deterministic pseudo-random pick, so demos are reproducible across machines."""
    h = hashlib.sha256(seed_text.encode()).digest()
    return options[h[0] % len(options)]
