"""The gateway every agent calls through.

One choke point for the five things that are wrong with a raw provider SDK call in
production: no retry policy, no trace, no cost attribution, no budget ceiling, and raw PII
on the wire. Agent code calls `gateway.complete(...)`; it gets all five for free.
"""

from __future__ import annotations

import random
import time
from dataclasses import dataclass
from typing import Any, TypeVar

from pydantic import BaseModel, ValidationError

from ..config import Settings, get_settings
from ..obs.run import RunContext
from ..security.redaction import Redactor
from .base import (
    Completion,
    LLMClient,
    LLMError,
    LLMRequest,
    Message,
    TransientLLMError,
)
from .offline import OfflineLLM

T = TypeVar("T", bound=BaseModel)


def build_llm(settings: Settings | None = None) -> LLMClient:
    s = settings or get_settings()
    if s.provider == "offline":
        model = s.model if s.model not in ("auto", "") else "offline-deterministic-v1"
        return OfflineLLM(model=model)
    if s.provider == "ollama":
        from .ollama import OllamaClient, load_token  # noqa: PLC0415

        timeout = max(s.request_timeout_s, 120.0)
        token = load_token(s.ollama_token_file, s.ollama_api_key)
        return OllamaClient(s.ollama_base_url, s.model, timeout_s=timeout, token=token)
    if s.provider == "anthropic":
        from .providers import AnthropicClient  # noqa: PLC0415

        if not s.anthropic_api_key:
            raise LLMError("AIVC_ANTHROPIC_API_KEY is not set")
        return AnthropicClient(s.anthropic_api_key, s.model, s.request_timeout_s)
    if s.provider == "openai":
        from .providers import OpenAIClient  # noqa: PLC0415

        if not s.openai_api_key:
            raise LLMError("AIVC_OPENAI_API_KEY is not set")
        return OpenAIClient(s.openai_api_key, s.model, s.request_timeout_s)
    raise LLMError(f"unknown provider '{s.provider}'")


class StructuredOutputError(LLMError):
    def __init__(self, attempts: int, last_error: str):
        super().__init__(f"structured output failed after {attempts} attempts: {last_error}")
        self.attempts = attempts
        self.last_error = last_error


@dataclass
class LLMGateway:
    llm: LLMClient
    ctx: RunContext
    redactor: Redactor | None = None

    @classmethod
    def for_run(cls, ctx: RunContext, llm: LLMClient | None = None, redact: bool = False) -> "LLMGateway":
        return cls(llm or build_llm(ctx.settings), ctx, Redactor() if redact else None)

    # -- core ---------------------------------------------------------------
    def complete(self, request: LLMRequest, label: str = "llm") -> Completion:
        self.ctx.checkpoint_guards()
        mapping: dict[str, str] = {}
        if self.redactor:
            request, mapping = _redact_request(request, self.redactor)

        with self.ctx.tracer.span(label, kind="llm", model=request.model or "default") as span:
            completion = self._with_retries(request, span)
            cost = self.ctx.ledger.record(label, completion.model, completion.usage)
            span.set(
                tokens_in=completion.usage.input_tokens,
                tokens_out=completion.usage.output_tokens,
                cached_tokens=completion.usage.cached_input_tokens,
                cost_usd=round(cost, 6),
                stop_reason=completion.stop_reason,
                redacted=bool(mapping),
                output_preview=completion.text[:400],
                tool_calls=[t.name for t in completion.tool_calls],
            )

        if mapping:
            completion.text = Redactor.restore(completion.text, mapping)
        return completion

    def _with_retries(self, request: LLMRequest, span: Any) -> Completion:
        s = self.ctx.settings
        last: Exception | None = None
        for attempt in range(1, s.max_retries + 1):
            try:
                completion = self.llm.complete(request)
                span.set(attempts=attempt)
                return completion
            except TransientLLMError as exc:
                last = exc
                if attempt == s.max_retries:
                    break
                # Full jitter: prevents a fleet of workers retrying in lockstep after an
                # provider incident and re-creating the thundering herd that caused it.
                delay = min(2 ** (attempt - 1), 8) * random.random()
                span.set(retry_delay_s=round(delay, 3))
                time.sleep(delay)
            except Exception as exc:
                raise LLMError(str(exc)) from exc
        raise LLMError(f"provider failed after {s.max_retries} attempts: {last}")

    # -- convenience --------------------------------------------------------
    def ask(
        self,
        system: str,
        user: str,
        *,
        label: str = "llm",
        temperature: float | None = None,
        max_tokens: int | None = None,
        model: str | None = None,
    ) -> Completion:
        s = self.ctx.settings
        return self.complete(
            LLMRequest(
                messages=[Message("user", user)],
                system=system,
                temperature=s.temperature if temperature is None else temperature,
                max_tokens=max_tokens or s.max_output_tokens,
                model=model,
            ),
            label=label,
        )

    def structured(
        self,
        system: str,
        user: str,
        schema: type[T],
        *,
        label: str = "llm.structured",
        max_repairs: int = 2,
        model: str | None = None,
    ) -> T:
        """Ask for a typed object, with a bounded self-repair loop.

        Validation failures are fed back as a tool-style error message rather than retried
        blind -- in practice that fixes the great majority of schema misses in one turn.
        """
        json_schema = schema.model_json_schema()
        messages = [Message("user", user)]
        last_error = ""
        for attempt in range(max_repairs + 1):
            completion = self.complete(
                LLMRequest(
                    messages=list(messages),
                    system=system,
                    response_schema=json_schema,
                    temperature=0.0,
                    max_tokens=self.ctx.settings.max_output_tokens,
                    model=model,
                ),
                label=label if attempt == 0 else f"{label}.repair{attempt}",
            )
            try:
                return schema.model_validate(completion.json())
            except (ValidationError, ValueError) as exc:
                last_error = str(exc)[:600]
                messages += [
                    Message("assistant", completion.text),
                    Message(
                        "user",
                        "That output did not validate against the required schema.\n"
                        f"Errors:\n{last_error}\n"
                        "Return corrected JSON only, no commentary.",
                    ),
                ]
        raise StructuredOutputError(max_repairs + 1, last_error)


def _redact_request(request: LLMRequest, redactor: Redactor) -> tuple[LLMRequest, dict[str, str]]:
    mapping: dict[str, str] = {}
    messages = []
    for m in request.messages:
        result = redactor.redact(m.content)
        mapping.update(result.mapping)
        messages.append(
            Message(m.role, result.text, m.tool_calls, m.tool_call_id, m.name)
        )
    sys_result = redactor.redact(request.system)
    mapping.update(sys_result.mapping)
    return (
        LLMRequest(
            messages=messages,
            system=sys_result.text,
            tools=request.tools,
            temperature=request.temperature,
            max_tokens=request.max_tokens,
            response_schema=request.response_schema,
            model=request.model,
        ),
        mapping,
    )
