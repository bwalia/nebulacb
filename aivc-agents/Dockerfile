# Multi-stage: dependencies resolve once, the runtime image stays small and non-root.
FROM python:3.11-slim AS builder

ENV PIP_NO_CACHE_DIR=1 PIP_DISABLE_PIP_VERSION_CHECK=1
WORKDIR /build

COPY pyproject.toml README.md ./
COPY src ./src
COPY agents ./agents

RUN python -m venv /opt/venv \
 && /opt/venv/bin/pip install --upgrade pip \
 && /opt/venv/bin/pip install .

# ---------------------------------------------------------------------------
FROM python:3.11-slim AS runtime

ENV PATH="/opt/venv/bin:$PATH" \
    PYTHONUNBUFFERED=1 \
    PYTHONDONTWRITEBYTECODE=1 \
    PYTHONPATH=/app/src:/app \
    AIVC_PROVIDER=offline \
    AIVC_STATE_DIR=/app/.state \
    AIVC_DATA_DIR=/app/data \
    AIVC_CORPUS_DIR=/app/data/corpus \
    AIVC_CHECKPOINT_DB=/app/.state/workflow.sqlite \
    AIVC_WAREHOUSE_DB=/app/.state/warehouse.sqlite \
    AIVC_TRACE_FILE=/app/.state/traces.jsonl

RUN groupadd --system aivc && useradd --system --gid aivc --home /app aivc
WORKDIR /app

COPY --from=builder /opt/venv /opt/venv
COPY src ./src
COPY agents ./agents
COPY data ./data
COPY scripts ./scripts

RUN mkdir -p /app/.state && chown -R aivc:aivc /app
USER aivc

EXPOSE 8000
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
  CMD python -c "import urllib.request,sys; sys.exit(0 if urllib.request.urlopen('http://localhost:8000/healthz', timeout=3).status==200 else 1)"

CMD ["uvicorn", "aivc.api:app", "--host", "0.0.0.0", "--port", "8000"]
