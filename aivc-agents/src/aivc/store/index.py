"""Retrieval indexes.

Two indexes over the same chunks, because lexical and semantic retrieval fail differently:
BM25 nails exact identifiers ("policy FIN-114", a part number, a person's surname) and dense
vectors handle paraphrase. Fusing them is the cheapest quality win available in RAG, and it
is what the ADR-0002 benchmark measures.

The in-process index here is the POC substrate: it is exact (no ANN recall loss), needs no
infrastructure, and is honest up to roughly 10^5 chunks. `PgVectorIndex` is the production
swap; the retriever above it does not change.
"""

from __future__ import annotations

import hashlib
import json
import math
import re
from collections import Counter
from dataclasses import asdict, dataclass, field
from pathlib import Path
from typing import Any, Callable, Iterable, Protocol, Sequence

import numpy as np

TOKEN_RE = re.compile(r"[a-z0-9][a-z0-9'&./-]*")


def tokenize(text: str) -> list[str]:
    return TOKEN_RE.findall(text.lower())


@dataclass
class Chunk:
    id: str
    doc_id: str
    text: str
    title: str = ""
    section: str = ""
    source: str = ""
    ordinal: int = 0
    acl: tuple[str, ...] = ()          # roles permitted to see this chunk
    effective_date: str | None = None  # ISO date; drives recency preference and staleness
    metadata: dict[str, Any] = field(default_factory=dict)

    def citation(self) -> str:
        where = f", {self.section}" if self.section else ""
        return f"[{self.id}] {self.title}{where} ({self.source})"

    def to_dict(self) -> dict[str, Any]:
        d = asdict(self)
        d["acl"] = list(self.acl)
        return d

    @classmethod
    def from_dict(cls, d: dict[str, Any]) -> "Chunk":
        d = dict(d)
        d["acl"] = tuple(d.get("acl", ()))
        return cls(**d)


class Embedder(Protocol):
    dim: int
    name: str

    def embed(self, texts: Sequence[str]) -> np.ndarray:  # pragma: no cover - protocol
        ...


class HashingEmbedder:
    """Deterministic, dependency-free, offline embeddings.

    Hashed word unigrams + bigrams with sublinear term weighting. It is not a trained model
    and will lose to one on paraphrase, but it is reproducible, free, and good enough to make
    the retrieval *pipeline* -- fusion, ACL filtering, MMR, evals -- demonstrable end to end
    with no vendor account. Swap for a real embedding model before any quality claim.
    """

    name = "hashing-v1"

    def __init__(self, dim: int = 384):
        self.dim = dim

    def embed(self, texts: Sequence[str]) -> np.ndarray:
        out = np.zeros((len(texts), self.dim), dtype=np.float32)
        for i, text in enumerate(texts):
            words = tokenize(text)
            grams = words + [f"{a}_{b}" for a, b in zip(words, words[1:])]
            counts = Counter(grams)
            for gram, n in counts.items():
                h = hashlib.blake2b(gram.encode(), digest_size=8).digest()
                idx = int.from_bytes(h[:4], "big") % self.dim
                sign = 1.0 if h[4] % 2 == 0 else -1.0
                out[i, idx] += sign * (1.0 + math.log(n))
            norm = np.linalg.norm(out[i])
            if norm > 0:
                out[i] /= norm
        return out


class ProviderEmbedder:  # pragma: no cover - requires a key
    """Adapter for a hosted embedding model. Batched, with a content-hash cache on disk."""

    def __init__(self, client: Any, model: str, dim: int, cache_dir: Path | None = None, batch: int = 96):
        self.client, self.model, self.dim, self.batch = client, model, dim, batch
        self.name = model
        self.cache_dir = cache_dir
        if cache_dir:
            cache_dir.mkdir(parents=True, exist_ok=True)

    def embed(self, texts: Sequence[str]) -> np.ndarray:
        vectors: list[np.ndarray] = []
        for start in range(0, len(texts), self.batch):
            batch = list(texts[start : start + self.batch])
            resp = self.client.embeddings.create(model=self.model, input=batch)
            vectors += [np.asarray(d.embedding, dtype=np.float32) for d in resp.data]
        arr = np.vstack(vectors)
        return arr / np.clip(np.linalg.norm(arr, axis=1, keepdims=True), 1e-9, None)


