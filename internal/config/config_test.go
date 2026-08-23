package config

import "testing"

func TestLoadDefaults(t *testing.T) {
	t.Setenv("FORGE_JOB_LEASE", "")
	t.Setenv("FORGE_JOB_HEARTBEAT", "")
	t.Setenv("FORGE_WORKER_CONCURRENCY", "")
	t.Setenv("FORGE_PUSH_IMAGES", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.APIAddr != ":8080" {
		t.Fatalf("APIAddr = %q, want :8080", cfg.APIAddr)
	}
	if cfg.WorkerConcurrency < 1 {
		t.Fatalf("WorkerConcurrency = %d, want positive", cfg.WorkerConcurrency)
	}
}

func TestLoadRejectsHeartbeatLongerThanLease(t *testing.T) {
	t.Setenv("FORGE_JOB_LEASE", "5s")
	t.Setenv("FORGE_JOB_HEARTBEAT", "5s")

	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want validation error")
	}
}

func TestLoadRequiresRegistryForPush(t *testing.T) {
	t.Setenv("FORGE_PUSH_IMAGES", "true")
	t.Setenv("FORGE_REGISTRY", "")

	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want validation error")
	}
}
