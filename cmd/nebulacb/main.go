package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/balinderwalia/nebulacb/internal/api"
	"github.com/balinderwalia/nebulacb/internal/config"
	"github.com/balinderwalia/nebulacb/internal/metrics"
	"github.com/balinderwalia/nebulacb/internal/monitor"
	"github.com/balinderwalia/nebulacb/internal/orchestrator"
	"github.com/balinderwalia/nebulacb/internal/reporting"
	"github.com/balinderwalia/nebulacb/internal/storm"
	"github.com/balinderwalia/nebulacb/internal/validator"
	"github.com/balinderwalia/nebulacb/internal/xdcr"
	"github.com/balinderwalia/nebulacb/pkg/couchbase"
	"github.com/balinderwalia/nebulacb/pkg/kubernetes"
	ws "github.com/balinderwalia/nebulacb/pkg/websocket"
)

func main() {
	configFile := flag.String("config", "config.json", "Path to configuration file")
	flag.Parse()

	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)
	log.Println("========================================")
	log.Println("  NebulaCB — Mission Control")
	log.Println("  Upgrade Fearlessly. Validate Everything.")
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
	log.Printf("[Main] Cluster registry: %d clusters", len(allClusters))
	for name, cc := range allClusters {
		log.Printf("[Main]   %s → %s (role=%s bucket=%s)", name, cc.Host, cc.Role, cc.Bucket)
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
	k8sClient := kubernetes.NewK8sClient(cfg.Upgrade.Namespace, "")

	// Initialize components
	stormGen := storm.NewGenerator(cfg.Storm, sourceClient, collector)
	orch := orchestrator.NewOrchestrator(cfg.Upgrade, k8sClient, collector)
	xdcrEngine := xdcr.NewEngine(cfg.XDCR, collector)
	val := validator.NewValidator(cfg.Validator, sourceClient, targetClient, collector, cfg.Storm.KeyPrefix)
	reporter := reporting.NewEngine(collector, "reports")

	// Multi-cluster monitor (polls all clusters every 2s)
	clusterMonitor := monitor.NewMonitor(allClusters, collector, 2*time.Second)
	clusterMonitor.Start(ctx)

	// WebSocket hub
	hub := ws.NewHub()

	// API server
	server := api.NewServer(cfg, collector, hub, stormGen, orch, xdcrEngine, val, reporter, pool)

	if err := server.Start(ctx); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}

	log.Printf("NebulaCB is ready at http://%s:%d", cfg.Server.Host, cfg.Server.Port)
	log.Printf("Dashboard: http://localhost:%d", cfg.Server.Port)
	log.Printf("API: http://localhost:%d/api/v1/clusters", cfg.Server.Port)

	// Wait for shutdown signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Println("Shutting down NebulaCB...")
	clusterMonitor.Stop()
	stormGen.Stop()
	xdcrEngine.Stop()
	val.Stop()
	server.Stop()
	pool.Close()
	log.Println("NebulaCB stopped. Goodbye.")
}
