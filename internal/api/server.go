package api

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

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
	"github.com/balinderwalia/nebulacb/pkg/docker"
	ws "github.com/balinderwalia/nebulacb/pkg/websocket"
)

// Server is the NebulaCB API server.
type Server struct {
	config       *config.Config
	collector    *metrics.Collector
	hub          *ws.Hub
	pool         *couchbase.ClientPool
	storm        *storm.Generator
	orchestrator *orchestrator.Orchestrator
	xdcrEngine   *xdcr.Engine
	validator    *validator.Validator
	reporter     *reporting.Engine
	// New components
	aiAnalyzer      *ai.Analyzer
	backupMgr       *backup.Manager
	failoverMgr     *failover.Manager
	migrationEngine *migration.Engine
	regionMgr       *region.Manager
	dockerClient    *docker.Client

	httpServer   *http.Server
	sessions     *sessionStore
	startTime    time.Time
}

// NewServer creates a new API server.
func NewServer(
	cfg *config.Config,
	collector *metrics.Collector,
	hub *ws.Hub,
	stormGen *storm.Generator,
	orch *orchestrator.Orchestrator,
	xdcrEng *xdcr.Engine,
	val *validator.Validator,
	reporter *reporting.Engine,
	pool *couchbase.ClientPool,
	aiAnalyzer *ai.Analyzer,
	backupMgr *backup.Manager,
	failoverMgr *failover.Manager,
	migrationEngine *migration.Engine,
	regionMgr *region.Manager,
	dockerClient *docker.Client,
) *Server {
	return &Server{
		config:          cfg,
		collector:       collector,
		hub:             hub,
		pool:            pool,
		storm:           stormGen,
		orchestrator:    orch,
		xdcrEngine:      xdcrEng,
		validator:       val,
		reporter:        reporter,
		aiAnalyzer:      aiAnalyzer,
		backupMgr:       backupMgr,
		failoverMgr:     failoverMgr,
		migrationEngine: migrationEngine,
		regionMgr:       regionMgr,
		dockerClient:    dockerClient,
		sessions:        newSessionStore(),
		startTime:       time.Now(),
	}
}

