package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// Config represents runtime configuration for the Fenrir Daemon.
type Config struct {
	// ServerPort is the HTTP port the daemon listens on (default: 8080).
	ServerPort string

	// DockerHost is the target Docker daemon URL (e.g. "unix:///var/run/docker.sock" or "tcp://linux-box:2375").
	// If empty, the official Docker client defaults to standard local/environment conventions.
	DockerHost string

	// MetricsCacheTTL is the duration to cache host metrics to avoid frequent scraping overhead.
	MetricsCacheTTL time.Duration

	// GameFilterKeywords are substring matches against image names or container names to flag game servers.
	GameFilterKeywords []string

	// AllowedOrigins is the CORS allowed origins list (default: "*").
	AllowedOrigins []string

	// DatabasePath is the path to the SQLite database file (default: "fenrir.db").
	DatabasePath string

	// APIKey is a pre-shared master key for authenticating incoming API requests.
	APIKey string

	// MetricsCollectionInterval is the frequency to sample and record container metrics history.
	MetricsCollectionInterval time.Duration

	// StacksDir is the isolated root directory where Docker Compose stacks are provisioned (default: "/var/lib/fenrir/stacks/").
	StacksDir string

	// ArchivesDir is the directory where rolling backup archives are stored (default: "/var/lib/fenrir/archives/").
	ArchivesDir string

	// ArchiveRetentionDays is the duration in days to retain stack backup archives before automatic pruning (default: 90).
	ArchiveRetentionDays int
}

// Load reads configuration from environment variables with fallback defaults.
func Load() *Config {
	port := getEnv("FENRIR_PORT", "8080")
	if !strings.HasPrefix(port, ":") && !strings.Contains(port, ":") {
		port = ":" + port
	}

	rawTTL := getEnv("FENRIR_METRICS_CACHE_TTL_SEC", "2")
	ttlSec, err := strconv.Atoi(rawTTL)
	if err != nil || ttlSec < 1 {
		ttlSec = 2
	}

	keywordsRaw := getEnv("FENRIR_GAME_KEYWORDS", "minecraft,mc,itzg,paper,purpur,forge,fabric,spigot,bedrock,valheim")
	var keywords []string
	for _, kw := range strings.Split(keywordsRaw, ",") {
		kw = strings.TrimSpace(strings.ToLower(kw))
		if kw != "" {
			keywords = append(keywords, kw)
		}
	}

	rawInterval := getEnv("FENRIR_METRICS_INTERVAL_SEC", "30")
	intervalSec, err := strconv.Atoi(rawInterval)
	if err != nil || intervalSec < 0 {
		intervalSec = 30
	}

	rawRetention := getEnv("FENRIR_ARCHIVE_RETENTION_DAYS", "90")
	retentionDays, err := strconv.Atoi(rawRetention)
	if err != nil || retentionDays < 1 {
		retentionDays = 90
	}

	return &Config{
		ServerPort:                port,
		DockerHost:                getEnv("DOCKER_HOST", ""),
		MetricsCacheTTL:           time.Duration(ttlSec) * time.Second,
		GameFilterKeywords:        keywords,
		AllowedOrigins:            strings.Split(getEnv("FENRIR_CORS_ORIGINS", "*"), ","),
		DatabasePath:              getEnv("FENRIR_DB_PATH", "fenrir.db"),
		APIKey:                    os.Getenv("FENRIR_API_KEY"),
		MetricsCollectionInterval: time.Duration(intervalSec) * time.Second,
		StacksDir:                 getEnv("FENRIR_STACKS_DIR", "/var/lib/fenrir/stacks/"),
		ArchivesDir:               getEnv("FENRIR_ARCHIVES_DIR", "/var/lib/fenrir/archives/"),
		ArchiveRetentionDays:      retentionDays,
	}
}

func getEnv(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}
