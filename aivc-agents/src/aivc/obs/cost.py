"""Per-run cost ledger with a hard budget.

Non-negotiable in production. An agent loop that can retry is an agent loop that can burn
money without bound; the ledger turns that from an invoice surprise into a caught exception.
Attribution is per (agent, model) so the client can see cost-per-transaction by workflow.
"""

from __future__ import annotations

import threading
from collections import defaultdict
from dataclasses import dataclass, field

from ..llm.base import Usage
from ..llm.pricing import estimate_cost_usd


class BudgetExceeded(RuntimeError):
    def __init__(self, spent: float, budget: float):
        super().__init__(f"run budget exhausted: ${spent:.4f} spent against ${budget:.4f} cap")
        self.spent = spent
        self.budget = budget


@dataclass
class LedgerEntry:
    label: str
    model: str
    usage: Usage
    cost_usd: float


@dataclass
class CostLedger:
    budget_usd: float = 1.0
    entries: list[LedgerEntry] = field(default_factory=list)
    _lock: threading.Lock = field(default_factory=threading.Lock, repr=False)

    def record(self, label: str, model: str, usage: Usage) -> float:
        cost = estimate_cost_usd(model, usage)
        with self._lock:
            self.entries.append(LedgerEntry(label, model, usage, cost))
        return cost

    def check(self) -> None:
        """Call before dispatching more work; raises once the cap is breached."""
        if self.total_usd > self.budget_usd:
            raise BudgetExceeded(self.total_usd, self.budget_usd)

    @property
    def total_usd(self) -> float:
        return sum(e.cost_usd for e in self.entries)

    @property
    def total_usage(self) -> Usage:
        total = Usage()
        for e in self.entries:
            total = total + e.usage
        return total

    @property
    def call_count(self) -> int:
        return len(self.entries)

    def by_label(self) -> dict[str, float]:
        out: dict[str, float] = defaultdict(float)
        for e in self.entries:
            out[e.label] += e.cost_usd
        return dict(out)

    def summary(self) -> dict[str, object]:
        u = self.total_usage
        return {
            "calls": self.call_count,
            "input_tokens": u.input_tokens,
            "cached_input_tokens": u.cached_input_tokens,
            "output_tokens": u.output_tokens,
            "cost_usd": round(self.total_usd, 6),
            "budget_usd": self.budget_usd,
            "budget_used_pct": round(100 * self.total_usd / self.budget_usd, 2)
            if self.budget_usd
            else 0.0,
            "by_label": {k: round(v, 6) for k, v in self.by_label().items()},
        }
