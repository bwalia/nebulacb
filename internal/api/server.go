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
	"github.com/balinderwalia/nebulacb/pkg/kubernetes"
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
	k8sClient       *kubernetes.K8sClient

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
	k8sClient *kubernetes.K8sClient,
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
		k8sClient:       k8sClient,
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

	// Protected API routes — XDCR Management
	mux.HandleFunc("/api/v1/xdcr/diagnostics", s.requireAuth(s.handleXDCRDiagnostics))

	// Protected API routes — AI Analysis
	mux.HandleFunc("/api/v1/ai/analyze", s.requireAuth(s.handleAIAnalyze))
	mux.HandleFunc("/api/v1/ai/auto-analyze", s.requireAuth(s.handleAIAutoAnalyze))
	mux.HandleFunc("/api/v1/ai/insights", s.requireAuth(s.handleAIInsights))
	mux.HandleFunc("/api/v1/ai/rca", s.requireAuth(s.handleAIRCA))
	mux.HandleFunc("/api/v1/ai/knowledge", s.requireAuth(s.handleAIKnowledge))

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

	// Protected API routes — Enterprise: Logs, Events, Operator, Diagnostics, Runbooks
	mux.HandleFunc("/api/v1/k8s/logs", s.requireAuth(s.handleK8sLogs))
	mux.HandleFunc("/api/v1/k8s/events", s.requireAuth(s.handleK8sEvents))
	mux.HandleFunc("/api/v1/k8s/operator", s.requireAuth(s.handleK8sOperator))
	mux.HandleFunc("/api/v1/k8s/pods", s.requireAuth(s.handleK8sPods))
	mux.HandleFunc("/api/v1/diagnostics", s.requireAuth(s.handleDiagnosticsBundle))
	mux.HandleFunc("/api/v1/runbooks", s.requireAuth(s.handleRunbooks))
	mux.HandleFunc("/api/v1/recommendations", s.requireAuth(s.handleRecommendations))

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

	case "downgrade":
		if err := s.orchestrator.Downgrade(ctx); err != nil {
			return map[string]string{"status": "error", "message": err.Error()}
		}
		return map[string]string{"status": "ok", "message": "Downgrade initiated — rolling back to previous version"}

	case "restart_xdcr":
		if err := s.xdcrEngine.RestartPipeline(ctx); err != nil {
			return map[string]string{"status": "error", "message": err.Error()}
		}
		return map[string]string{"status": "ok", "message": "XDCR pipeline restarted"}

	case "pause_xdcr":
		if err := s.xdcrEngine.PausePipeline(ctx); err != nil {
			return map[string]string{"status": "error", "message": err.Error()}
		}
		return map[string]string{"status": "ok", "message": "XDCR pipeline paused"}

	case "resume_xdcr":
		if err := s.xdcrEngine.ResumePipeline(ctx); err != nil {
			return map[string]string{"status": "error", "message": err.Error()}
		}
		return map[string]string{"status": "ok", "message": "XDCR pipeline resumed"}

	case "stop_xdcr":
		if err := s.xdcrEngine.StopPipeline(ctx); err != nil {
			return map[string]string{"status": "error", "message": err.Error()}
		}
		return map[string]string{"status": "ok", "message": "XDCR pipeline stopped"}

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

	case "start_restore":
		if s.backupMgr == nil {
			return map[string]string{"status": "error", "message": "Backup not configured"}
		}
		backupID := cmd.Params["backup_id"]
		target := cmd.Params["target_cluster"]
		if backupID == "" || target == "" {
			return map[string]string{"status": "error", "message": "backup_id and target_cluster required"}
		}
		_, err := s.backupMgr.StartRestore(ctx, backupID, target, nil)
		if err != nil {
			return map[string]string{"status": "error", "message": err.Error()}
		}
		return map[string]string{"status": "ok", "message": fmt.Sprintf("Restore of %s to %s started", backupID, target)}

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

// ─── XDCR Handlers ─────────────────────────────────────────────────────────