// Start begins the HTTP/WebSocket server and dashboard broadcast loop.
func (s *Server) Start(ctx context.Context) error {
	mux := http.NewServeMux()

	// Public endpoints (no auth required)
	mux.HandleFunc("/api/v1/health", s.handleHealth)
	mux.HandleFunc("/api/v1/login", s.handleLogin)
	mux.HandleFunc("/api/v1/auth/check", s.handleAuthCheck)

	// Protected API routes — Core
	mux.HandleFunc("/api/v1/dashboard", s.requireAuth(s.handleDashboard))
	mux.HandleFunc("/api/v1/command", s.requireAuth(s.handleCommand))
	mux.HandleFunc("/api/v1/alerts", s.requireAuth(s.handleAlerts))
	mux.HandleFunc("/api/v1/reports", s.requireAuth(s.handleReports))
	mux.HandleFunc("/api/v1/config", s.requireAuth(s.handleConfig))
	mux.HandleFunc("/api/v1/clusters", s.requireAuth(s.handleClusters))
	mux.HandleFunc("/api/v1/logout", s.requireAuth(s.handleLogout))

	// Protected API routes — Multi-Region
	mux.HandleFunc("/api/v1/regions", s.requireAuth(s.handleRegions))
	mux.HandleFunc("/api/v1/regions/promote", s.requireAuth(s.handleRegionPromote))

	// Protected API routes — HA & Failover
	mux.HandleFunc("/api/v1/failover", s.requireAuth(s.handleFailover))
	mux.HandleFunc("/api/v1/failover/manual", s.requireAuth(s.handleManualFailover))
	mux.HandleFunc("/api/v1/failover/graceful", s.requireAuth(s.handleGracefulFailover))
	mux.HandleFunc("/api/v1/failover/history", s.requireAuth(s.handleFailoverHistory))

	// Protected API routes — Backup & Restore
	mux.HandleFunc("/api/v1/backup", s.requireAuth(s.handleBackup))
	mux.HandleFunc("/api/v1/backup/start", s.requireAuth(s.handleBackupStart))
	mux.HandleFunc("/api/v1/backup/restore", s.requireAuth(s.handleRestore))
	mux.HandleFunc("/api/v1/backup/list", s.requireAuth(s.handleBackupList))

	// Protected API routes — Data Migration
	mux.HandleFunc("/api/v1/migration", s.requireAuth(s.handleMigration))
	mux.HandleFunc("/api/v1/migration/start", s.requireAuth(s.handleMigrationStart))
	mux.HandleFunc("/api/v1/migration/history", s.requireAuth(s.handleMigrationHistory))

	// Protected API routes — AI Analysis
	mux.HandleFunc("/api/v1/ai/analyze", s.requireAuth(s.handleAIAnalyze))
	mux.HandleFunc("/api/v1/ai/auto-analyze", s.requireAuth(s.handleAIAutoAnalyze))
	mux.HandleFunc("/api/v1/ai/insights", s.requireAuth(s.handleAIInsights))

	// Protected API routes — Docker Management
	mux.HandleFunc("/api/v1/docker/containers", s.requireAuth(s.handleDockerContainers))
	mux.HandleFunc("/api/v1/docker/create", s.requireAuth(s.handleDockerCreate))
	mux.HandleFunc("/api/v1/docker/logs", s.requireAuth(s.handleDockerLogs))

	// Protected API routes — Cluster Management (buckets, indexes, users, edition)
	mux.HandleFunc("/api/v1/cluster/buckets", s.requireAuth(s.handleClusterBuckets))
	mux.HandleFunc("/api/v1/cluster/indexes", s.requireAuth(s.handleClusterIndexes))
	mux.HandleFunc("/api/v1/cluster/users", s.requireAuth(s.handleClusterUsers))
	mux.HandleFunc("/api/v1/cluster/edition", s.requireAuth(s.handleClusterEdition))
	mux.HandleFunc("/api/v1/cluster/topology", s.requireAuth(s.handleClusterTopology))
	mux.HandleFunc("/api/v1/cluster/logs", s.requireAuth(s.handleClusterLogs))

	// WebSocket (auth via query param token)
	mux.HandleFunc("/ws", s.handleAuthenticatedWS)

	// Serve React UI (static files) — public so the login page loads
	mux.Handle("/", http.FileServer(http.Dir("web/nebulacb-ui/build")))

	// Middleware chain: CORS → handler
	handler := corsMiddleware(mux)

	addr := fmt.Sprintf("%s:%d", s.config.Server.Host, s.config.Server.Port)
	s.httpServer = &http.Server{
		Addr:         addr,
		Handler:      handler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// Start WebSocket hub
	go s.hub.Run()

	// Start dashboard broadcast loop
	go s.broadcastLoop(ctx)

	// Handle incoming WebSocket commands
	s.hub.OnMessage(func(data []byte) {
		var cmd models.Command
		if err := json.Unmarshal(data, &cmd); err != nil {
			log.Printf("[API] Invalid command: %v", err)
			return
		}
		s.executeCommand(ctx, cmd)
	})

	if s.config.Server.Auth.Enabled {
		log.Printf("[API] Basic auth ENABLED (user: %s)", s.config.Server.Auth.Username)
	} else {
		log.Println("[API] Auth DISABLED — all endpoints are public")
	}

	if s.config.Server.TLS.Enabled {
		log.Printf("[API] TLS enabled, starting HTTPS on %s", addr)
		go func() {
			if err := s.httpServer.ListenAndServeTLS(
				s.config.Server.TLS.CertFile,
				s.config.Server.TLS.KeyFile,
			); err != nil && err != http.ErrServerClosed {
				log.Fatalf("[API] TLS server error: %v", err)
			}
		}()
	} else {
		log.Printf("[API] Server starting on %s", addr)
		go func() {
			if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Fatalf("[API] Server error: %v", err)
			}
		}()
	}

	return nil
}

// Stop gracefully shuts down the server.
func (s *Server) Stop() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return s.httpServer.Shutdown(ctx)
}

// ─── Authentication ──────────────────────────────────────────────────────────

// requireAuth wraps a handler with authentication checks.
func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.config.Server.Auth.Enabled {
			next(w, r)
			return
		}

		if token := extractBearerToken(r); token != "" {
			if s.sessions.valid(token) {
				next(w, r)
				return
			}
		}

		user, pass, ok := r.BasicAuth()
		if ok && s.checkCredentials(user, pass) {
			next(w, r)
			return
		}

		w.Header().Set("WWW-Authenticate", `Basic realm="NebulaCB"`)
		writeJSON(w, http.StatusUnauthorized, map[string]string{
			"error": "authentication required",
		})
	}
}