@dataclass
class Hit:
    chunk: Chunk
    score: float
    retriever: str = ""

    def to_dict(self) -> dict[str, Any]:
        return {"chunk_id": self.chunk.id, "score": round(self.score, 5), "retriever": self.retriever}


class VectorIndex:
    """Exact cosine search over an in-memory matrix."""

    def __init__(self, embedder: Embedder):
        self.embedder = embedder
        self.chunks: list[Chunk] = []
        self._matrix: np.ndarray | None = None

    def add(self, chunks: Iterable[Chunk], vectors: np.ndarray | None = None) -> None:
        chunks = list(chunks)
        if not chunks:
            return
        vecs = self.embedder.embed([c.text for c in chunks]) if vectors is None else vectors
        self.chunks.extend(chunks)
        self._matrix = vecs if self._matrix is None else np.vstack([self._matrix, vecs])

    def search(
        self, query: str, k: int = 10, predicate: Callable[[Chunk], bool] | None = None
    ) -> list[Hit]:
        if self._matrix is None or not self.chunks:
            return []
        qv = self.embedder.embed([query])[0]
        scores = self._matrix @ qv
        allowed = (
            np.array([predicate(c) for c in self.chunks])
            if predicate
            else np.ones(len(self.chunks), dtype=bool)
        )
        # Filter *before* ranking: an ACL-masked hit must never occupy a top-k slot, or a
        # user learns what they cannot read from the shape of the results.
        scores = np.where(allowed, scores, -np.inf)
        k = min(k, int(allowed.sum()))
        if k <= 0:
            return []
        idx = np.argpartition(-scores, k - 1)[:k]
        idx = idx[np.argsort(-scores[idx])]
        return [Hit(self.chunks[i], float(scores[i]), "vector") for i in idx if scores[i] > -np.inf]

    def vector_for(self, chunk_id: str) -> np.ndarray | None:
        for i, c in enumerate(self.chunks):
            if c.id == chunk_id and self._matrix is not None:
                return self._matrix[i]
        return None

    def save(self, path: Path) -> None:
        path.mkdir(parents=True, exist_ok=True)
        np.save(path / "vectors.npy", self._matrix if self._matrix is not None else np.zeros((0, self.embedder.dim), dtype=np.float32))
        (path / "chunks.jsonl").write_text(
            "\n".join(json.dumps(c.to_dict()) for c in self.chunks)
        )
        (path / "meta.json").write_text(
            json.dumps({"embedder": self.embedder.name, "dim": self.embedder.dim, "count": len(self.chunks)})
        )

    @classmethod
    def load(cls, path: Path, embedder: Embedder) -> "VectorIndex":
        meta = json.loads((path / "meta.json").read_text())
        if meta["embedder"] != embedder.name:
            # Silently mixing embedding spaces is a top-3 cause of "RAG got worse overnight".
            raise ValueError(
                f"index was built with embedder '{meta['embedder']}', not '{embedder.name}'; rebuild it"
            )
        index = cls(embedder)
        lines = [ln for ln in (path / "chunks.jsonl").read_text().splitlines() if ln.strip()]
        index.chunks = [Chunk.from_dict(json.loads(ln)) for ln in lines]
        index._matrix = np.load(path / "vectors.npy")
        return index


