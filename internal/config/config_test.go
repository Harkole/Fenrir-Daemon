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
}

func TestConfigCustomEnv(t *testing.T) {
	os.Setenv("FENRIR_PORT", "9090")
	os.Setenv("DOCKER_HOST", "tcp://192.168.1.100:2375")
	os.Setenv("FENRIR_METRICS_CACHE_TTL_SEC", "10")
	os.Setenv("FENRIR_GAME_KEYWORDS", "minecraft,palworld")
	defer func() {
		os.Unsetenv("FENRIR_PORT")
		os.Unsetenv("DOCKER_HOST")
		os.Unsetenv("FENRIR_METRICS_CACHE_TTL_SEC")
		os.Unsetenv("FENRIR_GAME_KEYWORDS")
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
}