func (s *Server) handleAuthenticatedWS(w http.ResponseWriter, r *http.Request) {
	if s.config.Server.Auth.Enabled {
		token := r.URL.Query().Get("token")
		if token != "" && s.sessions.valid(token) {
			s.hub.HandleWebSocket(w, r)
			return
		}

		user, pass, ok := r.BasicAuth()
		if ok && s.checkCredentials(user, pass) {
			s.hub.HandleWebSocket(w, r)
			return
		}

		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	s.hub.HandleWebSocket(w, r)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST only"})
		return
	}

	if !s.config.Server.Auth.Enabled {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"token":    "",
			"auth":     false,
			"username": "anonymous",
		})
		return
	}

	var creds struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&creds); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request"})
		return
	}

	if !s.checkCredentials(creds.Username, creds.Password) {
		log.Printf("[Auth] Failed login attempt for user: %s", creds.Username)
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid credentials"})
		return
	}

	token := s.sessions.create(creds.Username)
	log.Printf("[Auth] User %s logged in", creds.Username)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"token":    token,
		"auth":     true,
		"username": creds.Username,
	})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if token := extractBearerToken(r); token != "" {
		s.sessions.revoke(token)
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleAuthCheck(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"auth_enabled": s.config.Server.Auth.Enabled,
	})
}

func (s *Server) checkCredentials(user, pass string) bool {
	userOK := subtle.ConstantTimeCompare([]byte(user), []byte(s.config.Server.Auth.Username)) == 1
	passOK := subtle.ConstantTimeCompare([]byte(pass), []byte(s.config.Server.Auth.Password)) == 1
	return userOK && passOK
}

func extractBearerToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		return auth[7:]
	}
	return ""
}

// ─── Session Store ───────────────────────────────────────────────────────────

type session struct {
	username  string
	created   time.Time
}

type sessionStore struct {
	mu       sync.RWMutex
	sessions map[string]session
}

func newSessionStore() *sessionStore {
	return &sessionStore{sessions: make(map[string]session)}
}

func (ss *sessionStore) create(username string) string {
	b := make([]byte, 32)
	rand.Read(b)
	token := hex.EncodeToString(b)

	ss.mu.Lock()
	defer ss.mu.Unlock()
	ss.sessions[token] = session{username: username, created: time.Now()}

	for k, v := range ss.sessions {
		if time.Since(v.created) > 24*time.Hour {
			delete(ss.sessions, k)
		}
	}
	return token
}

func (ss *sessionStore) valid(token string) bool {
	ss.mu.RLock()
	defer ss.mu.RUnlock()
	s, ok := ss.sessions[token]
	if !ok {
		return false
	}
	return time.Since(s.created) < 24*time.Hour
}

func (ss *sessionStore) revoke(token string) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	delete(ss.sessions, token)
}

// ─── Cluster API ─────────────────────────────────────────────────────────────

func (s *Server) handleClusters(w http.ResponseWriter, r *http.Request) {
	type clusterInfo struct {
		Name      string `json:"name"`
		Host      string `json:"host"`
		Bucket    string `json:"bucket"`
		Role      string `json:"role"`
		Region    string `json:"region,omitempty"`
		Edition   string `json:"edition,omitempty"`
		Platform  string `json:"platform,omitempty"`
		Connected bool   `json:"connected"`
	}

	configs := s.pool.Configs()
	connected := s.pool.Connected()
	connSet := make(map[string]bool, len(connected))
	for _, name := range connected {
		connSet[name] = true
	}

	var clusters []clusterInfo
	for name, cfg := range configs {
		clusters = append(clusters, clusterInfo{
			Name:      name,
			Host:      cfg.Host,
			Bucket:    cfg.Bucket,
			Role:      cfg.Role,
			Region:    cfg.Region,
			Edition:   cfg.Edition,
			Platform:  cfg.Platform,
			Connected: connSet[name],
		})
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"clusters":  clusters,
		"total":     len(clusters),
		"connected": len(connected),
	})
}

// ─── Dashboard & API Handlers ────────────────────────────────────────────────

