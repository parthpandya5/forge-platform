package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Environment         string
	LogLevel            slog.Level
	DatabaseURL         string
	MigrationsPath      string
	APIAddr             string
	CORSOrigin          string
	LocalUserEmail      string
	LocalUserName       string
	EncryptionKey       string
	GitHubWebhookSecret string
	WorkerID            string
	WorkerMetricsAddr   string
	WorkerConcurrency   int
	JobLease            time.Duration
	JobHeartbeat        time.Duration
	JobMaxAttempts      int
	CloneTimeout        time.Duration
	BuildTimeout        time.Duration
	RolloutTimeout      time.Duration
	HealthInterval      time.Duration
	DockerHost          string
	ImageRepository     string
	Registry            string
	PushImages          bool
	Kubeconfig          string
	KubeNamespace       string
	IngressClass        string
	BaseDomain          string
	PullPolicy          string
}

func Load() (Config, error) {
	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "unknown"
	}

	cfg := Config{
		Environment:         env("FORGE_ENV", "development"),
		DatabaseURL:         env("FORGE_DATABASE_URL", "postgres://forge:forge@localhost:55432/forge?sslmode=disable"),
		MigrationsPath:      env("FORGE_MIGRATIONS_PATH", "migrations"),
		APIAddr:             env("FORGE_API_ADDR", ":8080"),
		CORSOrigin:          env("FORGE_CORS_ORIGIN", "http://localhost:3000"),
		LocalUserEmail:      env("FORGE_LOCAL_USER_EMAIL", "developer@forge.local"),
		LocalUserName:       env("FORGE_LOCAL_USER_NAME", "Local Developer"),
		EncryptionKey:       os.Getenv("FORGE_ENCRYPTION_KEY"),
		GitHubWebhookSecret: os.Getenv("FORGE_GITHUB_WEBHOOK_SECRET"),
		WorkerID:            env("FORGE_WORKER_ID", fmt.Sprintf("%s-%d", hostname, os.Getpid())),
		WorkerMetricsAddr:   env("FORGE_WORKER_METRICS_ADDR", ":9090"),
		DockerHost:          os.Getenv("FORGE_DOCKER_HOST"),
		ImageRepository:     env("FORGE_IMAGE_REPOSITORY", "forge"),
		Registry:            strings.TrimSuffix(os.Getenv("FORGE_REGISTRY"), "/"),
		Kubeconfig:          os.Getenv("FORGE_KUBECONFIG"),
		KubeNamespace:       env("FORGE_KUBE_NAMESPACE", "forge-apps"),
		IngressClass:        env("FORGE_INGRESS_CLASS", "nginx"),
		BaseDomain:          env("FORGE_BASE_DOMAIN", "localhost"),
		PullPolicy:          env("FORGE_PULL_POLICY", "IfNotPresent"),
	}

	var err error
	if cfg.LogLevel, err = parseLogLevel(env("FORGE_LOG_LEVEL", "info")); err != nil {
		return Config{}, err
	}
	if cfg.WorkerConcurrency, err = envInt("FORGE_WORKER_CONCURRENCY", min(2, runtime.NumCPU()), 1, 32); err != nil {
		return Config{}, err
	}
	if cfg.JobMaxAttempts, err = envInt("FORGE_JOB_MAX_ATTEMPTS", 3, 1, 20); err != nil {
		return Config{}, err
	}
	if cfg.JobLease, err = envDuration("FORGE_JOB_LEASE", 30*time.Second); err != nil {
		return Config{}, err
	}
	if cfg.JobHeartbeat, err = envDuration("FORGE_JOB_HEARTBEAT", 10*time.Second); err != nil {
		return Config{}, err
	}
	if cfg.CloneTimeout, err = envDuration("FORGE_CLONE_TIMEOUT", 2*time.Minute); err != nil {
		return Config{}, err
	}
	if cfg.BuildTimeout, err = envDuration("FORGE_BUILD_TIMEOUT", 15*time.Minute); err != nil {
		return Config{}, err
	}
	if cfg.RolloutTimeout, err = envDuration("FORGE_ROLLOUT_TIMEOUT", 5*time.Minute); err != nil {
		return Config{}, err
	}
	if cfg.HealthInterval, err = envDuration("FORGE_HEALTH_INTERVAL", 30*time.Second); err != nil {
		return Config{}, err
	}
	if cfg.PushImages, err = envBool("FORGE_PUSH_IMAGES", false); err != nil {
		return Config{}, err
	}
	if cfg.JobHeartbeat >= cfg.JobLease {
		return Config{}, errors.New("FORGE_JOB_HEARTBEAT must be shorter than FORGE_JOB_LEASE")
	}
	if cfg.PushImages && cfg.Registry == "" {
		return Config{}, errors.New("FORGE_REGISTRY is required when FORGE_PUSH_IMAGES=true")
	}
	return cfg, nil
}

func env(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func envInt(key string, fallback, minimum, maximum int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < minimum || value > maximum {
		return 0, fmt.Errorf("%s must be an integer between %d and %d", key, minimum, maximum)
	}
	return value, nil
}

func envBool(key string, fallback bool) (bool, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean: %w", key, err)
	}
	return value, nil
}

func envDuration(key string, fallback time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback, nil
	}
	value, err := time.ParseDuration(raw)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration", key)
	}
	return value, nil
}

func parseLogLevel(raw string) (slog.Level, error) {
	var level slog.Level
	if err := level.UnmarshalText([]byte(raw)); err != nil {
		return 0, fmt.Errorf("FORGE_LOG_LEVEL: %w", err)
	}
	return level, nil
}
