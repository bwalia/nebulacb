"""Agent 1: governed RAG over enterprise documents."""

from __future__ import annotations

from functools import lru_cache

from aivc.config import Settings, get_settings
from aivc.llm.gateway import LLMGateway
from aivc.obs.run import RunContext
from aivc.store.index import HashingEmbedder

from . import offline as _offline  # noqa: F401  (registers offline behaviour on import)
from .agent import GovernedRagAgent, GroundedAnswer, RagResponse
from .ingest import Corpus, ingest
from .retrieve import HybridRetriever

__all__ = [
    "GovernedRagAgent",
    "GroundedAnswer",
    "RagResponse",
    "Corpus",
    "HybridRetriever",
    "build_agent",
    "get_corpus",
]


@lru_cache(maxsize=4)
def get_corpus(corpus_dir: str | None = None, dim: int = 384) -> Corpus:
    """Cached ingest. In production this is a scheduled job writing to pgvector, not a
    process-local cache -- see docs/ARCHITECTURE.md."""
    s = get_settings()
    return ingest(
        corpus_dir=None if corpus_dir is None else __import__("pathlib").Path(corpus_dir),
        settings=s,
        embedder=HashingEmbedder(dim),
    )


def build_agent(ctx: RunContext, settings: Settings | None = None) -> GovernedRagAgent:
    s = settings or ctx.settings
    corpus = get_corpus(str(s.corpus_dir), s.embedding_dim)
    return GovernedRagAgent(corpus, LLMGateway.for_run(ctx))
