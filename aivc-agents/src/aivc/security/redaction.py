"""Reversible redaction at the provider boundary.

Pattern-based redaction is not a compliance programme, and this module does not pretend
otherwise -- it is a defence-in-depth control that keeps obvious identifiers out of a
third-party provider's logs. Where a client has a real DLP service, swap `Redactor` for a
thin adapter to it and keep the same call sites.

Reversible so the agent can still write a correct answer: placeholders go to the model,
real values are restored in the response rendered to an authorised user.
"""

from __future__ import annotations

import re
from dataclasses import dataclass, field

PATTERNS: dict[str, re.Pattern[str]] = {
    "EMAIL": re.compile(r"\b[\w.%+-]+@[\w.-]+\.[A-Za-z]{2,}\b"),
    "IBAN": re.compile(r"\b[A-Z]{2}\d{2}[A-Z0-9]{11,30}\b"),
    "CARD": re.compile(r"\b(?:\d[ -]?){13,19}\b"),
    "UK_NI": re.compile(r"\b[A-CEGHJ-PR-TW-Z]{2}\d{6}[A-D]\b"),
    # Requires at least one separator. Without that condition it swallows any long digit
    # run -- order numbers, part numbers, unvalidated card-shaped strings -- and mangling
    # business identifiers is its own kind of data loss.
    "PHONE": re.compile(
        r"(?<!\w)(?:\+\d{1,3}[\s-]?)?\(?\d{2,5}\)?[\s-](?:\d{2,4}[\s-]?){1,3}\d{2,4}(?!\w)"
    ),
    "SECRET": re.compile(r"\b(?:sk|pk|ghp|xox[baprs])[-_][A-Za-z0-9_-]{16,}\b"),
}

# Order matters: match the most specific patterns first so a card number is not eaten
# by the phone pattern.
ORDER = ["SECRET", "EMAIL", "IBAN", "UK_NI", "CARD", "PHONE"]


@dataclass
class RedactionResult:
    text: str
    mapping: dict[str, str] = field(default_factory=dict)  # placeholder -> original
    counts: dict[str, int] = field(default_factory=dict)

    @property
    def redacted_any(self) -> bool:
        return bool(self.mapping)


class Redactor:
    def __init__(self, kinds: list[str] | None = None):
        self.kinds = kinds or ORDER

    def redact(self, text: str) -> RedactionResult:
        mapping: dict[str, str] = {}
        counts: dict[str, int] = {}
        reverse: dict[str, str] = {}

        def make_sub(kind: str):
            def _sub(match: re.Match[str]) -> str:
                original = match.group(0)
                if kind == "CARD" and not _luhn(original):
                    return original
                if original in reverse:
                    return reverse[original]
                counts[kind] = counts.get(kind, 0) + 1
                token = f"[{kind}_{counts[kind]}]"
                mapping[token] = original
                reverse[original] = token
                return token

            return _sub

        out = text
        for kind in self.kinds:
            out = PATTERNS[kind].sub(make_sub(kind), out)
        return RedactionResult(out, mapping, counts)

    @staticmethod
    def restore(text: str, mapping: dict[str, str]) -> str:
        for token, original in mapping.items():
            text = text.replace(token, original)
        return text


def _luhn(value: str) -> bool:
    digits = [int(c) for c in re.sub(r"\D", "", value)]
    if not 13 <= len(digits) <= 19:
        return False
    total, parity = 0, len(digits) % 2
    for i, d in enumerate(digits):
        if i % 2 == parity:
            d *= 2
            if d > 9:
                d -= 9
        total += d
    return total % 10 == 0
