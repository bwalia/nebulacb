from __future__ import annotations

from pathlib import Path

import pytest

from aivc.config import reset_settings
from aivc.obs.run import RunContext
from aivc.security.identity import Principal
from aivc.store.checkpoint import CheckpointStore


@pytest.fixture()
def settings(tmp_path: Path):
    """Every test gets its own state directory, so nothing leaks between tests."""
    return reset_settings(
        provider="offline",
        state_dir=tmp_path / "state",
        checkpoint_db=tmp_path / "state" / "workflow.sqlite",
        warehouse_db=tmp_path / "state" / "warehouse.sqlite",
        trace_file=tmp_path / "state" / "traces.jsonl",
    )


@pytest.fixture()
def employee(settings) -> Principal:
    return Principal.user(
        "test.user", tenant="northgate", roles={"employee"},
        scopes={"corpus:read", "warehouse:read", "ap:read"},
    )


@pytest.fixture()
def ctx(settings, employee) -> RunContext:
    return RunContext.build(employee, settings)


@pytest.fixture()
def store(settings) -> CheckpointStore:
    return CheckpointStore(settings.checkpoint_db)
