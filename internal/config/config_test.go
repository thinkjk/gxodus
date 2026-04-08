package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.PollInterval != "5m" {
		t.Errorf("expected poll interval 5m, got %s", cfg.PollInterval)
	}
	if cfg.Extract != false {
		t.Errorf("expected extract false, got %v", cfg.Extract)
	}
	if cfg.KeepZip != true {
		t.Errorf("expected keep_zip true, got %v", cfg.KeepZip)
	}
	if cfg.FileSize != "2GB" {
		t.Errorf("expected file size 2GB, got %s", cfg.FileSize)
	}
}

func TestLoadMissingConfig(t *testing.T) {
	cfg, err := Load("/nonexistent/path/config.toml")
	if err != nil {
		t.Fatalf("expected no error for missing config, got %v", err)
	}
	if cfg.PollInterval != "5m" {
		t.Errorf("expected default poll interval, got %s", cfg.PollInterval)
	}
}

func TestLoadConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	content := `
output_dir = "/tmp/exports"
poll_interval = "10m"
extract = true
keep_zip = false
file_size = "4GB"

[notify]
on_export_complete = "echo done"
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("writing test config: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}

	if cfg.OutputDir != "/tmp/exports" {
		t.Errorf("expected output dir /tmp/exports, got %s", cfg.OutputDir)
	}
	if cfg.PollInterval != "10m" {
		t.Errorf("expected poll interval 10m, got %s", cfg.PollInterval)
	}
	if cfg.Extract != true {
		t.Errorf("expected extract true, got %v", cfg.Extract)
	}
	if cfg.KeepZip != false {
		t.Errorf("expected keep_zip false, got %v", cfg.KeepZip)
	}
	if cfg.Notify.OnExportComplete != "echo done" {
		t.Errorf("expected notify on_export_complete 'echo done', got %s", cfg.Notify.OnExportComplete)
	}
}

func TestPollDuration(t *testing.T) {
	cfg := DefaultConfig()
	d, err := cfg.PollDuration()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if d.Minutes() != 5 {
		t.Errorf("expected 5 minutes, got %v", d)
	}
}

func TestResolveOutputDir(t *testing.T) {
	cfg := &Config{OutputDir: "/absolute/path"}
	if cfg.ResolveOutputDir() != "/absolute/path" {
		t.Errorf("expected /absolute/path, got %s", cfg.ResolveOutputDir())
	}
}
