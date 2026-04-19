// nebulacb-mcp is a Model Context Protocol server that exposes the NebulaCB
// REST API as MCP tools over stdio, so MCP-aware clients (Claude Desktop,
// Claude Code, etc.) can drive a running NebulaCB server.
//
// Transport: JSON-RPC 2.0 over stdio, one request/notification per line,
// following the MCP spec (protocol version 2025-06-18).
//
// Configuration (via env vars):
//
//	NEBULACB_URL   — base URL of the NebulaCB API (default http://localhost:8080)
//	NEBULACB_USER  — Basic auth username (optional, used if NEBULACB_TOKEN unset)
//	NEBULACB_PASS  — Basic auth password (optional)
//	NEBULACB_TOKEN — bearer token obtained from /api/v1/login (preferred)
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const (
	protocolVersion = "2025-06-18"
	serverName      = "nebulacb-mcp"
	serverVersion   = "1.0.0"
	defaultBaseURL  = "http://localhost:8080"
)

func main() {
	log.SetOutput(os.Stderr) // stdout is reserved for JSON-RPC
	log.SetPrefix("[nebulacb-mcp] ")

	srv := newServer()
	if err := srv.serve(os.Stdin, os.Stdout); err != nil && err != io.EOF {
		log.Fatalf("serve: %v", err)
	}
}

// ─── JSON-RPC types ──────────────────────────────────────────────────────────

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  interface{}     `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// ─── Tool registry ───────────────────────────────────────────────────────────

type tool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"inputSchema"`
	handler     func(ctx context.Context, args map[string]interface{}) (string, error)
}

type server struct {
	client  *http.Client
	baseURL string
	user    string
	pass    string
	token   string
	tools   map[string]*tool
}

func newServer() *server {
	base := os.Getenv("NEBULACB_URL")
	if base == "" {
		base = defaultBaseURL
	}
	s := &server{
		client:  &http.Client{Timeout: 30 * time.Second},
		baseURL: strings.TrimRight(base, "/"),
		user:    firstEnv("NEBULACB_USER", "NEBULACB_AUTH_USERNAME"),
		pass:    firstEnv("NEBULACB_PASS", "NEBULACB_AUTH_PASSWORD"),
		token:   os.Getenv("NEBULACB_TOKEN"),
		tools:   map[string]*tool{},
	}
	s.registerTools()
	return s
}

func firstEnv(keys ...string) string {
	for _, k := range keys {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return ""
}

// ─── Main I/O loop ───────────────────────────────────────────────────────────

func (s *server) serve(in io.Reader, out io.Writer) error {
	reader := bufio.NewReaderSize(in, 1<<20)
	writer := bufio.NewWriter(out)
	enc := json.NewEncoder(writer)

	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			if resp, ok := s.handleLine(bytes.TrimSpace(line)); ok {
				if err := enc.Encode(resp); err != nil {
					return err
				}
				if err := writer.Flush(); err != nil {
					return err
				}
			}
		}
		if err != nil {
			return err
		}
	}
}

func (s *server) handleLine(line []byte) (*rpcResponse, bool) {
	if len(line) == 0 {
		return nil, false
	}
	var req rpcRequest
	if err := json.Unmarshal(line, &req); err != nil {
		return &rpcResponse{JSONRPC: "2.0", Error: &rpcError{Code: -32700, Message: "parse error: " + err.Error()}}, true
	}

	isNotification := len(req.ID) == 0

	result, rerr := s.dispatch(req)

	if isNotification {
		return nil, false
	}

	resp := &rpcResponse{JSONRPC: "2.0", ID: req.ID}
	if rerr != nil {
		resp.Error = rerr
	} else {
		resp.Result = result
	}
	return resp, true
}

