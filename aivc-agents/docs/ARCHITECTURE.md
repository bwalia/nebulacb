# Architecture

## Layering

Strict, one direction. Nothing in a lower layer imports from a higher one, which is what
makes any single layer replaceable on a client's stack without touching the others.

```
                    agents/            governed_rag   ops_workflow   supervisor
                       |
  ---------------------+---------------------------------------------------------
                    aivc/agent         tool loop  |  durable workflow engine
                    aivc/evals         scorers    |  regression harness
                       |
                    aivc/tools         registry: schema + scopes + side effects
                    aivc/store         BM25 + vector + RRF/MMR  |  checkpoints
                       |
                    aivc/security      identity | policy engine | redaction
                    aivc/obs           spans | cost ledger | run context
                    aivc/llm           provider adapters | offline | gateway
                    aivc/config        settings
```

The gateway is the choke point. Agent code never calls a provider SDK directly, so retry
policy, cost attribution, budget ceilings, tracing and redaction are properties of the
system rather than things each agent remembers to do.

## The request path

A single question through the governed RAG agent:

```
HTTP request
  -> require_principal          headers -> Principal (replace with real JWT verification)
  -> RunContext.build           run id, trace id, cost ledger with budget, deadline
  -> HybridRetriever
       ACL predicate applied inside the index, before ranking
       BM25 (candidate_k)   +   dense cosine (candidate_k)
       RRF fusion (k scaled to the candidate pool)
       recency preference
       MMR with the top-2 protected on relevance
       pack to a token budget
  -> LLMGateway.structured      typed answer, bounded self-repair on schema failure
       span: model, tokens in/out/cached, cost, retries, stop reason
       ledger.record + budget check
  -> verification               citations exist? groundedness? responsive to the question?
  -> RagResponse                answer or refusal-with-reason, citations, trace id, cost
```

The AP workflow path is the same up to `RunContext`, then enters the durable engine: each
step's output is committed to SQLite before the next starts, a step may raise `Suspend` to
park the run for a human, and a completed step is never re-executed on resume.

The supervisor routes with one small structured call, then runs specialists — each a
`ToolAgent` with a scoped tool subset and its own machine identity — sharing the single run
budget.

## What runs where in production

The POC substrate and its production replacement, per component:

| Concern | In this repo | On an engagement |
|---|---|---|
| Retrieval index | in-process BM25 + numpy cosine | pgvector on the client's Postgres (`PgVectorIndex`), or the vector feature of Snowflake / Databricks / Fabric if they already run one |
| Embeddings | `HashingEmbedder` (deterministic, untrained) | `ProviderEmbedder` against the client's approved model, with a content-hash cache |
| Ingestion | in-process on first use | scheduled job (Airflow / dbt / ADF) writing to the index, emitting the manifest as a run artefact |
| Durable state | SQLite, WAL, synchronous=FULL | Postgres with the same schema, or Temporal when the workflow grows timers and signals |
| Identity | `X-User` / `X-Roles` headers | OIDC token verification; roles from the client's IdP; agent identities as service principals |
| Tracing | JSONL + optional OTLP | the client's existing collector — Datadog, Grafana, Honeycomb |
| Secrets | environment variables | the client's vault, short-lived leases, no static keys in the agent process |
| Serving | uvicorn in one container | the client's standard runtime (ECS/EKS/AKS); the container is already non-root with a healthcheck |

## Data flow and governance boundaries

Three boundaries, each with an enforcement point rather than a convention:

1. **Corpus → user.** ACLs travel with the chunk from ingestion. The retrieval predicate is
   applied *inside* the index before ranking, so a filtered chunk never occupies a top-k slot
   and result counts do not leak the existence of restricted documents. Ingestion refuses to
   index a document with no ACL rather than defaulting it to public.

2. **Agent → tool.** Every call is authorised against (principal, tool, arguments). The
   effective principal is `caller ∩ agent`, so a specialist cannot exceed the person who
   invoked it. Rules can also carry argument guards (read-only SQL, value ceilings, tenant
   isolation, egress allowlists) and per-run rate limits.

3. **System → provider.** Optional reversible redaction at the gateway keeps obvious
   identifiers out of a third-party's logs; placeholders go out, real values are restored in
   the response to an authorised user. Where the client has a real DLP service, `Redactor` is
   a thin adapter to it.

## Extension points

The five things most likely to change on a new engagement, and the file to change:

| Change | Where |
|---|---|
| Different model or hosting (Bedrock, Azure OpenAI, vLLM) | `llm/providers.py` — one adapter class |
| Different vector store | `store/index.py` — implement `search`; the retriever is untouched |
| Different source systems (SAP, NetSuite, Dynamics) | `agents/ops_workflow/domain.py` — `ErpStub` is the seam |
| Different controls or thresholds | `agents/ops_workflow/domain.py` constants, with clause references |
| New agent | compose `LLMGateway` + `ToolRegistry.subset` + `PolicyEngine` + `ToolAgent`, add an eval suite |
