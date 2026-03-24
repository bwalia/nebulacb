package config

import (
	"encoding/json"
	"log"
	"os"
	"strings"
	"time"

	"github.com/balinderwalia/nebulacb/internal/models"
)

// Config is the top-level NebulaCB configuration.
type Config struct {
	Server    ServerConfig        `json:"server" yaml:"server"`
	// Multi-cluster: all clusters keyed by name. Built from Source/Target + env vars.
	Clusters  map[string]models.ClusterConfig `json:"clusters,omitempty" yaml:"clusters,omitempty"`
	// Legacy source/target fields (still supported, merged into Clusters).
	Source    models.ClusterConfig `json:"source" yaml:"source"`
	Target    models.ClusterConfig `json:"target" yaml:"target"`
	Storm     models.StormConfig   `json:"storm" yaml:"storm"`
	Upgrade   models.UpgradeConfig `json:"upgrade" yaml:"upgrade"`
	XDCR      models.XDCRConfig    `json:"xdcr" yaml:"xdcr"`
	Validator models.ValidatorConfig `json:"validator" yaml:"validator"`
	Metrics   MetricsConfig        `json:"metrics" yaml:"metrics"`
	// New: multi-region, HA, backup, migration, AI, Docker
	Regions   []models.RegionConfig   `json:"regions,omitempty" yaml:"regions,omitempty"`
	Failover  models.FailoverConfig   `json:"failover,omitempty" yaml:"failover,omitempty"`
	Backup    models.BackupConfig     `json:"backup,omitempty" yaml:"backup,omitempty"`
	Migration models.MigrationConfig  `json:"migration,omitempty" yaml:"migration,omitempty"`
	AI        models.AIConfig         `json:"ai,omitempty" yaml:"ai,omitempty"`
	Docker    models.DockerConfig     `json:"docker,omitempty" yaml:"docker,omitempty"`
}

// GetClusters returns the full cluster map (source/target + any additional clusters).
// This is the canonical way to get all connected clusters.
func (c *Config) GetClusters() map[string]models.ClusterConfig {
	result := make(map[string]models.ClusterConfig)
	// Copy explicit clusters map first
	for k, v := range c.Clusters {
		result[k] = v
	}
	// Source/Target always present (may be overridden by Clusters map)
	if c.Source.Host != "" {
		s := c.Source
		if s.Name == "" {
			s.Name = "source"
		}
		if s.Role == "" {
			s.Role = "source"
		}
		if _, exists := result[s.Name]; !exists {
			result[s.Name] = s
		}
	}
	if c.Target.Host != "" {
		t := c.Target
		if t.Name == "" {
			t.Name = "target"
		}
		if t.Role == "" {
			t.Role = "target"
		}
		if _, exists := result[t.Name]; !exists {
			result[t.Name] = t
		}
	}
	return result
}

// ServerConfig configures the API server.
type ServerConfig struct {
	Host   string     `json:"host" yaml:"host"`
	Port   int        `json:"port" yaml:"port"`
	Domain string     `json:"domain,omitempty" yaml:"domain,omitempty"`
	TLS    TLSConfig  `json:"tls,omitempty" yaml:"tls,omitempty"`
	Auth   AuthConfig `json:"auth" yaml:"auth"`
}

// AuthConfig configures basic authentication.
type AuthConfig struct {
	Enabled  bool   `json:"enabled" yaml:"enabled"`
	Username string `json:"username" yaml:"username"`
	Password string `json:"password" yaml:"password"`
}

// TLSConfig configures TLS termination.
type TLSConfig struct {
	Enabled  bool   `json:"enabled" yaml:"enabled"`
	CertFile string `json:"cert_file,omitempty" yaml:"cert_file,omitempty"`
	KeyFile  string `json:"key_file,omitempty" yaml:"key_file,omitempty"`
}

// MetricsConfig configures Prometheus metrics export.
type MetricsConfig struct {
	Enabled  bool   `json:"enabled" yaml:"enabled"`
	Endpoint string `json:"endpoint" yaml:"endpoint"`
	Port     int    `json:"port" yaml:"port"`
}

