"""Ollama provider helpers."""

from __future__ import annotations

from pathlib import Path
from unittest.mock import patch

import httpx
import pytest

from aivc.llm.ollama import OllamaClient, auth_headers, load_token, resolve_model


class TestOllamaAuth:
    def test_load_token_from_file(self, tmp_path: Path):
        token_file = tmp_path / "ollama"
        token_file.write_text("secret-token\n", encoding="utf-8")
        assert load_token(token_file) == "secret-token"

    def test_api_key_overrides_file(self, tmp_path: Path):
        token_file = tmp_path / "ollama"
        token_file.write_text("from-file\n", encoding="utf-8")
        assert load_token(token_file, api_key="from-env") == "from-env"

    def test_missing_file_returns_none(self, tmp_path: Path):
        assert load_token(tmp_path / "missing") is None

    def test_auth_headers(self):
        assert auth_headers(None) == {}
        assert auth_headers("abc") == {"Authorization": "Bearer abc"}


class TestOllamaClient:
    def test_sends_bearer_token(self, tmp_path: Path):
        token_file = tmp_path / "ollama"
        token_file.write_text("lan-secret", encoding="utf-8")

        def handler(request: httpx.Request) -> httpx.Response:
            if request.url.path.endswith("/api/tags"):
                return httpx.Response(
                    200,
                    json={"models": [{"name": "qwen3:30b-a3b"}]},
                )
            assert request.headers.get("authorization") == "Bearer lan-secret"
            return httpx.Response(
                200,
                json={
                    "message": {"role": "assistant", "content": '{"answer":"ok"}'},
                    "prompt_eval_count": 10,
                    "eval_count": 5,
                },
            )

        transport = httpx.MockTransport(handler)
        with patch("aivc.llm.ollama.httpx.get", side_effect=lambda url, **kw: httpx.Client(transport=transport).get(url, **kw)), patch(
            "aivc.llm.ollama.httpx.post",
            side_effect=lambda url, **kw: httpx.Client(transport=transport).post(url, **kw),
        ):
            client = OllamaClient(
                "http://192.168.1.177:11434",
                "qwen3:30b-a3b",
                token=load_token(token_file),
            )
            from aivc.llm.base import LLMRequest, Message

            out = client.complete(LLMRequest(messages=[Message("user", "hi")]))
            assert "ok" in out.text

    def test_resolve_auto_prefers_qwen(self):
        with patch(
            "aivc.llm.ollama.list_models",
            return_value=["llama3:latest", "qwen3:30b-a3b"],
        ):
            assert resolve_model("http://host", "auto") == "qwen3:30b-a3b"
