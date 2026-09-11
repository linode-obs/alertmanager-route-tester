package main

import (
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"runtime"
	"runtime/debug"
	"strings"
	"time"

	"github.com/wbollock/alertmanager-route-tester/internal/alertmanager"
	"github.com/wbollock/alertmanager-route-tester/internal/cli"
	appconfig "github.com/wbollock/alertmanager-route-tester/internal/config"
	"github.com/wbollock/alertmanager-route-tester/internal/handler"
)

func main() {
	configPath := flag.String("config", "config.yaml", "Path to configuration file")
	flag.Parse()

	cfg, err := appconfig.Load(*configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}

	client, err := alertmanager.NewClientWithOptions(alertmanager.ClientOptions{
		BaseURL: cfg.Alertmanager.URL,
		TLS: alertmanager.TLSOptions{
			SkipVerify: cfg.Alertmanager.HTTP.TLS.SkipVerify,
			CAFile:     cfg.Alertmanager.HTTP.TLS.CAFile,
			CertFile:   cfg.Alertmanager.HTTP.TLS.CertFile,
			KeyFile:    cfg.Alertmanager.HTTP.TLS.KeyFile,
		},
		Timeouts: alertmanager.TimeoutOptions{
			Request:        cfg.Alertmanager.HTTP.Timeouts.Request.Duration,
			Dial:           cfg.Alertmanager.HTTP.Timeouts.Dial.Duration,
			TLSHandshake:   cfg.Alertmanager.HTTP.Timeouts.TLSHandshake.Duration,
			ResponseHeader: cfg.Alertmanager.HTTP.Timeouts.ResponseHeader.Duration,
			IdleConn:       cfg.Alertmanager.HTTP.Timeouts.IdleConn.Duration,
			ExpectContinue: cfg.Alertmanager.HTTP.Timeouts.ExpectContinue.Duration,
		},
		Retry: alertmanager.RetryOptions{
			MaxAttempts: cfg.Alertmanager.Retry.MaxAttempts,
			Backoff:     cfg.Alertmanager.Retry.Backoff.Duration,
		},
		Pool: alertmanager.PoolOptions{
			MaxIdleConns:        cfg.Alertmanager.Pool.MaxIdleConns,
			MaxIdleConnsPerHost: cfg.Alertmanager.Pool.MaxIdleConnsPerHost,
			MaxConnsPerHost:     cfg.Alertmanager.Pool.MaxConnsPerHost,
		},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}

	version, revision, modified, goVersion := buildInfo()
	connectionErr := client.CheckConnection()
	slog.Info("startup",
		"listen", cfg.App.Server.Listen,
		"alertmanager_url", cfg.Alertmanager.URL,
		"server_enabled", cfg.ServerEnabled(),
		"cli_test_enabled", cfg.App.CLITestMode.Enabled,
		"version", version,
		"vcs_revision", revision,
		"vcs_modified", modified,
		"go_version", goVersion,
		"connection_ok", connectionErr == nil,
		"connection_error", errString(connectionErr),
		"config_path", *configPath,
	)

	// CLI test mode
	if cfg.App.CLITestMode.Enabled {
		result, _ := cli.TestRouting(client, cfg.App.CLITestMode.Labels)

		format := cli.OutputFormat(strings.ToLower(cfg.App.CLITestMode.Format))
		if err := cli.PrintResult(result, format); err != nil {
			os.Exit(1)
		}

		if result.Error != "" {
			os.Exit(1)
		}
		return
	}
	if !cfg.ServerEnabled() {
		fmt.Fprintln(os.Stderr, "Error: alertmanager-route-tester.server.enabled is false and cli-test-mode.enabled is false")
		os.Exit(1)
	}

	// Web server mode
	h := handler.New(client)

	http.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))
	http.HandleFunc("/", h.HandleIndex)
	http.HandleFunc("/test", h.HandleTest)
	http.HandleFunc("/config/labels", h.HandleConfigLabels)
	http.HandleFunc("/config/reload", h.HandleReloadConfig)

	slog.Info("starting server", "listen", cfg.App.Server.Listen)
	slog.Info("using alertmanager", "url", cfg.Alertmanager.URL)
	server := &http.Server{
		Addr:              cfg.App.Server.Listen,
		Handler:           nil,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	if err := server.ListenAndServe(); err != nil {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func buildInfo() (version string, revision string, modified bool, goVersion string) {
	version = "dev"
	goVersion = runtime.Version()
	if info, ok := debug.ReadBuildInfo(); ok {
		if info.Main.Version != "" && info.Main.Version != "(devel)" {
			version = info.Main.Version
		}
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				revision = setting.Value
			case "vcs.modified":
				modified = setting.Value == "true"
			}
		}
	}
	return version, revision, modified, goVersion
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
