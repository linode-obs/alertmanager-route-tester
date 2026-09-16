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
	App           AppConfig                     `yaml:"alertmanager-route-tester"`
	Alertmanagers map[string]AlertmanagerConfig `yaml:"alertmanagers"`
}

type AppConfig struct {
	Server      ServerConfig `yaml:"server"`
	CLITestMode TestConfig   `yaml:"cli-test-mode"`
}

type ServerConfig struct {
	Enabled           *bool    `yaml:"enabled"`
	Listen            string   `yaml:"listen"`
	ReadHeaderTimeout Duration `yaml:"read_header_timeout"`
	ReadTimeout       Duration `yaml:"read_timeout"`
	WriteTimeout      Duration `yaml:"write_timeout"`
	IdleTimeout       Duration `yaml:"idle_timeout"`
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
	contents, err := os.ReadFile(path) // #nosec G304 -- the path is the explicitly selected application config file.
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

	for name, alertmanager := range cfg.Alertmanagers {
		applyAlertmanagerDefaults(&alertmanager)
		cfg.Alertmanagers[name] = alertmanager
	}
	applyServerTimeoutDefaults(cfg)

	if cfg.App.CLITestMode.Format == "" {
		cfg.App.CLITestMode.Format = "simple"
	}
	if cfg.App.CLITestMode.Labels == nil {
		cfg.App.CLITestMode.Labels = map[string]string{}
	}
}

func applyAlertmanagerDefaults(cfg *AlertmanagerConfig) {
	if cfg.HTTP.Timeouts.Request.Duration == 0 {
		cfg.HTTP.Timeouts.Request = Duration{Duration: 15 * time.Second}
	}
	if cfg.HTTP.Timeouts.Dial.Duration == 0 {
		cfg.HTTP.Timeouts.Dial = Duration{Duration: 5 * time.Second}
	}
	if cfg.HTTP.Timeouts.TLSHandshake.Duration == 0 {
		cfg.HTTP.Timeouts.TLSHandshake = Duration{Duration: 5 * time.Second}
	}
	if cfg.HTTP.Timeouts.ResponseHeader.Duration == 0 {
		cfg.HTTP.Timeouts.ResponseHeader = Duration{Duration: 10 * time.Second}
	}
	if cfg.HTTP.Timeouts.IdleConn.Duration == 0 {
		cfg.HTTP.Timeouts.IdleConn = Duration{Duration: 90 * time.Second}
	}
	if cfg.HTTP.Timeouts.ExpectContinue.Duration == 0 {
		cfg.HTTP.Timeouts.ExpectContinue = Duration{Duration: time.Second}
	}
	if cfg.Retry.MaxAttempts == 0 {
		cfg.Retry.MaxAttempts = 3
	}
	if cfg.Retry.Backoff.Duration == 0 {
		cfg.Retry.Backoff = Duration{Duration: 300 * time.Millisecond}
	}
	if cfg.Pool.MaxIdleConns == 0 {
		cfg.Pool.MaxIdleConns = 100
	}
	if cfg.Pool.MaxIdleConnsPerHost == 0 {
		cfg.Pool.MaxIdleConnsPerHost = 10
	}
}

func applyServerTimeoutDefaults(cfg *Config) {
	var retryBudget time.Duration
	for _, alertmanager := range cfg.Alertmanagers {
		attempts := alertmanager.Retry.MaxAttempts
		if attempts < 1 {
			attempts = 1
		}
		budget := alertmanager.HTTP.Timeouts.Request.Duration * time.Duration(attempts)
		if attempts > 1 {
			budget += alertmanager.Retry.Backoff.Duration * time.Duration(attempts-1)
		}
		if budget > retryBudget {
			retryBudget = budget
		}
	}
	if cfg.App.Server.ReadHeaderTimeout.Duration == 0 {
		cfg.App.Server.ReadHeaderTimeout = Duration{Duration: 5 * time.Second}
	}
	if cfg.App.Server.WriteTimeout.Duration == 0 {
		cfg.App.Server.WriteTimeout = Duration{Duration: retryBudget + 5*time.Second}
	}
	if cfg.App.Server.ReadTimeout.Duration == 0 {
		cfg.App.Server.ReadTimeout = cfg.App.Server.WriteTimeout
	}
	if cfg.App.Server.IdleTimeout.Duration == 0 {
		cfg.App.Server.IdleTimeout = Duration{Duration: 60 * time.Second}
	}
}

func validate(cfg *Config) error {
	if len(cfg.Alertmanagers) == 0 {
		return errors.New("alertmanagers is required and must contain at least one named instance")
	}
	for name, alertmanager := range cfg.Alertmanagers {
		if name == "" {
			return errors.New("alertmanagers contains an empty instance name")
		}
		if alertmanager.URL == "" {
			return fmt.Errorf("alertmanagers.%s.url is required", name)
		}
		for field, value := range map[string]time.Duration{
			"request":         alertmanager.HTTP.Timeouts.Request.Duration,
			"dial":            alertmanager.HTTP.Timeouts.Dial.Duration,
			"tls_handshake":   alertmanager.HTTP.Timeouts.TLSHandshake.Duration,
			"response_header": alertmanager.HTTP.Timeouts.ResponseHeader.Duration,
			"idle_conn":       alertmanager.HTTP.Timeouts.IdleConn.Duration,
			"expect_continue": alertmanager.HTTP.Timeouts.ExpectContinue.Duration,
		} {
			if value < 0 {
				return fmt.Errorf("alertmanagers.%s.http.timeouts.%s must be >= 0", name, field)
			}
		}
		if alertmanager.HTTP.TLS.CertFile != "" && alertmanager.HTTP.TLS.KeyFile == "" {
			return fmt.Errorf("alertmanagers.%s.http.tls.key_file is required when cert_file is set", name)
		}
		if alertmanager.HTTP.TLS.KeyFile != "" && alertmanager.HTTP.TLS.CertFile == "" {
			return fmt.Errorf("alertmanagers.%s.http.tls.cert_file is required when key_file is set", name)
		}
		if alertmanager.Retry.MaxAttempts < 1 {
			return fmt.Errorf("alertmanagers.%s.retry.max_attempts must be >= 1", name)
		}
		if alertmanager.Retry.Backoff.Duration < 0 {
			return fmt.Errorf("alertmanagers.%s.retry.backoff must be >= 0", name)
		}
	}

	serverTimeouts := map[string]time.Duration{
		"read_header_timeout": cfg.App.Server.ReadHeaderTimeout.Duration,
		"read_timeout":        cfg.App.Server.ReadTimeout.Duration,
		"write_timeout":       cfg.App.Server.WriteTimeout.Duration,
		"idle_timeout":        cfg.App.Server.IdleTimeout.Duration,
	}
	for name, value := range serverTimeouts {
		if value < 0 {
			return fmt.Errorf("alertmanager-route-tester.server.%s must be >= 0", name)
		}
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
