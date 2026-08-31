"""Provider-neutral LLM interface.

Deliberately small. Everything above this line (agents, retrieval, workflows) is written
against `LLMClient` only, so swapping Anthropic -> Azure OpenAI -> Bedrock at a portfolio
company is a config change plus one adapter file, not a rewrite.
"""

from __future__ import annotations

import json
from dataclasses import dataclass, field
from typing import Any, Literal, Protocol, runtime_checkable

Role = Literal["system", "user", "assistant", "tool"]


@dataclass(slots=True)
class ToolCall:
    id: str
    name: str
    arguments: dict[str, Any]


@dataclass(slots=True)
class Message:
    role: Role
    content: str = ""
    tool_calls: list[ToolCall] = field(default_factory=list)
    tool_call_id: str | None = None
    name: str | None = None

    def to_dict(self) -> dict[str, Any]:
        d: dict[str, Any] = {"role": self.role, "content": self.content}
        if self.tool_calls:
            d["tool_calls"] = [
                {"id": t.id, "name": t.name, "arguments": t.arguments} for t in self.tool_calls
            ]
        if self.tool_call_id:
            d["tool_call_id"] = self.tool_call_id
        return d


@dataclass(slots=True)
class Usage:
    input_tokens: int = 0
    output_tokens: int = 0
    cached_input_tokens: int = 0

    def __add__(self, other: "Usage") -> "Usage":
        return Usage(
            self.input_tokens + other.input_tokens,
            self.output_tokens + other.output_tokens,
            self.cached_input_tokens + other.cached_input_tokens,
        )

    @property
    def total(self) -> int:
        return self.input_tokens + self.output_tokens


@dataclass(slots=True)
class Completion:
    text: str
    model: str
    usage: Usage
    tool_calls: list[ToolCall] = field(default_factory=list)
    stop_reason: str = "stop"
    latency_ms: float = 0.0
    raw: dict[str, Any] | None = None

    def json(self) -> Any:
        """Parse the completion text as JSON, tolerating fenced code blocks."""
        return parse_json_lenient(self.text)


@dataclass(slots=True)
class LLMRequest:
    messages: list[Message]
    system: str = ""
    tools: list[dict[str, Any]] = field(default_factory=list)
    temperature: float = 0.0
    max_tokens: int = 1024
    response_schema: dict[str, Any] | None = None
    model: str | None = None

    def last_user_text(self) -> str:
        for m in reversed(self.messages):
            if m.role == "user":
                return m.content
        return ""

    def all_text(self) -> str:
        return "\n".join([self.system, *(m.content for m in self.messages)])


@runtime_checkable
class LLMClient(Protocol):
    name: str

    def complete(self, request: LLMRequest) -> Completion:  # pragma: no cover - protocol
        ...


class LLMError(RuntimeError):
    """Non-retryable provider error."""


class TransientLLMError(LLMError):
    """Retryable provider error (429, 5xx, timeout)."""


def parse_json_lenient(text: str) -> Any:
    """LLMs wrap JSON in prose or fences more often than anyone would like."""
    text = text.strip()
    if text.startswith("```"):
        text = text.split("```")[1]
        if text.lstrip().lower().startswith("json"):
            text = text.lstrip()[4:]
        text = text.strip()
    try:
        return json.loads(text)
    except json.JSONDecodeError:
        pass
    for opener, closer in (("{", "}"), ("[", "]")):
        start, end = text.find(opener), text.rfind(closer)
        if start != -1 and end > start:
            try:
                return json.loads(text[start : end + 1])
            except json.JSONDecodeError:
                continue
    raise ValueError(f"no JSON object found in model output: {text[:200]!r}")
