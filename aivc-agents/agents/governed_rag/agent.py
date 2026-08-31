"""Agent 1 -- Governed RAG over enterprise documents.

The demo question this answers for a portfolio company: *"can our people ask our policies a
question and trust the answer?"*

What makes it production-shaped rather than a notebook:

  * Access control is enforced at retrieval against the caller's roles, so the same question
    from two people returns different, correct answers -- and the restricted document is
    invisible, not redacted-after-the-fact.
  * The model is not trusted to police itself. Its citations are verified against what was
    actually retrieved, and a lexical-support check runs on the answer it produced. Below
    threshold, the system refuses rather than shipping a plausible paragraph.
  * Refusal is a first-class outcome with a reason code, not an error, so the business can
    measure *why* coverage gaps happen and fix the corpus.
  * Everything is traced and costed per question.
"""

from __future__ import annotations

import re
from dataclasses import asdict, dataclass, field
from typing import Any

from pydantic import BaseModel, Field

from aivc.evals.scorers import CITATION_RE, lexical_support
from aivc.llm.gateway import LLMGateway, StructuredOutputError
from aivc.obs.run import RunContext
from aivc.security.identity import Principal
from aivc.store.index import Hit

from aivc.evals.scorers import _STOPWORDS
from aivc.store.index import tokenize

from .ingest import Corpus
from .retrieve import HybridRetriever, build_context


def question_coverage(question: str, evidence: str) -> float:
    """Fraction of the question's distinctive terms that appear in the cited evidence."""
    q_terms = {
        t for t in tokenize(question)
        if t not in _STOPWORDS and (len(t) > 2 or any(ch.isdigit() for ch in t))
    }
    if not q_terms:
        return 1.0
    # Light suffix folding stands in for a stemmer: "variance"/"variances" should match, and
    # a full stemmer is a dependency this layer does not need.
    def variants(term: str) -> set[str]:
        out = {term}
        for suffix in ("s", "es", "ing", "ed"):
            if term.endswith(suffix) and len(term) - len(suffix) >= 4:
                out.add(term[: -len(suffix)])
        out |= {term + s for s in ("s", "es")}
        return out

    evidence_terms = {v for t in tokenize(evidence) for v in variants(t)}
    matched = sum(1 for t in q_terms if variants(t) & evidence_terms)
    return matched / len(q_terms)

MARKER = "AIVC_RAG_ANSWER_V1"

SYSTEM_PROMPT = f"""{MARKER}
You answer questions about Northgate Industrial Group's internal policies for an employee.

Rules, in priority order:
1. Use ONLY the numbered context passages provided. You have no other knowledge of this
   company. If the context does not contain the answer, set sufficient=false and say so.
2. Cite the passage id in square brackets immediately after each claim, e.g. "claims over
   GBP 2,000 need Finance Director approval [POL-EXP-114#2.0]".
3. Never cite an id that is not in the context. Never merge two passages into one citation.
4. Prefer the passage with the most recent effective date when passages conflict, and say
   that they conflict.
5. Quote figures, thresholds and dates exactly as written. Do not round, convert or infer.
6. Be direct and short. No preamble, no restating the question.

Return JSON only:
{{"answer": str, "citations": [passage ids used], "sufficient": bool, "confidence": 0..1}}
"""

REFUSAL_TEXT = (
    "I could not find enough in the documents you have access to to answer that reliably. "
    "This may be because the policy is not in the indexed corpus, or because it sits in a "
    "document your role cannot see."
)


class GroundedAnswer(BaseModel):
    answer: str = Field(description="The answer, with inline [passage-id] citations")
    citations: list[str] = Field(default_factory=list)
    sufficient: bool = True
    confidence: float = Field(default=0.5, ge=0.0, le=1.0)


@dataclass
class RagResponse:
    question: str
    answer: str
    refused: bool
    refusal_reason: str | None
    citations: list[dict[str, Any]]
    context: list[dict[str, Any]]
    groundedness: float
    confidence: float
    fabricated_citations: list[str]
    retrieval: dict[str, Any]
    cost_usd: float
    run_id: str
    trace_id: str
    latency_ms: float = 0.0
    metadata: dict[str, Any] = field(default_factory=dict)

    def to_dict(self) -> dict[str, Any]:
        return asdict(self)


