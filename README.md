# NebulaCB

Couchbase Migration & Upgrade Mission Control Platform.

Simulate, validate, and observe Couchbase cluster upgrades under real-world load with zero data loss guarantees. Upgrade fearlessly. Validate everything. Lose nothing.

---

## Architecture

```
                        React Dashboard (port 8080)
                               |
                          WebSocket + REST
                               |
                     NebulaCB API Server
                    /    |    |    |    \
              Storm  XDCR  Validator  Orchestrator  Monitor
                |      |      |           |            |
             ClientPool (SDK + REST connections)
              /                                  \
   Couchbase Source (8091)              Couchbase Target (9091)
```

**Core modules:**

| Module | Purpose |
|--------|---------|
| Storm Generator | Continuous load generation (writes, reads, deletes) |
| Upgrade Orchestrator | Helm-based rolling upgrade with pause/resume |
| XDCR Engine | Replication monitoring, lag tracking, pipeline state |
| Data Validator | Source vs target integrity checks (hash, sequence, key diff) |
| Cluster Monitor | Polls all clusters every 2 seconds |
| Failover Manager | Automatic/manual HA failover |
| Backup Manager | Scheduled backups with retention |
| Migration Engine | Cross-cluster data migration |
| Region Manager | Multi-region orchestration |
| AI Analyzer | Claude API integration for log analysis |

---

## Prerequisites

- **Go** 1.24+
- **Node.js** 18+ and npm (for the web UI)
- **Two Couchbase clusters** (source and target) accessible over the network
- **kubectl** and **helm** (optional, for Kubernetes deployments)
- **Docker** and **docker-compose** (optional, for local dev with containers)

---

## Quick Start

### 1. Clone and build

```bash
git clone https://github.com/balinderwalia/nebulacb.git
cd nebulacb
make build
```

This produces two binaries in `bin/`:
- `bin/nebulacb` — the server
- `bin/nebulacb-cli` — the CLI client

### 2. Configure

Edit `config.json` to match your environment:

```jsonc
{
  "server": {
    "host": "0.0.0.0",
    "port": 8899,
    "auth": {
      "enabled": true,
      "username": "admin",
      "password": "nebulacb"
    }
  },
  "source": {
    "name": "cb-local",
    "host": "localhost:8091",
    "username": "Administrator",
    "password": "password123",
    "bucket": "xdcr-test"
  },
  "target": {
    "name": "cb-test",
    "host": "localhost:9091",
    "username": "Administrator",
    "password": "password123",
    "bucket": "xdcr-test"
  }
}
```

Key fields to update:
- `source.host` / `target.host` — your Couchbase cluster addresses
- `source.username` / `source.password` — Couchbase credentials
- `source.bucket` / `target.bucket` — bucket names
- `kubeconfig` — path to kubeconfig if using Kubernetes clusters (enables auto port-forwarding)

