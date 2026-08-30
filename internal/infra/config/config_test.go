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
	cfg.FTS.TSConfig = "simple"
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