// DefaultConfig returns a configuration with sensible defaults.
func DefaultConfig() *Config {
	return &Config{
		Server: ServerConfig{
			Host: "0.0.0.0",
			Port: 8080,
			Auth: AuthConfig{
				Enabled:  true,
				Username: "admin",
				Password: "nebulacb",
			},
		},
		Source: models.ClusterConfig{
			Name:   "source",
			Host:   "localhost:8091",
			Bucket: "default",
		},
		Target: models.ClusterConfig{
			Name:   "target",
			Host:   "localhost:9091",
			Bucket: "default",
		},
		Storm: models.StormConfig{
			WritesPerSecond:  1000,
			ReadsPerSecond:   500,
			DocSizeMin:       1024,
			DocSizeMax:       10240,
			Workers:          16,
			Region:           "us-east-1",
			BurstEnabled:     false,
			BurstMultiplier:  3.0,
			BurstInterval:    30 * time.Second,
			BurstDuration:    10 * time.Second,
			HotKeyPercentage: 0.2,
			DeletePercentage: 0.05,
			UpdatePercentage: 0.30,
			KeyPrefix:        "nebula",
		},
		Upgrade: models.UpgradeConfig{
			SourceVersion:  "7.2.2",
			TargetVersion:  "7.6.0",
			Namespace:      "couchbase",
			ClusterName:    "cb-cluster",
			RollingDelay:   60 * time.Second,
			MaxUnavailable: 1,
		},
		XDCR: models.XDCRConfig{
			PollInterval:     5 * time.Second,
			GoxdcrDelayAlert: 5 * time.Minute,
		},
		Validator: models.ValidatorConfig{
			BatchSize:     10000,
			ScanInterval:  30 * time.Second,
			HashCheck:     true,
			SequenceCheck: true,
			KeySampling:   1.0,
		},
		Metrics: MetricsConfig{
			Enabled:  true,
			Endpoint: "/metrics",
			Port:     9090,
		},
		Failover: models.FailoverConfig{
			Enabled:             false,
			AutoFailover:        false,
			FailoverTimeout:     120 * time.Second,
			MaxAutoFailovers:    1,
			HealthCheckInterval: 10 * time.Second,
			RecoveryMode:        "manual",
			PreserveData:        true,
		},
		Backup: models.BackupConfig{
			Enabled:       false,
			Schedule:      "0 2 * * *",
			Repository:    "/tmp/nebulacb-backups",
			RetentionDays: 7,
			Compression:   true,
			Encryption:    false,
			Timeout:       30 * time.Minute,
		},
		Migration: models.MigrationConfig{
			Workers:       8,
			BatchSize:     1000,
			Timeout:       24 * time.Hour,
			RetryAttempts: 3,
			ValidateAfter: true,
		},
		AI: models.AIConfig{
			Enabled:     false,
			Provider:    "anthropic",
			Model:       "claude-sonnet-4-20250514",
			MaxTokens:   4096,
			AutoAnalyze: false,
		},
		Docker: models.DockerConfig{
			Enabled: false,
			Host:    "unix:///var/run/docker.sock",
			Network: "nebulacb-net",
		},
	}
}

// LoadFromFile loads config from a JSON file, overlaying on defaults.
// Environment variables override file values:
//   NEBULACB_AUTH_USERNAME, NEBULACB_AUTH_PASSWORD, NEBULACB_AUTH_ENABLED
//   NEBULACB_DOMAIN, NEBULACB_TLS_CERT, NEBULACB_TLS_KEY
//   NEBULACB_AI_PROVIDER, NEBULACB_AI_API_KEY, NEBULACB_AI_MODEL
func LoadFromFile(path string) (*Config, error) {
	cfg := DefaultConfig()

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	if err := json.Unmarshal(data, cfg); err != nil {
		return nil, err
	}

	applyEnvOverrides(cfg)
	return cfg, nil
}