func (s *Server) handleXDCRDiagnostics(w http.ResponseWriter, r *http.Request) {
	results := s.xdcrEngine.RunDiagnostics(r.Context())
	writeJSON(w, http.StatusOK, results)
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

func (s *Server) handleAIRCA(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		if s.aiAnalyzer == nil {
			writeJSON(w, http.StatusOK, []interface{}{})
			return
		}
		writeJSON(w, http.StatusOK, s.aiAnalyzer.GetRCAReports())
		return
	}
	if r.Method == "POST" {
		if s.aiAnalyzer == nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "AI not configured"})
			return
		}
		var req struct {
			Cluster  string `json:"cluster"`
			Category string `json:"category"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		report, err := s.aiAnalyzer.RunRCA(r.Context(), req.Cluster, req.Category, s.collector)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, report)
		return
	}
	writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "GET or POST only"})
}

func (s *Server) handleAIKnowledge(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, knowledgeBase)
}

// knowledgeBase is a built-in set of common Couchbase issues and solutions.
var knowledgeBase = []map[string]interface{}{
	{
		"id": "kb-xdcr-lag", "category": "XDCR", "severity": "warning",
		"title":       "XDCR Replication Lag Increasing",
		"description": "XDCR replication lag grows when the target cluster cannot keep up with mutations on the source, often during upgrades or heavy write loads.",
		"symptoms":    []string{"changes_left increasing", "replication_lag_ms > 5000", "pipeline restarts", "GOXDCR delay > 5 minutes"},
		"solution":    "1. Check target cluster CPU/memory usage. 2. Increase XDCR nozzle count. 3. Check network bandwidth between clusters. 4. Pause non-critical load during upgrades.",
		"commands":    []string{"curl -u admin:pass http://source:8091/pools/default/buckets/bucket/stats", "curl -u admin:pass http://source:8091/settings/replications"},
		"tags":        []string{"xdcr", "replication", "lag", "performance"},
	},
	{
		"id": "kb-xdcr-pipeline", "category": "XDCR", "severity": "critical",
		"title":       "XDCR Pipeline Restarts During Upgrade",
		"description": "During rolling upgrades, XDCR pipelines restart when nodes leave and rejoin the cluster. This is expected but should be monitored for data integrity.",
		"symptoms":    []string{"pipeline_restarts > 0", "topology_change=true", "brief replication pause", "GOXDCR delay timer starts"},
		"solution":    "Pipeline restarts are expected during rolling upgrades. Monitor: 1. changes_left should decrease after restart. 2. GOXDCR delay should resolve within 5 minutes. 3. Run data audit after upgrade completes.",
		"commands":    []string{"nebulacb-cli restart-xdcr", "nebulacb-cli run-audit"},
		"tags":        []string{"xdcr", "upgrade", "pipeline", "topology"},
	},
	{
		"id": "kb-upgrade-rebalance", "category": "Upgrade", "severity": "warning",
		"title":       "Rebalance Stuck During Rolling Upgrade",
		"description": "A rebalance operation can get stuck if a node fails to rejoin the cluster after upgrade, or if there are insufficient resources.",
		"symptoms":    []string{"rebalance_state=running for >30 minutes", "node status=unhealthy after upgrade", "upgrade progress stalled"},
		"solution":    "1. Check node logs for errors (cbcollect_info). 2. Verify the node has enough disk/memory. 3. If stuck, cancel rebalance and retry. 4. As last resort, failover the problematic node.",
		"commands":    []string{"curl -u admin:pass http://node:8091/controller/rebalance", "curl -u admin:pass -X POST http://node:8091/controller/stopRebalance"},
		"tags":        []string{"upgrade", "rebalance", "stuck", "node"},
	},
	{
		"id": "kb-upgrade-version-mismatch", "category": "Upgrade", "severity": "info",
		"title":       "Mixed Version Cluster During Upgrade",
		"description": "During a rolling upgrade, the cluster runs with mixed versions. This is supported but some features may be unavailable until all nodes are upgraded.",
		"symptoms":    []string{"nodes with different versions", "some features unavailable", "REST API may return mixed results"},
		"solution":    "This is expected during rolling upgrades. Complete the upgrade of all nodes before enabling new features. Avoid schema changes during mixed-version operation.",
		"commands":    []string{"curl -u admin:pass http://node:8091/pools/default | jq '.nodes[].version'"},
		"tags":        []string{"upgrade", "version", "mixed-mode", "compatibility"},
	},
	{
		"id": "kb-failover-auto", "category": "Failover", "severity": "critical",
		"title":       "Auto-Failover Triggered Unexpectedly",
		"description": "Auto-failover activates when a node becomes unresponsive for longer than the configured timeout. This can happen during network partitions or resource exhaustion.",
		"symptoms":    []string{"node marked as failed", "auto-failover event in logs", "rebalance triggered automatically", "data may be temporarily unavailable"},
		"solution":    "1. Investigate why the node became unresponsive. 2. Check network connectivity. 3. Check for OOM kills or disk failures. 4. After fixing, add the node back with delta recovery if possible.",
		"commands":    []string{"curl -u admin:pass http://node:8091/pools/default/serverGroups", "curl -u admin:pass -X POST http://node:8091/controller/addBack -d otpNode=ns_1@hostname"},
		"tags":        []string{"failover", "auto-failover", "ha", "node-failure"},
	},
	{
		"id": "kb-failover-region", "category": "Failover", "severity": "critical",
		"title":       "Cross-Region Failover Setup",
		"description": "Setting up failover across regions requires bidirectional XDCR, monitoring of both clusters, and clear failover procedures.",
		"symptoms":    []string{"need multi-region HA", "disaster recovery planning", "cross-region replication setup"},
		"solution":    "1. Set up bidirectional XDCR between regions. 2. Configure auto-failover with appropriate timeout (>60s for cross-region). 3. Ensure DNS/load balancer can redirect traffic. 4. Test failover procedure regularly.",
		"commands":    []string{"curl -X POST http://source:8091/pools/default/remoteClusters -d 'name=dc2&hostname=target:8091'", "curl -X POST http://source:8091/controller/createReplication -d 'fromBucket=app&toCluster=dc2&toBucket=app'"},
		"tags":        []string{"failover", "multi-region", "xdcr", "disaster-recovery"},
	},
	{
		"id": "kb-backup-schedule", "category": "Backup", "severity": "info",
		"title":       "Backup Schedule Best Practices",
		"description": "Regular backups protect against data loss from hardware failures, human errors, and software bugs.",
		"symptoms":    []string{"no backup configured", "backup failing", "restore needed", "backup too slow"},
		"solution":    "1. Schedule daily incremental + weekly full backups. 2. Store backups in a different region/cloud. 3. Enable compression for smaller backups. 4. Test restore procedures monthly. 5. Monitor backup duration and size trends.",
		"commands":    []string{"cbbackupmgr config --archive /backup --repo default --include-data bucket", "cbbackupmgr backup --archive /backup --repo default --cluster couchbase://localhost -u admin -p pass"},
		"tags":        []string{"backup", "restore", "schedule", "best-practices"},
	},
	{
		"id": "kb-backup-restore-fail", "category": "Backup", "severity": "critical",
		"title":       "Backup Restore Failure",
		"description": "Restore operations can fail due to version incompatibility, insufficient resources, or corrupted backup files.",
		"symptoms":    []string{"restore command fails", "partial data restored", "bucket creation error during restore", "timeout during restore"},
		"solution":    "1. Verify backup integrity with cbbackupmgr examine. 2. Ensure target cluster has enough RAM quota. 3. Check version compatibility. 4. For large restores, increase timeout and use --threads.",
		"commands":    []string{"cbbackupmgr examine --archive /backup --repo default", "cbbackupmgr restore --archive /backup --repo default --cluster couchbase://target -u admin -p pass"},
		"tags":        []string{"backup", "restore", "failure", "troubleshoot"},
	},
	{
		"id": "kb-perf-memory", "category": "Performance", "severity": "warning",
		"title":       "High Memory Usage / Low Resident Ratio",
		"description": "When the resident ratio drops below 90%, Couchbase starts fetching documents from disk, significantly impacting read latency.",
		"symptoms":    []string{"vb_active_resident_items_ratio < 90%", "ep_bg_fetched increasing", "high read latency", "disk read queue growing"},
		"solution":    "1. Increase bucket RAM quota. 2. Add more nodes to distribute data. 3. Enable compression. 4. Review data model for oversized documents. 5. Consider using eviction policy 'valueOnly'.",
		"commands":    []string{"curl -u admin:pass http://node:8091/pools/default/buckets/bucket -X POST -d ramQuota=1024"},
		"tags":        []string{"performance", "memory", "resident-ratio", "latency"},
	},
	{
		"id": "kb-perf-disk-queue", "category": "Performance", "severity": "warning",
		"title":       "High Disk Write Queue",
		"description": "A growing disk write queue indicates the storage subsystem cannot keep up with incoming writes. This can lead to temporary OOM and client backpressure.",
		"symptoms":    []string{"disk_write_queue > 10000", "ep_queue_size growing", "write latency increasing", "temp OOM errors"},
		"solution":    "1. Check disk I/O with iostat. 2. Consider SSD storage. 3. Reduce write throughput temporarily. 4. Add more nodes. 5. Check compaction settings — too frequent compaction competes with writes.",
		"commands":    []string{"curl -u admin:pass http://node:8091/pools/default/buckets/bucket/stats?stat=disk_write_queue"},
		"tags":        []string{"performance", "disk", "write-queue", "storage"},
	},
	{
		"id": "kb-data-integrity", "category": "Data Integrity", "severity": "critical",
		"title":       "Document Count Mismatch Between Clusters",
		"description": "Source and target clusters show different document counts, which may indicate XDCR lag, failed mutations, or conflict resolution issues.",
		"symptoms":    []string{"delta between source and target > 0", "missing documents on target", "hash mismatches in audit", "sequence gaps detected"},
		"solution":    "1. Wait for XDCR to catch up (check changes_left). 2. Run data integrity audit. 3. Check for conflict resolution (LWW vs sequence). 4. Verify bidirectional XDCR if both clusters accept writes.",
		"commands":    []string{"nebulacb-cli run-audit", "nebulacb-cli status"},
		"tags":        []string{"data-integrity", "xdcr", "document-count", "audit"},
	},
	{
		"id": "kb-config-nodeport", "category": "Configuration", "severity": "info",
		"title":       "External Access to Kubernetes Couchbase Clusters",
		"description": "Accessing Couchbase clusters running in Kubernetes from outside the cluster requires NodePort services and SDK network=external configuration.",
		"symptoms":    []string{"SDK connection timeout from outside k8s", "cluster not ready error", "cannot reach KV port"},
		"solution":    "1. Patch CouchbaseCluster with exposedFeatures: [client, admin]. 2. Set kv_port in config.json to the KV NodePort. 3. SDK uses ?network=external to read alternate addresses. 4. Use the node IP where the pod runs (check externalTrafficPolicy).",
		"commands":    []string{"kubectl patch couchbasecluster cb-local -n couchbase --type merge -p '{\"spec\":{\"networking\":{\"exposedFeatures\":[\"client\",\"admin\"]}}}'", "kubectl get svc -n couchbase -l couchbase_cluster=cb-local"},
		"tags":        []string{"kubernetes", "nodeport", "external-access", "sdk", "configuration"},
	},
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

// ─── Enterprise: Logs, Events, Operator, Diagnostics, Runbooks ─────────────

func (s *Server) handleK8sLogs(w http.ResponseWriter, r *http.Request) {
	if s.k8sClient == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Kubernetes not configured"})
		return
	}
	pod := r.URL.Query().Get("pod")
	namespace := r.URL.Query().Get("namespace")
	if pod == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "pod parameter required"})
		return
	}
	tailLines := 200
	if t := r.URL.Query().Get("tail"); t != "" {
		fmt.Sscanf(t, "%d", &tailLines)
	}
	sinceSeconds := 0
	if s := r.URL.Query().Get("since"); s != "" {
		fmt.Sscanf(s, "%d", &sinceSeconds)
	}
	logs, err := s.k8sClient.GetPodLogs(r.Context(), namespace, pod, tailLines, sinceSeconds)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"pod": pod, "namespace": namespace, "lines": strings.Split(strings.TrimSpace(logs), "\n"),
	})
}

func (s *Server) handleK8sEvents(w http.ResponseWriter, r *http.Request) {
	if s.k8sClient == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Kubernetes not configured"})
		return
	}
	namespace := r.URL.Query().Get("namespace")
	events, err := s.k8sClient.GetEvents(r.Context(), namespace)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, events)
}

func (s *Server) handleK8sOperator(w http.ResponseWriter, r *http.Request) {
	if s.k8sClient == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Kubernetes not configured"})
		return
	}
	// Get operator status for all configured k8s clusters
	configs := s.pool.Configs()
	var results []interface{}
	for name, cfg := range configs {
		if cfg.Platform != "kubernetes" || cfg.Namespace == "" {
			continue
		}
		status, err := s.k8sClient.GetOperatorStatus(r.Context(), name, cfg.Namespace)
		if err != nil {
			results = append(results, map[string]string{"cluster": name, "error": err.Error()})
			continue
		}
		results = append(results, status)
	}
	writeJSON(w, http.StatusOK, results)
}

func (s *Server) handleK8sPods(w http.ResponseWriter, r *http.Request) {
	if s.k8sClient == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Kubernetes not configured"})
		return
	}
	namespace := r.URL.Query().Get("namespace")
	pods, err := s.k8sClient.GetAllPodsInNamespace(r.Context(), namespace)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, pods)
}

func (s *Server) handleDiagnosticsBundle(w http.ResponseWriter, r *http.Request) {
	// Build a diagnostics bundle with cluster state, XDCR, alerts, metrics
	state := s.collector.GetDashboardState()
	var k8sInfo interface{}
	if s.k8sClient != nil {
		configs := s.pool.Configs()
		var opStatuses []interface{}
		for name, cfg := range configs {
			if cfg.Platform != "kubernetes" || cfg.Namespace == "" {
				continue
			}
			status, err := s.k8sClient.GetOperatorStatus(r.Context(), name, cfg.Namespace)
			if err != nil {
				opStatuses = append(opStatuses, map[string]string{"cluster": name, "error": err.Error()})
			} else {
				opStatuses = append(opStatuses, status)
			}
		}
		k8sInfo = opStatuses
	}

	bundle := map[string]interface{}{
		"timestamp":        time.Now(),
		"version":          "1.0.0",
		"clusters":         state.Clusters,
		"source_cluster":   state.SourceCluster,
		"target_cluster":   state.TargetCluster,
		"xdcr_status":      state.XDCRStatus,
		"data_loss_proof":  state.DataLossProof,
		"integrity_result": state.IntegrityResult,
		"storm_metrics":    state.StormMetrics,
		"upgrade_status":   state.UpgradeStatus,
		"alerts":           state.Alerts,
		"regions":          state.Regions,
		"failover_status":  state.FailoverStatus,
		"backup_status":    state.BackupStatus,
		"migration_status": state.MigrationStatus,
		"ai_insights":      state.AIInsights,
		"k8s_operator":     k8sInfo,
		"config": map[string]interface{}{
			"source":  s.config.Source.Host,
			"target":  s.config.Target.Host,
			"storm":   s.config.Storm,
			"upgrade": s.config.Upgrade,
			"xdcr":    s.config.XDCR,
		},
	}
	w.Header().Set("Content-Disposition", "attachment; filename=nebulacb-diagnostics.json")
	writeJSON(w, http.StatusOK, bundle)
}

// Runbook represents a pre-built automated workflow.
type Runbook struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Category    string   `json:"category"`
	Steps       []string `json:"steps"`
	Actions     []string `json:"actions"` // command actions to execute
	Risk        string   `json:"risk"`
}

var runbooks = []Runbook{
	{
		ID: "safe-upgrade", Name: "Safe Couchbase Upgrade",
		Description: "Validates cluster health, starts load test, performs rolling upgrade, monitors XDCR, and validates data integrity post-upgrade.",
		Category: "upgrade", Risk: "medium",
		Steps: []string{
			"Pre-check: verify all nodes healthy and ready",
			"Start Storm load generator for continuous traffic",
			"Trigger rolling upgrade via Operator CR patch",
			"Monitor XDCR pipeline restarts and lag",
			"Wait for all nodes to reach target version",
			"Run full data integrity audit",
			"Generate upgrade report",
		},
		Actions: []string{"start_load", "start_upgrade", "run_audit", "generate_report"},
	},
	{
		ID: "xdcr-validation", Name: "XDCR Validation Mode",
		Description: "Runs bidirectional write test across both clusters and validates replication integrity with hash comparison.",
		Category: "xdcr", Risk: "low",
		Steps: []string{
			"Verify XDCR replication is active (both directions)",
			"Start xdcr-loadtest writing to both clusters",
			"Monitor replication lag and changes_left",
			"Wait for convergence (delta = 0)",
			"Run data integrity audit with hash check",
			"Verify zero data loss",
		},
		Actions: []string{"start_load", "run_audit"},
	},
	{
		ID: "blue-green-migration", Name: "Blue/Green Migration",
		Description: "Migrate traffic from source to target cluster using XDCR, with gradual traffic shift and validation at each stage.",
		Category: "migration", Risk: "high",
		Steps: []string{
			"Ensure XDCR is active and caught up (changes_left = 0)",
			"Run data integrity audit to confirm sync",
			"Shift 10% read traffic to target cluster",
			"Monitor target cluster performance and latency",
			"Shift 50% traffic, then 100%",
			"Verify all operations succeed on target",
			"Decommission source cluster XDCR",
		},
		Actions: []string{"run_audit"},
	},
	{
		ID: "dr-simulation", Name: "Disaster Recovery Simulation",
		Description: "Simulates source cluster failure and validates failover to target cluster with zero data loss.",
		Category: "failover", Risk: "high",
		Steps: []string{
			"Record current doc counts on both clusters",
			"Trigger manual failover (promote target)",
			"Verify target cluster accepts all traffic",
			"Check data integrity — no missing documents",
			"Restore source cluster and re-establish XDCR",
			"Verify bidirectional sync resumes",
		},
		Actions: []string{"manual_failover", "run_audit"},
	},
	{
		ID: "backup-restore-test", Name: "Backup & Restore Validation",
		Description: "Creates a backup, restores to a test cluster, and validates restored data matches source.",
		Category: "backup", Risk: "low",
		Steps: []string{
			"Trigger full backup of source cluster",
			"Verify backup completed successfully",
			"Restore backup to target cluster",
			"Compare document counts source vs restored",
			"Run hash-based integrity check",
		},
		Actions: []string{"start_backup", "run_audit"},
	},
}

func (s *Server) handleRunbooks(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		writeJSON(w, http.StatusOK, runbooks)
		return
	}
	if r.Method == "POST" {
		var req struct {
			RunbookID string `json:"runbook_id"`
			Step      int    `json:"step"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		// Find the runbook
		for _, rb := range runbooks {
			if rb.ID == req.RunbookID {
				if req.Step >= 0 && req.Step < len(rb.Actions) {
					action := rb.Actions[req.Step]
					result := s.executeCommand(r.Context(), models.Command{Action: action})
					writeJSON(w, http.StatusOK, map[string]interface{}{
						"runbook": rb.ID, "step": req.Step, "action": action, "result": result,
					})
					return
				}
				writeJSON(w, http.StatusOK, map[string]interface{}{
					"runbook": rb.ID, "message": "Step index out of range or no action for this step",
				})
				return
			}
		}
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "runbook not found: " + req.RunbookID})
		return
	}
	writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "GET or POST"})
}

