package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadAllowsCLITestSuiteWithoutSingleLabels(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	contents := []byte(`alertmanager-route-tester:
  server:
    enabled: false
  cli-test-mode:
    enabled: true
    suite:
      - name: critical alert
        labels:
          severity: critical
        expected_receivers:
          - pagerduty-critical
alertmanagers:
  production:
    url: http://production.example.test
`)
	if err := os.WriteFile(path, contents, 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got := cfg.App.CLITestMode.Suite; len(got) != 1 || got[0].Name != "critical alert" {
		t.Fatalf("CLI suite = %#v, want one named case", got)
	}
	if got := cfg.App.CLITestMode.Suite[0].ExpectedReceivers; len(got) != 1 || got[0] != "pagerduty-critical" {
		t.Fatalf("expected receivers = %v, want [pagerduty-critical]", got)
	}
}

func TestValidateRejectsInvalidCLITestSuites(t *testing.T) {
	validCase := CLITestCase{Name: "alert", ExpectedReceivers: []string{"default"}}
	suite := func(cases ...CLITestCase) TestConfig {
		return TestConfig{Enabled: true, Suite: cases}
	}
	tests := []struct {
		name string
		mode TestConfig
		want string
	}{
		{
			name: "suite mode disabled",
			mode: TestConfig{Suite: []CLITestCase{validCase}},
			want: "cli-test-mode.enabled must be true",
		},
		{
			name: "suite combined with labels",
			mode: TestConfig{Enabled: true, Labels: map[string]string{"severity": "critical"}, Suite: []CLITestCase{validCase}},
			want: "labels cannot be combined with suite",
		},
		{
			name: "blank case name",
			mode: suite(CLITestCase{ExpectedReceivers: []string{"default"}}),
			want: "suite[0].name is required",
		},
		{
			name: "duplicate case names",
			mode: suite(validCase, validCase),
			want: "contains duplicate case name",
		},
		{
			name: "missing expected receivers",
			mode: suite(CLITestCase{Name: "alert"}),
			want: "suite[0].expected_receivers is required",
		},
		{
			name: "blank expected receiver",
			mode: suite(CLITestCase{Name: "alert", ExpectedReceivers: []string{" "}}),
			want: "contains an empty receiver",
		},
		{
			name: "duplicate expected receivers",
			mode: suite(CLITestCase{Name: "alert", ExpectedReceivers: []string{"default", "default"}}),
			want: "contains duplicate receiver",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := &Config{
				App:           AppConfig{CLITestMode: test.mode},
				Alertmanagers: map[string]AlertmanagerConfig{"test": {URL: "http://alertmanager.example.test"}},
			}
			applyDefaults(cfg)

			err := validate(cfg)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validate() error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestLoadNamedAlertmanagers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	contents := []byte(`alertmanagers:
  production:
    url: http://production.example.test
    matcher_mode: classic
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
	if got := cfg.Alertmanagers["production"].MatcherMode; got != "classic" {
		t.Fatalf("production matcher mode = %q, want classic", got)
	}
	if got := cfg.Alertmanagers["staging"].MatcherMode; got != "fallback" {
		t.Fatalf("staging matcher mode = %q, want fallback default", got)
	}
}

func TestLoadRejectsUnknownMatcherMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	contents := []byte(`alertmanagers:
  production:
    url: http://production.example.test
    matcher_mode: invalid
`)
	if err := os.WriteFile(path, contents, 0600); err != nil {
		t.Fatal(err)
	}

	if _, err := Load(path); err == nil {
		t.Fatal("Load() error = nil, want invalid matcher mode error")
	}
}

func TestLoadForCLIAllowsLabelsFromCommandLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	contents := []byte(`alertmanager-route-tester:
  cli-test-mode:
    enabled: true
    labels: {}
alertmanagers:
  production:
    url: http://production.example.test
`)
	if err := os.WriteFile(path, contents, 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadForCLI(path)
	if err != nil {
		t.Fatalf("LoadForCLI() error = %v", err)
	}
	if cfg.App.CLITestMode.Labels == nil {
		t.Fatal("LoadForCLI() labels = nil, want initialized map")
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

func TestServerDefaultsBindToLoopback(t *testing.T) {
	cfg := &Config{Alertmanagers: map[string]AlertmanagerConfig{
		"test": {URL: "http://localhost:9093"},
	}}

	applyDefaults(cfg)

	if got := cfg.App.Server.Listen; got != "127.0.0.1:8080" {
		t.Fatalf("Listen = %q, want 127.0.0.1:8080", got)
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