All config values can be overridden via environment variables (see [Environment Variables](#environment-variables)).

### 3. Start the server

```bash
make run
# or directly:
bin/nebulacb --config config.json
```

The server starts on the configured port (default 8899). Open the dashboard at:

```
http://localhost:8899
```

Login with the credentials from `config.json` (`admin` / `nebulacb` by default).

### 4. Start the web UI in development mode (optional)

If you want hot-reloading during frontend development:

```bash
make ui-dev
# or manually:
cd web/nebulacb-ui
npm install
npm start
```

This starts the React dev server on port 3000 with live reload. It proxies API requests to the backend.

For a production build of the UI (served by the Go server):

```bash
make ui
```

---

## Docker Compose (Easiest Local Setup)

Spin up NebulaCB with two Couchbase clusters locally:

```bash
docker-compose up -d
```

This starts:
- **NebulaCB** on port 8080 (API + UI) and 9090 (metrics)
- **Couchbase Source** (v7.2.2) on port 8091
- **Couchbase Target** (v7.6.0) on port 9091

Open `http://localhost:8080` in your browser.

To tear down:

```bash
docker-compose down
```

---

## Using the Dashboard

The web dashboard is a mission-control cockpit with real-time updates via WebSocket.

**Dashboard panels:**
- **Cluster Health** — node status, versions, CPU/memory, rebalance state
- **XDCR Replication** — lag, pipeline state, topology changes, GOXDCR delay timer
- **Data Integrity** — doc counts, missing/extra keys, hash mismatches
- **Load Metrics** — writes/sec, reads/sec, latency P50/P95/P99
- **Upgrade Progress** — phase, node-by-node progress, availability %
- **Alerts** — data loss warnings, replication stalled, node failures

**Control buttons (in the Control Panel):**
- Start / Pause / Resume / Stop Load
- Start / Abort Upgrade
- Restart XDCR
- Inject Failure (chaos testing)
- Run Audit (data integrity check)

---

## Using the CLI

The CLI is an HTTP client that talks to the running NebulaCB server.

### Setup

```bash
export NEBULACB_URL=http://localhost:8899
export NEBULACB_USER=admin
export NEBULACB_PASS=nebulacb
```

### Commands

```bash
# Show full dashboard status
bin/nebulacb-cli status

# Load generation
bin/nebulacb-cli start-load
bin/nebulacb-cli pause-load
bin/nebulacb-cli resume-load
bin/nebulacb-cli stop-load

# Upgrade orchestration
bin/nebulacb-cli start-upgrade
bin/nebulacb-cli abort-upgrade

# XDCR
bin/nebulacb-cli restart-xdcr

# Data integrity
bin/nebulacb-cli run-audit

# Chaos testing
bin/nebulacb-cli inject-failure

# Monitoring
bin/nebulacb-cli alerts
bin/nebulacb-cli health
bin/nebulacb-cli config

# Reporting
bin/nebulacb-cli report
```

Or use Makefile shortcuts:

```bash
make start-load
make stop-load
make start-upgrade
make status
make audit
make report
```

---

## XDCR Load Test Script

A standalone script for sending test data to both clusters randomly during upgrades to validate XDCR replication behavior.

### Run

```bash
# Default: 100 writes/sec, 50/50 split across clusters
go run ./cmd/xdcr-loadtest/

# Higher throughput, 70/30 split favoring source, 10 minute run
go run ./cmd/xdcr-loadtest/ -rate 500 -ratio 0.7 -duration 10m

# Custom config and larger documents
go run ./cmd/xdcr-loadtest/ -config config.json -rate 200 -doc-min 1024 -doc-max 8192 -workers 16
```

### Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-config` | `config.json` | Config file path |
| `-rate` | `100` | Total writes per second across both clusters |
| `-ratio` | `0.5` | Fraction sent to cluster 1 (0.5 = equal split) |
| `-duration` | `0` | Test duration (0 = run until Ctrl+C) |
| `-workers` | `8` | Concurrent writer goroutines |
| `-doc-min` | `256` | Minimum document size in bytes |
| `-doc-max` | `4096` | Maximum document size in bytes |
| `-prefix` | `xdcr-test` | Document key prefix |

The script prints stats every 5 seconds and a final summary on exit:

```
[STATS] cluster1=247 (+50) cluster2=253 (+51) errors=0 (+0) avg_latency=2.31ms
```

---

## Kubernetes Deployment

### Using Helm

```bash
# Install
make helm-install
# or:
helm install nebulacb deploy/helm/nebulacb -n nebulacb --create-namespace

# Upgrade
make helm-upgrade

# Uninstall
make helm-uninstall
```

The Helm chart deploys:
- Deployment with health checks (liveness + readiness on `/api/v1/health`)
- ClusterIP Service on port 8080
- ConfigMap and Secrets for configuration
- RBAC (ServiceAccount + Role)
- Optional Ingress (disabled by default)

Edit `deploy/helm/nebulacb/values.yaml` to customize image, resources, ingress, etc.

### Kubeconfig auto port-forwarding

If `kubeconfig` is set in `config.json` and clusters have `platform: "kubernetes"`, NebulaCB automatically sets up port-forwarding to Couchbase pods in the specified namespaces.

---

## API Reference

All endpoints require Basic Auth (unless auth is disabled).

### Core

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/api/v1/health` | Health check (no auth required) |
| POST | `/api/v1/login` | Get session token |
| GET | `/api/v1/dashboard` | Full dashboard state |
| POST | `/api/v1/command` | Execute action |
| GET | `/api/v1/alerts` | Active alerts |
| POST | `/api/v1/reports` | Generate report |
| GET | `/api/v1/config` | Show configuration |
| GET | `/api/v1/clusters` | List connected clusters |

### Failover & HA

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/api/v1/failover` | Failover status |
| POST | `/api/v1/failover/manual` | Trigger manual failover |
| POST | `/api/v1/failover/graceful` | Graceful failover |
| GET | `/api/v1/failover/history` | Failover event history |

### Backup & Restore

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/api/v1/backup` | Backup status |
| POST | `/api/v1/backup/start` | Start backup |
| POST | `/api/v1/backup/restore` | Restore from backup |
| GET | `/api/v1/backup/list` | List backups |

### Migration

| Method | Endpoint | Description |
|--------|----------|-------------|
| GET | `/api/v1/migration` | Migration status |
| POST | `/api/v1/migration/start` | Start migration |
| GET | `/api/v1/migration/history` | Migration history |

### AI Analysis (requires `ai.enabled: true`)

| Method | Endpoint | Description |
|--------|----------|-------------|
| POST | `/api/v1/ai/analyze` | Analyze cluster state |
| POST | `/api/v1/ai/auto-analyze` | Auto-analyze with AI |
| GET | `/api/v1/ai/insights` | Get AI insights |

### WebSocket

Connect to `ws://localhost:8899/ws` for real-time dashboard updates. Authenticate with Basic Auth header or `token` query parameter.

---

## Environment Variables

All config values can be overridden via environment variables:

| Variable | Description |
|----------|-------------|
| `NEBULACB_AUTH_USERNAME` | API auth username |
| `NEBULACB_AUTH_PASSWORD` | API auth password |
| `NEBULACB_AUTH_ENABLED` | Set to `false` to disable auth |
| `NEBULACB_DOMAIN` | Server domain |
| `NEBULACB_TLS_CERT` | TLS certificate file path |
| `NEBULACB_TLS_KEY` | TLS key file path |
| `NEBULACB_SOURCE_HOST` | Source cluster host |
| `NEBULACB_SOURCE_USERNAME` | Source cluster username |
| `NEBULACB_SOURCE_PASSWORD` | Source cluster password |
| `NEBULACB_SOURCE_BUCKET` | Source cluster bucket |
| `NEBULACB_TARGET_HOST` | Target cluster host |
| `NEBULACB_TARGET_USERNAME` | Target cluster username |
| `NEBULACB_TARGET_PASSWORD` | Target cluster password |
| `NEBULACB_TARGET_BUCKET` | Target cluster bucket |
| `NEBULACB_AI_PROVIDER` | AI provider (anthropic, openai, ollama) |
| `NEBULACB_AI_API_KEY` | AI API key |
| `NEBULACB_AI_MODEL` | AI model name |
| `NEBULACB_AI_ENDPOINT` | Custom AI endpoint |
| `NEBULACB_DOCKER_HOST` | Docker daemon socket |

**Dynamic cluster registration** via env vars:

```bash
export NEBULACB_CLUSTER_DC1_HOST=10.0.1.100:8091
export NEBULACB_CLUSTER_DC1_USERNAME=admin
export NEBULACB_CLUSTER_DC1_PASSWORD=secret
export NEBULACB_CLUSTER_DC1_BUCKET=myapp
export NEBULACB_CLUSTER_DC1_ROLE=source
export NEBULACB_CLUSTER_DC1_REGION=us-east-1
```

Any `NEBULACB_CLUSTER_<NAME>_<FIELD>` variable registers a cluster dynamically.

---

## Makefile Reference

```bash
make build            # Build Go binaries (bin/nebulacb, bin/nebulacb-cli)
make run              # Build and run the server
make test             # Run Go tests
make clean            # Remove build artifacts

make ui               # Build React UI for production
make ui-dev           # Start React dev server (port 3000)

make docker           # Build Docker image
make docker-up        # docker-compose up -d
make docker-down      # docker-compose down

make helm-install     # Install Helm chart to Kubernetes
make helm-upgrade     # Upgrade Helm release
make helm-uninstall   # Uninstall Helm release

make start-load       # CLI: start load generation
make stop-load        # CLI: stop load generation
make start-upgrade    # CLI: start upgrade
make status           # CLI: show dashboard
make audit            # CLI: run data integrity audit
make report           # CLI: generate report
```

---

## Typical Upgrade Test Workflow

1. **Start NebulaCB** and open the dashboard
   ```bash
   make run
   # Open http://localhost:8899
   ```

2. **Start load generation** to simulate production traffic
   ```bash
   bin/nebulacb-cli start-load
   ```

3. **Start the XDCR load test** (optional, for dual-cluster writes)
   ```bash
   go run ./cmd/xdcr-loadtest/ -rate 200 -duration 30m
   ```

4. **Trigger the rolling upgrade**
   ```bash
   bin/nebulacb-cli start-upgrade
   ```

5. **Monitor** the dashboard for:
   - XDCR replication lag and pipeline restarts
   - Data integrity (missing keys, hash mismatches)
   - Node health during rolling restart
   - Load generator latency spikes

6. **Run a post-upgrade audit**
   ```bash
   bin/nebulacb-cli run-audit
   ```

7. **Generate a report**
   ```bash
   bin/nebulacb-cli report
   ```

---

## Project Structure

```
nebulacb/
  cmd/
    nebulacb/          # Main server entry point
    cli/               # CLI client
    xdcr-loadtest/     # Standalone XDCR load test script
  internal/
    api/               # HTTP server, routes, WebSocket
    ai/                # AI-powered analysis (Claude API)
    backup/            # Backup & restore management
    config/            # Configuration loading
    failover/          # HA & failover orchestration
    metrics/           # Prometheus metrics collector
    migration/         # Cross-cluster data migration
    models/            # Data structures
    monitor/           # Multi-cluster health polling
    orchestrator/      # Helm-based upgrade automation
    region/            # Multi-region management
    reporting/         # Report generation
    scenarios/         # Pre-built test scenarios
    storm/             # Load generator
    validator/         # Data integrity validation
    xdcr/              # XDCR replication engine
  pkg/
    couchbase/         # Couchbase SDK client & connection pool
    docker/            # Docker client
    kubernetes/        # K8s port-forwarding
    websocket/         # WebSocket hub
  web/
    nebulacb-ui/       # React dashboard (TypeScript)
  deploy/
    helm/nebulacb/     # Kubernetes Helm chart
  config.json          # Default configuration
  docker-compose.yaml  # Local dev with Couchbase containers
  Dockerfile           # Multi-stage Docker build
  Makefile             # Build, run, deploy shortcuts
```

---

## License

See [LICENSE](LICENSE) for details.
