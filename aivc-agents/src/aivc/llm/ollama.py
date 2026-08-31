"""Ollama provider — local / LAN inference via the native chat API.

Uses httpx (already a project dependency). Auth token is read from a file (default
``/tmp/ollama``) or ``AIVC_OLLAMA_API_KEY``, sent as ``Authorization: Bearer …``.
"""

from __future__ import annotations

import json
import time
import uuid
from pathlib import Path
from typing import Any
from urllib.parse import urljoin

import httpx

from .base import Completion, LLMError, LLMRequest, Message, ToolCall, TransientLLMError, Usage
from .providers import classify

# Best-first for this demo corpus (structured JSON + tool use). First tag match wins.
MODEL_PREFERENCE: tuple[str, ...] = (
    "qwen3:30b-a3b",
    "qwen3-coder:30b",
    "qwen3-coder:latest",
    "qwen2.5:14b-instruct",
    "qwen2.5:7b-instruct",
    "qwen2.5:3b-instruct",
    "llama3.1:8b",
    "llama3:latest",
)

DEFAULT_TOKEN_FILE = Path("/tmp/ollama")


def load_token(
    token_file: Path | str | None = None,
    api_key: str | None = None,
) -> str | None:
    """Resolve the Ollama bearer token. Env/key wins over file."""
    if api_key and api_key.strip():
        return api_key.strip()
    path = Path(token_file) if token_file else DEFAULT_TOKEN_FILE
    try:
        if path.is_file():
            token = path.read_text(encoding="utf-8").strip()
            return token or None
    except OSError:
        return None
    return None


def auth_headers(token: str | None) -> dict[str, str]:
    if not token:
        return {}
    return {"Authorization": f"Bearer {token}"}


def _base(base_url: str) -> str:
    return base_url.rstrip("/") + "/"


def list_models(
    base_url: str,
    timeout_s: float = 5.0,
    *,
    token: str | None = None,
) -> list[str]:
    url = urljoin(_base(base_url), "api/tags")
    try:
        resp = httpx.get(url, headers=auth_headers(token), timeout=timeout_s)
        resp.raise_for_status()
    except Exception as exc:
        raise TransientLLMError(f"cannot reach Ollama at {base_url}: {exc}") from exc
    data = resp.json()
    return [m["name"] for m in data.get("models", []) if m.get("name")]


def resolve_model(
    base_url: str,
    requested: str,
    timeout_s: float = 5.0,
    *,
    token: str | None = None,
) -> str:
    """Pick a model tag. `auto` selects the best Qwen (etc.) available on the host."""
    available = list_models(base_url, timeout_s, token=token)
    if not available:
        raise LLMError(
            f"no models reported by Ollama at {base_url} — run `ollama pull qwen3:30b-a3b` on that host"
        )
    if requested and requested != "auto":
        if requested in available:
            return requested
        for name in available:
            if name.startswith(f"{requested}:"):
                return name
        raise LLMError(
            f"model '{requested}' not found on Ollama at {base_url}. "
            f"Available: {', '.join(available[:8])}"
        )
    for pref in MODEL_PREFERENCE:
        if pref in available:
            return pref
        stem = pref.split(":")[0]
        for name in available:
            if name.split(":")[0] == stem:
                return name
    return available[0]


def ping(
    base_url: str,
    timeout_s: float = 3.0,
    *,
    token: str | None = None,
) -> bool:
    try:
        list_models(base_url, timeout_s, token=token)
        return True
    except Exception:
        return False


def _to_ollama_messages(request: LLMRequest) -> list[dict[str, Any]]:
    messages: list[dict[str, Any]] = []
    if request.system:
        messages.append({"role": "system", "content": request.system})
    for m in request.messages:
        if m.role == "tool":
            messages.append({"role": "tool", "content": m.content})
        elif m.role == "assistant" and m.tool_calls:
            messages.append(
                {
                    "role": "assistant",
                    "content": m.content or "",
                    "tool_calls": [
                        {
                            "function": {
                                "name": t.name,
                                "arguments": t.arguments,
                            }
                        }
                        for t in m.tool_calls
                    ],
                }
            )
        else:
            messages.append({"role": m.role, "content": m.content})
    return messages


def _to_ollama_tools(tools: list[dict[str, Any]]) -> list[dict[str, Any]]:
    out: list[dict[str, Any]] = []
    for t in tools:
        out.append(
            {
                "type": "function",
                "function": {
                    "name": t["name"],
                    "description": t.get("description", ""),
                    "parameters": t.get("parameters", {"type": "object", "properties": {}}),
                },
            }
        )
    return out


class OllamaClient:
    name = "ollama"

    def __init__(
        self,
        base_url: str,
        model: str,
        timeout_s: float = 120.0,
        *,
        token: str | None = None,
    ):
        self.base_url = _base(base_url)
        self._token = token
        self.model = resolve_model(
            base_url, model, timeout_s=min(timeout_s, 15.0), token=token
        )
        self._timeout_s = timeout_s

    def complete(self, request: LLMRequest) -> Completion:
        start = time.perf_counter()
        payload: dict[str, Any] = {
            "model": request.model or self.model,
            "messages": _to_ollama_messages(request),
            "stream": False,
            "options": {
                "temperature": request.temperature,
                "num_predict": request.max_tokens,
            },
        }
        if request.response_schema:
            payload["format"] = "json"
            schema_hint = json.dumps(request.response_schema)
            if payload["messages"]:
                last = payload["messages"][-1]
                if last.get("role") == "user":
                    last["content"] = (
                        f"{last['content']}\n\n"
                        "Reply with a single JSON object matching this schema and nothing else:\n"
                        f"{schema_hint}"
                    )
        if request.tools:
            payload["tools"] = _to_ollama_tools(request.tools)

        url = urljoin(self.base_url, "api/chat")
        try:
            resp = httpx.post(
                url,
                json=payload,
                headers=auth_headers(self._token),
                timeout=self._timeout_s,
            )
            if resp.status_code in (408, 429, 500, 502, 503, 504):
                raise TransientLLMError(f"Ollama HTTP {resp.status_code}: {resp.text[:300]}")
            resp.raise_for_status()
            data = resp.json()
        except TransientLLMError:
            raise
        except httpx.TimeoutException as exc:
            raise TransientLLMError(f"Ollama request timed out after {self._timeout_s}s") from exc
        except Exception as exc:
            raise classify(exc) from exc

        message = data.get("message") or {}
        text = message.get("content") or ""
        tool_calls: list[ToolCall] = []
        for tc in message.get("tool_calls") or []:
            fn = tc.get("function") or {}
            args = fn.get("arguments")
            if isinstance(args, str):
                try:
                    args = json.loads(args) if args.strip() else {}
                except json.JSONDecodeError:
                    args = {}
            elif not isinstance(args, dict):
                args = {}
            tool_calls.append(
                ToolCall(
                    id=str(uuid.uuid4()),
                    name=fn.get("name", ""),
                    arguments=args,
                )
            )

        prompt_tokens = int(data.get("prompt_eval_count") or 0)
        output_tokens = int(data.get("eval_count") or 0)
        return Completion(
            text=text,
            model=payload["model"],
            usage=Usage(input_tokens=prompt_tokens, output_tokens=output_tokens),
            tool_calls=tool_calls,
            stop_reason="stop",
            latency_ms=(time.perf_counter() - start) * 1000,
            raw={"provider": "ollama", "base_url": self.base_url.rstrip("/")},
        )
