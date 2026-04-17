# NebulaCB

Couchbase Migration & Upgrade Mission Control Platform.

Simulate, validate, and observe Couchbase cluster upgrades under real-world load with zero data loss guarantees. Upgrade fearlessly. Validate everything. Lose nothing.

---

## Features

Operator-facing capabilities exposed through the Mission Control cockpit and `/api/v1/command`:

- **Multi-cluster monitoring** — live metrics for every cluster every 2s (ops/sec, resident ratio, disk queue, cache miss, connections).
- **Rolling upgrades** — Kubernetes Operator-aware, pod-by-pod with live progress and abort.
- **Downgrade** — roll back to prior image via Operator rolling restart with confirmation modal.
- **Storm load generator** — start / pause / resume / stop, SDK or REST client, configurable regions and rates.
- **XDCR management** — pause, resume, stop, restart, and troubleshoot diagnostics modal with delay history.
- **Data integrity audit** — source vs target hash + sequence + key-diff comparison.
- **Chaos injection** — XDCR partition and node failure injection for resilience testing.
- **Backup & restore (EE + CE)** — auto-detects `cbbackupmgr`; falls back to a **parallel SDK JSONL export** on Community Edition. Restore modal picks any prior backup and target cluster. Live docs/bytes progress.
- **HA manual failover** — source/target promotion with confirmation modal.
- **AI analysis** — on-demand RCA via Ollama (local) or Anthropic API.
- **Kubernetes observability** — pod logs, events, Operator status, runbooks, recommendations tabs.
- **Multi-region orchestration** — primary/secondary region topology and failover.
- **Data migration** — parallel workers, batching, optional transform rules, post-migration validation.
- **Systemd packaging** — `.deb` / `.rpm` installers and `deploy/install/install.sh` for bare-metal VMs.

See the cockpit **Mission Control Panel** or `docs/landing/index.html` for the full 18-command reference.

---

## Architecture

```
                       React Dashboard (port 8899)
                               |
                          WebSocket + REST
                               |
                     NebulaCB API Server
              /    |    |    |    |    |    |    \
        Storm  XDCR  Validator  Orchestrator  Monitor  AI  Failover  K8s
            |     |       |          |          |      |      |       |
         ClientPool (SDK + REST + NodePort + Operator client)
              /                                  \
   Couchbase Source                         Couchbase Target
   (k8s / docker / native)                  (k8s / docker / native)
              \____________ XDCR (bidirectional) ___________/
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
| Backup Manager | Scheduled backups with retention; EE (`cbbackupmgr`) and CE (SDK JSONL) engines |
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

> **Want a one-line install on a Linux server?** Skip ahead to [Option D: System Package (`.deb` / `.rpm`) with systemd](#option-d-system-package-deb--rpm-with-systemd).

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

### Option D: System Package (`.deb` / `.rpm`) with systemd

NebulaCB ships as native packages for **Ubuntu / Debian** (`.deb`) and **CentOS / RHEL / Rocky / Alma / Fedora / openSUSE / SLES** (`.rpm`), built with [nfpm](https://github.com/goreleaser/nfpm). All packages install a hardened systemd service that listens on **port 8899**.

#### Build the packages

```bash
# Install nfpm once (writes to ~/go/bin)
go install github.com/goreleaser/nfpm/v2/cmd/nfpm@latest
export PATH=$PATH:$(go env GOPATH)/bin

# Build .deb + .rpm into ./dist/
make package

# Or build a single format
make package-deb     # Ubuntu / Debian
make package-rpm     # CentOS / RHEL / Rocky / Alma / Fedora / openSUSE / SLES
```

This produces:
```
dist/nebulacb_1.0.0_amd64.deb
dist/nebulacb-1.0.0-1.x86_64.rpm
```

#### Install via package manager

```bash
# Debian / Ubuntu
sudo dpkg -i dist/nebulacb_1.0.0_amd64.deb

# RHEL / CentOS / Rocky / Alma / Fedora
sudo dnf install dist/nebulacb-1.0.0-1.x86_64.rpm
# or:
sudo rpm -i dist/nebulacb-1.0.0-1.x86_64.rpm

# openSUSE / SLES
sudo zypper install dist/nebulacb-1.0.0-1.x86_64.rpm
```

The package post-install hook automatically:
- Creates the `nebulacb` system user
- Creates `/etc/nebulacb`, `/var/lib/nebulacb`, `/var/log/nebulacb`
- Reloads systemd, enables and starts `nebulacb.service`

#### Install via shell script (no package manager)

If you want to install directly from the source tree:

```bash
# Builds binary + UI, then installs system-wide
make install-local

