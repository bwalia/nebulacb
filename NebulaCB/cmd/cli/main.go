package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"text/tabwriter"
)

const defaultBaseURL = "http://localhost:8080"

// authHeader returns Basic auth header if credentials are set via env vars.
func authHeader() string {
	user := os.Getenv("NEBULACB_AUTH_USERNAME")
	pass := os.Getenv("NEBULACB_AUTH_PASSWORD")
	if user == "" && pass == "" {
		user = os.Getenv("NEBULACB_USER")
		pass = os.Getenv("NEBULACB_PASS")
	}
	if user != "" {
		return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
	}
	return ""
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	baseURL := os.Getenv("NEBULACB_URL")
	if baseURL == "" {
		baseURL = defaultBaseURL
	}

	command := os.Args[1]
	switch command {
	case "status", "dashboard":
		cmdDashboard(baseURL)
	case "start-load":
		cmdAction(baseURL, "start_load")
	case "pause-load":
		cmdAction(baseURL, "pause_load")
	case "resume-load":
		cmdAction(baseURL, "resume_load")
	case "stop-load":
		cmdAction(baseURL, "stop_load")
	case "start-upgrade":
		cmdAction(baseURL, "start_upgrade")
	case "abort-upgrade":
		cmdAction(baseURL, "abort_upgrade")
	case "restart-xdcr":
		cmdAction(baseURL, "restart_xdcr")
	case "run-audit":
		cmdAction(baseURL, "run_audit")
	case "inject-failure":
		params := map[string]string{}
		if len(os.Args) > 2 {
			params["type"] = os.Args[2]
		}
		if len(os.Args) > 3 {
			params["target"] = os.Args[3]
		}
		cmdActionWithParams(baseURL, "inject_failure", params)
	case "alerts":
		cmdAlerts(baseURL)
	case "report":
		cmdReport(baseURL)
	case "config":
		cmdConfig(baseURL)
	case "health":
		cmdHealth(baseURL)
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", command)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println(`NebulaCB CLI — Couchbase Migration & Upgrade Mission Control

Usage:
  nebulacb <command> [args]

Commands:
  status           Show full dashboard status
  start-load       Start the Data Storm Generator
  pause-load       Pause load generation
  resume-load      Resume load generation
  stop-load        Stop load generation
  start-upgrade    Start Couchbase rolling upgrade
  abort-upgrade    Abort the current upgrade
  restart-xdcr     Restart the XDCR pipeline
  run-audit        Run a full data integrity audit
  inject-failure   Inject failure (type target)
  alerts           Show active alerts
  report           Generate a report
  config           Show configuration
  health           Health check

Environment:
  NEBULACB_URL     API base URL (default: http://localhost:8080)
  NEBULACB_USER    Auth username (or NEBULACB_AUTH_USERNAME)
  NEBULACB_PASS    Auth password (or NEBULACB_AUTH_PASSWORD)`)
}

func cmdDashboard(baseURL string) {
	data, err := httpGet(baseURL + "/api/v1/dashboard")
	if err != nil {
		fatal(err)
	}

	var state map[string]interface{}
	json.Unmarshal(data, &state)

	fmt.Println("╔══════════════════════════════════════════════════════════╗")
	fmt.Println("║           NebulaCB — Mission Control Dashboard          ║")
	fmt.Println("╠══════════════════════════════════════════════════════════╣")

	// Cluster Health
	if ch, ok := state["cluster_health"].(map[string]interface{}); ok {
		fmt.Println("║ CLUSTER HEALTH                                           ║")
		fmt.Printf("║  Rebalance: %-46s║\n", getString(ch, "rebalance_state"))
		if nodes, ok := ch["nodes"].([]interface{}); ok {
			for _, n := range nodes {
				node := n.(map[string]interface{})
				fmt.Printf("║  %-12s %-8s v%-26s║\n",
					getString(node, "host"),
					getString(node, "status"),
					getString(node, "version"))
			}
		}
	}

	fmt.Println("╠══════════════════════════════════════════════════════════╣")

	// Upgrade Status
	if us, ok := state["upgrade_status"].(map[string]interface{}); ok {
		fmt.Println("║ UPGRADE STATUS                                           ║")
		fmt.Printf("║  Phase: %-50s║\n", getString(us, "phase"))
		fmt.Printf("║  Progress: %-47.1f%%║\n", getFloat(us, "progress"))
		fmt.Printf("║  %s -> %-51s║\n", getString(us, "source_version"), getString(us, "target_version"))
	}

	fmt.Println("╠══════════════════════════════════════════════════════════╣")

	// XDCR Status
	if xs, ok := state["xdcr_status"].(map[string]interface{}); ok {
		fmt.Println("║ XDCR REPLICATION                                         ║")
		fmt.Printf("║  State: %-50s║\n", getString(xs, "state"))
		fmt.Printf("║  Changes Left: %-42.0f║\n", getFloat(xs, "changes_left"))
		fmt.Printf("║  Lag (ms): %-47.1f║\n", getFloat(xs, "replication_lag_ms"))
		fmt.Printf("║  Pipeline Restarts: %-38.0f║\n", getFloat(xs, "pipeline_restarts"))
	}

	fmt.Println("╠══════════════════════════════════════════════════════════╣")

	// Storm Metrics
	if sm, ok := state["storm_metrics"].(map[string]interface{}); ok {
		fmt.Println("║ LOAD METRICS                                             ║")
		fmt.Printf("║  Writes/sec: %-45.1f║\n", getFloat(sm, "writes_per_sec"))
		fmt.Printf("║  Reads/sec: %-46.1f║\n", getFloat(sm, "reads_per_sec"))
		fmt.Printf("║  P95 Latency: %-43.1fms║\n", getFloat(sm, "latency_p95_ms"))
		fmt.Printf("║  P99 Latency: %-43.1fms║\n", getFloat(sm, "latency_p99_ms"))
		fmt.Printf("║  Total Written: %-42.0f║\n", getFloat(sm, "total_written"))
		fmt.Printf("║  Total Errors: %-43.0f║\n", getFloat(sm, "total_errors"))
	}

	fmt.Println("╠══════════════════════════════════════════════════════════╣")

	// Integrity
	if ir, ok := state["integrity_result"].(map[string]interface{}); ok {
		fmt.Println("║ DATA INTEGRITY                                           ║")
		fmt.Printf("║  Status: %-49s║\n", getString(ir, "status"))
		fmt.Printf("║  Source Docs: %-44.0f║\n", getFloat(ir, "source_docs"))
		fmt.Printf("║  Target Docs: %-44.0f║\n", getFloat(ir, "target_docs"))
		fmt.Printf("║  Missing: %-48.0f║\n", getFloat(ir, "missing_count"))
		fmt.Printf("║  Mismatch: %-46.2f%%║\n", getFloat(ir, "mismatch_percent"))
	}

	fmt.Println("╠══════════════════════════════════════════════════════════╣")

	// Alerts
	if alerts, ok := state["alerts"].([]interface{}); ok && len(alerts) > 0 {
		fmt.Println("║ ALERTS                                                   ║")
		for _, a := range alerts {
			alert := a.(map[string]interface{})
			sev := strings.ToUpper(getString(alert, "severity"))
			fmt.Printf("║  [%-8s] %-46s║\n", sev, getString(alert, "title"))
		}
	} else {
		fmt.Println("║ ALERTS: None                                             ║")
	}

	fmt.Println("╚══════════════════════════════════════════════════════════╝")
}

func cmdAlerts(baseURL string) {
	data, err := httpGet(baseURL + "/api/v1/alerts")
	if err != nil {
		fatal(err)
	}

	var alerts []map[string]interface{}
	json.Unmarshal(data, &alerts)

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "SEVERITY\tCATEGORY\tTITLE\tMESSAGE")
	for _, a := range alerts {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
			getString(a, "severity"),
			getString(a, "category"),
			getString(a, "title"),
			getString(a, "message"))
	}
	w.Flush()
}

func cmdReport(baseURL string) {
	data, err := httpPost(baseURL+"/api/v1/reports", nil)
	if err != nil {
		fatal(err)
	}

	var report map[string]interface{}
	json.Unmarshal(data, &report)

	pretty, _ := json.MarshalIndent(report, "", "  ")
	fmt.Println(string(pretty))
}

func cmdConfig(baseURL string) {
	data, err := httpGet(baseURL + "/api/v1/config")
	if err != nil {
		fatal(err)
	}
	var cfg interface{}
	json.Unmarshal(data, &cfg)
	pretty, _ := json.MarshalIndent(cfg, "", "  ")
	fmt.Println(string(pretty))
}

func cmdHealth(baseURL string) {
	data, err := httpGet(baseURL + "/api/v1/health")
	if err != nil {
		fatal(err)
	}
	var health interface{}
	json.Unmarshal(data, &health)
	pretty, _ := json.MarshalIndent(health, "", "  ")
	fmt.Println(string(pretty))
}

func cmdAction(baseURL string, action string) {
	cmdActionWithParams(baseURL, action, nil)
}

func cmdActionWithParams(baseURL string, action string, params map[string]string) {
	body := map[string]interface{}{
		"action": action,
	}
	if params != nil {
		body["params"] = params
	}

	payload, _ := json.Marshal(body)
	data, err := httpPost(baseURL+"/api/v1/command", payload)
	if err != nil {
		fatal(err)
	}

	var result map[string]string
	json.Unmarshal(data, &result)
	fmt.Printf("[%s] %s\n", result["status"], result["message"])
}

func httpGet(url string) ([]byte, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	if auth := authHeader(); auth != "" {
		req.Header.Set("Authorization", auth)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 {
		return nil, fmt.Errorf("authentication failed — set NEBULACB_USER and NEBULACB_PASS env vars")
	}
	return io.ReadAll(resp.Body)
}

func httpPost(url string, body []byte) ([]byte, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequest("POST", url, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if auth := authHeader(); auth != "" {
		req.Header.Set("Authorization", auth)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("POST %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 {
		return nil, fmt.Errorf("authentication failed — set NEBULACB_USER and NEBULACB_PASS env vars")
	}
	return io.ReadAll(resp.Body)
}

func getString(m map[string]interface{}, key string) string {
	if v, ok := m[key]; ok {
		return fmt.Sprintf("%v", v)
	}
	return ""
}

func getFloat(m map[string]interface{}, key string) float64 {
	if v, ok := m[key].(float64); ok {
		return v
	}
	return 0
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "Error: %v\n", err)
	os.Exit(1)
}