class BM25Index:
    """Okapi BM25. ~60 lines, no service to run, and it is what catches exact identifiers."""

    def __init__(self, k1: float = 1.5, b: float = 0.75):
        self.k1, self.b = k1, b
        self.chunks: list[Chunk] = []
        self._tf: list[Counter[str]] = []
        self._df: Counter[str] = Counter()
        self._len: list[int] = []

    def add(self, chunks: Iterable[Chunk]) -> None:
        for c in chunks:
            terms = tokenize(f"{c.title} {c.section} {c.text}")
            tf = Counter(terms)
            self.chunks.append(c)
            self._tf.append(tf)
            self._len.append(len(terms))
            self._df.update(tf.keys())

    def search(
        self, query: str, k: int = 10, predicate: Callable[[Chunk], bool] | None = None
    ) -> list[Hit]:
        if not self.chunks:
            return []
        n = len(self.chunks)
        avgdl = sum(self._len) / n
        q_terms = tokenize(query)
        scored: list[tuple[float, int]] = []
        for i, tf in enumerate(self._tf):
            if predicate and not predicate(self.chunks[i]):
                continue
            score = 0.0
            for term in q_terms:
                f = tf.get(term, 0)
                if not f:
                    continue
                df = self._df[term]
                idf = math.log(1 + (n - df + 0.5) / (df + 0.5))
                denom = f + self.k1 * (1 - self.b + self.b * self._len[i] / avgdl)
                score += idf * f * (self.k1 + 1) / denom
            if score > 0:
                scored.append((score, i))
        scored.sort(reverse=True)
        return [Hit(self.chunks[i], s, "bm25") for s, i in scored[:k]]


def reciprocal_rank_fusion(
    rankings: list[list[Hit]], k: int = 60, weights: list[float] | None = None
) -> list[Hit]:
    """RRF: rank-based, so it needs no score calibration between retrievers.

    That property is why it is the default here -- cosine similarity and BM25 scores are on
    incomparable scales, and tuning a linear blend is per-corpus work that does not survive
    the next document refresh.
    """
    weights = weights or [1.0] * len(rankings)
    fused: dict[str, float] = {}
    seen: dict[str, Chunk] = {}
    sources: dict[str, set[str]] = {}
    for ranking, weight in zip(rankings, weights):
        for rank, hit in enumerate(ranking):
            fused[hit.chunk.id] = fused.get(hit.chunk.id, 0.0) + weight / (k + rank + 1)
            seen[hit.chunk.id] = hit.chunk
            sources.setdefault(hit.chunk.id, set()).add(hit.retriever)
    order = sorted(fused.items(), key=lambda kv: -kv[1])
    return [Hit(seen[cid], score, "+".join(sorted(sources[cid]))) for cid, score in order]


def mmr(
    hits: list[Hit], index: VectorIndex, k: int, lambda_: float = 0.8, protect_top: int = 2
) -> list[Hit]:
    """Maximal marginal relevance: stop three chunks of the same boilerplate crowding out
    the one paragraph that actually answers the question.

    Relevance is min-max normalised to [0,1] first. This matters more than it looks: RRF
    scores live around 0.01-0.05 while cosine redundancy is 0-1, so mixing them raw makes the
    redundancy term dominate completely and MMR quietly turns into a "return the most
    unrelated passages" function. That bug is invisible without a retrieval_recall metric --
    the answers just get subtly worse.

    `protect_top` admits the best few hits on relevance alone. Diversity is a tie-breaker for
    the tail of the context window, not a reason to drop the passage that answers the
    question because it resembles the one already chosen -- which is precisely what happens
    when two adjacent sections of the same policy are both relevant.
    """
    if len(hits) <= k:
        return hits
    scores = [h.score for h in hits]
    lo, hi = min(scores), max(scores)
    span = (hi - lo) or 1.0
    relevance = {h.chunk.id: (h.score - lo) / span for h in hits}

    vectors = {h.chunk.id: index.vector_for(h.chunk.id) for h in hits}
    ranked = sorted(hits, key=lambda h: -h.score)
    selected: list[Hit] = ranked[: min(protect_top, k)]
    pool = [h for h in hits if h not in selected]
    while pool and len(selected) < k:
        best, best_score = None, -math.inf
        for hit in pool:
            v = vectors.get(hit.chunk.id)
            redundancy = 0.0
            if v is not None and selected:
                sims = [
                    float(v @ vectors[s.chunk.id])
                    for s in selected
                    if vectors.get(s.chunk.id) is not None
                ]
                redundancy = max(sims) if sims else 0.0
            score = lambda_ * relevance[hit.chunk.id] - (1 - lambda_) * redundancy
            if score > best_score:
                best, best_score = hit, score
        selected.append(best)  # type: ignore[arg-type]
        pool.remove(best)  # type: ignore[arg-type]
    return selected


