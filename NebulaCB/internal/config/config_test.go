package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Server.Host != "0.0.0.0" {
		t.Errorf("expected host 0.0.0.0, got %s", cfg.Server.Host)
	}
	if cfg.Server.Port != 8080 {
		t.Errorf("expected port 8080, got %d", cfg.Server.Port)
	}
	if cfg.Server.Auth.Username != "admin" {
		t.Errorf("expected auth username admin, got %s", cfg.Server.Auth.Username)
	}
	if cfg.Server.Auth.Password != "nebulacb" {
		t.Errorf("expected auth password nebulacb, got %s", cfg.Server.Auth.Password)
	}
	if !cfg.Server.Auth.Enabled {
		t.Error("expected auth to be enabled by default")
	}
	if cfg.Source.Host != "localhost:8091" {
		t.Errorf("expected source host localhost:8091, got %s", cfg.Source.Host)
	}
	if cfg.Target.Host != "localhost:9091" {
		t.Errorf("expected target host localhost:9091, got %s", cfg.Target.Host)
	}
	if cfg.Storm.WritesPerSecond != 1000 {
		t.Errorf("expected 1000 writes/sec, got %d", cfg.Storm.WritesPerSecond)
	}
	if cfg.Storm.Workers != 16 {
		t.Errorf("expected 16 workers, got %d", cfg.Storm.Workers)
	}
	if cfg.Metrics.Port != 9090 {
		t.Errorf("expected metrics port 9090, got %d", cfg.Metrics.Port)
	}
}

func TestLoadFromFile(t *testing.T) {
	content := `{
		"server": {
			"host": "127.0.0.1",
			"port": 9999,
			"auth": {"enabled": false, "username": "testuser", "password": "testpass"}
		},
		"source": {"host": "cb1:8091", "bucket": "mybucket"},
		"target": {"host": "cb2:8091", "bucket": "mybucket2"}
	}`

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadFromFile(configPath)
	if err != nil {
		t.Fatalf("LoadFromFile failed: %v", err)
	}

	if cfg.Server.Host != "127.0.0.1" {
		t.Errorf("expected host 127.0.0.1, got %s", cfg.Server.Host)
	}
	if cfg.Server.Port != 9999 {
		t.Errorf("expected port 9999, got %d", cfg.Server.Port)
	}
	if cfg.Server.Auth.Enabled {
		t.Error("expected auth to be disabled")
	}
	if cfg.Source.Host != "cb1:8091" {
		t.Errorf("expected source host cb1:8091, got %s", cfg.Source.Host)
	}
	if cfg.Target.Bucket != "mybucket2" {
		t.Errorf("expected target bucket mybucket2, got %s", cfg.Target.Bucket)
	}
}

func TestLoadFromFile_NotFound(t *testing.T) {
	_, err := LoadFromFile("/nonexistent/path/config.json")
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestLoadFromFile_InvalidJSON(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "bad.json")
	os.WriteFile(configPath, []byte("{invalid json}"), 0644)

	_, err := LoadFromFile(configPath)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestGetClusters(t *testing.T) {
	cfg := DefaultConfig()
	clusters := cfg.GetClusters()

	if len(clusters) != 2 {
		t.Errorf("expected 2 clusters, got %d", len(clusters))
	}

	source, ok := clusters["source"]
	if !ok {
		t.Fatal("expected 'source' cluster")
	}
	if source.Role != "source" {
		t.Errorf("expected source role, got %s", source.Role)
	}

	target, ok := clusters["target"]
	if !ok {
		t.Fatal("expected 'target' cluster")
	}
	if target.Role != "target" {
		t.Errorf("expected target role, got %s", target.Role)
	}
}

func TestEnvOverrides(t *testing.T) {
	content := `{"server": {"auth": {"enabled": true, "username": "orig", "password": "orig"}}}`

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	os.WriteFile(configPath, []byte(content), 0644)

	t.Setenv("NEBULACB_AUTH_USERNAME", "envuser")
	t.Setenv("NEBULACB_AUTH_PASSWORD", "envpass")
	t.Setenv("NEBULACB_AUTH_ENABLED", "false")
	t.Setenv("NEBULACB_DOMAIN", "example.com")
	t.Setenv("NEBULACB_SOURCE_HOST", "env-source:8091")

	cfg, err := LoadFromFile(configPath)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Server.Auth.Username != "envuser" {
		t.Errorf("expected envuser, got %s", cfg.Server.Auth.Username)
	}
	if cfg.Server.Auth.Password != "envpass" {
		t.Errorf("expected envpass, got %s", cfg.Server.Auth.Password)
	}
	if cfg.Server.Auth.Enabled {
		t.Error("expected auth disabled via env")
	}
	if cfg.Server.Domain != "example.com" {
		t.Errorf("expected domain example.com, got %s", cfg.Server.Domain)
	}
	if cfg.Source.Host != "env-source:8091" {
		t.Errorf("expected source host env-source:8091, got %s", cfg.Source.Host)
	}
}

func TestClusterEnvVars(t *testing.T) {
	content := `{}`

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	os.WriteFile(configPath, []byte(content), 0644)

	t.Setenv("NEBULACB_CLUSTER_DC1_HOST", "dc1.example.com:8091")
	t.Setenv("NEBULACB_CLUSTER_DC1_USERNAME", "admin")
	t.Setenv("NEBULACB_CLUSTER_DC1_PASSWORD", "secret")
	t.Setenv("NEBULACB_CLUSTER_DC1_BUCKET", "travel")
	t.Setenv("NEBULACB_CLUSTER_DC1_ROLE", "source")

	cfg, err := LoadFromFile(configPath)
	if err != nil {
		t.Fatal(err)
	}

	dc1, ok := cfg.Clusters["dc1"]
	if !ok {
		t.Fatal("expected dc1 cluster from env vars")
	}
	if dc1.Host != "dc1.example.com:8091" {
		t.Errorf("expected dc1 host, got %s", dc1.Host)
	}
	if dc1.Username != "admin" {
		t.Errorf("expected dc1 username admin, got %s", dc1.Username)
	}
	if dc1.Bucket != "travel" {
		t.Errorf("expected dc1 bucket travel, got %s", dc1.Bucket)
	}
	if dc1.Role != "source" {
		t.Errorf("expected dc1 role source, got %s", dc1.Role)
	}
}
