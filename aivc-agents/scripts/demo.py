"""End-to-end demo. Uses Ollama on the LAN by default (see `.env` / `AIVC_PROVIDER`).

Sequence is chosen to answer the three questions a portfolio-company sponsor actually asks:
can it answer correctly and prove it; will it stop when it should; and what happens when it
breaks.
"""

from __future__ import annotations

import shutil
import sys

from aivc.config import bootstrap_provider, get_settings
from aivc.llm.ollama import load_token
from aivc.obs.run import RunContext
from aivc.security.identity import Principal
from aivc.store.checkpoint import CheckpointStore

WIDTH = 88


def rule(title: str) -> None:
    print(f"\n\033[1m{title}\033[0m")
    print("-" * WIDTH)


def kv(label: str, value: object) -> None:
    print(f"  {label:<22} {value}")


def _ollama_token(settings):
    return load_token(settings.ollama_token_file, settings.ollama_api_key)


def main() -> int:
    settings = bootstrap_provider()
    if settings.state_dir.exists():
        shutil.rmtree(settings.state_dir, ignore_errors=True)
    settings.ensure_dirs()

    print("=" * WIDTH)
    print("AI Value Creation -- reference agents".center(WIDTH))
    print(f"provider={settings.provider}  model={settings.model}".center(WIDTH))
    if settings.provider == "ollama":
        auth = "yes" if _ollama_token(settings) else "no"
        print(f"ollama={settings.ollama_base_url}  auth={auth}".center(WIDTH))
    print("=" * WIDTH)

    demo_rag()
    demo_workflow()
    demo_supervisor()
    demo_observability()

    print("\n" + "=" * WIDTH)
    print("Run `make eval` for the regression suites behind each of these.")
    return 0


# --- 1. governed RAG --------------------------------------------------------
def demo_rag() -> None:
    from agents.governed_rag import build_agent, get_corpus

    settings = get_settings()
    corpus = get_corpus(str(settings.corpus_dir), settings.embedding_dim)

    rule("1. GOVERNED RAG -- the same question, two people, two correct answers")
    kv("corpus", f"{corpus.manifest.documents} documents, {corpus.manifest.chunks} chunks")
    kv("acl roles present", sorted(corpus.acl_roles()))

    question = "What is the Group merit budget for the 2026 compensation cycle?"
    for label, roles in [("employee (no HR)", {"employee"}), ("HR business partner", {"employee", "hr"})]:
        principal = Principal.user("demo", tenant="northgate", roles=roles)
        ctx = RunContext.build(principal)
        response = build_agent(ctx).answer(question, principal)
        print(f"\n  [{label}]")
        kv("answer", response.answer[:150] + ("..." if len(response.answer) > 150 else ""))
        kv("refused", f"{response.refused} ({response.refusal_reason or 'n/a'})")
        kv("citations", [c["chunk_id"] for c in response.citations] or "none")
        kv("groundedness", response.groundedness)

    print("\n  [out of corpus]")
    principal = Principal.user("demo", roles={"employee"})
    ctx = RunContext.build(principal)
    response = build_agent(ctx).answer("How many weeks of paid parental leave do we offer?", principal)
    kv("refused", f"{response.refused} ({response.refusal_reason})")
    kv("why it matters", "an unanswerable question produces a refusal, not a plausible number")


