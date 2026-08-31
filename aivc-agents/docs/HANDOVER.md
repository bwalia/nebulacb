# Handover

The engagement ends. The client's team owns this. This document is what they get on the last
day, and it is written to be read by an engineer who has never met us.

## Day one for the receiving team

```bash
git clone <repo> && cd aivc-agents
make install
make demo      # all three agents, offline, about 5 seconds
make test      # 85 tests
make eval      # quality gates; exits non-zero on a breach
```

If those four commands work, you have everything. There is no hidden service and no vendor
account required to develop against this.

## Read in this order

1. `README.md` — what the three agents do and the design decisions behind them
2. `docs/ARCHITECTURE.md` — the layering, and what runs where in production
3. `src/aivc/agent/loop.py` — the agent loop, ~150 lines, all guardrails visible
4. `src/aivc/agent/durable.py` — the workflow engine
5. `docs/EVALUATION.md` — how to know whether your change made things worse
6. The three ADRs — why it is built this way, and when to change it

## The five things to change first

| You want to | Change | Then |
|---|---|---|
| Swap the model or host it elsewhere | one adapter in `llm/providers.py`; `AIVC_PROVIDER` | re-baseline the evals |
| Add a document source | ingestion in `agents/governed_rag/ingest.py` | check ACLs travel with the chunk |
| Change a finance control | the constants in `agents/ops_workflow/domain.py` | update the clause reference and the eval case |
| Add a tool | `@registry.tool` with scopes, plus a `ToolRule` in the policy | add a least-privilege eval case |
| Add an agent | compose gateway + scoped registry + policy + `ToolAgent` | add an eval suite before shipping |

## Operating it

### Health

- `GET /healthz` — process is up
- `GET /readyz` — corpus loaded and checkpoint store reachable; 503 with per-check detail

### Where to look when something is wrong

Every request has a `trace_id` and a `run_id`, returned in the response body. Traces are JSONL
at `AIVC_TRACE_FILE` (or in your collector, if `AIVC_OTEL_ENDPOINT` is set).

```bash
# everything for one request
grep '"trace_id": "tr_abc123"' .state/traces.jsonl | python -m json.tool

# every denied tool call today
grep '"allowed": false' .state/traces.jsonl

# spend by agent
python -c "
import json,collections
c=collections.Counter()
for l in open('.state/traces.jsonl'):
    s=json.loads(l)
    if s['kind']=='llm': c[s['name']]+=s['attributes'].get('cost_usd',0)
print(c.most_common())"
```

### Common situations

| Symptom | Likely cause | Action |
|---|---|---|
| Answers suddenly refuse a lot | corpus re-ingested with wrong ACLs, or an embedding model change | check the ingest manifest; `retrieval_recall` in the eval report will confirm |
| `index was built with embedder X` | embedding model changed without a rebuild | rebuild the index; never mix embedding spaces |
| A workflow run is stuck | it is `awaiting_approval` by design | `GET /v1/ap/queue`; approve or reject via `/v1/ap/approve` |
| A run says `failed` | a permanent step error | `GET /v1/runs/{id}` for the step and error; fix the cause, then resume — completed steps are skipped |
| HTTP 429 from an agent | per-run cost budget exhausted | check the trace for a retry loop before raising `AIVC_RUN_COST_BUDGET_USD` |
| Spend rising with no traffic change | model or prompt change, or retries | `by_label` in the ledger summary attributes it per agent |

### Resuming a failed run

```bash
python -m aivc.cli approve <run_id> --user f.controller          # approve
python -m aivc.cli approve <run_id> --reject --note "duplicate"  # reject
```

A failed run is resumable after a fix. Steps that already committed are skipped, so an LLM
extraction is not re-billed and a posted payment is not re-posted.

## Routine maintenance

| Cadence | Task |
|---|---|
| Every PR | `make test` and `make eval` — both gate the merge |
| Weekly | review refusals and denied tool calls; promote real failures into eval cases |
| Monthly | reconcile spend against the ledger; check `unpriced_models()` is empty |
| Quarterly | re-verify ACLs against the source systems (POL-SEC-007 s3 requires this) |
| On model change | re-baseline every suite before rollout; the model is a dependency like any other |
| On corpus change | re-ingest, check the manifest, re-run the RAG suite |

## What we did not build

Stated so it is a backlog rather than a surprise:

- durable timers and time-based escalation (ADR-0003)
- a reranker in the retrieval pipeline (ADR-0002)
- streaming responses
- a UI — there is an API and a CLI
- multi-region or HA deployment
- automated ACL synchronisation from source systems
- a load test

## Deliberate decisions that will look like bugs

Read these before "fixing" one:

- **Ingestion refuses a document with no ACL.** A missing ACL is a missing field, not "public".
- **Unclassified step errors are permanent, not retried.** Retrying an unknown error against a
  non-idempotent step is how duplicate payments happen. A step that wants a retry raises
  `TransientStepError`.
- **A cross-tenant run returns 404, not 403.** A 403 confirms the run exists.
- **The AP specialist agent cannot approve anything**, even though it lists the approval tool.
  It stages a decision; a human commits it through the path where segregation of duties is
  checked.
- **`refusal_correctness` counts over-refusal as failure.** A system that refuses too often is
  abandoned as fast as one that invents answers.

## Contacts and provenance

- Every automated decision records `decided_by: agent:<workflow>@<model>` and the run id.
- Audit records are written by the final workflow step; retention is the client's to configure
  (FIN-AP-090 s6 requires seven years).
- The eval baselines in `baselines/` are the quality contract as of handover. Diff against
  them; do not silently regenerate them.
