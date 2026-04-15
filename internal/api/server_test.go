package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/balinderwalia/nebulacb/internal/ai"
	"github.com/balinderwalia/nebulacb/internal/backup"
	"github.com/balinderwalia/nebulacb/internal/config"
	"github.com/balinderwalia/nebulacb/internal/failover"
	"github.com/balinderwalia/nebulacb/internal/metrics"
	"github.com/balinderwalia/nebulacb/internal/migration"
	"github.com/balinderwalia/nebulacb/internal/models"
	"github.com/balinderwalia/nebulacb/internal/orchestrator"
	"github.com/balinderwalia/nebulacb/internal/region"
	"github.com/balinderwalia/nebulacb/internal/reporting"
	"github.com/balinderwalia/nebulacb/internal/storm"
	"github.com/balinderwalia/nebulacb/internal/validator"
	"github.com/balinderwalia/nebulacb/internal/xdcr"
	"github.com/balinderwalia/nebulacb/pkg/couchbase"
	ws "github.com/balinderwalia/nebulacb/pkg/websocket"
)

func newTestServer(authEnabled bool) *Server {
	cfg := config.DefaultConfig()
	cfg.Server.Auth.Enabled = authEnabled

	collector := metrics.NewCollector()
	hub := ws.NewHub()
	stormGen := storm.NewGenerator(cfg.Storm, nil, collector)
	orch := orchestrator.NewOrchestrator(cfg.Upgrade, nil, collector)
	xdcrEngine := xdcr.NewEngine(cfg.XDCR, collector)
	val := validator.NewValidator(cfg.Validator, nil, nil, collector, "nebula")
	reporter := reporting.NewEngine(collector, "reports")
	pool := couchbase.NewClientPool(nil)

	allClusters := cfg.GetClusters()
	aiAnalyzer := ai.NewAnalyzer(cfg.AI, collector)
	backupMgr := backup.NewManager(cfg.Backup, allClusters, collector)
	failoverMgr := failover.NewManager(cfg.Failover, allClusters, collector)
	migrationEngine := migration.NewEngine(cfg.Migration, pool, collector)
	regionMgr := region.NewManager(cfg.Regions, allClusters, collector)

	return NewServer(cfg, collector, hub, stormGen, orch, xdcrEngine, val, reporter, pool,
		aiAnalyzer, backupMgr, failoverMgr, migrationEngine, regionMgr, nil, nil)
}

func TestHealthEndpoint(t *testing.T) {
	s := newTestServer(false)

	req := httptest.NewRequest("GET", "/api/v1/health", nil)
	w := httptest.NewRecorder()
	s.handleHealth(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)

	if resp["status"] != "ok" {
		t.Errorf("expected status ok, got %v", resp["status"])
	}
	if resp["version"] != "1.0.0" {
		t.Errorf("expected version 1.0.0, got %v", resp["version"])
	}
}

func TestAuthCheckEndpoint(t *testing.T) {
	s := newTestServer(true)

	req := httptest.NewRequest("GET", "/api/v1/auth/check", nil)
	w := httptest.NewRecorder()
	s.handleAuthCheck(w, req)

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)

	if resp["auth_enabled"] != true {
		t.Errorf("expected auth_enabled true, got %v", resp["auth_enabled"])
	}
}

func TestLoginSuccess(t *testing.T) {
	s := newTestServer(true)

	body := `{"username":"admin","password":"nebulacb"}`
	req := httptest.NewRequest("POST", "/api/v1/login", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.handleLogin(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)

	if resp["token"] == "" {
		t.Error("expected non-empty token")
	}
	if resp["username"] != "admin" {
		t.Errorf("expected username admin, got %v", resp["username"])
	}
}

func TestLoginFailure(t *testing.T) {
	s := newTestServer(true)

	body := `{"username":"admin","password":"wrong"}`
	req := httptest.NewRequest("POST", "/api/v1/login", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.handleLogin(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestLoginMethodNotAllowed(t *testing.T) {
	s := newTestServer(true)

	req := httptest.NewRequest("GET", "/api/v1/login", nil)
	w := httptest.NewRecorder()
	s.handleLogin(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestLoginAuthDisabled(t *testing.T) {
	s := newTestServer(false)

	body := `{"username":"any","password":"any"}`
	req := httptest.NewRequest("POST", "/api/v1/login", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.handleLogin(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	json.NewDecoder(w.Body).Decode(&resp)

	if resp["auth"] != false {
		t.Error("expected auth false when disabled")
	}
}

func TestRequireAuth_NoAuth(t *testing.T) {
	s := newTestServer(true)

	handler := s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"ok": "true"})
	})

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestRequireAuth_BasicAuth(t *testing.T) {
	s := newTestServer(true)

	handler := s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"ok": "true"})
	})

	req := httptest.NewRequest("GET", "/test", nil)
	req.SetBasicAuth("admin", "nebulacb")
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestRequireAuth_BearerToken(t *testing.T) {
	s := newTestServer(true)

	// Create a session token via login
	token := s.sessions.create("admin")

	handler := s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"ok": "true"})
	})

	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestRequireAuth_AuthDisabled(t *testing.T) {
	s := newTestServer(false)

	handler := s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"ok": "true"})
	})

	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200 when auth disabled, got %d", w.Code)
	}
}