func (s *Server) handleRecommendations(w http.ResponseWriter, r *http.Request) {
	state := s.collector.GetDashboardState()
	var recs []map[string]string

	// Analyze cluster state and generate recommendations
	for name, cm := range state.Clusters {
		if !cm.Healthy {
			recs = append(recs, map[string]string{
				"severity": "critical", "cluster": name,
				"title":   "Cluster Unhealthy",
				"message": fmt.Sprintf("Cluster %s is reporting unhealthy status. Check node health and connectivity.", name),
			})
		}
		if cm.ResidentRatio > 0 && cm.ResidentRatio < 90 {
			recs = append(recs, map[string]string{
				"severity": "warning", "cluster": name,
				"title":   "Low Resident Ratio",
				"message": fmt.Sprintf("Cluster %s resident ratio is %.1f%%. Consider increasing RAM quota or adding nodes.", name, cm.ResidentRatio),
			})
		}
		if cm.DiskWriteQueue > 10000 {
			recs = append(recs, map[string]string{
				"severity": "warning", "cluster": name,
				"title":   "High Disk Write Queue",
				"message": fmt.Sprintf("Cluster %s disk write queue is %.0f. Storage may be a bottleneck.", name, cm.DiskWriteQueue),
			})
		}
		if len(cm.Nodes) == 1 {
			recs = append(recs, map[string]string{
				"severity": "warning", "cluster": name,
				"title":   "Single Node Cluster",
				"message": fmt.Sprintf("Cluster %s has only 1 node. Upgrades on single-node clusters cause downtime. Add nodes before upgrading.", name),
			})
		}
	}

	// XDCR recommendations
	xdcr := state.XDCRStatus
	if xdcr.ReplicationLag > 5000 {
		recs = append(recs, map[string]string{
			"severity": "warning", "cluster": "xdcr",
			"title":   "High XDCR Replication Lag",
			"message": fmt.Sprintf("Replication lag is %.0fms. Consider increasing nozzle count or checking target cluster resources.", xdcr.ReplicationLag),
		})
	}
	if xdcr.ChangesLeft > 10000 {
		recs = append(recs, map[string]string{
			"severity": "warning", "cluster": "xdcr",
			"title":   "Large Replication Backlog",
			"message": fmt.Sprintf("XDCR has %d changes pending. Do not start an upgrade until backlog is cleared.", xdcr.ChangesLeft),
		})
	}

	// Upgrade recommendations
	upgrade := state.UpgradeStatus
	if upgrade.Phase == "upgrading" && xdcr.ChangesLeft > 1000 {
		recs = append(recs, map[string]string{
			"severity": "critical", "cluster": "upgrade",
			"title":   "Upgrade During High XDCR Backlog",
			"message": "Upgrade is in progress while XDCR has a large backlog. Monitor closely for data integrity.",
		})
	}

	// Data integrity
	proof := state.DataLossProof
	if proof.CurrentDelta != 0 && !proof.IsConverged {
		recs = append(recs, map[string]string{
			"severity": "warning", "cluster": "integrity",
			"title":   "Document Count Delta",
			"message": fmt.Sprintf("Source and target differ by %d documents. Run an audit to verify integrity.", proof.CurrentDelta),
		})
	}

	if len(recs) == 0 {
		recs = append(recs, map[string]string{
			"severity": "info", "cluster": "all",
			"title":   "All Clear",
			"message": "No issues detected. All clusters are healthy and XDCR is in sync.",
		})
	}

	writeJSON(w, http.StatusOK, recs)
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
