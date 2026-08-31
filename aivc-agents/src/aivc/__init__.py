"""aivc -- shared platform layer for portfolio-company AI deployments.

Layering (nothing below imports from above):

    config          settings, one place per environment
    llm/            provider-neutral client, offline provider, gateway (retry+cost+trace)
    obs/            spans, cost ledger, run context
    security/       identity, deny-by-default tool policy, redaction
    tools/          tool registry with scopes and side-effect metadata
    store/          retrieval indexes, durable-execution checkpoints
    agent/          the tool-calling loop
    evals/          scorers and the regression harness

Agents in `agents/` compose these. That is the whole architecture.
"""

from .config import Settings, get_settings, reset_settings

__version__ = "0.3.0"
__all__ = ["Settings", "get_settings", "reset_settings"]