func (s *Server) broadcastLoop(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			state := s.collector.GetDashboardState()
			// Enrich with new component data
			if s.regionMgr != nil {
				state.Regions = s.regionMgr.GetRegionStatuses()
			}
			if s.failoverMgr != nil {
				fs := s.failoverMgr.GetStatus()
				state.FailoverStatus = &fs
			}
			if s.backupMgr != nil {
				bs := s.backupMgr.GetStatus()
				state.BackupStatus = &bs
			}
			if s.migrationEngine != nil {
				state.MigrationStatus = s.migrationEngine.GetStatus()
			}
			if s.aiAnalyzer != nil {
				state.AIInsights = s.aiAnalyzer.GetInsights()
			}
			s.hub.Broadcast("dashboard_update", state)
		}
	}
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	state := s.collector.GetDashboardState()
	if s.regionMgr != nil {
		state.Regions = s.regionMgr.GetRegionStatuses()
	}
	if s.failoverMgr != nil {
		fs := s.failoverMgr.GetStatus()
		state.FailoverStatus = &fs
	}
	if s.backupMgr != nil {
		bs := s.backupMgr.GetStatus()
		state.BackupStatus = &bs
	}
	if s.migrationEngine != nil {
		state.MigrationStatus = s.migrationEngine.GetStatus()
	}
	if s.aiAnalyzer != nil {
		state.AIInsights = s.aiAnalyzer.GetInsights()
	}
	writeJSON(w, http.StatusOK, state)
}

func (s *Server) handleAlerts(w http.ResponseWriter, r *http.Request) {
	alerts := s.collector.GetAlerts()
	writeJSON(w, http.StatusOK, alerts)
}

func (s *Server) handleReports(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		report, err := s.reporter.GenerateReport(r.Context())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, report)
		return
	}
	reports := s.reporter.GetReports()
	writeJSON(w, http.StatusOK, reports)
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	safe := *s.config
	safe.Source.Password = "***"
	safe.Target.Password = "***"
	safe.Server.Auth.Password = "***"
	safe.AI.APIKey = "***"
	writeJSON(w, http.StatusOK, safe)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	features := []string{"monitoring", "upgrades", "xdcr", "storm", "validation", "reporting"}
	if s.config.AI.Enabled {
		features = append(features, "ai_analysis")
	}
	if s.config.Failover.Enabled {
		features = append(features, "ha_failover")
	}
	if s.config.Backup.Enabled {
		features = append(features, "backup_restore")
	}
	if s.config.Docker.Enabled {
		features = append(features, "docker_management")
	}
	if len(s.config.Regions) > 0 {
		features = append(features, "multi_region")
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":              "ok",
		"version":             "1.0.0",
		"uptime":              time.Since(s.startTime).Round(time.Second).String(),
		"ws_clients":          s.hub.ClientCount(),
		"auth":                s.config.Server.Auth.Enabled,
		"domain":              s.config.Server.Domain,
		"tls":                 s.config.Server.TLS.Enabled,
		"clusters_configured": len(s.pool.Configs()),
		"clusters_connected":  len(s.pool.Connected()),
		"features":            features,
		"regions":             len(s.config.Regions),
	})
}

