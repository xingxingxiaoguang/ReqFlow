package config

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestValidateRejectsPartialOpenSearchCredentials(t *testing.T) {
	var cfg Config
	cfg.Database.DSN = "postgres://example"
	cfg.OpenSearch.Username = "admin"

	errs, _ := cfg.Validate()
	if !slices.Contains(errs, "opensearch.username 与 opensearch.password 必须同时配置或同时留空") {
		t.Fatalf("expected partial credential error, got %v", errs)
	}
}

func TestCheckExampleLeakIncludesOpenSearchPassword(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.example.yaml")
	if err := os.WriteFile(path, []byte("opensearch:\n  password: your-opensearch-password\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	leaks := CheckExampleLeak(path)
	if !slices.Contains(leaks, "opensearch.password") {
		t.Fatalf("expected opensearch.password leak, got %v", leaks)
	}
}

func TestLoadAppliesWorkerDefaultsAndEnvironmentOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("database:\n  dsn: postgres://example\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("REQFLOW_WORKER_CONCURRENCY", "8")

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Worker.Concurrency != 8 || cfg.Worker.LeaseSeconds != 30 || cfg.Worker.PollIntervalMs != 500 ||
		cfg.Worker.RecoveryIntervalMs != 5000 || cfg.Worker.ReconcileLimit != 100 {
		t.Fatalf("Worker 默认配置或环境变量覆盖错误: %+v", cfg.Worker)
	}
	if errs, _ := cfg.Validate(); len(errs) != 0 {
		t.Fatalf("默认 Worker 配置应通过校验: %v", errs)
	}
}

func TestValidateRejectsUnsafeWorkerConcurrency(t *testing.T) {
	var cfg Config
	cfg.Database.DSN = "postgres://example"
	applyDefaults(&cfg)
	cfg.Worker.Concurrency = 129

	errs, _ := cfg.Validate()
	if !slices.Contains(errs, "worker.concurrency 必须在 1 到 128 之间") {
		t.Fatalf("expected worker concurrency error, got %v", errs)
	}
}