# Seed /etc/nebulacb/config.json from your own file
make install-local SOURCE_CONFIG=/path/to/config.json
```

#### Verify and operate

```bash
sudo systemctl status nebulacb       # check service
sudo systemctl restart nebulacb      # after editing /etc/nebulacb/config.json
sudo journalctl -u nebulacb -f       # tail logs
curl http://localhost:8899/api/v1/health
```

#### What gets installed

| Path | Purpose |
|------|---------|
| `/usr/local/bin/nebulacb` | Static Go binary (~20 MB) |
| `/usr/local/share/nebulacb/web/nebulacb-ui/build/` | React UI bundle |
| `/etc/nebulacb/config.json` | Editable config (`0640 root:nebulacb`) |
| `/etc/systemd/system/nebulacb.service` | Hardened systemd unit |
| `/var/lib/nebulacb/` | State directory |
| `/var/log/nebulacb/` | Log directory (journal also captures everything) |

The systemd unit applies these hardening flags:
```
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
RestrictNamespaces=true
LimitNOFILE=65536
```

#### Uninstall

```bash
# Preserve config + data
sudo dpkg -r nebulacb            # Debian / Ubuntu
sudo dnf remove nebulacb         # RHEL family
sudo zypper remove nebulacb      # openSUSE / SLES

# Or via the shell uninstaller
make uninstall-local             # leaves /etc/nebulacb intact
make uninstall-local ARGS=--purge  # full removal (config, data, logs, user)
```

The installer script auto-detects the host distribution from `/etc/os-release` and uses the right `useradd` / `adduser` flags for that family. See `deploy/install/install.sh` and `deploy/packaging/nfpm.yaml` for the full source.

---

## Using the Dashboard

The web dashboard is a mission-control cockpit with real-time updates via WebSocket. It ships with **10 workspace tabs**:

| Tab | Purpose |
|-----|---------|
| 🛰 **Cockpit** *(default)* | NASA-style mission-control grid: status pill, 4-tile top strip (source / target / XDCR flow / load + alerts), full-width upgrade timeline with phase rail, 3-column bottom row (live logs / data integrity / controls) |
| ◈ **Dashboard** | Legacy panel-stack view (preserved for parity) |
| 🤖 **Ask AI** | Chat with the AI about cluster issues — full context of state, XDCR, alerts, metrics |
| 🔍 **RCA** | AI-powered root cause analysis with evidence chain + remediation |
| 📚 **Knowledge** | 12+ built-in troubleshooting guides (XDCR, upgrade, failover, backup, perf, etc.) |
| 📊 **Insights** | History of all AI analyses |
| 📜 **Pod Logs** | Live tail of Couchbase / Operator pod logs across namespaces |
| ⚡ **Events** | Real-time Kubernetes event stream with filters |
| ☸ **Operator** | CouchbaseCluster CR + Operator health and status |
| 📋 **Runbooks** | Opinionated remediation playbooks |

**Cockpit panels (default tab):**
- **Cluster Health** — node status, versions, CPU/memory, rebalance state, glowing health tiles
- **XDCR Replication** — lag, pipeline state, topology changes, GOXDCR delay timer with countdown
- **Data Integrity** — doc counts, delta convergence chart, hash mismatches, zero-loss verdict
- **Load Metrics** — writes/sec, reads/sec, latency P50/P95/P99, streaming charts
- **Upgrade Timeline** — 6-stage phase rail, progress bar, live event stream
- **Live Logs** — tail-style log panel with severity + source filters, pause toggle
- **Alerts** — data loss warnings, replication stalled, node failures
- **Mission Control** — 16 action buttons (load, upgrade, downgrade, XDCR, audit, AI, backup, failover, chaos)

**Control buttons (in the Mission Control Panel):**
- Start / Pause / Resume / Stop Load
- Start / Abort Upgrade · **Downgrade** (rollback to previous version)
- Pause / Resume / Stop / Restart XDCR · XDCR Troubleshoot
- Run Audit · Inject Failure · AI Analyze
- Backup · Manual Failover · Force Reconnect

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

make package          # Build .deb + .rpm into ./dist/ (requires nfpm)
make package-deb      # Ubuntu / Debian package only
make package-rpm      # CentOS / RHEL / Fedora / openSUSE / SLES package only
make install-local    # Install to /usr/local/bin + systemd (sudo)
make uninstall-local  # Remove the local install (sudo)

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
    nebulacb-ui/       # React dashboard (TypeScript) — Cockpit + 9 other tabs
      src/components/
        CockpitView.tsx     # Mission-control grid
        UpgradeTimeline.tsx # Centerpiece phase rail + event stream
        LogsPanel.tsx       # Live log tail with filters
        K8sLogsPanel.tsx    # Pod log streaming
        K8sEventsPanel.tsx  # Kubernetes event stream
        OperatorPanel.tsx   # CouchbaseCluster CR + Operator state
        RunbooksPanel.tsx   # Built-in remediation playbooks
  deploy/
    helm/nebulacb/     # Kubernetes Helm chart
    systemd/           # nebulacb.service unit (hardened)
    install/           # install.sh, uninstall.sh, config.sample.json
    packaging/         # nfpm.yaml + scripts/ for .deb / .rpm builds
  docs/
    landing/           # Public landing page (docs/landing/index.html)
  config.json          # Default configuration
  docker-compose.yaml  # Local dev with Couchbase containers
  Dockerfile           # Multi-stage Docker build
  Makefile             # Build, run, deploy, package shortcuts
```

---

## License

See [LICENSE](LICENSE) for details.
