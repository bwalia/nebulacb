"""Document parsing and chunking.

Chunking is the single highest-leverage decision in a RAG build and the one most often made
by accepting a library default. The strategy here is structure-first: split on the document's
own heading hierarchy, then window only within a section that is too long. That keeps a
policy clause intact, and it means every chunk carries the heading path, so the model sees
"POL-EXP-114 > Approval thresholds" rather than an anonymous slab of text.

Chunk ids are stable and human-readable (`POL-EXP-114#2.0`), because a citation a user can
look up in the source system is worth more than a UUID.
"""

from __future__ import annotations

import hashlib
import re
from dataclasses import dataclass
from pathlib import Path
from typing import Any

from aivc.store.index import Chunk

FRONT_MATTER_RE = re.compile(r"\A---\s*\n(.*?)\n---\s*\n", re.S)
HEADING_RE = re.compile(r"^(#{1,6})\s+(.*)$", re.M)


@dataclass
class ParsedDoc:
    doc_id: str
    title: str
    source: str
    body: str
    acl: tuple[str, ...]
    effective_date: str | None
    metadata: dict[str, Any]
    content_hash: str


def parse_front_matter(text: str) -> tuple[dict[str, Any], str]:
    """Minimal YAML-subset front matter: scalars and inline lists.

    Deliberately not a full YAML parser -- a dependency-free reader that fails loudly on
    anything it does not understand beats one that silently mis-parses an ACL.
    """
    match = FRONT_MATTER_RE.match(text)
    if not match:
        return {}, text
    meta: dict[str, Any] = {}
    for line in match.group(1).splitlines():
        line = line.strip()
        if not line or line.startswith("#"):
            continue
        if ":" not in line:
            raise ValueError(f"unparseable front matter line: {line!r}")
        key, _, raw = line.partition(":")
        raw = raw.strip()
        if raw.startswith("[") and raw.endswith("]"):
            meta[key.strip()] = [v.strip().strip("'\"") for v in raw[1:-1].split(",") if v.strip()]
        else:
            meta[key.strip()] = raw.strip("'\"")
    return meta, text[match.end() :]


def parse_document(path: Path) -> ParsedDoc:
    raw = path.read_text()
    meta, body = parse_front_matter(raw)
    doc_id = meta.get("doc_id") or path.stem
    return ParsedDoc(
        doc_id=doc_id,
        title=meta.get("title", doc_id),
        source=meta.get("source", str(path.name)),
        body=body,
        acl=tuple(meta.get("acl", []) or []),
        effective_date=meta.get("effective_date"),
        metadata={k: v for k, v in meta.items()
                  if k not in {"doc_id", "title", "source", "acl", "effective_date"}},
        content_hash=hashlib.sha256(raw.encode()).hexdigest()[:16],
    )


def approx_tokens(text: str) -> int:
    """~4 chars/token. Good enough for chunk sizing; use the provider's tokenizer when the
    budget is tight enough that the difference matters."""
    return max(1, len(text) // 4)


def split_sections(body: str) -> list[tuple[str, str]]:
    """Return (heading_path, text) pairs following the document's own structure."""
    matches = list(HEADING_RE.finditer(body))
    if not matches:
        return [("", body.strip())]
    sections: list[tuple[str, str]] = []
    preamble = body[: matches[0].start()].strip()
    if preamble:
        sections.append(("", preamble))
    path: list[str] = []
    for i, m in enumerate(matches):
        level, heading = len(m.group(1)), m.group(2).strip()
        path = path[: level - 1] + [heading]
        end = matches[i + 1].start() if i + 1 < len(matches) else len(body)
        text = body[m.end() : end].strip()
        if text:
            sections.append((" > ".join(path[1:]) or heading, text))
    return sections


def window(text: str, max_tokens: int, overlap_tokens: int) -> list[str]:
    """Sentence-aligned sliding window; only invoked for sections that overflow."""
    if approx_tokens(text) <= max_tokens:
        return [text]
    sentences = re.split(r"(?<=[.!?])\s+", text)
    chunks: list[str] = []
    current: list[str] = []
    current_tokens = 0
    for sentence in sentences:
        st = approx_tokens(sentence)
        if current and current_tokens + st > max_tokens:
            chunks.append(" ".join(current))
            # Carry back whole sentences worth of overlap so no clause is split mid-thought.
            carry: list[str] = []
            carried = 0
            for s in reversed(current):
                if carried >= overlap_tokens:
                    break
                carry.insert(0, s)
                carried += approx_tokens(s)
            current, current_tokens = carry, carried
        current.append(sentence)
        current_tokens += st
    if current:
        chunks.append(" ".join(current))
    return chunks


def chunk_document(
    doc: ParsedDoc, max_tokens: int = 320, overlap_tokens: int = 60
) -> list[Chunk]:
    chunks: list[Chunk] = []
    for s_idx, (section, text) in enumerate(split_sections(doc.body)):
        for p_idx, part in enumerate(window(text, max_tokens, overlap_tokens)):
            chunks.append(
                Chunk(
                    id=f"{doc.doc_id}#{s_idx}.{p_idx}",
                    doc_id=doc.doc_id,
                    text=part,
                    title=doc.title,
                    section=section,
                    source=doc.source,
                    ordinal=len(chunks),
                    acl=doc.acl,
                    effective_date=doc.effective_date,
                    metadata={**doc.metadata, "content_hash": doc.content_hash},
                )
            )
    return chunks
