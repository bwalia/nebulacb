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

## Local Development Setup

This section walks through setting up a complete local environment from scratch.

### Option A: Docker Compose (quickest)

#### 1. Start the containers

```bash
docker-compose up -d
```

This starts two Couchbase clusters and NebulaCB:
- **Couchbase Source** (v7.2.2) — `http://localhost:8091`
- **Couchbase Target** (v7.6.0) — `http://localhost:9091`
- **NebulaCB** — `http://localhost:8080`

#### 2. Initialize the Couchbase clusters

After the containers start, the Couchbase nodes need to be initialized. Open each Couchbase UI and complete the setup wizard, or use the REST API:

**Source cluster (localhost:8091):**

```bash
# Initialize the node
curl -X POST http://localhost:8091/nodes/self/controller/settings \
  -d 'data_path=/opt/couchbase/var/lib/couchbase/data' \
  -d 'index_path=/opt/couchbase/var/lib/couchbase/data'

# Set up services
curl -X POST http://localhost:8091/node/controller/setupServices \
  -d 'services=kv,n1ql,index'

# Set admin credentials
curl -X POST http://localhost:8091/settings/web \
  -d 'port=8091' \
  -d 'username=Administrator' \
  -d 'password=password123'

# Create the test bucket
curl -X POST http://localhost:8091/pools/default/buckets \
  -u Administrator:password123 \
  -d 'name=xdcr-test' \
  -d 'ramQuota=256' \
  -d 'bucketType=couchbase'
```

**Target cluster (localhost:9091):**

```bash
curl -X POST http://localhost:9091/nodes/self/controller/settings \
  -d 'data_path=/opt/couchbase/var/lib/couchbase/data' \
  -d 'index_path=/opt/couchbase/var/lib/couchbase/data'

curl -X POST http://localhost:9091/node/controller/setupServices \
  -d 'services=kv,n1ql,index'

curl -X POST http://localhost:9091/settings/web \
  -d 'port=8091' \
  -d 'username=Administrator' \
  -d 'password=password123'

curl -X POST http://localhost:9091/pools/default/buckets \
  -u Administrator:password123 \
  -d 'name=xdcr-test' \
  -d 'ramQuota=256' \
  -d 'bucketType=couchbase'
```

#### 3. Set up XDCR replication

```bash
# Create a remote cluster reference on the source pointing to the target
curl -X POST http://localhost:8091/pools/default/remoteClusters \
  -u Administrator:password123 \
  -d 'name=cb-test' \
  -d 'hostname=couchbase-target:8091' \
  -d 'username=Administrator' \
  -d 'password=password123'

# Create the XDCR replication (source bucket → target bucket)
curl -X POST http://localhost:8091/controller/createReplication \
  -u Administrator:password123 \
  -d 'fromBucket=xdcr-test' \
  -d 'toCluster=cb-test' \
  -d 'toBucket=xdcr-test' \
  -d 'replicationType=continuous'
```

> **Note:** In docker-compose the target hostname is `couchbase-target` (the Docker service name). From outside Docker, use `localhost:9091`.

#### 4. Open the dashboard

Go to `http://localhost:8080` and log in with `admin` / `nebulacb`.

#### 5. Run the load test

```bash
# Use Docker config (source=localhost:8091, target=localhost:9091)
go run ./cmd/xdcr-loadtest/ -rate 100 -duration 10m
```

To tear down:

```bash
docker-compose down
# Add -v to also remove persistent Couchbase data:
docker-compose down -v
```

---

### Option B: Kubernetes (k3s) with NodePort

If your Couchbase clusters are running in Kubernetes via the Couchbase Autonomous Operator:

#### 1. Expose clusters externally

Patch the Couchbase Operator to expose external addresses:

```bash
# Source cluster
kubectl patch couchbasecluster cb-local -n couchbase --type merge -p \
  '{"spec":{"networking":{"exposedFeatures":["client","admin"],"exposedFeatureServiceType":"NodePort","adminConsoleServiceType":"NodePort"}}}'

# Target cluster
kubectl patch couchbasecluster cb-test -n couchbase-test --type merge -p \
  '{"spec":{"networking":{"exposedFeatures":["client","admin"],"exposedFeatureServiceType":"NodePort","adminConsoleServiceType":"NodePort"}}}'
```

