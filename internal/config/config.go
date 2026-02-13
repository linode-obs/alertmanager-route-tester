package config

import (
	"errors"
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type Duration struct {
	time.Duration
}

func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind != yaml.ScalarNode {
		return fmt.Errorf("duration must be a scalar")
	}
	if value.Value == "" {
		d.Duration = 0
		return nil
	}
	parsed, err := time.ParseDuration(value.Value)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", value.Value, err)
	}
	d.Duration = parsed
	return nil
}

type Config struct {
	App          AppConfig          `yaml:"alertmanager-route-tester"`
	Alertmanager AlertmanagerConfig `yaml:"alertmanager"`
}

type AppConfig struct {
	Server      ServerConfig `yaml:"server"`
	CLITestMode TestConfig   `yaml:"cli-test-mode"`
}

type ServerConfig struct {
	Enabled *bool  `yaml:"enabled"`
	Listen  string `yaml:"listen"`
}

type AlertmanagerConfig struct {
	URL   string      `yaml:"url"`
	HTTP  HTTPConfig  `yaml:"http"`
	Retry RetryConfig `yaml:"retry"`
	Pool  PoolConfig  `yaml:"pool"`
}

type HTTPConfig struct {
	TLS      TLSConfig     `yaml:"tls"`
	Timeouts TimeoutConfig `yaml:"timeouts"`
}

type TLSConfig struct {
	SkipVerify bool   `yaml:"skip_verify"`
	CAFile     string `yaml:"ca_file"`
	CertFile   string `yaml:"cert_file"`
	KeyFile    string `yaml:"key_file"`
}

type TimeoutConfig struct {
	Request        Duration `yaml:"request"`
	Dial           Duration `yaml:"dial"`
	TLSHandshake   Duration `yaml:"tls_handshake"`
	ResponseHeader Duration `yaml:"response_header"`
	IdleConn       Duration `yaml:"idle_conn"`
	ExpectContinue Duration `yaml:"expect_continue"`
}

type RetryConfig struct {
	MaxAttempts int      `yaml:"max_attempts"`
	Backoff     Duration `yaml:"backoff"`
}

type PoolConfig struct {
	MaxIdleConns        int `yaml:"max_idle_conns"`
	MaxIdleConnsPerHost int `yaml:"max_idle_conns_per_host"`
	MaxConnsPerHost     int `yaml:"max_conns_per_host"`
}

type TestConfig struct {
	Enabled bool              `yaml:"enabled"`
	Labels  map[string]string `yaml:"labels"`
	Format  string            `yaml:"format"`
}

func Load(path string) (*Config, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file %q: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(contents, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file %q: %w", path, err)
	}

	applyDefaults(&cfg)
	if err := validate(&cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func applyDefaults(cfg *Config) {
	if cfg.App.Server.Listen == "" {
		cfg.App.Server.Listen = ":8080"
	}
	if cfg.App.Server.Enabled == nil {
		enabled := true
		cfg.App.Server.Enabled = &enabled
	}

	if cfg.Alertmanager.HTTP.Timeouts.Request.Duration == 0 {
		cfg.Alertmanager.HTTP.Timeouts.Request = Duration{Duration: 15 * time.Second}
	}
	if cfg.Alertmanager.HTTP.Timeouts.Dial.Duration == 0 {
		cfg.Alertmanager.HTTP.Timeouts.Dial = Duration{Duration: 5 * time.Second}
	}
	if cfg.Alertmanager.HTTP.Timeouts.TLSHandshake.Duration == 0 {
		cfg.Alertmanager.HTTP.Timeouts.TLSHandshake = Duration{Duration: 5 * time.Second}
	}
	if cfg.Alertmanager.HTTP.Timeouts.ResponseHeader.Duration == 0 {
		cfg.Alertmanager.HTTP.Timeouts.ResponseHeader = Duration{Duration: 10 * time.Second}
	}
	if cfg.Alertmanager.HTTP.Timeouts.IdleConn.Duration == 0 {
		cfg.Alertmanager.HTTP.Timeouts.IdleConn = Duration{Duration: 90 * time.Second}
	}
	if cfg.Alertmanager.HTTP.Timeouts.ExpectContinue.Duration == 0 {
		cfg.Alertmanager.HTTP.Timeouts.ExpectContinue = Duration{Duration: 1 * time.Second}
	}

	if cfg.Alertmanager.Retry.MaxAttempts == 0 {
		cfg.Alertmanager.Retry.MaxAttempts = 3
	}
	if cfg.Alertmanager.Retry.Backoff.Duration == 0 {
		cfg.Alertmanager.Retry.Backoff = Duration{Duration: 300 * time.Millisecond}
	}

	if cfg.Alertmanager.Pool.MaxIdleConns == 0 {
		cfg.Alertmanager.Pool.MaxIdleConns = 100
	}
	if cfg.Alertmanager.Pool.MaxIdleConnsPerHost == 0 {
		cfg.Alertmanager.Pool.MaxIdleConnsPerHost = 10
	}

	if cfg.App.CLITestMode.Format == "" {
		cfg.App.CLITestMode.Format = "simple"
	}
	if cfg.App.CLITestMode.Labels == nil {
		cfg.App.CLITestMode.Labels = map[string]string{}
	}
}

func validate(cfg *Config) error {
	if cfg.Alertmanager.URL == "" {
		return errors.New("alertmanager.url is required")
	}

	if cfg.Alertmanager.HTTP.TLS.CertFile != "" && cfg.Alertmanager.HTTP.TLS.KeyFile == "" {
		return errors.New("alertmanager.http.tls.key_file is required when cert_file is set")
	}
	if cfg.Alertmanager.HTTP.TLS.KeyFile != "" && cfg.Alertmanager.HTTP.TLS.CertFile == "" {
		return errors.New("alertmanager.http.tls.cert_file is required when key_file is set")
	}

	if cfg.Alertmanager.Retry.MaxAttempts < 1 {
		return errors.New("alertmanager.retry.max_attempts must be >= 1")
	}

	if cfg.App.CLITestMode.Enabled && len(cfg.App.CLITestMode.Labels) == 0 {
		return errors.New("alertmanager-route-tester.cli-test-mode.labels is required when cli-test-mode.enabled is true")
	}

	return nil
}

func (cfg *Config) ServerEnabled() bool {
	if cfg.App.Server.Enabled == nil {
		return true
	}
	return *cfg.App.Server.Enabled
}