func (s *Server) handleCommand(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST only"})
		return
	}

	var cmd models.Command
	if err := json.NewDecoder(r.Body).Decode(&cmd); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	result := s.executeCommand(r.Context(), cmd)
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) executeCommand(ctx context.Context, cmd models.Command) map[string]string {
	log.Printf("[API] Executing command: %s", cmd.Action)

	switch cmd.Action {
	case "start_load":
		if err := s.storm.Start(ctx); err != nil {
			return map[string]string{"status": "error", "message": err.Error()}
		}
		return map[string]string{"status": "ok", "message": "Load test started"}

	case "pause_load":
		s.storm.Pause()
		return map[string]string{"status": "ok", "message": "Load test paused"}

	case "resume_load":
		s.storm.Resume()
		return map[string]string{"status": "ok", "message": "Load test resumed"}

	case "stop_load":
		s.storm.Stop()
		return map[string]string{"status": "ok", "message": "Load test stopped"}

	case "start_upgrade":
		clusterName := cmd.Params["cluster_name"]
		targetVersion := cmd.Params["target_version"]
		namespace := cmd.Params["namespace"]
		if err := s.orchestrator.StartWithFullParams(ctx, clusterName, targetVersion, namespace); err != nil {
			return map[string]string{"status": "error", "message": err.Error()}
		}
		msg := "Upgrade started"
		if clusterName != "" && targetVersion != "" {
			msg = fmt.Sprintf("Upgrading %s to v%s", clusterName, targetVersion)
		}
		return map[string]string{"status": "ok", "message": msg}

	case "abort_upgrade":
		s.orchestrator.Abort()
		return map[string]string{"status": "ok", "message": "Upgrade aborted"}

	case "restart_xdcr":
		if err := s.xdcrEngine.RestartPipeline(ctx); err != nil {
			return map[string]string{"status": "error", "message": err.Error()}
		}
		return map[string]string{"status": "ok", "message": "XDCR pipeline restarted"}

	case "run_audit":
		if s.validator == nil {
			return map[string]string{"status": "error", "message": "Validator not initialised"}
		}
		if err := s.validator.CanAudit(); err != nil {
			return map[string]string{"status": "error", "message": err.Error()}
		}
		go func() {
			result, err := s.validator.RunFullAudit(context.Background())
			if err != nil {
				s.hub.Broadcast("alert", models.Alert{
					Severity: "critical",
					Title:    "Audit Error",
					Message:  err.Error(),
				})
				return
			}
			s.hub.Broadcast("audit_complete", result)
		}()
		return map[string]string{"status": "ok", "message": "Full audit started"}

	case "inject_failure":
		failType := cmd.Params["type"]
		target := cmd.Params["target"]
		log.Printf("[API] Injecting failure: type=%s target=%s", failType, target)
		return map[string]string{"status": "ok", "message": fmt.Sprintf("Failure injected: %s on %s", failType, target)}

	case "ai_analyze":
		if s.aiAnalyzer == nil {
			return map[string]string{"status": "error", "message": "AI analysis not configured"}
		}
		go func() {
			resp, err := s.aiAnalyzer.AutoAnalyze(context.Background())
			if err != nil {
				log.Printf("[AI] Auto-analyze error: %v", err)
				return
			}
			s.hub.Broadcast("ai_insight", resp.Insight)
		}()
		return map[string]string{"status": "ok", "message": "AI analysis started"}

	case "start_backup":
		if s.backupMgr == nil {
			return map[string]string{"status": "error", "message": "Backup not configured"}
		}
		cluster := cmd.Params["cluster_name"]
		if cluster == "" {
			return map[string]string{"status": "error", "message": "cluster_name required"}
		}
		_, err := s.backupMgr.StartBackup(ctx, cluster, "full", nil)
		if err != nil {
			return map[string]string{"status": "error", "message": err.Error()}
		}
		return map[string]string{"status": "ok", "message": fmt.Sprintf("Backup of %s started", cluster)}

	case "manual_failover":
		if s.failoverMgr == nil {
			return map[string]string{"status": "error", "message": "Failover not configured"}
		}
		source := cmd.Params["source_cluster"]
		target := cmd.Params["target_cluster"]
		_, err := s.failoverMgr.ManualFailover(ctx, source, target)
		if err != nil {
			return map[string]string{"status": "error", "message": err.Error()}
		}
		return map[string]string{"status": "ok", "message": fmt.Sprintf("Failover %s -> %s completed", source, target)}

	default:
		return map[string]string{"status": "error", "message": "unknown command: " + cmd.Action}
	}
}

// ─── Multi-Region Handlers ──────────────────────────────────────────────────

func (s *Server) handleRegions(w http.ResponseWriter, r *http.Request) {
	if s.regionMgr == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"regions": []interface{}{}, "message": "multi-region not configured"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"regions":       s.regionMgr.GetRegionStatuses(),
		"replications":  s.regionMgr.GetReplications(),
		"configs":       s.regionMgr.GetRegionConfigs(),
	})
}

func (s *Server) handleRegionPromote(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST only"})
		return
	}
	if s.regionMgr == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "multi-region not configured"})
		return
	}
	var req struct {
		Region string `json:"region"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := s.regionMgr.PromoteRegion(req.Region); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "message": req.Region + " promoted to primary"})
}

// ─── HA & Failover Handlers ─────────────────────────────────────────────────

func (s *Server) handleFailover(w http.ResponseWriter, r *http.Request) {
	if s.failoverMgr == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"status": "disabled", "message": "failover not configured"})
		return
	}
	writeJSON(w, http.StatusOK, s.failoverMgr.GetStatus())
}

func (s *Server) handleManualFailover(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST only"})
		return
	}
	if s.failoverMgr == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "failover not configured"})
		return
	}
	var req struct {
		SourceCluster string `json:"source_cluster"`
		TargetCluster string `json:"target_cluster"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	event, err := s.failoverMgr.ManualFailover(r.Context(), req.SourceCluster, req.TargetCluster)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, event)
}

