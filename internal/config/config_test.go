package config

import (
	"os"
	"testing"
	"time"
)

func TestConfigDefaults(t *testing.T) {
	os.Unsetenv("FENRIR_PORT")
	os.Unsetenv("DOCKER_HOST")
	os.Unsetenv("FENRIR_METRICS_CACHE_TTL_SEC")
	os.Unsetenv("FENRIR_GAME_KEYWORDS")

	cfg := Load()

	if cfg.ServerPort != ":8080" {
		t.Errorf("expected default port :8080, got %s", cfg.ServerPort)
	}
	if cfg.MetricsCacheTTL != 2*time.Second {
		t.Errorf("expected default TTL 2s, got %v", cfg.MetricsCacheTTL)
	}
	if len(cfg.GameFilterKeywords) == 0 {
		t.Errorf("expected default game keywords, got empty slice")
	}
	if cfg.DatabasePath != "fenrir.db" {
		t.Errorf("expected default db path 'fenrir.db', got %s", cfg.DatabasePath)
	}
	if cfg.MetricsCollectionInterval != 30*time.Second {
		t.Errorf("expected default metrics interval 30s, got %v", cfg.MetricsCollectionInterval)
	}
}

func TestConfigCustomEnv(t *testing.T) {
	os.Setenv("FENRIR_PORT", "9090")
	os.Setenv("DOCKER_HOST", "tcp://192.168.1.100:2375")
	os.Setenv("FENRIR_METRICS_CACHE_TTL_SEC", "10")
	os.Setenv("FENRIR_GAME_KEYWORDS", "minecraft,palworld")
	os.Setenv("FENRIR_DB_PATH", "custom.db")
	os.Setenv("FENRIR_API_KEY", "secret-test-key")
	os.Setenv("FENRIR_METRICS_INTERVAL_SEC", "15")
	defer func() {
		os.Unsetenv("FENRIR_PORT")
		os.Unsetenv("DOCKER_HOST")
		os.Unsetenv("FENRIR_METRICS_CACHE_TTL_SEC")
		os.Unsetenv("FENRIR_GAME_KEYWORDS")
		os.Unsetenv("FENRIR_DB_PATH")
		os.Unsetenv("FENRIR_API_KEY")
		os.Unsetenv("FENRIR_METRICS_INTERVAL_SEC")
	}()

	cfg := Load()

	if cfg.ServerPort != ":9090" {
		t.Errorf("expected port :9090, got %s", cfg.ServerPort)
	}
	if cfg.DockerHost != "tcp://192.168.1.100:2375" {
		t.Errorf("expected custom docker host, got %s", cfg.DockerHost)
	}
	if cfg.MetricsCacheTTL != 10*time.Second {
		t.Errorf("expected TTL 10s, got %v", cfg.MetricsCacheTTL)
	}
	if len(cfg.GameFilterKeywords) != 2 || cfg.GameFilterKeywords[0] != "minecraft" || cfg.GameFilterKeywords[1] != "palworld" {
		t.Errorf("expected keywords [minecraft palworld], got %v", cfg.GameFilterKeywords)
	}
	if cfg.DatabasePath != "custom.db" {
		t.Errorf("expected custom db path, got %s", cfg.DatabasePath)
	}
	if cfg.APIKey != "secret-test-key" {
		t.Errorf("expected api key 'secret-test-key', got %s", cfg.APIKey)
	}
	if cfg.MetricsCollectionInterval != 15*time.Second {
		t.Errorf("expected metrics interval 15s, got %v", cfg.MetricsCollectionInterval)
	}
}