func (s *server) dispatch(req rpcRequest) (interface{}, *rpcError) {
	switch req.Method {
	case "initialize":
		return map[string]interface{}{
			"protocolVersion": protocolVersion,
			"capabilities": map[string]interface{}{
				"tools": map[string]interface{}{"listChanged": false},
			},
			"serverInfo": map[string]interface{}{
				"name":    serverName,
				"version": serverVersion,
			},
			"instructions": "NebulaCB mission-control tools. Use nebulacb_health first to confirm the server is reachable, then nebulacb_dashboard for a full status snapshot. All read-only tools are safe; mutating tools (load/upgrade/failover/backup) affect live clusters — confirm with the user first.",
		}, nil

	case "notifications/initialized", "initialized":
		return nil, nil

	case "ping":
		return map[string]interface{}{}, nil

	case "tools/list":
		list := make([]*tool, 0, len(s.tools))
		for _, t := range s.tools {
			list = append(list, t)
		}
		return map[string]interface{}{"tools": list}, nil

	case "tools/call":
		return s.callTool(req.Params)

	case "shutdown":
		return nil, nil

	default:
		return nil, &rpcError{Code: -32601, Message: "method not found: " + req.Method}
	}
}

func (s *server) callTool(raw json.RawMessage) (interface{}, *rpcError) {
	var p struct {
		Name      string                 `json:"name"`
		Arguments map[string]interface{} `json:"arguments"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, &rpcError{Code: -32602, Message: "invalid params: " + err.Error()}
	}
	t, ok := s.tools[p.Name]
	if !ok {
		return nil, &rpcError{Code: -32602, Message: "unknown tool: " + p.Name}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	out, err := t.handler(ctx, p.Arguments)
	if err != nil {
		return map[string]interface{}{
			"isError": true,
			"content": []map[string]interface{}{
				{"type": "text", "text": err.Error()},
			},
		}, nil
	}
	return map[string]interface{}{
		"content": []map[string]interface{}{
			{"type": "text", "text": out},
		},
	}, nil
}

// ─── HTTP helpers ────────────────────────────────────────────────────────────

func (s *server) do(ctx context.Context, method, path string, body interface{}) (string, error) {
	var reader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return "", err
		}
		reader = bytes.NewReader(buf)
	}

	req, err := http.NewRequestWithContext(ctx, method, s.baseURL+path, reader)
	if err != nil {
		return "", err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if s.token != "" {
		req.Header.Set("Authorization", "Bearer "+s.token)
	} else if s.user != "" {
		req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(s.user+":"+s.pass)))
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return "", fmt.Errorf("unauthorized — set NEBULACB_TOKEN or NEBULACB_USER/NEBULACB_PASS")
	}
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("%s %s: HTTP %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return prettyJSON(raw), nil
}

func prettyJSON(raw []byte) string {
	var v interface{}
	if err := json.Unmarshal(raw, &v); err != nil {
		return string(raw)
	}
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return string(raw)
	}
	return string(out)
}

func argStr(args map[string]interface{}, key string) string {
	if args == nil {
		return ""
	}
	if v, ok := args[key]; ok && v != nil {
		return fmt.Sprintf("%v", v)
	}
	return ""
}

// ─── Tool definitions ────────────────────────────────────────────────────────

func (s *server) registerTools() {
	reg := func(t *tool) { s.tools[t.Name] = t }

	// --- Core read-only ------------------------------------------------------

	reg(&tool{
		Name:        "nebulacb_health",
		Description: "Check the NebulaCB server's health, uptime, enabled features, and cluster connection counts.",
		InputSchema: objectSchema(nil, nil),
		handler: func(ctx context.Context, _ map[string]interface{}) (string, error) {
			return s.do(ctx, "GET", "/api/v1/health", nil)
		},
	})

	reg(&tool{
		Name:        "nebulacb_dashboard",
		Description: "Get the full mission-control dashboard: cluster health, XDCR, upgrade status, storm load metrics, integrity audit, alerts, regions, failover, backup, migration, AI insights.",
		InputSchema: objectSchema(nil, nil),
		handler: func(ctx context.Context, _ map[string]interface{}) (string, error) {
			return s.do(ctx, "GET", "/api/v1/dashboard", nil)
		},
	})

	reg(&tool{
		Name:        "nebulacb_alerts",
		Description: "List active alerts (severity, category, title, message).",
		InputSchema: objectSchema(nil, nil),
		handler: func(ctx context.Context, _ map[string]interface{}) (string, error) {
			return s.do(ctx, "GET", "/api/v1/alerts", nil)
		},
	})

	reg(&tool{
		Name:        "nebulacb_config",
		Description: "Return the running NebulaCB configuration (passwords and API keys are redacted).",
		InputSchema: objectSchema(nil, nil),
		handler: func(ctx context.Context, _ map[string]interface{}) (string, error) {
			return s.do(ctx, "GET", "/api/v1/config", nil)
		},
	})

	reg(&tool{
		Name:        "nebulacb_reports_list",
		Description: "List previously generated reports.",
		InputSchema: objectSchema(nil, nil),
		handler: func(ctx context.Context, _ map[string]interface{}) (string, error) {
			return s.do(ctx, "GET", "/api/v1/reports", nil)
		},
	})

	reg(&tool{
		Name:        "nebulacb_report_generate",
		Description: "Generate a new full report (mutating — triggers report generation).",
		InputSchema: objectSchema(nil, nil),
		handler: func(ctx context.Context, _ map[string]interface{}) (string, error) {
			return s.do(ctx, "POST", "/api/v1/reports", nil)
		},
	})

	// --- Clusters ------------------------------------------------------------

	reg(&tool{
		Name:        "nebulacb_clusters",
		Description: "List registered Couchbase clusters and connection status.",
		InputSchema: objectSchema(nil, nil),
		handler: func(ctx context.Context, _ map[string]interface{}) (string, error) {
			return s.do(ctx, "GET", "/api/v1/clusters", nil)
		},
	})

	for _, ep := range []struct {
		tool, path, desc string
	}{
		{"nebulacb_cluster_buckets", "buckets", "List buckets on a cluster."},
		{"nebulacb_cluster_indexes", "indexes", "List indexes on a cluster."},
		{"nebulacb_cluster_users", "users", "List RBAC users on a cluster."},
		{"nebulacb_cluster_edition", "edition", "Detect Enterprise vs Community edition."},
		{"nebulacb_cluster_topology", "topology", "Full topology snapshot (edition, buckets, indexes)."},
		{"nebulacb_cluster_logs", "logs", "Fetch recent cluster-level log lines."},
	} {
		path, desc := ep.path, ep.desc
		reg(&tool{
			Name:        ep.tool,
			Description: desc + " Optional `cluster` arg; defaults to the `source`-role cluster.",
			InputSchema: objectSchema(map[string]propSchema{
				"cluster": {Type: "string", Description: "Cluster name; omit for the default source cluster."},
			}, nil),
			handler: func(ctx context.Context, args map[string]interface{}) (string, error) {
				q := ""
				if c := argStr(args, "cluster"); c != "" {
					q = "?cluster=" + url.QueryEscape(c)
				}
				return s.do(ctx, "GET", "/api/v1/cluster/"+path+q, nil)
			},
		})
	}

	// --- Command dispatch (covers most mutating actions) --------------------

	reg(&tool{
		Name: "nebulacb_command",
		Description: "Execute a mission-control action via the orchestrator. MUTATING — confirm with the user before calling. " +
			"Supported actions: start_load, pause_load, resume_load, stop_load, " +
			"start_upgrade (params: cluster_name, target_version, namespace), abort_upgrade, downgrade, " +
			"restart_xdcr, pause_xdcr, resume_xdcr, stop_xdcr, run_audit, inject_failure (params: type, target), " +
			"ai_analyze, start_backup (params: cluster_name), start_restore (params: backup_id, target_cluster), " +
			"manual_failover (params: source_cluster, target_cluster).",
		InputSchema: objectSchema(map[string]propSchema{
			"action": {Type: "string", Description: "Action name."},
			"params": {Type: "object", Description: "String-valued parameter map (keys depend on action).", AdditionalProperties: &propSchema{Type: "string"}},
		}, []string{"action"}),
		handler: func(ctx context.Context, args map[string]interface{}) (string, error) {
			body := map[string]interface{}{"action": argStr(args, "action")}
			if p, ok := args["params"].(map[string]interface{}); ok {
				body["params"] = p
			}
			return s.do(ctx, "POST", "/api/v1/command", body)
		},
	})

	// --- XDCR / Regions / Failover ------------------------------------------

	reg(&tool{
		Name:        "nebulacb_xdcr_diagnostics",
		Description: "Run XDCR diagnostics and return per-check results.",
		InputSchema: objectSchema(nil, nil),
		handler: func(ctx context.Context, _ map[string]interface{}) (string, error) {
			return s.do(ctx, "GET", "/api/v1/xdcr/diagnostics", nil)
		},
	})

	reg(&tool{
		Name:        "nebulacb_regions",
		Description: "Get region topology, replications, and configs.",
		InputSchema: objectSchema(nil, nil),
		handler: func(ctx context.Context, _ map[string]interface{}) (string, error) {
			return s.do(ctx, "GET", "/api/v1/regions", nil)
		},
	})

	reg(&tool{
		Name:        "nebulacb_region_promote",
		Description: "Promote a region to primary. MUTATING — confirm before calling.",
		InputSchema: objectSchema(map[string]propSchema{
			"region": {Type: "string", Description: "Region name to promote."},
		}, []string{"region"}),
		handler: func(ctx context.Context, args map[string]interface{}) (string, error) {
			return s.do(ctx, "POST", "/api/v1/regions/promote", map[string]string{"region": argStr(args, "region")})
		},
	})

	reg(&tool{
		Name:        "nebulacb_failover_status",
		Description: "Current failover manager status.",
		InputSchema: objectSchema(nil, nil),
		handler: func(ctx context.Context, _ map[string]interface{}) (string, error) {
			return s.do(ctx, "GET", "/api/v1/failover", nil)
		},
	})

	reg(&tool{
		Name:        "nebulacb_failover_history",
		Description: "Historical failover events.",
		InputSchema: objectSchema(nil, nil),
		handler: func(ctx context.Context, _ map[string]interface{}) (string, error) {
			return s.do(ctx, "GET", "/api/v1/failover/history", nil)
		},
	})

	// --- Backup / Restore ----------------------------------------------------

	reg(&tool{
		Name:        "nebulacb_backup_status",
		Description: "Current backup manager status.",
		InputSchema: objectSchema(nil, nil),
		handler: func(ctx context.Context, _ map[string]interface{}) (string, error) {
			return s.do(ctx, "GET", "/api/v1/backup", nil)
		},
	})

	reg(&tool{
		Name:        "nebulacb_backup_list",
		Description: "List backups in the repository.",
		InputSchema: objectSchema(nil, nil),
		handler: func(ctx context.Context, _ map[string]interface{}) (string, error) {
			return s.do(ctx, "GET", "/api/v1/backup/list", nil)
		},
	})

	reg(&tool{
		Name:        "nebulacb_backup_start",
		Description: "Start a backup of a cluster. MUTATING.",
		InputSchema: objectSchema(map[string]propSchema{
			"cluster_name": {Type: "string"},
			"type":         {Type: "string", Description: "full (default) or incremental."},
			"buckets":      {Type: "array", Items: &propSchema{Type: "string"}},
		}, []string{"cluster_name"}),
		handler: func(ctx context.Context, args map[string]interface{}) (string, error) {
			return s.do(ctx, "POST", "/api/v1/backup/start", args)
		},
	})

	reg(&tool{
		Name:        "nebulacb_backup_restore",
		Description: "Restore a backup to a target cluster. MUTATING.",
		InputSchema: objectSchema(map[string]propSchema{
			"backup_id":      {Type: "string"},
			"target_cluster": {Type: "string"},
			"buckets":        {Type: "array", Items: &propSchema{Type: "string"}},
		}, []string{"backup_id", "target_cluster"}),
		handler: func(ctx context.Context, args map[string]interface{}) (string, error) {
			return s.do(ctx, "POST", "/api/v1/backup/restore", args)
		},
	})

	// --- Migration -----------------------------------------------------------

	reg(&tool{
		Name:        "nebulacb_migration_status",
		Description: "Current migration status.",
		InputSchema: objectSchema(nil, nil),
		handler: func(ctx context.Context, _ map[string]interface{}) (string, error) {
			return s.do(ctx, "GET", "/api/v1/migration", nil)
		},
	})

	reg(&tool{
		Name:        "nebulacb_migration_history",
		Description: "Historical migration runs.",
		InputSchema: objectSchema(nil, nil),
		handler: func(ctx context.Context, _ map[string]interface{}) (string, error) {
			return s.do(ctx, "GET", "/api/v1/migration/history", nil)
		},
	})

	// --- AI ------------------------------------------------------------------

	reg(&tool{
		Name:        "nebulacb_ai_insights",
		Description: "Most recent AI insights.",
		InputSchema: objectSchema(nil, nil),
		handler: func(ctx context.Context, _ map[string]interface{}) (string, error) {
			return s.do(ctx, "GET", "/api/v1/ai/insights", nil)
		},
	})

	reg(&tool{
		Name:        "nebulacb_ai_knowledge",
		Description: "Built-in knowledge base of common Couchbase issues and solutions.",
		InputSchema: objectSchema(nil, nil),
		handler: func(ctx context.Context, _ map[string]interface{}) (string, error) {
			return s.do(ctx, "GET", "/api/v1/ai/knowledge", nil)
		},
	})

	reg(&tool{
		Name:        "nebulacb_ai_rca",
		Description: "Run an RCA for a cluster + category (e.g. XDCR, Upgrade, Performance). MUTATING (invokes AI provider).",
		InputSchema: objectSchema(map[string]propSchema{
			"cluster":  {Type: "string"},
			"category": {Type: "string"},
		}, nil),
		handler: func(ctx context.Context, args map[string]interface{}) (string, error) {
			return s.do(ctx, "POST", "/api/v1/ai/rca", args)
		},
	})

	// --- Enterprise ----------------------------------------------------------

	reg(&tool{
		Name:        "nebulacb_runbooks_list",
		Description: "List built-in runbooks (safe-upgrade, xdcr-validation, blue-green-migration, dr-simulation, backup-restore-test).",
		InputSchema: objectSchema(nil, nil),
		handler: func(ctx context.Context, _ map[string]interface{}) (string, error) {
			return s.do(ctx, "GET", "/api/v1/runbooks", nil)
		},
	})

	reg(&tool{
		Name:        "nebulacb_runbook_step",
		Description: "Execute one step of a runbook. MUTATING — confirm the runbook + step with the user before calling.",
		InputSchema: objectSchema(map[string]propSchema{
			"runbook_id": {Type: "string"},
			"step":       {Type: "integer", Description: "Zero-based step index."},
		}, []string{"runbook_id", "step"}),
		handler: func(ctx context.Context, args map[string]interface{}) (string, error) {
			return s.do(ctx, "POST", "/api/v1/runbooks", args)
		},
	})

	reg(&tool{
		Name:        "nebulacb_recommendations",
		Description: "Auto-generated cluster recommendations (health, XDCR, upgrade, integrity).",
		InputSchema: objectSchema(nil, nil),
		handler: func(ctx context.Context, _ map[string]interface{}) (string, error) {
			return s.do(ctx, "GET", "/api/v1/recommendations", nil)
		},
	})

	reg(&tool{
		Name:        "nebulacb_diagnostics_bundle",
		Description: "Download a full diagnostics bundle (clusters, XDCR, alerts, metrics, k8s operator status).",
		InputSchema: objectSchema(nil, nil),
		handler: func(ctx context.Context, _ map[string]interface{}) (string, error) {
			return s.do(ctx, "GET", "/api/v1/diagnostics", nil)
		},
	})

	// --- Kubernetes ----------------------------------------------------------

	reg(&tool{
		Name:        "nebulacb_k8s_pods",
		Description: "List pods in a namespace.",
		InputSchema: objectSchema(map[string]propSchema{
			"namespace": {Type: "string"},
		}, nil),
		handler: func(ctx context.Context, args map[string]interface{}) (string, error) {
			q := ""
			if ns := argStr(args, "namespace"); ns != "" {
				q = "?namespace=" + url.QueryEscape(ns)
			}
			return s.do(ctx, "GET", "/api/v1/k8s/pods"+q, nil)
		},
	})

	reg(&tool{
		Name:        "nebulacb_k8s_events",
		Description: "Kubernetes events in a namespace.",
		InputSchema: objectSchema(map[string]propSchema{
			"namespace": {Type: "string"},
		}, nil),
		handler: func(ctx context.Context, args map[string]interface{}) (string, error) {
			q := ""
			if ns := argStr(args, "namespace"); ns != "" {
				q = "?namespace=" + url.QueryEscape(ns)
			}
			return s.do(ctx, "GET", "/api/v1/k8s/events"+q, nil)
		},
	})

	reg(&tool{
		Name:        "nebulacb_k8s_logs",
		Description: "Fetch logs from a pod.",
		InputSchema: objectSchema(map[string]propSchema{
			"pod":       {Type: "string"},
			"namespace": {Type: "string"},
			"tail":      {Type: "integer", Description: "Lines to tail (default 200)."},
			"since":     {Type: "integer", Description: "Seconds since now."},
		}, []string{"pod"}),
		handler: func(ctx context.Context, args map[string]interface{}) (string, error) {
			q := url.Values{}
			q.Set("pod", argStr(args, "pod"))
			if ns := argStr(args, "namespace"); ns != "" {
				q.Set("namespace", ns)
			}
			if t := argStr(args, "tail"); t != "" {
				q.Set("tail", t)
			}
			if si := argStr(args, "since"); si != "" {
				q.Set("since", si)
			}
			return s.do(ctx, "GET", "/api/v1/k8s/logs?"+q.Encode(), nil)
		},
	})

	reg(&tool{
		Name:        "nebulacb_k8s_operator",
		Description: "Couchbase operator status across configured k8s clusters.",
		InputSchema: objectSchema(nil, nil),
		handler: func(ctx context.Context, _ map[string]interface{}) (string, error) {
			return s.do(ctx, "GET", "/api/v1/k8s/operator", nil)
		},
	})
}

// ─── JSON Schema helpers ─────────────────────────────────────────────────────

type propSchema struct {
	Type                 string                 `json:"type,omitempty"`
	Description          string                 `json:"description,omitempty"`
	Items                *propSchema            `json:"items,omitempty"`
	AdditionalProperties *propSchema            `json:"additionalProperties,omitempty"`
	Enum                 []string               `json:"enum,omitempty"`
	Properties           map[string]*propSchema `json:"properties,omitempty"`
}

func objectSchema(props map[string]propSchema, required []string) map[string]interface{} {
	schema := map[string]interface{}{"type": "object"}
	if len(props) > 0 {
		p := map[string]interface{}{}
		for k, v := range props {
			p[k] = v
		}
		schema["properties"] = p
	} else {
		schema["properties"] = map[string]interface{}{}
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}
