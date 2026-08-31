# ADR-0002 — Hybrid retrieval, structure-first chunking, and ACL filtering inside the index

**Status:** accepted · **Date:** 2026-08

## Context

Retrieval quality decides whether a policy assistant is trusted or quietly abandoned. Three
decisions dominate the outcome and are usually made by accepting a library default: how
documents are split, how candidates are found, and where access control is applied.

The corpus in an enterprise engagement looks like the one in `data/corpus/`: policy documents
with clause numbering, effective dates, supersession, and different audiences per document.
Questions are specific and turn on exact figures — thresholds, tolerances, notification
windows, identifiers like `S1` or `POL-EXP-114`.

## Decision

**Structure-first chunking.** Split on the document's own heading hierarchy, then window only
within a section that overflows the token budget, with sentence-aligned overlap. Chunk ids are
stable and human-readable (`POL-EXP-114#1.0`) and carry the heading path, so the model sees
`Employee Expenses Policy > 2. Approval thresholds` rather than an anonymous slab, and a user
can look a citation up in the source system.

**Hybrid retrieval, fused by RRF.** BM25 and dense cosine in parallel over the same chunks,
fused by reciprocal rank. They fail differently: BM25 nails exact identifiers and rare terms,
dense handles paraphrase. RRF is rank-based, so it needs no score calibration between two
retrievers whose scores are on incomparable scales — and score calibration is per-corpus work
that does not survive the next document refresh.

**MMR for diversity, with the top hits protected.** Diversity is a tie-breaker for the tail of
the context window, not a reason to drop the passage that answers the question.

**ACL filtering inside the index, before ranking.** Not a post-filter. A filtered chunk must
never occupy a top-k slot, and result counts must not reveal the existence of documents the
user cannot read.

**Refuse on weak evidence.** Three independent gates after generation: citations must exist in
what was retrieved, the answer must be lexically supported by its cited passages, and the
answer must be *responsive* to the question asked.

## Two bugs the eval suite caught, and why they matter

Both were invisible in spot-checking and obvious the moment `retrieval_recall` was measured
as a separate metric. They are the argument for component-level evals in one paragraph each.

**MMR score-scale mismatch.** MMR trades relevance against redundancy:
`λ·relevance − (1−λ)·redundancy`. Relevance came from RRF (~0.01–0.05); redundancy is a
cosine (0–1). The redundancy term dominated completely, so MMR was quietly returning the
*most unrelated* passages. Answers were still fluent, still cited, still grounded — just
answering from the wrong document. Fixed by min-max normalising relevance before the trade,
and by protecting the top 2 hits on relevance alone. Retrieval recall went from 0.86 to 0.95.

**RRF's smoothing constant was sized for a different corpus.** The textbook `k=60` is tuned
for TREC-scale runs. Against a few dozen candidates it flattens the ranking until a retriever
that was decisively right — BM25 scoring the correct chunk at 12.98 against 8.16 for the
next — contributes almost nothing. Fixed by scaling `k` to the candidate pool
(`max(10, candidate_k // 3)`). Recall went from 0.95 to 1.00.

**The lesson to carry into engagements:** end-to-end answer quality cannot tell you whether to
fix the retriever or the prompt. Measure retrieval recall against known-good chunk ids
separately, or you will spend a week tuning prompts around a ranking bug.

## The responsiveness gate

Groundedness asks "is this answer supported by its sources". It passes trivially for an
extract from an adjacent, visible-but-wrong policy — which is exactly what an ACL boundary
manufactures: the right document was filtered out, so the best remaining passage is merely
*similar*. A finance-restricted question about autonomous resolution limits came back, for an
ordinary employee, with three-way match tolerances from the procurement policy. No leak, no
hallucination, correct citation — and a wrong answer that looks right.

The fix is a second, cheap check in the opposite direction: what fraction of the question's
distinctive terms appear in the cited passages. On this corpus, answerable cases sit at
0.71–1.00 and the near-miss at 0.50, so the threshold is 0.60 — the midpoint of the observed
gap, not a guess. **Re-derive it per engagement** by running the client's own question set and
looking at the distribution; a threshold copied between corpora is worse than none.

## Consequences

- Two indexes to keep in sync; ingestion is idempotent and content-hashed so this is cheap.
- MMR and RRF have knobs (`mmr_lambda`, `rrf_k`, `protect_top`, `recency_boost`) that need a
  short tuning pass per corpus. They are constructor arguments, not constants, for that reason.
- Refusal thresholds trade coverage against trust. Both directions are measured
  (`refusal_correctness` scores over-refusal and unsafe answering separately), because a
  system that refuses too often is abandoned just as fast as one that invents answers.
- The index refuses to ingest a document with no ACL. This will stop a client's first
  ingestion run. That is the intended behaviour: a missing ACL is a missing field, not
  "public".

## Alternatives considered

**Dense-only.** Simpler, one index. Rejected: it loses exact identifiers, and enterprise
policy questions are full of them.

**A cross-encoder reranker.** Genuinely improves precision and is the first upgrade to make
once a real embedding model is in place. Out of scope here because it needs a model, and the
offline demo must not.

**Fixed-size chunking with overlap.** The common default. Rejected: it splits clauses across
chunks, which is precisely where the thresholds a policy question asks about live.

**Post-filtering results by ACL.** Simpler to bolt onto an existing index. Rejected: it burns
top-k slots on unreadable chunks and leaks existence through result counts.