func (s *Server) handleGracefulFailover(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST only"})
		return
	}
	if s.failoverMgr == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "failover not configured"})
		return
	}
	var req struct {
		Cluster string   `json:"cluster"`
		Nodes   []string `json:"nodes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	event, err := s.failoverMgr.GracefulFailover(r.Context(), req.Cluster, req.Nodes)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, event)
}

func (s *Server) handleFailoverHistory(w http.ResponseWriter, r *http.Request) {
	if s.failoverMgr == nil {
		writeJSON(w, http.StatusOK, []interface{}{})
		return
	}
	writeJSON(w, http.StatusOK, s.failoverMgr.GetHistory())
}

// ─── Backup & Restore Handlers ──────────────────────────────────────────────

func (s *Server) handleBackup(w http.ResponseWriter, r *http.Request) {
	if s.backupMgr == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"status": "disabled", "message": "backup not configured"})
		return
	}
	writeJSON(w, http.StatusOK, s.backupMgr.GetStatus())
}

func (s *Server) handleBackupStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST only"})
		return
	}
	if s.backupMgr == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "backup not configured"})
		return
	}
	var req struct {
		ClusterName string   `json:"cluster_name"`
		Type        string   `json:"type"`
		Buckets     []string `json:"buckets,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if req.Type == "" {
		req.Type = "full"
	}
	info, err := s.backupMgr.StartBackup(r.Context(), req.ClusterName, req.Type, req.Buckets)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) handleRestore(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST only"})
		return
	}
	if s.backupMgr == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "backup not configured"})
		return
	}
	var req struct {
		BackupID      string   `json:"backup_id"`
		TargetCluster string   `json:"target_cluster"`
		Buckets       []string `json:"buckets,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	info, err := s.backupMgr.StartRestore(r.Context(), req.BackupID, req.TargetCluster, req.Buckets)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) handleBackupList(w http.ResponseWriter, r *http.Request) {
	if s.backupMgr == nil {
		writeJSON(w, http.StatusOK, []interface{}{})
		return
	}
	backups, err := s.backupMgr.ListRepositoryBackups(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, backups)
}

// ─── Data Migration Handlers ────────────────────────────────────────────────

func (s *Server) handleMigration(w http.ResponseWriter, r *http.Request) {
	if s.migrationEngine == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"status": "disabled"})
		return
	}
	status := s.migrationEngine.GetStatus()
	if status == nil {
		writeJSON(w, http.StatusOK, map[string]string{"status": "idle"})
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) handleMigrationStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST only"})
		return
	}
	if s.migrationEngine == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "migration engine not available"})
		return
	}
	var req models.MigrationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	status, err := s.migrationEngine.StartMigration(r.Context(), req)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) handleMigrationHistory(w http.ResponseWriter, r *http.Request) {
	if s.migrationEngine == nil {
		writeJSON(w, http.StatusOK, []interface{}{})
		return
	}
	writeJSON(w, http.StatusOK, s.migrationEngine.GetHistory())
}

// ─── AI Analysis Handlers ───────────────────────────────────────────────────

func (s *Server) handleAIAnalyze(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST only"})
		return
	}
	if s.aiAnalyzer == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "AI analysis not configured"})
		return
	}
	var req models.AIAnalysisRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	resp, err := s.aiAnalyzer.Analyze(r.Context(), req)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleAIAutoAnalyze(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST only"})
		return
	}
	if s.aiAnalyzer == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "AI analysis not configured"})
		return
	}
	resp, err := s.aiAnalyzer.AutoAnalyze(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleAIInsights(w http.ResponseWriter, r *http.Request) {
	if s.aiAnalyzer == nil {
		writeJSON(w, http.StatusOK, []interface{}{})
		return
	}
	writeJSON(w, http.StatusOK, s.aiAnalyzer.GetInsights())
}

// ─── Docker Management Handlers ─────────────────────────────────────────────

func (s *Server) handleDockerContainers(w http.ResponseWriter, r *http.Request) {
	if s.dockerClient == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"containers": []interface{}{}, "message": "Docker not configured"})
		return
	}
	containers, err := s.dockerClient.ListContainers(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"containers": containers})
}

func (s *Server) handleDockerCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST only"})
		return
	}
	if s.dockerClient == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Docker not configured"})
		return
	}
	var req struct {
		Name  string            `json:"name"`
		Image string            `json:"image"`
		Ports map[string]string `json:"ports"`
		Env   map[string]string `json:"env"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	info, err := s.dockerClient.CreateCluster(r.Context(), req.Name, req.Image, req.Ports, req.Env)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) handleDockerLogs(w http.ResponseWriter, r *http.Request) {
	if s.dockerClient == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Docker not configured"})
		return
	}
	container := r.URL.Query().Get("container")
	if container == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "container parameter required"})
		return
	}
	logs, err := s.dockerClient.GetContainerLogs(r.Context(), container, 200)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"logs": logs, "container": container})
}

