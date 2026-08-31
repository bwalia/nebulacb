# Engagement playbook

How this repository maps onto a 3–12 week deployment at a portfolio company, once use cases
have been identified and prioritised.

The premise: the differentiator is not the agent, it is the speed from "use case agreed" to
"running in their stack, with evidence, owned by their team". Everything here exists to
compress that.

## Week 0 — before the engagement starts

Answer these before day one; each has a direct consequence in the code.

| Question | Consequence |
|---|---|
| Which model, hosted where, under whose contract? | `AIVC_PROVIDER`, the adapter, and data-residency constraints |
| What is the contracted rate card? | `AIVC_PRICING_FILE` — without it every cost figure is fiction |
| What is the data platform — Snowflake, Databricks, Fabric, plain Postgres? | where retrieval and the semantic layer live |
| Where does identity come from? | what replaces `require_principal`, and how roles map to ACLs |
| What does the network allow? | whether the offline provider is a demo convenience or the only thing that runs |
| Who owns this after we leave, and are they in the room from week 1? | whether handover works |

## Weeks 1–2 — data layer and thin slice

The unglamorous half, and the half that decides the outcome. Do not start with the agent.

- Map the source systems: what documents, what tables, what ACLs, refresh cadence, who owns
  each. Where do permissions actually live, and are they trustworthy?
- Stand up ingestion for one narrow domain. Content-hashed and idempotent from the start;
  re-embedding a corpus twice is a real invoice.
- Confirm ACLs travel with the content. If they cannot be sourced reliably, that is a finding
  to raise in week 1, not week 8.
- Ship one thin end-to-end slice — one question type, one user, real data, tracing on.

**Exit criterion:** a named business user asks a real question and gets a correct, cited
answer from their own data.

## Weeks 3–5 — the agent, with the evals written alongside

- Build the agent against `LLMGateway` and the policy engine. Do not reinvent the platform
  layer per use case.
- Write eval cases **with their SMEs, in the same week as the feature**. Cases are JSONL so a
  business user can add one in a PR. They supply the cases and the right answers; we supply
  the scorers.
- Calibrate thresholds from the observed distribution on their corpus (see EVALUATION.md).
  Never copy thresholds between engagements.
- Wire the eval command into their CI. This is the single most valuable artefact left behind:
  it is what lets their team change a prompt in month four and know whether they broke
  something.

**Exit criterion:** `make eval` green in *their* CI, on *their* data, with thresholds their
business owner agreed.

## Weeks 6–8 — hardening and adoption

Two tracks in parallel, shared with the Deployment Lead.

**Technical:** the pre-production checklist in SECURITY.md — real authentication, secrets in
their vault, read-only database roles, egress allowlisting, traces into their collector,
budgets set from the real rate card, a red-team pass on injection through retrieved documents.

**Human:** this is where deployments actually fail. Run hands-on sessions with the people who
will use it. Coach two or three power users who become the internal advocates. Watch someone
use it without helping them — the gap between what we built and what they need shows up in
the first ten minutes and nowhere in the eval suite.

Log every real question from day one of the pilot. Promote the failures into eval cases. A
curated suite is not production traffic.

**Exit criterion:** the pilot cohort uses it unprompted for a week, and their questions are
feeding the eval suite.

## Weeks 9–12 — measurement, handover, codification

- Report the outcome in the business's own terms, not ours. For the AP workflow that is:
  straight-through rate, exceptions requiring a human, average time to resolution, cost per
  invoice. The trace and ledger already carry all of it.
- Walk their engineers through HANDOVER.md *by doing it*: have them make a change, break an
  eval gate, and fix it, while we watch.
- Freeze the baselines. They are the quality contract.
- Codify what generalised back into the shared knowledge base — a scorer, a policy guard, an
  adapter, an anti-pattern. The next engagement should start further along than this one did.

**Exit criterion:** their engineer ships a change to production without us.

## What generalises and what does not

**Reusable across engagements (`src/aivc`):** the gateway with retry/cost/trace/budget, the
policy engine and guards, tracing and the cost ledger, the tool registry, the durable engine,
the eval harness and scorers, retrieval primitives, the CI shape and the container.

**Rebuilt every time (`agents/`, `data/`):** the corpus and its ACL model, domain controls and
their clause references, source-system adapters, the semantic layer, eval cases and
thresholds, prompts.

Keeping that line clean is what makes the second engagement faster than the first. The
temptation to generalise a domain control into the platform layer should be resisted — the
next client's finance policy will differ in exactly the way that makes the abstraction wrong.

## Anti-patterns worth naming

Each of these has cost a real project time:

- **Starting with the agent instead of the data.** The demo works on five documents and dies
  on fifty thousand, because nobody looked at how permissions actually work.
- **Evals written at the end.** They become a rubber stamp on whatever was built rather than a
  specification of what "working" means.
- **A control implemented in a prompt.** Un-auditable, untestable, and silently different
  after a model upgrade.
- **Copying thresholds between clients.** A threshold not derived from this corpus is worse
  than no threshold, because it looks rigorous.
- **A vector database nobody will own.** If they already run Postgres, use pgvector until the
  scale genuinely demands otherwise.
- **Handover as a document.** Handover is their engineer shipping a change while we watch.
- **Quoting a cost per transaction from list prices.** Set the rate card first.
