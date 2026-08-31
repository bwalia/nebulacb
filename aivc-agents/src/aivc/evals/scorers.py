"""Scorers.

Design rule: prefer a cheap deterministic scorer to an LLM judge wherever the property is
mechanically checkable. Citation validity, refusal correctness, routing accuracy, PII leakage
and cost are all checkable without a second model -- which means they run in CI on every PR,
for free, with no judge drift. Reserve the judge for genuinely subjective properties and
report its agreement rate against human labels before anyone trusts it.
"""

from __future__ import annotations

import re
from dataclasses import dataclass
from typing import Any, Callable, Protocol

from ..store.index import tokenize

CITATION_RE = re.compile(r"\[([A-Za-z0-9_.:#-]+)\]")


@dataclass(slots=True)
class Score:
    name: str
    value: float          # normalised 0..1 where 1 is good
    passed: bool
    detail: str = ""


class Scorer(Protocol):
    name: str

    def __call__(self, case: Any, output: Any) -> Score:  # pragma: no cover - protocol
        ...


def _score(name: str, value: float, threshold: float, detail: str = "") -> Score:
    return Score(name, value, value >= threshold, detail)


# --- generic ---------------------------------------------------------------

def exact_match(field: str = "answer", expected_field: str = "expected") -> Scorer:
    def scorer(case: Any, output: Any) -> Score:
        got = str(_get(output, field)).strip().lower()
        want = str(_get(case, expected_field)).strip().lower()
        return _score("exact_match", 1.0 if got == want else 0.0, 1.0, f"{got!r} vs {want!r}")

    scorer.name = "exact_match"  # type: ignore[attr-defined]
    return scorer


def contains_all(expected_field: str = "must_contain", field: str = "answer") -> Scorer:
    """Keyword recall. Blunt, but it catches the regression where an agent stops mentioning
    the number the business actually cares about."""

    def scorer(case: Any, output: Any) -> Score:
        text = str(_get(output, field, "")).lower()
        needles = [n.lower() for n in (_get(case, expected_field, []) or [])]
        if not needles:
            return Score("contains_all", 1.0, True, "no expectations")
        hit = [n for n in needles if n in text]
        value = len(hit) / len(needles)
        missing = sorted(set(needles) - set(hit))
        return _score("contains_all", value, 1.0, f"missing={missing}")

    scorer.name = "contains_all"  # type: ignore[attr-defined]
    return scorer


# --- RAG -------------------------------------------------------------------

def citation_validity(threshold: float = 1.0) -> Scorer:
    """Every [chunk_id] in the answer must exist in the retrieved context.

    A fabricated citation is the failure mode that destroys user trust fastest, because it
    looks *more* credible than an ordinary hallucination.
    """

    def scorer(case: Any, output: Any) -> Score:
        answer = str(_get(output, "answer", ""))
        valid_ids = {c["chunk_id"] if isinstance(c, dict) else c for c in _get(output, "citations", [])}
        cited = set(CITATION_RE.findall(answer))
        if not cited:
            return Score("citation_validity", 1.0, True, "no citations emitted")
        bad = sorted(cited - valid_ids)
        value = 1 - len(bad) / len(cited)
        return _score("citation_validity", value, threshold, f"fabricated={bad}")

    scorer.name = "citation_validity"  # type: ignore[attr-defined]
    return scorer


def groundedness(threshold: float = 0.5) -> Scorer:
    """Lexical support: what fraction of the answer's content words appear in cited context.

    A proxy, not truth -- it under-scores good paraphrase and over-scores copied text. Its
    value is as a *regression* signal that is free and deterministic. Pair it with the LLM
    judge below on a sampled subset before making a quality claim.
    """

    def scorer(case: Any, output: Any) -> Score:
        if _get(output, "refused", False):
            # A refusal has no claims to ground. Scoring its boilerplate against the context
            # would drag the metric down for behaviour we explicitly want.
            return Score("groundedness", 1.0, True, "refused; not applicable")
        answer = str(_get(output, "answer", ""))
        context = " ".join(
            c["text"] if isinstance(c, dict) else str(c) for c in _get(output, "context", [])
        )
        value = lexical_support(answer, context)
        return _score("groundedness", value, threshold, f"support={value:.2f}")

    scorer.name = "groundedness"  # type: ignore[attr-defined]
    return scorer


def lexical_support(answer: str, context: str) -> float:
    stop = _STOPWORDS
    a = [t for t in tokenize(answer) if t not in stop and len(t) > 2]
    if not a:
        return 1.0
    c = set(tokenize(context))
    return sum(1 for t in a if t in c) / len(a)


def refusal_correctness() -> Scorer:
    """Did the agent abstain exactly when it should have?

    Scored explicitly because both errors are expensive: answering an unanswerable question
    is a hallucination, and refusing an answerable one is the reason pilots get abandoned.
    """

    def scorer(case: Any, output: Any) -> Score:
        should_refuse = bool(_get(case, "should_refuse", False))
        did_refuse = bool(_get(output, "refused", False))
        ok = should_refuse == did_refuse
        kind = "correct" if ok else ("over-refusal" if did_refuse else "should have refused")
        return Score("refusal_correctness", 1.0 if ok else 0.0, ok, kind)

    scorer.name = "refusal_correctness"  # type: ignore[attr-defined]
    return scorer


