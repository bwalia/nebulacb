"""Offline behaviour for the RAG agent.

Extractive, not generative: it selects the best-matching sentences from the retrieved
passages and cites them. That is deliberately a *weaker* answerer than a real model -- it
cannot paraphrase or synthesise across passages -- but it exercises the entire pipeline
(retrieval, ACL, citation verification, groundedness gate, refusal path) deterministically
and for free, which is exactly what a CI gate and a no-egress demo need.

Point AIVC_PROVIDER at a hosted model and nothing above this file changes.
"""

from __future__ import annotations

import json
import re

from aivc.evals.scorers import _STOPWORDS
from aivc.llm.base import LLMRequest
from aivc.llm.offline import register
from aivc.store.index import tokenize

from .agent import MARKER

BLOCK_SPLIT = re.compile(r"\n\n---\n\n")
ID_RE = re.compile(r"^\[([^\]]+)\]")
SENTENCE_SPLIT = re.compile(r"(?<=[.!?])\s+")
MIN_OVERLAP = 2


def _content_terms(text: str) -> set[str]:
    # Short alphanumeric tokens are kept: "s1", "8d", "po" are exactly the identifiers a
    # policy question turns on, and dropping them by length is a classic silent recall bug.
    return {
        t
        for t in tokenize(text)
        if t not in _STOPWORDS and (len(t) > 2 or any(ch.isdigit() for ch in t))
    }


def _candidate_units(text: str) -> list[str]:
    """Sentences, plus adjacent pairs.

    Enterprise documents put the label and the value in different sentences far more often
    than a demo corpus suggests ("S1: production stoppage." / "Response within 4 hours.").
    Scoring pairs as well as singles is what stops that pattern from being unanswerable.
    """
    sentences = [s.strip() for s in SENTENCE_SPLIT.split(text) if s.strip()]
    pairs = [f"{a} {b}" for a, b in zip(sentences, sentences[1:])]
    return sentences + pairs


def _answer(request: LLMRequest) -> str:
    user = request.last_user_text()
    question = user.split("QUESTION:")[-1].strip()
    context = user.split("CONTEXT PASSAGES:")[-1].split("QUESTION:")[0].strip()
    q_terms = _content_terms(question)

    scored: list[tuple[float, str, str]] = []  # (score, chunk_id, sentence)
    for block in BLOCK_SPLIT.split(context):
        lines = block.strip().split("\n", 1)
        if len(lines) < 2:
            continue
        match = ID_RE.match(lines[0].strip())
        if not match:
            continue
        chunk_id = match.group(1)
        for sentence in _candidate_units(lines[1].replace("\n", " ")):
            if len(sentence) < 25:
                continue
            overlap = q_terms & _content_terms(sentence)
            if len(overlap) < MIN_OVERLAP:
                continue
            # Normalise by sentence length so a long paragraph does not win on volume alone.
            score = len(overlap) / (len(_content_terms(sentence)) ** 0.5 or 1)
            scored.append((score, chunk_id, sentence))

    scored.sort(key=lambda t: -t[0])
    if not scored:
        return json.dumps(
            {
                "answer": "The provided passages do not cover this question.",
                "citations": [],
                "sufficient": False,
                "confidence": 0.0,
            }
        )

    picked: list[tuple[str, str]] = []
    seen_chunks: set[str] = set()
    for _, chunk_id, sentence in scored:
        if len(picked) >= 3:
            break
        if len(seen_chunks) >= 2 and chunk_id not in seen_chunks:
            continue
        # Sentences and sentence-pairs are both candidates, so the same text can win twice.
        # Drop anything that contains, or is contained by, something already selected.
        if any(sentence in prior or prior in sentence for _, prior in picked):
            continue
        picked.append((chunk_id, sentence))
        seen_chunks.add(chunk_id)

    answer = " ".join(f"{sentence} [{chunk_id}]" for chunk_id, sentence in picked)
    return json.dumps(
        {
            "answer": answer,
            "citations": sorted(seen_chunks),
            "sufficient": True,
            "confidence": round(min(0.95, 0.45 + scored[0][0] / 4), 2),
        }
    )


def install() -> None:
    register("governed_rag.answer", lambda req: MARKER in req.system, _answer, priority=10)


install()
