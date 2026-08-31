"""Runtime configuration.

Every knob that differs between a portfolio company's environment and ours lives here,
so the agent code never reads os.environ directly.
"""

from __future__ import annotations

from pathlib import Path
from typing import Literal

from pydantic_settings import BaseSettings, SettingsConfigDict

REPO_ROOT = Path(__file__).resolve().parents[2]


class Settings(BaseSettings):
    model_config = SettingsConfigDict(env_prefix="AIVC_", env_file=".env", extra="ignore")

    # --- provider -----------------------------------------------------------
    # offline = deterministic stub (CI, no network). ollama = LAN/local real model.
    # anthropic/openai = hosted APIs.
    provider: Literal["offline", "ollama", "anthropic", "openai"] = "ollama"
    # Use "auto" with ollama to pick the best Qwen (etc.) available on the host.
    model: str = "auto"
    fast_model: str = "auto"
    temperature: float = 0.0
    max_output_tokens: int = 1024
    request_timeout_s: float = 60.0
    max_retries: int = 3

    ollama_base_url: str = "http://192.168.1.177:11434"
    # Bearer token for authenticated Ollama hosts. Read from file if api_key unset.
    ollama_token_file: Path = Path("/tmp/ollama")
    ollama_api_key: str | None = None

    anthropic_api_key: str | None = None
    openai_api_key: str | None = None

    # --- guardrails ---------------------------------------------------------
    # Hard ceilings. Exceeding either aborts the run rather than silently spending.
    run_cost_budget_usd: float = 0.50
    run_step_budget: int = 12
    tool_timeout_s: float = 30.0

    # --- retrieval ----------------------------------------------------------
    chunk_tokens: int = 320
    chunk_overlap_tokens: int = 60
    retrieve_k: int = 6
    candidate_k: int = 30
    min_groundedness: float = 0.35
    # Calibrated, not guessed: on the sample corpus the answerable cases sit at 0.71-1.00 and
    # the near-miss case at 0.50, so 0.60 is the midpoint of the observed gap. Re-derive this
    # from the client's own question set on every engagement -- see docs/EVALUATION.md.
    min_question_coverage: float = 0.60
    embedding_dim: int = 384

    # --- storage ------------------------------------------------------------
    data_dir: Path = REPO_ROOT / "data"
    state_dir: Path = REPO_ROOT / ".state"
    corpus_dir: Path = REPO_ROOT / "data" / "corpus"
    trace_file: Path = REPO_ROOT / ".state" / "traces.jsonl"
    checkpoint_db: Path = REPO_ROOT / ".state" / "workflow.sqlite"
    warehouse_db: Path = REPO_ROOT / ".state" / "warehouse.sqlite"

    # --- observability ------------------------------------------------------
    trace_console: bool = False
    otel_endpoint: str | None = None
    service_name: str = "aivc-agents"

    def ensure_dirs(self) -> None:
        self.state_dir.mkdir(parents=True, exist_ok=True)
        self.data_dir.mkdir(parents=True, exist_ok=True)


_settings: Settings | None = None


def get_settings() -> Settings:
    global _settings
    if _settings is None:
        _settings = Settings()
        _settings.ensure_dirs()
    return _settings


def reset_settings(**overrides) -> Settings:
    """Test/demo helper: rebuild settings with explicit overrides."""
    global _settings
    base = get_settings()
    merged = {**base.model_dump(), **overrides}
    _settings = Settings(**merged)
    _settings.ensure_dirs()
    return _settings


class ProviderBootstrapError(RuntimeError):
    """The configured LLM provider could not be initialised."""


def bootstrap_provider(settings: Settings | None = None) -> Settings:
    """Resolve and validate the active provider. Default setup requires a reachable Ollama host."""
    s = settings or get_settings()
    if s.provider != "ollama":
        return s

    from aivc.llm.ollama import load_token, ping, resolve_model  # noqa: PLC0415

    token = load_token(s.ollama_token_file, s.ollama_api_key)
    if not ping(s.ollama_base_url, token=token):
        raise ProviderBootstrapError(
            f"Ollama not reachable at {s.ollama_base_url}. "
            "Check the host is up, or set AIVC_PROVIDER=offline for CI."
        )
    try:
        model = resolve_model(s.ollama_base_url, s.model, token=token)
    except Exception as exc:
        raise ProviderBootstrapError(f"Ollama model resolution failed: {exc}") from exc

    fast_model = s.fast_model
    if fast_model in ("auto", ""):
        fast_model = model

    return reset_settings(
        provider="ollama",
        model=model,
        fast_model=fast_model,
        ollama_base_url=s.ollama_base_url,
        ollama_token_file=s.ollama_token_file,
        ollama_api_key=s.ollama_api_key,
        request_timeout_s=max(s.request_timeout_s, 120.0),
    )