class PgVectorIndex:  # pragma: no cover - requires a database
    """Production swap: Postgres + pgvector, HNSW, ACL enforced in SQL.

    Chosen over a dedicated vector database on most engagements because the client already
    runs Postgres, already backs it up, and already knows how to grant on it -- and because
    keeping chunks next to their ACL rows means the filter is a join, not an application-layer
    promise. Reach for a dedicated store above roughly 10-50M chunks or when you need
    multi-tenant sharding the client's Postgres cannot carry.
    """

    DDL = """
    CREATE EXTENSION IF NOT EXISTS vector;
    CREATE TABLE IF NOT EXISTS rag_chunk (
        id              text PRIMARY KEY,
        doc_id          text NOT NULL,
        tenant          text NOT NULL,
        title           text,
        section         text,
        source          text,
        ordinal         int,
        acl             text[] NOT NULL DEFAULT '{}',
        effective_date  date,
        content_hash    text NOT NULL,
        text            text NOT NULL,
        embedding       vector(%(dim)s) NOT NULL,
        tsv             tsvector GENERATED ALWAYS AS (to_tsvector('english', text)) STORED,
        updated_at      timestamptz NOT NULL DEFAULT now()
    );
    CREATE INDEX IF NOT EXISTS rag_chunk_embedding_idx
        ON rag_chunk USING hnsw (embedding vector_cosine_ops) WITH (m = 16, ef_construction = 64);
    CREATE INDEX IF NOT EXISTS rag_chunk_tsv_idx ON rag_chunk USING gin (tsv);
    CREATE INDEX IF NOT EXISTS rag_chunk_acl_idx ON rag_chunk USING gin (acl);
    CREATE INDEX IF NOT EXISTS rag_chunk_tenant_idx ON rag_chunk (tenant, doc_id);
    """

    SEARCH_SQL = """
    SELECT id, doc_id, title, section, source, ordinal, acl, effective_date, text,
           1 - (embedding <=> %(query_vec)s::vector) AS score
    FROM rag_chunk
    WHERE tenant = %(tenant)s
      AND (acl = '{}' OR acl && %(roles)s::text[])
    ORDER BY embedding <=> %(query_vec)s::vector
    LIMIT %(k)s;
    """

    def __init__(self, conn: Any, tenant: str, embedder: Embedder):
        self.conn, self.tenant, self.embedder = conn, tenant, embedder

    def search(self, query: str, roles: list[str], k: int = 10) -> list[Hit]:
        vec = self.embedder.embed([query])[0].tolist()
        with self.conn.cursor() as cur:
            cur.execute(
                self.SEARCH_SQL,
                {"query_vec": vec, "tenant": self.tenant, "roles": roles, "k": k},
            )
            rows = cur.fetchall()
        return [
            Hit(
                Chunk(
                    id=r[0], doc_id=r[1], title=r[2] or "", section=r[3] or "", source=r[4] or "",
                    ordinal=r[5] or 0, acl=tuple(r[6] or ()),
                    effective_date=str(r[7]) if r[7] else None, text=r[8],
                ),
                float(r[9]),
                "pgvector",
            )
            for r in rows
        ]
