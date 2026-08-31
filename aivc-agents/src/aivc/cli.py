"""Command line entry point.

`aivc demo` is the thing to run in front of a client: it walks all three agents, offline, in
a few seconds, and prints what each one decided and why.
"""

from __future__ import annotations

import argparse
import json
import sys

from .config import bootstrap_provider, get_settings, reset_settings
from .obs.run import RunContext
from .security.identity import Principal


def _principal(args: argparse.Namespace) -> Principal:
    return Principal.user(
        args.user,
        tenant=args.tenant,
        roles=set(filter(None, args.roles.split(","))),
        scopes=set(filter(None, args.scopes.split(","))),
    )


def cmd_ingest(args: argparse.Namespace) -> int:
    from agents.governed_rag.ingest import ingest, summarise

    corpus = ingest()
    print(json.dumps({**corpus.manifest.to_dict(), **summarise(corpus.chunks)}, indent=2))
    return 0


def cmd_ask(args: argparse.Namespace) -> int:
    from agents.governed_rag import build_agent

    ctx = RunContext.build(_principal(args))
    response = build_agent(ctx).answer(args.question)
    print(response.answer)
    if response.citations:
        print("\nSources:")
        for c in response.citations:
            print(f"  [{c['chunk_id']}] {c['title']} > {c['section']} ({c['source']})")
    print(
        f"\n[refused={response.refused} groundedness={response.groundedness} "
        f"cost=${response.cost_usd:.5f} trace={response.trace_id}]"
    )
    return 0


def cmd_assistant(args: argparse.Namespace) -> int:
    from agents.supervisor import build_agent

    ctx = RunContext.build(_principal(args))
    response = build_agent(ctx).handle(args.question)
    print(response.answer)
    print(f"\n[route={response.route or 'declined'} cost=${response.cost_usd:.5f}]")
    return 0


def cmd_triage(args: argparse.Namespace) -> int:
    from agents.ops_workflow import build_workflow

    ctx = RunContext.build(_principal(args))
    result = build_workflow().start(args.invoice_id, ctx)
    print(json.dumps(result.to_dict(), indent=2, default=str))
    return 0


def cmd_approve(args: argparse.Namespace) -> int:
    from agents.ops_workflow import build_workflow

    ctx = RunContext.build(_principal(args))
    result = build_workflow().resume(
        args.run_id, ctx, approved=not args.reject, approver=args.user, note=args.note
    )
    print(json.dumps(result.to_dict(), indent=2, default=str))
    return 0


def cmd_eval(args: argparse.Namespace) -> int:
    suites = {
        "rag": "agents.governed_rag.evals",
        "ops": "agents.ops_workflow.evals",
        "supervisor": "agents.supervisor.evals",
    }
    names = [args.suite] if args.suite != "all" else list(suites)
    failed = False
    for name in names:
        module = __import__(suites[name], fromlist=["run"])
        report = module.run(repeats=args.repeats, progress=True)
        print(report.to_markdown())
        if args.baseline:
            print(json.dumps(report.compare(f"{args.baseline}/{name}.json"), indent=2))
        if args.save:
            path = report.save(f"{args.save}/{name}.json")
            print(f"saved: {path}")
        ok, _ = report.gate()
        failed = failed or not ok
        print()
    return 1 if failed else 0


def cmd_demo(args: argparse.Namespace) -> int:
    from scripts.demo import main as demo_main

    return demo_main()


def cmd_serve(args: argparse.Namespace) -> int:
    import uvicorn

    uvicorn.run("aivc.api:app", host=args.host, port=args.port, reload=args.reload)
    return 0


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(prog="aivc", description="AI Value Creation reference agents")
    parser.add_argument("--user", default="demo.user")
    parser.add_argument("--tenant", default="northgate")
    parser.add_argument("--roles", default="employee,finance")
    parser.add_argument("--scopes", default="corpus:read,warehouse:read,ap:read")
    parser.add_argument(
        "--provider",
        default=None,
        help="ollama (default) | offline | anthropic | openai",
    )
    sub = parser.add_subparsers(dest="command", required=True)

    sub.add_parser("ingest", help="build the retrieval index and print the manifest").set_defaults(
        func=cmd_ingest
    )

    p = sub.add_parser("ask", help="ask the governed RAG agent")
    p.add_argument("question")
    p.set_defaults(func=cmd_ask)

    p = sub.add_parser("assistant", help="ask the supervisor")
    p.add_argument("question")
    p.set_defaults(func=cmd_assistant)

    p = sub.add_parser("triage", help="run the AP exception workflow for one invoice")
    p.add_argument("invoice_id")
    p.set_defaults(func=cmd_triage)

    p = sub.add_parser("approve", help="resume a suspended AP run")
    p.add_argument("run_id")
    p.add_argument("--reject", action="store_true")
    p.add_argument("--note", default="")
    p.set_defaults(func=cmd_approve)

    p = sub.add_parser("eval", help="run an eval suite")
    p.add_argument("suite", choices=["rag", "ops", "supervisor", "all"], default="all", nargs="?")
    p.add_argument("--repeats", type=int, default=3)
    p.add_argument("--save", default=None, help="directory to write reports into")
    p.add_argument("--baseline", default=None, help="directory of baseline reports to diff")
    p.set_defaults(func=cmd_eval)

    sub.add_parser("demo", help="walk all three agents end to end").set_defaults(func=cmd_demo)

    p = sub.add_parser("serve", help="run the HTTP API")
    p.add_argument("--host", default="0.0.0.0")
    p.add_argument("--port", type=int, default=8000)
    p.add_argument("--reload", action="store_true")
    p.set_defaults(func=cmd_serve)

    args = parser.parse_args(argv)
    if args.provider:
        reset_settings(provider=args.provider)
    else:
        bootstrap_provider()
    return args.func(args)


if __name__ == "__main__":  # pragma: no cover
    sys.exit(main())