// ─── Cluster Management Handlers (Buckets, Indexes, Users, Edition) ─────────

func (s *Server) handleClusterBuckets(w http.ResponseWriter, r *http.Request) {
	clusterName := r.URL.Query().Get("cluster")
	if clusterName == "" {
		clusterName = s.pool.GetNameByRole("source")
	}

	configs := s.pool.Configs()
	cfg, ok := configs[clusterName]
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "cluster not found: " + clusterName})
		return
	}

	buckets, err := fetchBuckets(r.Context(), cfg)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"cluster": clusterName, "buckets": buckets})
}

func (s *Server) handleClusterIndexes(w http.ResponseWriter, r *http.Request) {
	clusterName := r.URL.Query().Get("cluster")
	if clusterName == "" {
		clusterName = s.pool.GetNameByRole("source")
	}

	configs := s.pool.Configs()
	cfg, ok := configs[clusterName]
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "cluster not found: " + clusterName})
		return
	}

	indexes, err := fetchIndexes(r.Context(), cfg)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"cluster": clusterName, "indexes": indexes})
}

func (s *Server) handleClusterUsers(w http.ResponseWriter, r *http.Request) {
	clusterName := r.URL.Query().Get("cluster")
	if clusterName == "" {
		clusterName = s.pool.GetNameByRole("source")
	}

	configs := s.pool.Configs()
	cfg, ok := configs[clusterName]
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "cluster not found: " + clusterName})
		return
	}

	users, err := fetchUsers(r.Context(), cfg)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"cluster": clusterName, "users": users})
}

func (s *Server) handleClusterEdition(w http.ResponseWriter, r *http.Request) {
	clusterName := r.URL.Query().Get("cluster")
	if clusterName == "" {
		clusterName = s.pool.GetNameByRole("source")
	}

	configs := s.pool.Configs()
	cfg, ok := configs[clusterName]
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "cluster not found: " + clusterName})
		return
	}

	edition, err := detectEdition(r.Context(), cfg)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"cluster": clusterName, "edition": edition})
}

func (s *Server) handleClusterTopology(w http.ResponseWriter, r *http.Request) {
	clusterName := r.URL.Query().Get("cluster")
	if clusterName == "" {
		clusterName = s.pool.GetNameByRole("source")
	}

	configs := s.pool.Configs()
	cfg, ok := configs[clusterName]
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "cluster not found: " + clusterName})
		return
	}

	edition, _ := detectEdition(r.Context(), cfg)
	buckets, _ := fetchBuckets(r.Context(), cfg)
	indexes, _ := fetchIndexes(r.Context(), cfg)

	topology := models.ClusterTopology{
		ClusterName: clusterName,
		Edition:     edition,
		Platform:    cfg.Platform,
		Region:      cfg.Region,
		Zone:        cfg.Zone,
		Buckets:     buckets,
		Indexes:     indexes,
		Timestamp:   time.Now(),
	}

	writeJSON(w, http.StatusOK, topology)
}

func (s *Server) handleClusterLogs(w http.ResponseWriter, r *http.Request) {
	clusterName := r.URL.Query().Get("cluster")
	if clusterName == "" {
		clusterName = s.pool.GetNameByRole("source")
	}

	configs := s.pool.Configs()
	cfg, ok := configs[clusterName]
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "cluster not found: " + clusterName})
		return
	}

	logs, err := fetchLogs(r.Context(), cfg)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"cluster": clusterName, "logs": logs})
}

// ─── Utilities ───────────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}
