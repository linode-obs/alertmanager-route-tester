package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadNamedAlertmanagers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	contents := []byte(`alertmanagers:
  production:
    url: http://production.example.test
  staging:
    url: http://staging.example.test
`)
	if err := os.WriteFile(path, contents, 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(cfg.Alertmanagers) != 2 {
		t.Fatalf("Alertmanagers = %#v, want two entries", cfg.Alertmanagers)
	}
	if got := cfg.Alertmanagers["production"].URL; got != "http://production.example.test" {
		t.Fatalf("production URL = %q, want production URL", got)
	}
}

func TestLoadRejectsSingularAlertmanagerConfiguration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	contents := []byte(`alertmanager:
  url: http://localhost:9093
`)
	if err := os.WriteFile(path, contents, 0600); err != nil {
		t.Fatal(err)
	}

	if _, err := Load(path); err == nil {
		t.Fatal("Load() error = nil, want singular configuration error")
	}
}

func TestServerTimeoutDefaultsCoverRetryBudget(t *testing.T) {
	cfg := &Config{
		Alertmanagers: map[string]AlertmanagerConfig{
			"test": {
				URL:   "http://localhost:9093",
				HTTP:  HTTPConfig{Timeouts: TimeoutConfig{Request: Duration{Duration: 15 * time.Second}}},
				Retry: RetryConfig{MaxAttempts: 3, Backoff: Duration{Duration: 300 * time.Millisecond}},
			},
		},
	}

	applyDefaults(cfg)

	wantWrite := 50*time.Second + 600*time.Millisecond
	if got := cfg.App.Server.WriteTimeout.Duration; got != wantWrite {
		t.Fatalf("WriteTimeout = %s, want %s", got, wantWrite)
	}
	if cfg.App.Server.ReadTimeout.Duration != wantWrite {
		t.Fatalf("ReadTimeout = %s, want %s", cfg.App.Server.ReadTimeout.Duration, wantWrite)
	}
}

func TestNegativeRetryValuesAreRejected(t *testing.T) {
	cfg := &Config{
		Alertmanagers: map[string]AlertmanagerConfig{
			"test": {
				URL:   "http://localhost:9093",
				Retry: RetryConfig{MaxAttempts: -1, Backoff: Duration{Duration: -time.Second}},
			},
		},
	}
	applyDefaults(cfg)

	if err := validate(cfg); err == nil {
		t.Fatal("validate() error = nil, want negative retry value error")
	}
}

func TestNegativeServerTimeoutsAreRejected(t *testing.T) {
	cfg := &Config{
		Alertmanagers: map[string]AlertmanagerConfig{
			"test": {URL: "http://localhost:9093"},
		},
		App: AppConfig{Server: ServerConfig{WriteTimeout: Duration{Duration: -time.Second}}},
	}
	applyDefaults(cfg)

	if err := validate(cfg); err == nil {
		t.Fatal("validate() error = nil, want negative server timeout error")
	}
}

func TestNegativeHTTPTimeoutsAreRejected(t *testing.T) {
	cfg := &Config{
		Alertmanagers: map[string]AlertmanagerConfig{
			"test": {
				URL:  "http://localhost:9093",
				HTTP: HTTPConfig{Timeouts: TimeoutConfig{Request: Duration{Duration: -time.Second}}},
			},
		},
	}
	applyDefaults(cfg)

	if err := validate(cfg); err == nil {
		t.Fatal("validate() error = nil, want negative HTTP timeout error")
	}
}

func TestServerTimeoutsCanBeConfigured(t *testing.T) {
	cfg := &Config{
		App: AppConfig{Server: ServerConfig{
			ReadHeaderTimeout: Duration{Duration: 2 * time.Second},
			ReadTimeout:       Duration{Duration: 3 * time.Second},
			WriteTimeout:      Duration{Duration: 4 * time.Second},
			IdleTimeout:       Duration{Duration: 5 * time.Second},
		}},
		Alertmanagers: map[string]AlertmanagerConfig{
			"test": {
				URL:   "http://localhost:9093",
				HTTP:  HTTPConfig{Timeouts: TimeoutConfig{Request: Duration{Duration: time.Second}}},
				Retry: RetryConfig{MaxAttempts: 1},
			},
		},
	}

	applyDefaults(cfg)

	if cfg.App.Server.ReadHeaderTimeout.Duration != 2*time.Second ||
		cfg.App.Server.ReadTimeout.Duration != 3*time.Second ||
		cfg.App.Server.WriteTimeout.Duration != 4*time.Second ||
		cfg.App.Server.IdleTimeout.Duration != 5*time.Second {
		t.Fatalf("configured server timeouts were changed: %#v", cfg.App.Server)
	}
}
