"""Durable execution store.

An agent that calls a payment API, waits four hours for a human approval and then posts to
an ERP cannot live in a process's memory. Every step's output is committed before the next
one starts, so a crash, a deploy or a pod eviction costs one step, not the run -- and a
completed step is never re-executed on resume, which is what stops a retry from paying an
invoice twice.

SQLite here because it needs nothing and survives restarts; the same interface is a few
lines away from Postgres (see `SCHEMA` -- it is portable SQL). This is the pattern that
LangGraph/Temporal give you off the shelf; ADR-0003 records why we hand-rolled it for POCs.
"""

from __future__ import annotations

import json
import sqlite3
import threading
import time
import uuid
from dataclasses import dataclass
from pathlib import Path
from typing import Any, Iterator

SCHEMA = """
CREATE TABLE IF NOT EXISTS run (
    run_id           TEXT PRIMARY KEY,
    workflow         TEXT NOT NULL,
    status           TEXT NOT NULL,
    idempotency_key  TEXT,
    principal        TEXT,
    tenant           TEXT,
    input_json       TEXT NOT NULL,
    output_json      TEXT,
    error            TEXT,
    cursor_step      TEXT,
    created_at       REAL NOT NULL,
    updated_at       REAL NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS run_idem_idx
    ON run (workflow, idempotency_key) WHERE idempotency_key IS NOT NULL;
CREATE INDEX IF NOT EXISTS run_status_idx ON run (status, updated_at);

CREATE TABLE IF NOT EXISTS step (
    run_id      TEXT NOT NULL,
    name        TEXT NOT NULL,
    status      TEXT NOT NULL,
    attempt     INTEGER NOT NULL DEFAULT 1,
    output_json TEXT,
    error       TEXT,
    started_at  REAL NOT NULL,
    finished_at REAL,
    PRIMARY KEY (run_id, name)
);

CREATE TABLE IF NOT EXISTS event (
    id       INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id   TEXT NOT NULL,
    ts       REAL NOT NULL,
    kind     TEXT NOT NULL,
    payload  TEXT
);
CREATE INDEX IF NOT EXISTS event_run_idx ON event (run_id, id);
"""

RunStatus = str  # running | awaiting_approval | succeeded | failed | cancelled


@dataclass
class RunRecord:
    run_id: str
    workflow: str
    status: RunStatus
    input: dict[str, Any]
    output: dict[str, Any] | None = None
    error: str | None = None
    cursor_step: str | None = None
    principal: str | None = None
    tenant: str | None = None
    idempotency_key: str | None = None
    created_at: float = 0.0
    updated_at: float = 0.0
    resumed: bool = False


@dataclass
class StepRecord:
    run_id: str
    name: str
    status: str  # succeeded | failed | running
    attempt: int
    output: Any = None
    error: str | None = None