The Operator creates per-pod NodePort services (e.g. `cb-local-0003`, `cb-test-0002`) with management, KV, and query ports.

#### 2. Find the assigned ports and node IP

```bash
# Find which node the pods run on
kubectl get pods -n couchbase -o wide
kubectl get pods -n couchbase-test -o wide

# Get the per-pod NodePort services
kubectl get svc -n couchbase -l couchbase_cluster=cb-local
kubectl get svc -n couchbase-test -l couchbase_cluster=cb-test
```

Look for the `NodePort` services (not the headless `ClusterIP` ones). Note the ports for `mgmt` (8091) and `kv` (11210).

> **Important:** If the services have `externalTrafficPolicy: Local`, NodePort only works on the node where the pod runs. Use that node's IP.

#### 3. Verify the cluster map has external addresses

```bash
curl -s -u Administrator:password123 http://<NODE_IP>:<MGMT_NODEPORT>/pools/default \
  | python3 -c "import sys,json; [print(n.get('alternateAddresses',{})) for n in json.load(sys.stdin)['nodes']]"
```

You should see `external.hostname` and `external.ports.kv` in the output.

#### 4. Update config.json

```jsonc
{
  "source": {
    "name": "cb-local",
    "host": "<NODE_IP>:<MGMT_NODEPORT>",
    "username": "Administrator",
    "password": "password123",
    "bucket": "xdcr-test",
    "platform": "kubernetes",
    "namespace": "couchbase",
    "kv_port": <KV_NODEPORT>
  },
  "target": {
    "name": "cb-test",
    "host": "<NODE_IP>:<MGMT_NODEPORT>",
    "username": "Administrator",
    "password": "password123",
    "bucket": "xdcr-test",
    "platform": "kubernetes",
    "namespace": "couchbase-test",
    "kv_port": <KV_NODEPORT>
  }
}
```

The `kv_port` field tells the SDK to connect via the KV NodePort and use `?network=external` so it reads the alternate addresses from the cluster map instead of internal pod IPs.

#### 5. Create the test bucket (if it doesn't exist)

```bash
curl -X POST http://<NODE_IP>:<MGMT_NODEPORT>/pools/default/buckets \
  -u Administrator:password123 \
  -d 'name=xdcr-test' \
  -d 'ramQuota=256' \
  -d 'bucketType=couchbase'
```

#### 6. Set up XDCR replication

XDCR is configured between the clusters inside Kubernetes. Use the Couchbase UI or REST API on the source cluster:

```bash
# Create remote cluster reference (use internal k8s DNS)
curl -X POST http://<NODE_IP>:<SOURCE_MGMT_NODEPORT>/pools/default/remoteClusters \
  -u Administrator:password123 \
  -d 'name=cb-test' \
  -d 'hostname=cb-test.couchbase-test.svc:8091' \
  -d 'username=Administrator' \
  -d 'password=password123'

# Create replication
curl -X POST http://<NODE_IP>:<SOURCE_MGMT_NODEPORT>/controller/createReplication \
  -u Administrator:password123 \
  -d 'fromBucket=xdcr-test' \
  -d 'toCluster=cb-test' \
  -d 'toBucket=xdcr-test' \
  -d 'replicationType=continuous'
```

#### 7. Start NebulaCB and run the load test

```bash
# Start the server
make run

# In another terminal, run the XDCR load test
go run ./cmd/xdcr-loadtest/ -rate 100 -duration 10m

# Open dashboard
open http://localhost:8899
```

---

### Option C: Native Go server with port-forwarding (no NodePort)

If you don't want to expose NodePorts, NebulaCB can auto-forward ports via kubectl:

Set `platform: "kubernetes"` and `namespace` in config.json, and provide a `kubeconfig` path. NebulaCB will automatically discover Couchbase pods and set up `kubectl port-forward` on startup. No `kv_port` needed — the SDK connects to `localhost` directly.

```jsonc
{
  "kubeconfig": "~/.kube/config",
  "source": {
    "name": "cb-local",
    "host": "localhost:8091",
    "platform": "kubernetes",
    "namespace": "couchbase"
  }
}
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
