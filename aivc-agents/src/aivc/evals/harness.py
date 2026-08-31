"""Eval harness.

Three things this does that a notebook of spot-checks does not:

1. **Repeats.** LLM systems are non-deterministic; a single pass tells you nothing about
   whether a change helped. Every case runs N times and the report carries both the mean and
   the pass-consistency, so "it worked when I tried it" stops being the acceptance criterion.
2. **Gates.** Thresholds are declared per metric and the suite exits non-zero when one is
   breached. That is what makes it a CI gate rather than a dashboard.
3. **Baselines.** A saved report is a baseline; the next run diffs against it and flags
   regressions. This is the artefact that lets a client's team change a prompt after handover
   and know whether they broke anything.
"""

from __future__ import annotations

import json
import statistics
import time
import traceback
from dataclasses import asdict, dataclass, field
from pathlib import Path
from typing import Any, Callable, Iterable, Sequence

from .scorers import Score, Scorer


@dataclass
class EvalCase:
    id: str
    inputs: dict[str, Any] = field(default_factory=dict)
    expected: dict[str, Any] = field(default_factory=dict)
    tags: list[str] = field(default_factory=list)

    def __getattr__(self, item: str) -> Any:
        # Scorers read case.question / case.expected_route etc. without caring which bag
        # the field lives in.
        for bag in ("expected", "inputs"):
            data = self.__dict__.get(bag, {})
            if item in data:
                return data[item]
        raise AttributeError(item)

    @classmethod
    def from_dict(cls, d: dict[str, Any]) -> "EvalCase":
        known = {"id", "inputs", "expected", "tags"}
        if known & set(d):
            return cls(
                id=d.get("id", ""),
                inputs=d.get("inputs", {}),
                expected=d.get("expected", {}),
                tags=d.get("tags", []),
            )
        # flat form: everything that is not the case id is an expectation/input
        cid = d.pop("id", "")
        return cls(id=cid, inputs=d, expected=d)


def load_cases(path: str | Path) -> list[EvalCase]:
    """JSONL, one case per line. Kept as a flat file so a business SME can add cases in a PR."""
    cases = []
    for line in Path(path).read_text().splitlines():
        line = line.strip()
        if line and not line.startswith("//"):
            cases.append(EvalCase.from_dict(json.loads(line)))
    return cases


@dataclass
class CaseResult:
    case_id: str
    repeat: int
    scores: list[Score]
    output: dict[str, Any]
    latency_ms: float
    error: str | None = None

    @property
    def passed(self) -> bool:
        return self.error is None and all(s.passed for s in self.scores)