func TestDashboardEndpoint(t *testing.T) {
	s := newTestServer(false)

	req := httptest.NewRequest("GET", "/api/v1/dashboard", nil)
	w := httptest.NewRecorder()
	s.handleDashboard(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var state models.DashboardState
	if err := json.NewDecoder(w.Body).Decode(&state); err != nil {
		t.Fatalf("failed to decode dashboard: %v", err)
	}
}

func TestCommandEndpoint(t *testing.T) {
	s := newTestServer(false)

	body := `{"action":"unknown_command"}`
	req := httptest.NewRequest("POST", "/api/v1/command", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.handleCommand(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp map[string]string
	json.NewDecoder(w.Body).Decode(&resp)

	if resp["status"] != "error" {
		t.Errorf("expected error status for unknown command, got %s", resp["status"])
	}
}

// TestCommandEndpoint_AllButtonActions covers every control-panel button
// action and verifies the server recognises the command (never returns
// "unknown command"). Actions that require live infrastructure may still
// come back with status=error, but the handler must route them.
func TestCommandEndpoint_AllButtonActions(t *testing.T) {
	s := newTestServer(false)

	cases := []struct {
		name string
		body string
	}{
		{"start_load", `{"action":"start_load"}`},
		{"pause_load", `{"action":"pause_load"}`},
		{"resume_load", `{"action":"resume_load"}`},
		{"stop_load", `{"action":"stop_load"}`},
		{"start_upgrade", `{"action":"start_upgrade","params":{"cluster_name":"src","target_version":"7.6.0","namespace":"couchbase"}}`},
		{"abort_upgrade", `{"action":"abort_upgrade"}`},
		{"downgrade", `{"action":"downgrade"}`},
		{"pause_xdcr", `{"action":"pause_xdcr"}`},
		{"resume_xdcr", `{"action":"resume_xdcr"}`},
		{"stop_xdcr", `{"action":"stop_xdcr"}`},
		{"restart_xdcr", `{"action":"restart_xdcr"}`},
		{"run_audit", `{"action":"run_audit"}`},
		{"inject_failure", `{"action":"inject_failure","params":{"type":"xdcr_partition","target":"replication"}}`},
		{"ai_analyze", `{"action":"ai_analyze"}`},
		{"start_backup", `{"action":"start_backup","params":{"cluster_name":"src"}}`},
		{"manual_failover", `{"action":"manual_failover","params":{"source_cluster":"src","target_cluster":"tgt"}}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/api/v1/command", strings.NewReader(tc.body))
			w := httptest.NewRecorder()
			s.handleCommand(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d (%s)", w.Code, w.Body.String())
			}

			var resp map[string]string
			if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
				t.Fatalf("decode: %v", err)
			}

			if resp["status"] == "" {
				t.Fatalf("missing status in response: %v", resp)
			}
			if strings.Contains(strings.ToLower(resp["message"]), "unknown command") {
				t.Fatalf("command %s not routed: %v", tc.name, resp)
			}
		})
	}
}

// TestBackupManager_DetachedContext verifies that the backup goroutine is
// not tied to the caller's cancelled context, which was the root cause of
// the "backup button unreliability" bug.
func TestBackupManager_DetachedContext(t *testing.T) {
	cfg := config.DefaultConfig()
	collector := metrics.NewCollector()
	allClusters := cfg.GetClusters()
	mgr := backup.NewManager(cfg.Backup, allClusters, collector)

	// Pick the first configured cluster.
	var clusterName string
	for name := range allClusters {
		clusterName = name
		break
	}
	if clusterName == "" {
		t.Skip("no clusters in default config")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // simulate HTTP request context cancelled immediately after reply

	info, err := mgr.StartBackup(ctx, clusterName, "full", nil)
	if err != nil {
		t.Fatalf("StartBackup: %v", err)
	}
	if info == nil || info.ID == "" {
		t.Fatalf("expected backup info, got %+v", info)
	}
}

func TestCommandEndpointMethodNotAllowed(t *testing.T) {
	s := newTestServer(false)

	req := httptest.NewRequest("GET", "/api/v1/command", nil)
	w := httptest.NewRecorder()
	s.handleCommand(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestConfigEndpoint_RedactsPasswords(t *testing.T) {
	s := newTestServer(false)

	req := httptest.NewRequest("GET", "/api/v1/config", nil)
	w := httptest.NewRecorder()
	s.handleConfig(w, req)

	body := w.Body.String()
	if strings.Contains(body, "nebulacb") && strings.Contains(body, `"password"`) {
		// Check the password field is redacted
		var resp map[string]interface{}
		json.Unmarshal([]byte(body), &resp)
		if server, ok := resp["server"].(map[string]interface{}); ok {
			if auth, ok := server["auth"].(map[string]interface{}); ok {
				if auth["password"] != "***" {
					t.Error("expected auth password to be redacted")
				}
			}
		}
	}
}

func TestSessionStore(t *testing.T) {
	ss := newSessionStore()

	token := ss.create("testuser")
	if token == "" {
		t.Fatal("expected non-empty token")
	}

	if !ss.valid(token) {
		t.Error("expected token to be valid")
	}

	if ss.valid("invalidtoken") {
		t.Error("expected invalid token to be rejected")
	}

	ss.revoke(token)
	if ss.valid(token) {
		t.Error("expected revoked token to be invalid")
	}
}

func TestCORSMiddleware(t *testing.T) {
	handler := corsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Test OPTIONS preflight
	req := httptest.NewRequest("OPTIONS", "/api/v1/health", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("expected 204 for OPTIONS, got %d", w.Code)
	}
	if w.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Error("expected CORS allow origin header")
	}

	// Test normal request
	req = httptest.NewRequest("GET", "/api/v1/health", nil)
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if w.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Error("expected CORS header on normal request")
	}
}

func TestLogout(t *testing.T) {
	s := newTestServer(true)

	token := s.sessions.create("admin")

	req := httptest.NewRequest("POST", "/api/v1/logout", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	s.handleLogout(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	if s.sessions.valid(token) {
		t.Error("expected token to be revoked after logout")
	}
}
