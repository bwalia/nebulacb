"""Hosted-provider adapters.

Kept thin on purpose: the adapter's only job is message-shape translation and error
classification. Retries, tracing, cost accounting and budget enforcement live one layer up
so they behave identically no matter which provider a client insists on.
"""

from __future__ import annotations

import json
import time
import uuid
from typing import Any

from .base import Completion, LLMError, LLMRequest, Message, ToolCall, TransientLLMError, Usage

_TRANSIENT_MARKERS = ("429", "500", "502", "503", "504", "overloaded", "timeout", "rate limit")


def classify(exc: Exception) -> Exception:
    text = f"{type(exc).__name__}: {exc}".lower()
    status = getattr(exc, "status_code", None)
    if status in (408, 409, 429, 500, 502, 503, 504) or any(m in text for m in _TRANSIENT_MARKERS):
        return TransientLLMError(str(exc))
    return LLMError(str(exc))


class AnthropicClient:
    name = "anthropic"

    def __init__(self, api_key: str, model: str, timeout_s: float = 60.0):
        try:
            import anthropic  # noqa: PLC0415
        except ImportError as e:  # pragma: no cover
            raise LLMError("pip install 'aivc-agents[anthropic]' to use the Anthropic provider") from e
        self._client = anthropic.Anthropic(api_key=api_key, timeout=timeout_s)
        self.model = model

    def complete(self, request: LLMRequest) -> Completion:
        start = time.perf_counter()
        kwargs: dict[str, Any] = {
            "model": request.model or self.model,
            "max_tokens": request.max_tokens,
            "temperature": request.temperature,
            "messages": _to_anthropic_messages(request.messages),
        }
        system = request.system
        if request.response_schema:
            system += (
                "\n\nReply with a single JSON object matching this schema and nothing else:\n"
                + json.dumps(request.response_schema)
            )
        if system:
            # Cache the static preamble; on long system prompts this is the single biggest
            # cost lever available on repeated calls.
            kwargs["system"] = [
                {"type": "text", "text": system, "cache_control": {"type": "ephemeral"}}
            ]
        if request.tools:
            kwargs["tools"] = [
                {
                    "name": t["name"],
                    "description": t.get("description", ""),
                    "input_schema": t.get("parameters", {"type": "object", "properties": {}}),
                }
                for t in request.tools
            ]
        try:
            resp = self._client.messages.create(**kwargs)
        except Exception as exc:
            raise classify(exc) from exc

        text_parts, tool_calls = [], []
        for block in resp.content:
            if block.type == "text":
                text_parts.append(block.text)
            elif block.type == "tool_use":
                tool_calls.append(ToolCall(id=block.id, name=block.name, arguments=dict(block.input)))
        u = resp.usage
        return Completion(
            text="".join(text_parts),
            model=resp.model,
            usage=Usage(
                input_tokens=getattr(u, "input_tokens", 0),
                output_tokens=getattr(u, "output_tokens", 0),
                cached_input_tokens=getattr(u, "cache_read_input_tokens", 0) or 0,
            ),
            tool_calls=tool_calls,
            stop_reason=resp.stop_reason or "stop",
            latency_ms=(time.perf_counter() - start) * 1000,
        )


def _to_anthropic_messages(messages: list[Message]) -> list[dict[str, Any]]:
    out: list[dict[str, Any]] = []
    for m in messages:
        if m.role == "system":
            continue
        if m.role == "tool":
            out.append(
                {
                    "role": "user",
                    "content": [
                        {
                            "type": "tool_result",
                            "tool_use_id": m.tool_call_id or "",
                            "content": m.content,
                        }
                    ],
                }
            )
        elif m.role == "assistant" and m.tool_calls:
            content: list[dict[str, Any]] = []
            if m.content:
                content.append({"type": "text", "text": m.content})
            content += [
                {"type": "tool_use", "id": t.id, "name": t.name, "input": t.arguments}
                for t in m.tool_calls
            ]
            out.append({"role": "assistant", "content": content})
        else:
            out.append({"role": m.role, "content": m.content})
    return out


class OpenAIClient:
    name = "openai"

    def __init__(self, api_key: str, model: str, timeout_s: float = 60.0, base_url: str | None = None):
        try:
            from openai import OpenAI  # noqa: PLC0415
        except ImportError as e:  # pragma: no cover
            raise LLMError("pip install 'aivc-agents[openai]' to use the OpenAI provider") from e
        self._client = OpenAI(api_key=api_key, timeout=timeout_s, base_url=base_url)
        self.model = model

    def complete(self, request: LLMRequest) -> Completion:
        start = time.perf_counter()
        messages: list[dict[str, Any]] = []
        if request.system:
            messages.append({"role": "system", "content": request.system})
        for m in request.messages:
            if m.role == "tool":
                messages.append(
                    {"role": "tool", "tool_call_id": m.tool_call_id, "content": m.content}
                )
            elif m.role == "assistant" and m.tool_calls:
                messages.append(
                    {
                        "role": "assistant",
                        "content": m.content or None,
                        "tool_calls": [
                            {
                                "id": t.id,
                                "type": "function",
                                "function": {"name": t.name, "arguments": json.dumps(t.arguments)},
                            }
                            for t in m.tool_calls
                        ],
                    }
                )
            else:
                messages.append({"role": m.role, "content": m.content})

        kwargs: dict[str, Any] = {
            "model": request.model or self.model,
            "messages": messages,
            "temperature": request.temperature,
            "max_tokens": request.max_tokens,
        }
        if request.response_schema:
            kwargs["response_format"] = {
                "type": "json_schema",
                "json_schema": {
                    "name": "response",
                    "schema": request.response_schema,
                    "strict": False,
                },
            }
        if request.tools:
            kwargs["tools"] = [
                {
                    "type": "function",
                    "function": {
                        "name": t["name"],
                        "description": t.get("description", ""),
                        "parameters": t.get("parameters", {"type": "object", "properties": {}}),
                    },
                }
                for t in request.tools
            ]
        try:
            resp = self._client.chat.completions.create(**kwargs)
        except Exception as exc:
            raise classify(exc) from exc

        choice = resp.choices[0]
        tool_calls = [
            ToolCall(
                id=tc.id or str(uuid.uuid4()),
                name=tc.function.name,
                arguments=json.loads(tc.function.arguments or "{}"),
            )
            for tc in (choice.message.tool_calls or [])
        ]
        u = resp.usage
        cached = 0
        details = getattr(u, "prompt_tokens_details", None)
        if details is not None:
            cached = getattr(details, "cached_tokens", 0) or 0
        return Completion(
            text=choice.message.content or "",
            model=resp.model,
            usage=Usage(
                input_tokens=getattr(u, "prompt_tokens", 0),
                output_tokens=getattr(u, "completion_tokens", 0),
                cached_input_tokens=cached,
            ),
            tool_calls=tool_calls,
            stop_reason=choice.finish_reason or "stop",
            latency_ms=(time.perf_counter() - start) * 1000,
        )
