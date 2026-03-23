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

	"github.com/balinderwalia/nebulacb/internal/config"
	"github.com/balinderwalia/nebulacb/internal/metrics"
	"github.com/balinderwalia/nebulacb/internal/models"
	"github.com/balinderwalia/nebulacb/internal/orchestrator"
	"github.com/balinderwalia/nebulacb/internal/reporting"
	"github.com/balinderwalia/nebulacb/internal/storm"
	"github.com/balinderwalia/nebulacb/internal/validator"
	"github.com/balinderwalia/nebulacb/internal/xdcr"
	"github.com/balinderwalia/nebulacb/pkg/couchbase"
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
) *Server {
	return &Server{
		config:       cfg,
		collector:    collector,
		hub:          hub,
		pool:         pool,
		storm:        stormGen,
		orchestrator: orch,
		xdcrEngine:   xdcrEng,
		validator:    val,
		reporter:     reporter,
		sessions:     newSessionStore(),
		startTime:    time.Now(),
	}
}

// Start begins the HTTP/WebSocket server and dashboard broadcast loop.
func (s *Server) Start(ctx context.Context) error {
	mux := http.NewServeMux()

	// Public endpoints (no auth required)
	mux.HandleFunc("/api/v1/health", s.handleHealth)
	mux.HandleFunc("/api/v1/login", s.handleLogin)
	mux.HandleFunc("/api/v1/auth/check", s.handleAuthCheck)

	// Protected API routes
	mux.HandleFunc("/api/v1/dashboard", s.requireAuth(s.handleDashboard))
	mux.HandleFunc("/api/v1/command", s.requireAuth(s.handleCommand))
	mux.HandleFunc("/api/v1/alerts", s.requireAuth(s.handleAlerts))
	mux.HandleFunc("/api/v1/reports", s.requireAuth(s.handleReports))
	mux.HandleFunc("/api/v1/config", s.requireAuth(s.handleConfig))
	mux.HandleFunc("/api/v1/clusters", s.requireAuth(s.handleClusters))
	mux.HandleFunc("/api/v1/logout", s.requireAuth(s.handleLogout))

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
// Accepts: Basic auth header OR Bearer token from /api/v1/login.
func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.config.Server.Auth.Enabled {
			next(w, r)
			return
		}

		// Try Bearer token first
		if token := extractBearerToken(r); token != "" {
			if s.sessions.valid(token) {
				next(w, r)
				return
			}
		}

		// Try HTTP Basic Auth
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
		// WS auth: check ?token= query param or Basic auth header
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

	// Evict sessions older than 24h
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
			Connected: connSet[name],
		})
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"clusters": clusters,
		"total":    len(clusters),
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
			s.hub.Broadcast("dashboard_update", state)
		}
	}
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	state := s.collector.GetDashboardState()
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
	// Redact passwords before sending
	safe := *s.config
	safe.Source.Password = "***"
	safe.Target.Password = "***"
	safe.Server.Auth.Password = "***"
	writeJSON(w, http.StatusOK, safe)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":             "ok",
		"version":            "0.1.0",
		"uptime":             time.Since(s.startTime).Round(time.Second).String(),
		"ws_clients":         s.hub.ClientCount(),
		"auth":               s.config.Server.Auth.Enabled,
		"domain":             s.config.Server.Domain,
		"tls":                s.config.Server.TLS.Enabled,
		"clusters_configured": len(s.pool.Configs()),
		"clusters_connected":  len(s.pool.Connected()),
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
		if err := s.orchestrator.Start(ctx); err != nil {
			return map[string]string{"status": "error", "message": err.Error()}
		}
		return map[string]string{"status": "ok", "message": "Upgrade started"}

	case "abort_upgrade":
		s.orchestrator.Abort()
		return map[string]string{"status": "ok", "message": "Upgrade aborted"}

	case "restart_xdcr":
		if err := s.xdcrEngine.RestartPipeline(ctx); err != nil {
			return map[string]string{"status": "error", "message": err.Error()}
		}
		return map[string]string{"status": "ok", "message": "XDCR pipeline restarted"}

	case "run_audit":
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

	default:
		return map[string]string{"status": "error", "message": "unknown command: " + cmd.Action}
	}
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