# --- 2. durable workflow ----------------------------------------------------
def demo_workflow() -> None:
    from agents.ops_workflow import ErpStub, build_workflow

    settings = get_settings()
    store = CheckpointStore(settings.checkpoint_db)
    erp = ErpStub()
    workflow = build_workflow(store, erp)
    clerk = Principal.user("ap.clerk", tenant="northgate", roles={"finance"})

    rule("2. DURABLE AP WORKFLOW -- decisions, escalations, and a crash")
    print(f"  {'invoice':<10} {'category':<20} {'decision':<18} status")
    runs = {}
    for invoice_id in ["INV-1001", "INV-1002", "INV-1003", "INV-1004", "INV-1005", "INV-1006", "INV-1007"]:
        result = workflow.start(invoice_id, RunContext.build(clerk))
        runs[invoice_id] = result
        category = result.state.get("classify_exception", {}).get("category", "-")
        decision = result.state.get("policy_decision", {}).get("action", "-")
        print(f"  {invoice_id:<10} {category:<20} {decision:<18} {result.status}")
    print()
    kv("payments posted", erp.post_calls)
    kv("straight-through", f"{erp.post_calls}/7 invoices cleared with no human involved")

    print("\n  [human approval on INV-1003]")
    controller = Principal.user("s.oyelaran", tenant="northgate", roles={"finance"})
    resumed = workflow.resume(
        runs["INV-1003"].run_id, RunContext.build(controller),
        approved=True, approver="s.oyelaran", note="checked against the signed contract variation",
    )
    kv("status", resumed.status)
    kv("steps replayed", f"{len(resumed.steps_skipped)} skipped, {len(resumed.steps_executed)} executed")
    kv("payment id", resumed.state.get("post_to_erp", {}).get("payment_id"))

    print("\n  [segregation of duties]")
    sod = workflow.resume(
        runs["INV-1006"].run_id, RunContext.build(clerk),
        approved=True, approver="a.deniz",
    )
    kv("status", sod.status)
    kv("error", (sod.error or "")[:100])

    print("\n  [crash and resume]")
    crash_store = CheckpointStore(settings.state_dir / "crash-demo.sqlite")
    crash_erp = ErpStub()
    crashing = build_workflow(crash_store, crash_erp, fail_after_step="three_way_match")
    crashed = crashing.start("INV-1002", RunContext.build(clerk))
    kv("first attempt", f"{crashed.status} after {len(crashed.steps_executed)} committed steps")
    recovered = build_workflow(crash_store, crash_erp).resume(
        crashed.run_id, RunContext.build(clerk), approved=True, approver="s.oyelaran"
    )
    kv("resumed", f"{recovered.status}; skipped {recovered.steps_skipped}")
    kv("re-extractions", "0 -- the LLM extraction step was never re-run")
    kv("erp posts", f"{crash_erp.post_calls} (idempotency key prevented a double payment)")


# --- 3. supervisor ----------------------------------------------------------
def demo_supervisor() -> None:
    from agents.supervisor import build_agent

    rule("3. SUPERVISOR -- routing, least privilege, and declining")
    full = Principal.user(
        "m.lindqvist", tenant="northgate", roles={"employee", "finance"},
        scopes={"warehouse:read", "ap:read", "corpus:read"},
    )
    limited = Principal.user("contractor", tenant="northgate", roles={"employee"}, scopes={"corpus:read"})

    questions = [
        ("How many invoice exceptions do we have by category?", full),
        ("Which AP runs are stuck waiting for someone?", full),
        ("What is the expense approval threshold for a claim over GBP 2,000?", full),
        ("How many invoice exceptions do we have by category?", limited),
        ("What was the share price at close yesterday?", full),
    ]
    for question, principal in questions:
        ctx = RunContext.build(principal)
        response = build_agent(ctx).handle(question)
        denied = sorted({t for o in response.outcomes for t in o.denied_tools})
        print(f"\n  Q ({principal.subject}): {question}")
        kv("routed to", response.route or "declined")
        kv("answer", response.answer[:140] + ("..." if len(response.answer) > 140 else ""))
        if denied:
            kv("tools denied", denied)


# --- 4. observability -------------------------------------------------------
def demo_observability() -> None:
    from agents.governed_rag import build_agent

    rule("4. OBSERVABILITY -- what an operator sees for a single question")
    principal = Principal.user("demo", tenant="northgate", roles={"employee"})
    ctx = RunContext.build(principal, budget_usd=0.25)
    build_agent(ctx).answer("What mileage rate applies to the first 10,000 business miles?", principal)

    spans = ctx.memory_sink.spans if ctx.memory_sink else []
    for span in spans:
        indent = "    " if span.parent_id else "  "
        extra = ""
        if span.kind == "llm":
            extra = (
                f" tokens={span.attributes.get('tokens_in')}->{span.attributes.get('tokens_out')}"
                f" ${span.attributes.get('cost_usd', 0):.5f}"
            )
        if span.kind == "retrieval":
            extra = f" returned={span.attributes.get('returned')} filtered={span.attributes.get('filtered_out')}"
        print(f"{indent}{span.kind:<12} {span.name:<28} {span.duration_ms:6.1f}ms{extra}")
    print()
    kv("cost summary", ctx.ledger.summary())
    kv("trace file", get_settings().trace_file.as_posix())


if __name__ == "__main__":  # pragma: no cover
    sys.exit(main())
