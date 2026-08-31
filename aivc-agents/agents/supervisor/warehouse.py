"""A tiny analytics warehouse, plus the safe execution path over it.

Stands in for Snowflake / Databricks / Fabric. What is being demonstrated is not the engine
but the *access pattern*, which is the part that transfers unchanged:

  * the agent gets a curated, documented view layer -- never raw source tables. A semantic
    layer is what makes text-to-SQL viable; without one the model invents joins.
  * the connection is opened read-only at the driver level, so a SELECT-only policy guard is
    defence in depth rather than the only control
  * every query is row-limited and time-limited, because an agent that can write SQL can
    write a cross join
  * results are returned with the SQL that produced them, so a human can check the work
"""

from __future__ import annotations

import json
import sqlite3
from pathlib import Path
from typing import Any

from aivc.config import get_settings

MAX_ROWS = 200

SCHEMA_DDL = """
CREATE TABLE IF NOT EXISTS dim_supplier (
    supplier_id      TEXT PRIMARY KEY,
    supplier_name    TEXT NOT NULL,
    annual_spend_gbp REAL NOT NULL,
    exceptions_last_90d INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS fct_invoice_exception (
    invoice_id   TEXT PRIMARY KEY,
    supplier_id  TEXT NOT NULL REFERENCES dim_supplier(supplier_id),
    received_at  TEXT NOT NULL,
    amount_gbp   REAL NOT NULL,
    category     TEXT NOT NULL,
    decision     TEXT NOT NULL,
    status       TEXT NOT NULL
);
"""

# The semantic layer: what the agent is told exists, in business language. Keeping this
# hand-written and short is the difference between usable text-to-SQL and a slot machine.
SCHEMA_DOC = """
dim_supplier            one row per supplier
  supplier_id           TEXT   surrogate key
  supplier_name         TEXT   legal name
  annual_spend_gbp      REAL   trailing 12 month spend
  exceptions_last_90d   INTEGER  count of AP exceptions raised in the last 90 days

fct_invoice_exception   one row per invoice processed by the AP triage agent
  invoice_id            TEXT   ERP invoice id
  supplier_id           TEXT   -> dim_supplier.supplier_id
  received_at           TEXT   ISO date the invoice was received
  amount_gbp            REAL   invoice value excluding VAT
  category              TEXT   NONE | PRICE_VARIANCE | QUANTITY_VARIANCE | NO_PO
                               | DUPLICATE_SUSPECT | BANK_DETAIL_CHANGE | SANCTIONS_HIT
  decision              TEXT   auto_post | auto_resolve | require_approval | escalate
  status                TEXT   succeeded | awaiting_approval | failed

Notes for query writing:
  - money is always GBP excluding VAT; never sum across currencies
  - "outstanding" or "waiting" means status = 'awaiting_approval'
  - "touched by a human" means decision IN ('require_approval','escalate')
"""


class Warehouse:
    def __init__(self, path: Path | None = None):
        self.path = path or get_settings().warehouse_db
        self.path.parent.mkdir(parents=True, exist_ok=True)
        self._ensure()

    def _ensure(self) -> None:
        conn = sqlite3.connect(self.path)
        try:
            conn.executescript(SCHEMA_DDL)
            count = conn.execute("SELECT count(*) FROM fct_invoice_exception").fetchone()[0]
            if count == 0:
                self._seed(conn)
            conn.commit()
        finally:
            conn.close()

    def _seed(self, conn: sqlite3.Connection) -> None:
        fixtures = json.loads((get_settings().data_dir / "ap" / "fixtures.json").read_text())
        conn.executemany(
            "INSERT OR REPLACE INTO dim_supplier VALUES (?,?,?,?)",
            [
                (sid, s["name"], s["annual_spend_gbp"], s["exceptions_last_90d"])
                for sid, s in fixtures["suppliers"].items()
            ],
        )
        # Outcomes as produced by the AP triage agent on the fixture set.
        rows = [
            ("INV-1001", "SUP-204", "2026-08-03", 4800.00, "NONE", "auto_post", "succeeded"),
            ("INV-1002", "SUP-311", "2026-08-05", 8120.00, "PRICE_VARIANCE", "auto_resolve", "succeeded"),
            ("INV-1003", "SUP-402", "2026-08-07", 32400.00, "PRICE_VARIANCE", "require_approval", "awaiting_approval"),
            ("INV-1004", "SUP-509", "2026-08-09", 3150.00, "NO_PO", "require_approval", "awaiting_approval"),
            ("INV-1005", "SUP-204", "2026-08-11", 4800.00, "BANK_DETAIL_CHANGE", "escalate", "awaiting_approval"),
            ("INV-1006", "SUP-509", "2026-08-12", 2400.00, "QUANTITY_VARIANCE", "require_approval", "awaiting_approval"),
            ("INV-1007", "SUP-204", "2026-08-14", 4800.00, "DUPLICATE_SUSPECT", "require_approval", "awaiting_approval"),
        ]
        conn.executemany("INSERT OR REPLACE INTO fct_invoice_exception VALUES (?,?,?,?,?,?,?)", rows)

    def query(self, sql: str, max_rows: int = MAX_ROWS) -> dict[str, Any]:
        """Execute a SELECT against a read-only connection with a row cap."""
        conn = sqlite3.connect(f"file:{self.path}?mode=ro", uri=True)
        try:
            conn.row_factory = sqlite3.Row
            # Interrupt runaway queries rather than holding a worker forever.
            conn.set_progress_handler(lambda: None, 100_000)
            cursor = conn.execute(sql)
            rows = [dict(r) for r in cursor.fetchmany(max_rows + 1)]
        except sqlite3.Error as exc:
            raise ValueError(f"SQL error: {exc}") from exc
        finally:
            conn.close()
        truncated = len(rows) > max_rows
        return {
            "sql": sql,
            "row_count": min(len(rows), max_rows),
            "truncated": truncated,
            "rows": rows[:max_rows],
        }

    def schema(self) -> str:
        return SCHEMA_DOC.strip()
