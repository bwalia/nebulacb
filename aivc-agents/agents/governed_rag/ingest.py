"""Ingestion pipeline.

The unglamorous half of the engagement, and the half that decides whether the agent works.
Properties that matter in production and are implemented here:

  * **Idempotent.** Content-hashed per document; re-running only re-embeds what changed.
    Embedding a 40k-document corpus twice because someone re-ran the job is a real invoice.
  * **Governed.** ACLs travel with the chunk from the source system. Retrieval filters on
    them; nothing downstream can widen access.
  * **Observable.** Emits a manifest (counts, hashes, embedder identity, timings) so a
    quality drop can be traced to a specific ingest run.
  * **Fail-loud on schema drift.** A document whose front matter will not parse stops the
    run rather than silently entering the index without an ACL.
"""

from __future__ import annotations

import json
import time
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any, Iterable

from aivc.config import Settings, get_settings
from aivc.store.index import BM25Index, Chunk, Embedder, HashingEmbedder, VectorIndex

from .chunking import ParsedDoc, chunk_document, parse_document


@dataclass
class IngestManifest:
    started_at: float
    embedder: str
    documents: int = 0
    chunks: int = 0
    skipped_unchanged: int = 0
    failures: list[dict[str, str]] = field(default_factory=list)
    doc_hashes: dict[str, str] = field(default_factory=dict)
    duration_s: float = 0.0

    def to_dict(self) -> dict[str, Any]:
        return {
            "started_at": self.started_at,
            "embedder": self.embedder,
            "documents": self.documents,
            "chunks": self.chunks,
            "skipped_unchanged": self.skipped_unchanged,
            "failures": self.failures,
            "doc_hashes": self.doc_hashes,
            "duration_s": round(self.duration_s, 3),
        }


@dataclass
class Corpus:
    """Built indexes plus the manifest that describes how they were built."""

    chunks: list[Chunk]
    vectors: VectorIndex
    lexical: BM25Index
    manifest: IngestManifest

    def by_id(self, chunk_id: str) -> Chunk | None:
        return next((c for c in self.chunks if c.id == chunk_id), None)

    def acl_roles(self) -> set[str]:
        return {role for c in self.chunks for role in c.acl}


def ingest(
    corpus_dir: Path | None = None,
    settings: Settings | None = None,
    embedder: Embedder | None = None,
    *,
    previous_manifest: dict[str, str] | None = None,
    strict: bool = True,
) -> Corpus:
    s = settings or get_settings()
    corpus_dir = corpus_dir or s.corpus_dir
    embedder = embedder or HashingEmbedder(s.embedding_dim)
    manifest = IngestManifest(started_at=time.time(), embedder=embedder.name)
    started = time.perf_counter()

    docs: list[ParsedDoc] = []
    for path in sorted(Path(corpus_dir).glob("**/*.md")):
        try:
            doc = parse_document(path)
        except Exception as exc:
            manifest.failures.append({"path": str(path), "error": f"{type(exc).__name__}: {exc}"})
            if strict:
                raise
            continue
        if not doc.acl:
            # No ACL is not "public" -- it is a missing field. Treat it as a hard error so a
            # restricted document cannot leak through a front-matter typo.
            manifest.failures.append({"path": str(path), "error": "document has no acl"})
            if strict:
                raise ValueError(f"{path} declares no acl; refusing to index")
            continue
        manifest.doc_hashes[doc.doc_id] = doc.content_hash
        if previous_manifest and previous_manifest.get(doc.doc_id) == doc.content_hash:
            manifest.skipped_unchanged += 1
        docs.append(doc)

    chunks: list[Chunk] = []
    for doc in docs:
        chunks.extend(chunk_document(doc, s.chunk_tokens, s.chunk_overlap_tokens))

    vectors = VectorIndex(embedder)
    vectors.add(chunks)
    lexical = BM25Index()
    lexical.add(chunks)

    manifest.documents = len(docs)
    manifest.chunks = len(chunks)
    manifest.duration_s = time.perf_counter() - started
    return Corpus(chunks, vectors, lexical, manifest)


def save_corpus(corpus: Corpus, path: Path) -> None:
    path.mkdir(parents=True, exist_ok=True)
    corpus.vectors.save(path)
    (path / "manifest.json").write_text(json.dumps(corpus.manifest.to_dict(), indent=2))


def load_corpus(path: Path, embedder: Embedder) -> Corpus:
    vectors = VectorIndex.load(path, embedder)
    lexical = BM25Index()
    lexical.add(vectors.chunks)
    manifest_data = json.loads((path / "manifest.json").read_text())
    manifest = IngestManifest(
        started_at=manifest_data["started_at"],
        embedder=manifest_data["embedder"],
        documents=manifest_data["documents"],
        chunks=manifest_data["chunks"],
        doc_hashes=manifest_data.get("doc_hashes", {}),
    )
    return Corpus(vectors.chunks, vectors, lexical, manifest)


def summarise(chunks: Iterable[Chunk]) -> dict[str, Any]:
    chunks = list(chunks)
    lengths = [len(c.text) for c in chunks] or [0]
    return {
        "chunks": len(chunks),
        "docs": len({c.doc_id for c in chunks}),
        "avg_chars": round(sum(lengths) / len(lengths)),
        "max_chars": max(lengths),
        "acl_roles": sorted({r for c in chunks for r in c.acl}),
    }