func applyEnvOverrides(cfg *Config) {
	// Auth overrides
	if v := os.Getenv("NEBULACB_AUTH_USERNAME"); v != "" {
		cfg.Server.Auth.Username = v
	}
	if v := os.Getenv("NEBULACB_AUTH_PASSWORD"); v != "" {
		cfg.Server.Auth.Password = v
	}
	if v := os.Getenv("NEBULACB_AUTH_ENABLED"); v == "false" {
		cfg.Server.Auth.Enabled = false
	}
	if v := os.Getenv("NEBULACB_DOMAIN"); v != "" {
		cfg.Server.Domain = v
	}
	if v := os.Getenv("NEBULACB_TLS_CERT"); v != "" {
		cfg.Server.TLS.Enabled = true
		cfg.Server.TLS.CertFile = v
	}
	if v := os.Getenv("NEBULACB_TLS_KEY"); v != "" {
		cfg.Server.TLS.KeyFile = v
	}

	// Legacy source/target env overrides
	if v := os.Getenv("NEBULACB_SOURCE_HOST"); v != "" {
		cfg.Source.Host = v
	}
	if v := os.Getenv("NEBULACB_SOURCE_USERNAME"); v != "" {
		cfg.Source.Username = v
	}
	if v := os.Getenv("NEBULACB_SOURCE_PASSWORD"); v != "" {
		cfg.Source.Password = v
	}
	if v := os.Getenv("NEBULACB_SOURCE_BUCKET"); v != "" {
		cfg.Source.Bucket = v
	}
	if v := os.Getenv("NEBULACB_TARGET_HOST"); v != "" {
		cfg.Target.Host = v
	}
	if v := os.Getenv("NEBULACB_TARGET_USERNAME"); v != "" {
		cfg.Target.Username = v
	}
	if v := os.Getenv("NEBULACB_TARGET_PASSWORD"); v != "" {
		cfg.Target.Password = v
	}
	if v := os.Getenv("NEBULACB_TARGET_BUCKET"); v != "" {
		cfg.Target.Bucket = v
	}

	// AI env overrides
	if v := os.Getenv("NEBULACB_AI_PROVIDER"); v != "" {
		cfg.AI.Enabled = true
		cfg.AI.Provider = v
	}
	if v := os.Getenv("NEBULACB_AI_API_KEY"); v != "" {
		cfg.AI.APIKey = v
		cfg.AI.Enabled = true
	}
	if v := os.Getenv("NEBULACB_AI_MODEL"); v != "" {
		cfg.AI.Model = v
	}
	if v := os.Getenv("NEBULACB_AI_ENDPOINT"); v != "" {
		cfg.AI.APIEndpoint = v
	}

	// Docker env overrides
	if v := os.Getenv("NEBULACB_DOCKER_HOST"); v != "" {
		cfg.Docker.Enabled = true
		cfg.Docker.Host = v
	}

	// Multi-cluster env vars: NEBULACB_CLUSTER_<NAME>_<FIELD>
	parseClusterEnvVars(cfg)
}

// parseClusterEnvVars scans all env vars for NEBULACB_CLUSTER_<NAME>_<FIELD>
// and builds/merges them into cfg.Clusters.
func parseClusterEnvVars(cfg *Config) {
	if cfg.Clusters == nil {
		cfg.Clusters = make(map[string]models.ClusterConfig)
	}

	// Collect all cluster names from env vars
	clusterFields := make(map[string]map[string]string) // name -> field -> value
	const prefix = "NEBULACB_CLUSTER_"

	for _, env := range os.Environ() {
		if !strings.HasPrefix(env, prefix) {
			continue
		}
		kv := strings.SplitN(env, "=", 2)
		if len(kv) != 2 {
			continue
		}
		key := kv[0][len(prefix):] // e.g. "DC1_HOST"
		val := kv[1]

		// Split into cluster name and field: "DC1_HOST" -> "dc1", "HOST"
		parts := strings.SplitN(key, "_", 2)
		if len(parts) != 2 {
			continue
		}
		clusterName := strings.ToLower(parts[0])
		field := strings.ToUpper(parts[1])

		if clusterFields[clusterName] == nil {
			clusterFields[clusterName] = make(map[string]string)
		}
		clusterFields[clusterName][field] = val
	}

	for name, fields := range clusterFields {
		cc, exists := cfg.Clusters[name]
		if !exists {
			cc = models.ClusterConfig{Name: name}
		}

		if v, ok := fields["HOST"]; ok {
			cc.Host = v
		}
		if v, ok := fields["USERNAME"]; ok {
			cc.Username = v
		}
		if v, ok := fields["PASSWORD"]; ok {
			cc.Password = v
		}
		if v, ok := fields["BUCKET"]; ok {
			cc.Bucket = v
		}
		if v, ok := fields["SCOPE"]; ok {
			cc.Scope = v
		}
		if v, ok := fields["COLLECTION"]; ok {
			cc.Collection = v
		}
		if v, ok := fields["ROLE"]; ok {
			cc.Role = strings.ToLower(v)
		}
		if v, ok := fields["REGION"]; ok {
			cc.Region = v
		}
		if v, ok := fields["ZONE"]; ok {
			cc.Zone = v
		}
		if v, ok := fields["EDITION"]; ok {
			cc.Edition = strings.ToLower(v)
		}
		if v, ok := fields["PLATFORM"]; ok {
			cc.Platform = strings.ToLower(v)
		}
		if cc.Name == "" {
			cc.Name = name
		}
		if cc.Bucket == "" {
			cc.Bucket = "default"
		}

		cfg.Clusters[name] = cc
		log.Printf("[Config] Cluster from env: %s (host=%s role=%s bucket=%s region=%s platform=%s)", name, cc.Host, cc.Role, cc.Bucket, cc.Region, cc.Platform)
	}
}
