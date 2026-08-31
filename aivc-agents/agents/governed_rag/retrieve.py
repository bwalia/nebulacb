"""Retrieval.

Pipeline, in order, with the reason each stage exists:

  1. **ACL predicate** -- applied inside the index, before ranking. Filtering after ranking
     leaks information through result counts and burns top-k slots on unreadable chunks.
  2. **Lexical + dense in parallel** -- they fail differently (see store/index.py).
  3. **RRF fusion** -- rank-based, needs no per-corpus score calibration.
  4. **MMR** -- diversity, so near-duplicate boilerplate cannot crowd out the answer.
  5. **Recency preference** -- a superseded policy that still matches lexically is the most
     dangerous kind of correct-looking wrong answer.
  6. **Context budget** -- pack to a token ceiling, never "top 20 and hope".

Every stage is measurable on its own (`retrieval_recall` in the eval suite), which is what
lets you tell a prompt problem from a retrieval problem.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from datetime import date
from typing import Any, Callable

from aivc.obs.run import RunContext
from aivc.security.identity import Principal
from aivc.store.index import Chunk, Hit, mmr, reciprocal_rank_fusion

from .ingest import Corpus


def acl_predicate(principal: Principal) -> Callable[[Chunk], bool]:
    """A chunk is visible when the principal holds one of its ACL roles.

    Note what is *not* here: no 'admin bypasses everything' branch. Bypasses are what turn a
    RAG index into the fastest data-exfiltration path a company owns.
    """
    roles = set(principal.roles)

    def predicate(chunk: Chunk) -> bool:
        return bool(set(chunk.acl) & roles)

    return predicate


@dataclass
class RetrievalResult:
    hits: list[Hit]
    considered: int
    filtered_out: int
    query: str
    context_tokens: int = 0
    diagnostics: dict[str, Any] = field(default_factory=dict)

    @property
    def chunks(self) -> list[Chunk]:
        return [h.chunk for h in self.hits]


class HybridRetriever:
    def __init__(
        self,
        corpus: Corpus,
        *,
        k: int = 6,
        candidate_k: int = 30,
        weights: tuple[float, float] = (1.0, 1.0),  # (lexical, dense)
        mmr_lambda: float = 0.8,
        recency_boost: float = 0.05,
        context_token_budget: int = 2400,
        rrf_k: int | None = None,
    ):
        self.corpus = corpus
        self.k = k
        self.candidate_k = candidate_k
        self.weights = weights
        self.mmr_lambda = mmr_lambda
        # RRF's smoothing constant has to scale with the candidate pool. The textbook k=60
        # is tuned for TREC-sized runs; against a few dozen candidates it flattens the
        # ranking until a retriever that was decisively right (BM25 on an exact identifier)
        # counts for almost nothing. Sizing it to the pool restores that signal.
        self.rrf_k = rrf_k if rrf_k is not None else max(10, candidate_k // 3)
        self.recency_boost = recency_boost
        self.context_token_budget = context_token_budget

    def retrieve(
        self, query: str, principal: Principal, ctx: RunContext | None = None, k: int | None = None
    ) -> RetrievalResult:
        k = k or self.k
        predicate = acl_predicate(principal)
        visible = sum(1 for c in self.corpus.chunks if predicate(c))

        span_cm = (
            ctx.tracer.span("retrieval", kind="retrieval", query=query[:200], k=k)
            if ctx
            else _NullSpan()
        )
        with span_cm as span:
            lexical = self.corpus.lexical.search(query, self.candidate_k, predicate)
            dense = self.corpus.vectors.search(query, self.candidate_k, predicate)
            fused = reciprocal_rank_fusion(
                [lexical, dense], k=self.rrf_k, weights=list(self.weights)
            )
            fused = self._apply_recency(fused)
            selected = mmr(fused[: self.candidate_k], self.corpus.vectors, k, self.mmr_lambda)
            selected, tokens = self._fit_budget(selected)

            result = RetrievalResult(
                hits=selected,
                considered=len(self.corpus.chunks),
                filtered_out=len(self.corpus.chunks) - visible,
                query=query,
                context_tokens=tokens,
                diagnostics={
                    "lexical_hits": len(lexical),
                    "dense_hits": len(dense),
                    "fused_candidates": len(fused),
                    "visible_chunks": visible,
                    "top_score": round(selected[0].score, 5) if selected else 0.0,
                    "retrievers": sorted({h.retriever for h in selected}),
                },
            )
            span.set(
                returned=len(selected),
                filtered_out=result.filtered_out,
                context_tokens=tokens,
                chunk_ids=[h.chunk.id for h in selected],
                **result.diagnostics,
            )
        return result

    def _apply_recency(self, hits: list[Hit]) -> list[Hit]:
        if not self.recency_boost:
            return hits
        today = date.today()
        adjusted: list[Hit] = []
        for hit in hits:
            score = hit.score
            if hit.chunk.effective_date:
                try:
                    age_years = (today - date.fromisoformat(hit.chunk.effective_date)).days / 365.25
                    score *= 1 + self.recency_boost * max(0.0, 1.0 - min(age_years, 3.0) / 3.0)
                except ValueError:
                    pass
            adjusted.append(Hit(hit.chunk, score, hit.retriever))
        adjusted.sort(key=lambda h: -h.score)
        return adjusted

    def _fit_budget(self, hits: list[Hit]) -> tuple[list[Hit], int]:
        kept: list[Hit] = []
        total = 0
        for hit in hits:
            cost = max(1, len(hit.chunk.text) // 4)
            if total + cost > self.context_token_budget and kept:
                break
            kept.append(hit)
            total += cost
        return kept, total


def build_context(hits: list[Hit]) -> str:
    """Render retrieved chunks for the prompt.

    Explicit `[chunk_id]` labels are what make citations checkable after the fact -- the
    eval harness verifies every id the model emits exists here, which turns "did it
    hallucinate a source" into a deterministic test rather than a judgement call.
    """
    blocks = []
    for hit in hits:
        c = hit.chunk
        header = f"[{c.id}] {c.title}"
        if c.section:
            header += f" > {c.section}"
        if c.effective_date:
            header += f" (effective {c.effective_date})"
        blocks.append(f"{header}\n{c.text}")
    return "\n\n---\n\n".join(blocks)


class _NullSpan:
    def __enter__(self) -> "_NullSpan":
        return self

    def __exit__(self, *exc: Any) -> None:
        return None

    def set(self, **kwargs: Any) -> "_NullSpan":
        return self
