# AI Value Creation — reference agents

Three production-shaped agents on one shared platform layer, built to be deployed into a
portfolio company inside a 3–12 week engagement and handed over to that company's own
engineers at the end of it.

The whole thing runs offline — no API key, no network, no spend — so it can be demonstrated
on a locked-down laptop in a client meeting room and gated in CI for free.

```bash
make install
make demo          # all three agents end to end, about 5 seconds
make test          # 85 tests
make eval          # 3 suites; exits non-zero on a breached gate
make dashboard     # build the React workflow console (once)
make serve         # FastAPI + dashboard on :8000
```

With the server up:

- Workflow console: [http://localhost:8000/](http://localhost:8000/) or [/dashboard/](http://localhost:8000/dashboard/)
- Swagger Try-it-out: [http://localhost:8000/docs](http://localhost:8000/docs)

---

## What is here

| Agent | Business question it answers | The hard parts it actually solves |
|---|---|---|
| **1. Governed RAG** (`agents/governed_rag`) | *Can our people ask our policies a question and trust the answer?* | ACL-aware retrieval, hybrid lexical+dense, verified citations, groundedness and responsiveness gates, refusal as a first-class outcome |
| **2. Durable AP workflow** (`agents/ops_workflow`) | *Can an agent run a finance process end to end without anyone losing sleep?* | Checkpointed steps, crash resume, idempotent ERP writes, human-in-the-loop suspend, segregation of duties, audit evidence |
| **3. Supervisor** (`agents/supervisor`) | *One assistant over many systems — coherent and safe?* | Structured routing, per-specialist machine identity and scopes, caller-permission intersection, honest declining |

They share `src/aivc` — the layer that would otherwise be rewritten badly at every
engagement: a provider-neutral LLM gateway with retry/cost/trace/budget, a deny-by-default
tool policy engine, a tracing spine, retrieval indexes, a durable-execution engine and an
eval harness.

## What the demo shows

```
1. GOVERNED RAG
   employee asks for the merit budget      -> refused, no figure leaks
   HR asks the identical question          -> answered, 3.4%, cited [HR-COMP-003#2.0]
   question outside the corpus             -> refused, not invented

2. DURABLE AP WORKFLOW
   7 invoices triaged: 2 straight through, 5 stopped for a human, each with a policy clause
   BANK_DETAIL_CHANGE  -> escalated to the Group Financial Controller, never autonomous
   approval by the PO raiser -> rejected on segregation of duties (FIN-AP-090 s5)
   crash after step 3   -> resume skips 3 committed steps, pays exactly once

3. SUPERVISOR
   "exceptions by category"     -> data_analyst, SQL, real rows
   "what's stuck?"              -> ap_operations
   "expense threshold?"         -> policy_analyst, with a citation
   same question, no scope      -> tools denied by policy, answer says so
   "yesterday's share price?"   -> declined
```

## Design decisions, and why

Each of these is written up properly in `docs/`; the short version:

**Framework-light.** No orchestration framework. The agent loop is ~150 lines of explicit
control flow that a client's engineers can read on day one of handover, and the loop is where
the step ceiling, budget checks, authorisation and circuit breaker live — visible rather than
inherited. ([ADR-0001](docs/ADR-0001-framework-light.md) covers when this stops being the
right call.)

**Model reads, rules decide.** In the AP workflow the LLM extracts fields from a document —
a language problem it is good at. Whether an exception may clear without a human is a control
with a clause reference, and it is plain Python. A control implemented in a prompt cannot be
audited, tested, or defended to a regulator.

**The model does not police itself.** Its citations are checked against what was actually
retrieved; its extracted invoice total must appear verbatim on the source document; its
answer must be lexically supported by its cited passages *and* responsive to the question
asked. Each check is deterministic, cheap, and runs on every request.

**Deny by default at the tool boundary.** Every tool call is authorised against
(principal, tool, arguments) before execution. An agent's effective identity is the
intersection of the caller's permissions and its own scopes, so delegation can never escalate
privilege. This is what bounds prompt injection: the blast radius is the policy, not the
prompt's wording.

**Evals are a CI gate, not a dashboard.** Every case runs N times; the report carries pass
rate *and* consistency; thresholds are declared per metric and the command exits non-zero
when one is breached. Baselines are saved and diffed, so a client's team can change a prompt
after handover and know whether they broke something.

## Repository layout

```
src/aivc/            shared platform (nothing below imports from above)
  config.py          one settings object per environment
  llm/               provider-neutral client, offline provider, gateway (retry+cost+trace)
  obs/               spans, cost ledger with a hard budget, run context
  security/          identity, deny-by-default tool policy, reversible redaction
  tools/             tool registry with scopes and side-effect metadata
  store/             BM25 + vector indexes, RRF/MMR, durable-execution checkpoints
  agent/             the tool-calling loop, the durable workflow engine
  evals/             scorers and the regression harness
  api.py, cli.py     HTTP and command-line surfaces

agents/              the three agents, each with its own offline rules and eval suite
data/corpus/         six policy documents with ACLs and effective dates
data/ap/             invoice, PO and supplier fixtures
docs/                architecture, three ADRs, evaluation, security, handover
```

## Running against a real model

```bash
export AIVC_PROVIDER=anthropic
export AIVC_MODEL=<the model id agreed with the client>
export AIVC_ANTHROPIC_API_KEY=...
export AIVC_PRICING_FILE=./deploy/pricing.json   # the client's contracted rate card
make eval                                        # re-baseline before trusting any threshold
```

Nothing in the agents changes. `AIVC_PROVIDER` selects an adapter; the gateway above it
already handles retries, cost attribution, budget ceilings, tracing and redaction.

## Honest limitations

This is a POC substrate, not a finished platform. Written down so nobody is surprised in
week three:

- **The offline provider is a rule table, not a model.** It exists to make the pipeline
  demonstrable and CI deterministic. Every quality threshold in the eval suites is calibrated
  against it and must be re-derived against the client's chosen model on day one.
- **`HashingEmbedder` is not a trained embedding model.** It handles exact terms well and
  paraphrase poorly. Swap it for a real embedding model before making any retrieval-quality
  claim; `ProviderEmbedder` is the adapter.
- **In-process indexes are honest to roughly 10⁵ chunks.** `PgVectorIndex` carries the
  production DDL and query; the retriever above it does not change.
- **API identity is header-based.** `require_principal` is the one function to replace with
  real token verification, and it is deliberately the only place that knows how identity
  arrives.
- **The durable engine has no timers, signals or cross-service orchestration.** [ADR-0003](docs/ADR-0003-durable-execution.md)
  states the point at which to adopt Temporal or LangGraph's checkpointer instead.
- **Groundedness is a lexical proxy.** It is a free deterministic regression signal, not a
  measure of truth. Pair it with a sampled human or judge review before quality claims.

## Documentation

- [Architecture](docs/ARCHITECTURE.md) — layers, request flow, what runs where in production
- [ADR-0001](docs/ADR-0001-framework-light.md) — no orchestration framework, and when to change that
- [ADR-0002](docs/ADR-0002-retrieval.md) — hybrid retrieval, chunking, and the two bugs the evals caught
- [ADR-0003](docs/ADR-0003-durable-execution.md) — hand-rolled durable execution vs Temporal
- [Evaluation](docs/EVALUATION.md) — the metric set, thresholds, and how to re-baseline
- [Security & governance](docs/SECURITY.md) — identity, least privilege, injection, audit
- [Handover](docs/HANDOVER.md) — what the client's team owns on day one, and the runbook
- [Engagement playbook](docs/ENGAGEMENT-PLAYBOOK.md) — how this maps onto weeks 1–12