@dataclass
class SuiteReport:
    suite: str
    started_at: float
    results: list[CaseResult]
    thresholds: dict[str, float]
    repeats: int
    metadata: dict[str, Any] = field(default_factory=dict)

    # -- aggregates ---------------------------------------------------------
    def metric_means(self) -> dict[str, float]:
        buckets: dict[str, list[float]] = {}
        for r in self.results:
            for s in r.scores:
                buckets.setdefault(s.name, []).append(s.value)
        return {k: round(statistics.fmean(v), 4) for k, v in sorted(buckets.items())}

    def metric_stdev(self) -> dict[str, float]:
        buckets: dict[str, list[float]] = {}
        for r in self.results:
            for s in r.scores:
                buckets.setdefault(s.name, []).append(s.value)
        return {
            k: round(statistics.pstdev(v), 4) if len(v) > 1 else 0.0 for k, v in sorted(buckets.items())
        }

    def consistency(self) -> float:
        """Fraction of cases that passed on *every* repeat. Flaky cases are the ones that
        will page someone at 3am, so they are reported separately from the mean."""
        by_case: dict[str, list[bool]] = {}
        for r in self.results:
            by_case.setdefault(r.case_id, []).append(r.passed)
        if not by_case:
            return 1.0
        return round(sum(1 for v in by_case.values() if all(v)) / len(by_case), 4)

    def flaky_cases(self) -> list[str]:
        by_case: dict[str, list[bool]] = {}
        for r in self.results:
            by_case.setdefault(r.case_id, []).append(r.passed)
        return sorted(cid for cid, v in by_case.items() if any(v) and not all(v))

    @property
    def pass_rate(self) -> float:
        return round(sum(1 for r in self.results if r.passed) / len(self.results), 4) if self.results else 0.0

    def failures(self) -> list[CaseResult]:
        return [r for r in self.results if not r.passed]

    def gate(self) -> tuple[bool, list[str]]:
        """Apply declared thresholds. Returns (ok, breaches)."""
        breaches = []
        means = self.metric_means()
        for metric, threshold in self.thresholds.items():
            if metric == "pass_rate":
                actual = self.pass_rate
            elif metric == "consistency":
                actual = self.consistency()
            else:
                actual = means.get(metric)
                if actual is None:
                    breaches.append(f"{metric}: not measured by this suite")
                    continue
            if actual < threshold:
                breaches.append(f"{metric}: {actual:.3f} < required {threshold:.3f}")
        return (not breaches, breaches)

    # -- io -----------------------------------------------------------------
    def to_dict(self) -> dict[str, Any]:
        ok, breaches = self.gate()
        return {
            "suite": self.suite,
            "started_at": self.started_at,
            "repeats": self.repeats,
            "cases": len({r.case_id for r in self.results}),
            "runs": len(self.results),
            "pass_rate": self.pass_rate,
            "consistency": self.consistency(),
            "flaky_cases": self.flaky_cases(),
            "metrics": self.metric_means(),
            "metric_stdev": self.metric_stdev(),
            "thresholds": self.thresholds,
            "gate_passed": ok,
            "gate_breaches": breaches,
            "metadata": self.metadata,
            "failures": [
                {
                    "case_id": r.case_id,
                    "repeat": r.repeat,
                    "error": r.error,
                    "failed_scores": [
                        {"name": s.name, "value": s.value, "detail": s.detail}
                        for s in r.scores
                        if not s.passed
                    ],
                }
                for r in self.failures()
            ][:40],
        }

    def save(self, path: str | Path) -> Path:
        p = Path(path)
        p.parent.mkdir(parents=True, exist_ok=True)
        p.write_text(json.dumps(self.to_dict(), indent=2, default=str))
        return p

    def to_markdown(self) -> str:
        ok, breaches = self.gate()
        means, stdev = self.metric_means(), self.metric_stdev()
        lines = [
            f"### {self.suite}",
            "",
            f"- runs: **{len(self.results)}** ({len({r.case_id for r in self.results})} cases x {self.repeats} repeats)",
            f"- pass rate: **{self.pass_rate:.1%}**  |  consistency: **{self.consistency():.1%}**",
            f"- gate: **{'PASS' if ok else 'FAIL'}**",
            "",
            "| metric | mean | stdev | threshold |",
            "|---|---|---|---|",
        ]
        for metric, value in means.items():
            t = self.thresholds.get(metric)
            lines.append(f"| {metric} | {value:.3f} | {stdev.get(metric, 0):.3f} | {t if t is not None else '-'} |")
        if breaches:
            lines += ["", "**Breaches**"] + [f"- {b}" for b in breaches]
        if self.flaky_cases():
            lines += ["", f"**Flaky:** {', '.join(self.flaky_cases())}"]
        return "\n".join(lines)

    def compare(self, baseline_path: str | Path) -> dict[str, Any]:
        """Diff against a saved baseline report; this is the regression signal in CI."""
        p = Path(baseline_path)
        if not p.exists():
            return {"baseline": None, "note": "no baseline recorded yet"}
        base = json.loads(p.read_text())
        deltas = {}
        for metric, value in self.metric_means().items():
            prior = base.get("metrics", {}).get(metric)
            if prior is not None:
                deltas[metric] = round(value - prior, 4)
        regressions = {k: v for k, v in deltas.items() if v < -0.02}
        return {
            "baseline": base.get("started_at"),
            "deltas": deltas,
            "regressions": regressions,
            "pass_rate_delta": round(self.pass_rate - base.get("pass_rate", 0), 4),
        }


TaskFn = Callable[[EvalCase], dict[str, Any]]


def run_suite(
    name: str,
    cases: Iterable[EvalCase],
    task: TaskFn,
    scorers: Sequence[Scorer],
    *,
    repeats: int = 1,
    thresholds: dict[str, float] | None = None,
    tags: set[str] | None = None,
    progress: bool = False,
    metadata: dict[str, Any] | None = None,
) -> SuiteReport:
    cases = [c for c in cases if not tags or (tags & set(c.tags))]
    results: list[CaseResult] = []
    started = time.time()
    for case in cases:
        for repeat in range(repeats):
            t0 = time.perf_counter()
            try:
                output = task(case)
                latency = (time.perf_counter() - t0) * 1000
                output.setdefault("latency_ms", latency)
                scores = [s(case, output) for s in scorers]
                results.append(CaseResult(case.id, repeat, scores, output, latency))
            except Exception:
                latency = (time.perf_counter() - t0) * 1000
                results.append(
                    CaseResult(
                        case.id, repeat, [], {}, latency, error=traceback.format_exc(limit=4)
                    )
                )
            if progress:
                mark = "." if results[-1].passed else "F"
                print(mark, end="", flush=True)
    if progress:
        print()
    return SuiteReport(name, started, results, thresholds or {}, repeats, metadata or {})


def asdict_case(case: EvalCase) -> dict[str, Any]:
    return asdict(case)