def retrieval_recall(k_field: str = "citations", expected_field: str = "expected_chunks") -> Scorer:
    """Component-level metric. When end-to-end quality drops, this tells you whether to fix
    the retriever or the prompt -- without it you are guessing."""

    def scorer(case: Any, output: Any) -> Score:
        expected = set(_get(case, expected_field, []) or [])
        if not expected:
            return Score("retrieval_recall", 1.0, True, "no expectations")
        got = {c["chunk_id"] if isinstance(c, dict) else c for c in _get(output, k_field, [])}
        value = len(expected & got) / len(expected)
        return _score("retrieval_recall", value, 1.0, f"missed={sorted(expected - got)}")

    scorer.name = "retrieval_recall"  # type: ignore[attr-defined]
    return scorer


def no_pii_leak(patterns_field: str = "forbidden") -> Scorer:
    def scorer(case: Any, output: Any) -> Score:
        text = str(_get(output, "answer", "")) + str(_get(output, "summary", ""))
        forbidden = _get(case, patterns_field, []) or []
        leaked = [f for f in forbidden if f.lower() in text.lower()]
        return Score("no_pii_leak", 0.0 if leaked else 1.0, not leaked, f"leaked={leaked}")

    scorer.name = "no_pii_leak"  # type: ignore[attr-defined]
    return scorer


# --- agent / workflow ------------------------------------------------------

def routing_accuracy(expected_field: str = "expected_route", got_field: str = "route") -> Scorer:
    def scorer(case: Any, output: Any) -> Score:
        want = _get(case, expected_field)
        got = _get(output, got_field)
        return Score("routing_accuracy", 1.0 if want == got else 0.0, want == got, f"{got} vs {want}")

    scorer.name = "routing_accuracy"  # type: ignore[attr-defined]
    return scorer


def field_equals(field: str, expected_field: str, name: str | None = None) -> Scorer:
    label = name or f"{field}_match"

    def scorer(case: Any, output: Any) -> Score:
        want, got = _get(case, expected_field), _get(output, field)
        ok = want == got
        return Score(label, 1.0 if ok else 0.0, ok, f"{got!r} vs {want!r}")

    scorer.name = label  # type: ignore[attr-defined]
    return scorer


def cost_budget(max_usd: float) -> Scorer:
    def scorer(case: Any, output: Any) -> Score:
        spent = float(_get(output, "cost_usd", 0.0) or 0.0)
        ok = spent <= max_usd
        return Score("cost_budget", 1.0 if ok else 0.0, ok, f"${spent:.4f} <= ${max_usd:.4f}")

    scorer.name = "cost_budget"  # type: ignore[attr-defined]
    return scorer


def latency_budget(max_ms: float) -> Scorer:
    def scorer(case: Any, output: Any) -> Score:
        took = float(_get(output, "latency_ms", 0.0) or 0.0)
        ok = took <= max_ms
        return Score("latency_budget", 1.0 if ok else 0.0, ok, f"{took:.0f}ms <= {max_ms:.0f}ms")

    scorer.name = "latency_budget"  # type: ignore[attr-defined]
    return scorer


def llm_judge(gateway_factory: Callable[[], Any], rubric: str, threshold: float = 0.7) -> Scorer:
    """LLM-as-judge, used sparingly and never as the only gate.

    Judges drift with the model behind them, so pin the judge model separately from the
    system under test and re-measure agreement against human labels each time it changes.
    """
    from pydantic import BaseModel, Field  # noqa: PLC0415

    class Verdict(BaseModel):
        score: float = Field(ge=0, le=1)
        reasoning: str

    def scorer(case: Any, output: Any) -> Score:
        gw = gateway_factory()
        verdict = gw.structured(
            system=(
                "You are a strict evaluator. Score the response against the rubric. "
                "Return JSON: {\"score\": 0..1, \"reasoning\": \"...\"}. Be harsh; 1.0 means flawless."
            ),
            user=f"RUBRIC:\n{rubric}\n\nQUESTION:\n{_get(case, 'question', '')}\n\n"
                 f"RESPONSE:\n{_get(output, 'answer', '')}",
            schema=Verdict,
            label="eval.judge",
        )
        return _score("llm_judge", verdict.score, threshold, verdict.reasoning[:200])

    scorer.name = "llm_judge"  # type: ignore[attr-defined]
    return scorer


_STOPWORDS = {
    "the", "and", "for", "are", "was", "were", "with", "that", "this", "from", "have", "has",
    "had", "not", "but", "you", "your", "our", "its", "their", "there", "which", "when", "what",
    "who", "how", "why", "can", "will", "would", "should", "may", "must", "any", "all", "per",
    "into", "over", "under", "than", "then", "they", "them", "been", "being", "does", "did",
}


def _get(obj: Any, field: str, default: Any = None) -> Any:
    if isinstance(obj, dict):
        return obj.get(field, default)
    return getattr(obj, field, default)
