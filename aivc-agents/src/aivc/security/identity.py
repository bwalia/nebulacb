"""Agent and caller identity.

Two distinct principals matter and conflating them is the most common governance failure
we see: the *user* on whose behalf the agent acts, and the *agent* itself, which gets its
own machine identity with a narrower scope set. Data access is always evaluated against the
intersection, so an agent can never read something its caller could not.
"""

from __future__ import annotations

from dataclasses import dataclass, field


@dataclass(frozen=True, slots=True)
class Principal:
    subject: str
    tenant: str = "default"
    roles: frozenset[str] = field(default_factory=frozenset)
    scopes: frozenset[str] = field(default_factory=frozenset)
    kind: str = "user"  # user | service | agent
    attributes: tuple[tuple[str, str], ...] = ()

    @classmethod
    def user(cls, subject: str, tenant: str = "default", roles: set[str] | None = None,
             scopes: set[str] | None = None) -> "Principal":
        return cls(subject, tenant, frozenset(roles or set()), frozenset(scopes or set()), "user")

    @classmethod
    def agent(cls, name: str, tenant: str = "default", scopes: set[str] | None = None) -> "Principal":
        return cls(name, tenant, frozenset(), frozenset(scopes or set()), "agent")

    def has_scope(self, scope: str) -> bool:
        return scope in self.scopes or "*" in self.scopes

    def intersect(self, other: "Principal") -> "Principal":
        """Effective identity for an agent acting on a user's behalf: never wider than either."""
        return Principal(
            subject=f"{other.subject} on-behalf-of {self.subject}",
            tenant=self.tenant,
            roles=self.roles & (other.roles if other.roles else self.roles),
            scopes=self.scopes & other.scopes if "*" not in other.scopes else self.scopes,
            kind="agent",
        )


ANONYMOUS = Principal.user("anonymous", roles=set(), scopes=set())
