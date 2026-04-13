package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/balinderwalia/nebulacb/internal/ai"
	"github.com/balinderwalia/nebulacb/internal/api"
	"github.com/balinderwalia/nebulacb/internal/backup"
	"github.com/balinderwalia/nebulacb/internal/models"
	"github.com/balinderwalia/nebulacb/internal/config"
	"github.com/balinderwalia/nebulacb/internal/failover"
	"github.com/balinderwalia/nebulacb/internal/metrics"
	"github.com/balinderwalia/nebulacb/internal/migration"
	"github.com/balinderwalia/nebulacb/internal/monitor"
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

func main() {
	configFile := flag.String("config", "config.json", "Path to configuration file")
	flag.Parse()

	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)
	log.Println("========================================")
	log.Println("  NebulaCB — Mission Control v1.0")
	log.Println("  The Complete Couchbase Management Platform")
	log.Println("  Upgrade · Migrate · Monitor · Protect")
	log.Println("========================================")

	// Load configuration
	cfg, err := config.LoadFromFile(*configFile)
	if err != nil {
		log.Printf("Config file not found (%s), using defaults", *configFile)
		cfg = config.DefaultConfig()
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Initialize metrics collector
	collector := metrics.NewCollector()

	// Build the cluster registry (source/target + env vars + config file clusters)
	allClusters := cfg.GetClusters()

	// Auto port-forward for Kubernetes-hosted clusters
	var pfManager *kubernetes.PortForwardManager
	kubeconfig := cfg.Kubeconfig
	if kubeconfig == "" {
		kubeconfig = cfg.Upgrade.Kubeconfig
	}
	if kubeconfig != "" {
		pfManager = kubernetes.NewPortForwardManager(kubeconfig)
		allClusters = pfManager.SetupClusters(ctx, allClusters)
		// Start health check to auto-reconnect dead port-forwards every 30s
		pfManager.StartHealthCheck(ctx, allClusters, 30*time.Second)
	}

	log.Printf("[Main] Cluster registry: %d clusters", len(allClusters))
	for name, cc := range allClusters {
		log.Printf("[Main]   %s → %s (role=%s bucket=%s region=%s platform=%s edition=%s)",
			name, cc.Host, cc.Role, cc.Bucket, cc.Region, cc.Platform, cc.Edition)
	}

	// Connect to all clusters via ClientPool
	pool := couchbase.NewClientPool(allClusters)
	defer pool.Close()

	// Get source/target clients for storm/validator (backward compat)
	sourceClient := pool.GetByRole("source")
	if sourceClient == nil {
		sourceClient = pool.Get("source")
	}
	targetClient := pool.GetByRole("target")
	if targetClient == nil {
		targetClient = pool.Get("target")
	}

	// Kubernetes client
	k8sClient := kubernetes.NewK8sClient(cfg.Upgrade.Namespace, cfg.Upgrade.Kubeconfig)

	// ─── Core Components ─────────────────────────────────────────────────────
	stormGen := storm.NewGenerator(cfg.Storm, sourceClient, collector)
	// If SDK client is not available, set up REST fallback for storm generator
	if sourceClient == nil {
		// Prefer port-forwarded addresses from allClusters over raw config
		var sourceCfg models.ClusterConfig
		for _, cc := range allClusters {
			if cc.Role == "source" {
				sourceCfg = cc
				break
			}
		}
		if sourceCfg.Host == "" {
			sourceCfg = cfg.Source
		}
		if sourceCfg.Host != "" {
			restClient := couchbase.NewRESTClient(sourceCfg)
			stormGen.SetRESTClient(restClient)
			log.Printf("[Main] REST fallback configured for storm generator (%s)", sourceCfg.Host)
		}
	}
	orch := orchestrator.NewOrchestrator(cfg.Upgrade, k8sClient, collector)

	// Populate XDCR config with resolved cluster addresses (including port-forwards)
	xdcrCfg := cfg.XDCR
	for _, cc := range allClusters {
		if cc.Role == "source" {
			xdcrCfg.SourceCluster = cc
		}
		if cc.Role == "target" {
			xdcrCfg.TargetCluster = cc
		}
	}
	if xdcrCfg.SourceCluster.Host == "" {
		xdcrCfg.SourceCluster = cfg.Source
	}
	if xdcrCfg.TargetCluster.Host == "" {
		xdcrCfg.TargetCluster = cfg.Target
	}
	xdcrEngine := xdcr.NewEngine(xdcrCfg, collector)
	val := validator.NewValidator(cfg.Validator, sourceClient, targetClient, collector, cfg.Storm.KeyPrefix)
	// Set up REST fallback for validator when SDK is not available
	if sourceClient == nil || targetClient == nil {
		var srcCfg, tgtCfg models.ClusterConfig
		for _, cc := range allClusters {
			if cc.Role == "source" {
				srcCfg = cc
			}
			if cc.Role == "target" {
				tgtCfg = cc
			}
		}
		if srcCfg.Host == "" {
			srcCfg = cfg.Source
		}
		if tgtCfg.Host == "" {
			tgtCfg = cfg.Target
		}
		if srcCfg.Host != "" && tgtCfg.Host != "" {
			val.SetRESTClients(couchbase.NewRESTClient(srcCfg), couchbase.NewRESTClient(tgtCfg))
			log.Println("[Main] REST fallback configured for validator")
		}
	}
	reporter := reporting.NewEngine(collector, "reports")

	// Multi-cluster monitor (polls all clusters every 2s)
	clusterMonitor := monitor.NewMonitor(allClusters, collector, 2*time.Second)
	clusterMonitor.Start(ctx)

	// ─── New Components ──────────────────────────────────────────────────────

	// AI Analyzer
	var aiAnalyzer *ai.Analyzer
	if cfg.AI.Enabled {
		aiAnalyzer = ai.NewAnalyzer(cfg.AI, collector)
		log.Printf("[Main] AI Analysis enabled (provider=%s model=%s)", cfg.AI.Provider, cfg.AI.Model)
	} else {
		log.Println("[Main] AI Analysis disabled (set ai.enabled=true and ai.api_key to enable)")
		aiAnalyzer = ai.NewAnalyzer(cfg.AI, collector) // Create anyway for API availability
	}

	// Backup Manager
	var backupMgr *backup.Manager
	backupMgr = backup.NewManager(cfg.Backup, allClusters, collector)
	if cfg.Backup.Enabled {
		log.Printf("[Main] Backup management enabled (schedule=%s repo=%s)", cfg.Backup.Schedule, cfg.Backup.Repository)
	}

	// Failover Manager
	var failoverMgr *failover.Manager
	failoverMgr = failover.NewManager(cfg.Failover, allClusters, collector)
	failoverMgr.Start(ctx)
	if cfg.Failover.Enabled {
		log.Printf("[Main] HA/Failover enabled (auto=%v timeout=%s)", cfg.Failover.AutoFailover, cfg.Failover.FailoverTimeout)
	}

	// Migration Engine
	var migrationEngine *migration.Engine
	migrationEngine = migration.NewEngine(cfg.Migration, pool, collector)
	log.Printf("[Main] Migration engine ready (workers=%d batch=%d)", cfg.Migration.Workers, cfg.Migration.BatchSize)

	// Region Manager
	var regionMgr *region.Manager
	regionMgr = region.NewManager(cfg.Regions, allClusters, collector)
	regionMgr.Start(ctx)

	// Docker Client
	var dockerClient *docker.Client
	if cfg.Docker.Enabled {
		dockerClient = docker.NewClient(cfg.Docker)
		log.Printf("[Main] Docker management enabled (host=%s network=%s)", cfg.Docker.Host, cfg.Docker.Network)
	}

	// ─── Server ──────────────────────────────────────────────────────────────

	// WebSocket hub
	hub := ws.NewHub()

	// API server
	server := api.NewServer(
		cfg, collector, hub, stormGen, orch, xdcrEngine, val, reporter, pool,
		aiAnalyzer, backupMgr, failoverMgr, migrationEngine, regionMgr, dockerClient, k8sClient,
	)

	if err := server.Start(ctx); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}

	// ─── Print Feature Summary ───────────────────────────────────────────────

	log.Println("────────────────────────────────────────")
	log.Printf("  NebulaCB v1.0 is ready!")
	log.Printf("  Dashboard: http://localhost:%d", cfg.Server.Port)
	log.Printf("  API:       http://localhost:%d/api/v1", cfg.Server.Port)
	log.Println("────────────────────────────────────────")
	log.Println("  Features:")
	log.Println("    ✓ Multi-Cluster Monitoring")
	log.Println("    ✓ Rolling Upgrade Orchestration")
	log.Println("    ✓ XDCR Replication Management")
	log.Println("    ✓ Data Integrity Validation")
	log.Println("    ✓ Load Testing (Storm Generator)")
	log.Println("    ✓ Data Migration")
	log.Println("    ✓ Backup & Restore")
	if cfg.Failover.Enabled {
		log.Println("    ✓ HA & Automatic Failover")
	} else {
		log.Println("    · HA & Failover (available, disabled)")
	}
	if len(cfg.Regions) > 0 {
		log.Printf("    ✓ Multi-Region Management (%d regions)", len(cfg.Regions))
	} else {
		log.Println("    · Multi-Region (available, no regions configured)")
	}
	if cfg.AI.Enabled {
		log.Printf("    ✓ AI Analysis (%s/%s)", cfg.AI.Provider, cfg.AI.Model)
	} else {
		log.Println("    · AI Analysis (available, disabled)")
	}
	if cfg.Docker.Enabled {
		log.Println("    ✓ Docker Management")
	} else {
		log.Println("    · Docker Management (available, disabled)")
	}
	log.Println("    ✓ Kubernetes Orchestration")
	log.Println("    ✓ Community & Enterprise Edition Support")
	log.Println("────────────────────────────────────────")

	// Wait for shutdown signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Println("Shutting down NebulaCB...")
	clusterMonitor.Stop()
	stormGen.Stop()
	xdcrEngine.Stop()
	val.Stop()
	failoverMgr.Stop()
	regionMgr.Stop()
	server.Stop()
	pool.Close()
	if pfManager != nil {
		pfManager.Stop()
	}
	log.Println("NebulaCB stopped. Goodbye.")
}