class CheckpointStore:
    def __init__(self, path: str | Path):
        self.path = Path(path)
        self.path.parent.mkdir(parents=True, exist_ok=True)
        self._local = threading.local()
        with self._conn() as conn:
            conn.executescript(SCHEMA)

    def _conn(self) -> sqlite3.Connection:
        conn = getattr(self._local, "conn", None)
        if conn is None:
            conn = sqlite3.connect(self.path, isolation_level=None, timeout=30)
            conn.execute("PRAGMA journal_mode=WAL")   # concurrent readers during long runs
            conn.execute("PRAGMA synchronous=FULL")   # a checkpoint that is not durable is a lie
            conn.row_factory = sqlite3.Row
            self._local.conn = conn
        return conn

    # -- runs ---------------------------------------------------------------
    def start_run(
        self,
        workflow: str,
        payload: dict[str, Any],
        *,
        idempotency_key: str | None = None,
        principal: str | None = None,
        tenant: str | None = None,
    ) -> RunRecord:
        """Create a run, or return the existing one for this idempotency key.

        Callers retry: a queue redelivers, a user double-clicks, a webhook fires twice. The
        idempotency key makes those a no-op instead of a duplicate business transaction.
        """
        conn = self._conn()
        if idempotency_key:
            row = conn.execute(
                "SELECT * FROM run WHERE workflow=? AND idempotency_key=?",
                (workflow, idempotency_key),
            ).fetchone()
            if row:
                record = _row_to_run(row)
                record.resumed = True
                return record
        now = time.time()
        run_id = f"wf_{uuid.uuid4().hex[:16]}"
        conn.execute(
            "INSERT INTO run (run_id, workflow, status, idempotency_key, principal, tenant,"
            " input_json, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?)",
            (run_id, workflow, "running", idempotency_key, principal, tenant,
             json.dumps(payload, default=str), now, now),
        )
        return RunRecord(run_id, workflow, "running", payload, principal=principal,
                         tenant=tenant, idempotency_key=idempotency_key,
                         created_at=now, updated_at=now)

    def get_run(self, run_id: str) -> RunRecord | None:
        row = self._conn().execute("SELECT * FROM run WHERE run_id=?", (run_id,)).fetchone()
        return _row_to_run(row) if row else None

    def update_run(
        self,
        run_id: str,
        *,
        status: RunStatus | None = None,
        output: dict[str, Any] | None = None,
        error: str | None = None,
        cursor_step: str | None = None,
    ) -> None:
        sets, params = ["updated_at=?"], [time.time()]
        for column, value in (("status", status), ("error", error), ("cursor_step", cursor_step)):
            if value is not None:
                sets.append(f"{column}=?")
                params.append(value)
        if output is not None:
            sets.append("output_json=?")
            params.append(json.dumps(output, default=str))
        params.append(run_id)
        self._conn().execute(f"UPDATE run SET {', '.join(sets)} WHERE run_id=?", params)

    def list_runs(self, status: RunStatus | None = None, limit: int = 50) -> list[RunRecord]:
        sql = "SELECT * FROM run"
        params: list[Any] = []
        if status:
            sql += " WHERE status=?"
            params.append(status)
        sql += " ORDER BY updated_at DESC LIMIT ?"
        params.append(limit)
        return [_row_to_run(r) for r in self._conn().execute(sql, params).fetchall()]

    # -- steps --------------------------------------------------------------
    def get_step(self, run_id: str, name: str) -> StepRecord | None:
        row = self._conn().execute(
            "SELECT * FROM step WHERE run_id=? AND name=?", (run_id, name)
        ).fetchone()
        if not row:
            return None
        return StepRecord(
            run_id=row["run_id"],
            name=row["name"],
            status=row["status"],
            attempt=row["attempt"],
            output=json.loads(row["output_json"]) if row["output_json"] else None,
            error=row["error"],
        )

    def record_step(
        self,
        run_id: str,
        name: str,
        status: str,
        *,
        output: Any = None,
        error: str | None = None,
        attempt: int = 1,
        started_at: float | None = None,
    ) -> None:
        self._conn().execute(
            "INSERT INTO step (run_id, name, status, attempt, output_json, error, started_at,"
            " finished_at) VALUES (?,?,?,?,?,?,?,?)"
            " ON CONFLICT(run_id, name) DO UPDATE SET status=excluded.status,"
            " attempt=excluded.attempt, output_json=excluded.output_json, error=excluded.error,"
            " finished_at=excluded.finished_at",
            (run_id, name, status, attempt,
             json.dumps(output, default=str) if output is not None else None,
             error, started_at or time.time(), time.time()),
        )

    def steps(self, run_id: str) -> list[StepRecord]:
        rows = self._conn().execute(
            "SELECT * FROM step WHERE run_id=? ORDER BY started_at", (run_id,)
        ).fetchall()
        return [
            StepRecord(
                r["run_id"], r["name"], r["status"], r["attempt"],
                json.loads(r["output_json"]) if r["output_json"] else None, r["error"],
            )
            for r in rows
        ]

    # -- events -------------------------------------------------------------
    def append_event(self, run_id: str, kind: str, payload: Any = None) -> None:
        self._conn().execute(
            "INSERT INTO event (run_id, ts, kind, payload) VALUES (?,?,?,?)",
            (run_id, time.time(), kind, json.dumps(payload, default=str) if payload else None),
        )

    def events(self, run_id: str) -> Iterator[dict[str, Any]]:
        for row in self._conn().execute(
            "SELECT ts, kind, payload FROM event WHERE run_id=? ORDER BY id", (run_id,)
        ):
            yield {
                "ts": row["ts"],
                "kind": row["kind"],
                "payload": json.loads(row["payload"]) if row["payload"] else None,
            }


def _row_to_run(row: sqlite3.Row) -> RunRecord:
    return RunRecord(
        run_id=row["run_id"],
        workflow=row["workflow"],
        status=row["status"],
        input=json.loads(row["input_json"]),
        output=json.loads(row["output_json"]) if row["output_json"] else None,
        error=row["error"],
        cursor_step=row["cursor_step"],
        principal=row["principal"],
        tenant=row["tenant"],
        idempotency_key=row["idempotency_key"],
        created_at=row["created_at"],
        updated_at=row["updated_at"],
    )
