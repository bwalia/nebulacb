"""Tool registry.

A tool is a function plus a contract: JSON schema, required scopes, whether it mutates
state, and whether it is safe to retry. The last two are what let the durable workflow
engine retry confidently instead of double-paying an invoice.
"""

from __future__ import annotations

import inspect
import json
from dataclasses import dataclass, field
from typing import Any, Callable, Iterable, Literal, get_type_hints

from pydantic import BaseModel, ValidationError

SideEffect = Literal["read", "write", "external"]


@dataclass(slots=True)
class ToolSpec:
    name: str
    description: str
    parameters: dict[str, Any]
    fn: Callable[..., Any]
    scopes: set[str] = field(default_factory=set)
    side_effect: SideEffect = "read"
    idempotent: bool = True
    args_model: type[BaseModel] | None = None
    timeout_s: float | None = None

    def to_schema(self) -> dict[str, Any]:
        return {"name": self.name, "description": self.description, "parameters": self.parameters}

    def validate_args(self, args: dict[str, Any]) -> dict[str, Any]:
        if self.args_model is None:
            return args
        try:
            return self.args_model(**args).model_dump()
        except ValidationError as e:
            raise ToolArgumentError(self.name, e.errors()) from e


class ToolArgumentError(ValueError):
    def __init__(self, tool: str, errors: Any):
        self.tool = tool
        self.errors = errors
        super().__init__(f"invalid arguments for '{tool}': {json.dumps(errors, default=str)[:500]}")


class ToolRegistry:
    def __init__(self, specs: Iterable[ToolSpec] = ()):
        self._specs: dict[str, ToolSpec] = {s.name: s for s in specs}

    def register(self, spec: ToolSpec) -> ToolSpec:
        self._specs[spec.name] = spec
        return spec

    def tool(
        self,
        *,
        name: str | None = None,
        description: str | None = None,
        scopes: set[str] | None = None,
        side_effect: SideEffect = "read",
        idempotent: bool = True,
        timeout_s: float | None = None,
    ):
        """Decorator. The single pydantic-model argument defines the schema."""

        def deco(fn: Callable[..., Any]) -> Callable[..., Any]:
            args_model = _extract_args_model(fn)
            params = (
                args_model.model_json_schema()
                if args_model
                else {"type": "object", "properties": {}}
            )
            params.pop("title", None)
            self.register(
                ToolSpec(
                    name=name or fn.__name__,
                    description=description or inspect.getdoc(fn) or "",
                    parameters=params,
                    fn=fn,
                    scopes=scopes or set(),
                    side_effect=side_effect,
                    idempotent=idempotent,
                    args_model=args_model,
                    timeout_s=timeout_s,
                )
            )
            return fn

        return deco

    def get(self, name: str) -> ToolSpec:
        if name not in self._specs:
            raise KeyError(f"unknown tool '{name}' (registered: {sorted(self._specs)})")
        return self._specs[name]

    def names(self) -> list[str]:
        return sorted(self._specs)

    def schemas(self) -> list[dict[str, Any]]:
        return [s.to_schema() for s in self._specs.values()]

    def subset(self, names: Iterable[str]) -> "ToolRegistry":
        """Least privilege in one line: hand each specialist only the tools it needs."""
        wanted = list(names)
        return ToolRegistry(self.get(n) for n in wanted)

    def __contains__(self, name: object) -> bool:
        return name in self._specs

    def __len__(self) -> int:
        return len(self._specs)


def _extract_args_model(fn: Callable[..., Any]) -> type[BaseModel] | None:
    """Find the single pydantic model parameter that defines the tool's schema.

    Resolution has to tolerate `from __future__ import annotations`, under which every
    annotation is a string. get_type_hints handles the common case; when the model is defined
    in an enclosing function's scope it raises NameError, so fall back to the closure's own
    cell contents before giving up.
    """
    sig = inspect.signature(fn)
    try:
        hints = get_type_hints(fn)
    except NameError:
        hints = {}
    for pname in sig.parameters:
        hint = hints.get(pname)
        if isinstance(hint, type) and issubclass(hint, BaseModel):
            return hint

    raw = getattr(fn, "__annotations__", {})
    candidates: dict[str, Any] = dict(fn.__globals__)
    closure = getattr(fn, "__closure__", None) or ()
    for cell in closure:
        try:
            value = cell.cell_contents
        except ValueError:
            continue
        if isinstance(value, type) and issubclass(value, BaseModel):
            candidates[value.__name__] = value
    for pname in sig.parameters:
        annotation = raw.get(pname)
        resolved = candidates.get(annotation) if isinstance(annotation, str) else annotation
        if isinstance(resolved, type) and issubclass(resolved, BaseModel):
            return resolved
    return None