class GovernedRagAgent:
    def __init__(
        self,
        corpus: Corpus,
        gateway: LLMGateway,
        *,
        retriever: HybridRetriever | None = None,
        min_groundedness: float | None = None,
    ):
        self.corpus = corpus
        self.gateway = gateway
        s = gateway.ctx.settings
        self.retriever = retriever or HybridRetriever(
            corpus, k=s.retrieve_k, candidate_k=s.candidate_k
        )
        self.min_groundedness = (
            s.min_groundedness if min_groundedness is None else min_groundedness
        )
        self.min_question_coverage = s.min_question_coverage

    @property
    def ctx(self) -> RunContext:
        return self.gateway.ctx

    def answer(self, question: str, principal: Principal | None = None) -> RagResponse:
        principal = principal or self.ctx.principal
        with self.ctx.tracer.span(
            "agent.governed_rag", kind="agent", question=question[:300], principal=principal.subject
        ) as span:
            retrieval = self.retriever.retrieve(question, principal, self.ctx)

            if not retrieval.hits:
                span.set(outcome="refused", reason="no_visible_evidence")
                return self._refuse(question, retrieval, "no_visible_evidence")

            context = build_context(retrieval.hits)
            try:
                result = self.gateway.structured(
                    system=SYSTEM_PROMPT,
                    user=f"CONTEXT PASSAGES:\n\n{context}\n\nQUESTION: {question}",
                    schema=GroundedAnswer,
                    label="rag.answer",
                )
            except StructuredOutputError as exc:
                span.set(outcome="refused", reason="unparseable_model_output")
                return self._refuse(question, retrieval, "unparseable_model_output", detail=str(exc))

            verdict = self._verify(question, result, retrieval.hits)
            span.set(
                outcome="refused" if verdict["refused"] else "answered",
                groundedness=verdict["groundedness"],
                question_coverage=verdict["question_coverage"],
                fabricated=verdict["fabricated"],
                citations=verdict["valid_citations"],
            )

            if verdict["refused"]:
                return self._refuse(
                    question, retrieval, verdict["reason"], groundedness=verdict["groundedness"]
                )

            return RagResponse(
                question=question,
                answer=verdict["answer"],
                refused=False,
                refusal_reason=None,
                citations=[
                    self._cite(h) for h in retrieval.hits if h.chunk.id in verdict["valid_citations"]
                ],
                context=[{"chunk_id": h.chunk.id, "text": h.chunk.text} for h in retrieval.hits],
                groundedness=verdict["groundedness"],
                confidence=result.confidence,
                fabricated_citations=verdict["fabricated"],
                retrieval=retrieval.diagnostics,
                cost_usd=round(self.ctx.ledger.total_usd, 6),
                run_id=self.ctx.run_id,
                trace_id=self.ctx.tracer.trace_id,
            )

    # -- verification -------------------------------------------------------
    def _verify(self, question: str, result: GroundedAnswer, hits: list[Hit]) -> dict[str, Any]:
        """Post-generation checks. The model's own confidence is an input here, not the gate."""
        retrieved_ids = {h.chunk.id for h in hits}
        inline = set(CITATION_RE.findall(result.answer))
        declared = set(result.citations)
        claimed = inline | declared
        fabricated = sorted(claimed - retrieved_ids)
        valid = sorted(claimed & retrieved_ids)

        # Strip fabricated markers rather than showing a user a citation that goes nowhere.
        answer = result.answer
        for bad in fabricated:
            answer = answer.replace(f"[{bad}]", "").replace("  ", " ")
        answer = re.sub(r"\s+([.,;])", r"\1", answer).strip()

        cited = [h.chunk for h in hits if h.chunk.id in valid]
        cited_text = " ".join(c.text for c in cited)
        grounded = lexical_support(re.sub(CITATION_RE, "", answer), cited_text) if valid else 0.0

        # Responsiveness, the mirror image of groundedness. Groundedness asks "is the answer
        # supported by its sources"; an extract from an adjacent, visible-but-wrong policy
        # passes that trivially while answering a different question than the one asked.
        # That near-miss is the failure users report as "confidently wrong", and it is the
        # one an ACL boundary creates on purpose: the right document was filtered out, so the
        # best remaining passage is merely similar. Cheap fix -- require the question's own
        # distinctive terms to appear in the cited passages.
        coverage = question_coverage(
            question, " ".join(f"{c.title} {c.section} {c.text}" for c in cited)
        )

        reason = None
        if not result.sufficient:
            reason = "model_reported_insufficient_context"
        elif not valid:
            reason = "no_valid_citations"
        elif grounded < self.min_groundedness:
            reason = "low_groundedness"
        elif coverage < self.min_question_coverage:
            reason = "answer_not_responsive"

        return {
            "answer": answer,
            "valid_citations": valid,
            "fabricated": fabricated,
            "groundedness": round(grounded, 4),
            "question_coverage": round(coverage, 4),
            "refused": reason is not None,
            "reason": reason,
        }

    def _refuse(
        self,
        question: str,
        retrieval: Any,
        reason: str,
        *,
        groundedness: float = 0.0,
        detail: str | None = None,
    ) -> RagResponse:
        return RagResponse(
            question=question,
            answer=REFUSAL_TEXT,
            refused=True,
            refusal_reason=reason,
            citations=[],
            context=[{"chunk_id": h.chunk.id, "text": h.chunk.text} for h in retrieval.hits],
            groundedness=groundedness,
            confidence=0.0,
            fabricated_citations=[],
            retrieval=retrieval.diagnostics,
            cost_usd=round(self.ctx.ledger.total_usd, 6),
            run_id=self.ctx.run_id,
            trace_id=self.ctx.tracer.trace_id,
            metadata={"detail": detail} if detail else {},
        )

    @staticmethod
    def _cite(hit: Hit) -> dict[str, Any]:
        c = hit.chunk
        return {
            "chunk_id": c.id,
            "doc_id": c.doc_id,
            "title": c.title,
            "section": c.section,
            "source": c.source,
            "effective_date": c.effective_date,
            "score": round(hit.score, 5),
        }
