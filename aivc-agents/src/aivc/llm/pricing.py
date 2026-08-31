"""Token -> money.

IMPORTANT: the numbers below are *configuration, not fact*. Provider list prices change and
enterprise agreements differ. On every engagement, set these from the client's actual
contracted rate card (AIVC_PRICING_FILE, a JSON file with the same shape) before anyone
quotes a cost-per-transaction figure to a CFO.
"""

from __future__ import annotations

import json
import os
from dataclasses import dataclass

from .base import Usage


@dataclass(frozen=True, slots=True)
class ModelPrice:
    """USD per 1,000,000 tokens."""

    input_per_mtok: float
    output_per_mtok: float
    cached_input_per_mtok: float = 0.0


# Placeholder rate card. Replace per engagement.
DEFAULT_PRICING: dict[str, ModelPrice] = {
    "offline-deterministic-v1": ModelPrice(0.0, 0.0, 0.0),
    "auto": ModelPrice(0.0, 0.0, 0.0),
    "__ollama__": ModelPrice(0.0, 0.0, 0.0),
    "__default__": ModelPrice(3.0, 15.0, 0.30),
}

_UNPRICED_SEEN: set[str] = set()


def _load_pricing() -> dict[str, ModelPrice]:
    path = os.environ.get("AIVC_PRICING_FILE")
    table = dict(DEFAULT_PRICING)
    if path and os.path.exists(path):
        with open(path) as fh:
            for model, row in json.load(fh).items():
                table[model] = ModelPrice(**row)
    return table


PRICING = _load_pricing()


def price_for(model: str) -> ModelPrice:
    if model in PRICING:
        return PRICING[model]
    # Prefix match so "claude-x-20990101" picks up a "claude-x" entry.
    for key, price in PRICING.items():
        if key != "__default__" and model.startswith(key):
            return price
    _UNPRICED_SEEN.add(model)
    return PRICING["__default__"]


def estimate_cost_usd(model: str, usage: Usage) -> float:
    p = price_for(model)
    fresh_input = max(usage.input_tokens - usage.cached_input_tokens, 0)
    return (
        fresh_input * p.input_per_mtok
        + usage.cached_input_tokens * p.cached_input_per_mtok
        + usage.output_tokens * p.output_per_mtok
    ) / 1_000_000


def unpriced_models() -> set[str]:
    """Models that fell through to the default rate. Surface these in the handover report."""
    return set(_UNPRICED_SEEN)
