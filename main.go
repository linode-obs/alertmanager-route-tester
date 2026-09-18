package main

import (
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"

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

	clients, names, err := newClients(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
	defaultName := names[0]
	client := clients[defaultName]

	version, revision, modified, goVersion := buildInfo()
	connectionErrors := make(map[string]string)
	for _, name := range names {
		if err := clients[name].CheckConnection(); err != nil {
			connectionErrors[name] = err.Error()
		}
	}
	slog.Info("startup",
		"listen", cfg.App.Server.Listen,
		"alertmanagers", names,
		"server_enabled", cfg.ServerEnabled(),
		"cli_test_enabled", cfg.App.CLITestMode.Enabled,
		"version", version,
		"vcs_revision", revision,
		"vcs_modified", modified,
		"go_version", goVersion,
		"connection_errors", connectionErrors,
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
	h := handler.NewWithClients(clients, names, defaultName)

	http.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))
	http.HandleFunc("/", h.HandleIndex)
	http.HandleFunc("/test", h.HandleTest)
	http.HandleFunc("/config/labels", h.HandleConfigLabels)
	http.HandleFunc("/config/reload", h.HandleReloadConfig)

	slog.Info("starting server", "listen", cfg.App.Server.Listen)
	slog.Info("using alertmanager", "name", defaultName, "url", clients[defaultName].BaseURL())
	server := &http.Server{
		Addr:              cfg.App.Server.Listen,
		Handler:           nil,
		ReadHeaderTimeout: cfg.App.Server.ReadHeaderTimeout.Duration,
		ReadTimeout:       cfg.App.Server.ReadTimeout.Duration,
		WriteTimeout:      cfg.App.Server.WriteTimeout.Duration,
		IdleTimeout:       cfg.App.Server.IdleTimeout.Duration,
	}
	if err := server.ListenAndServe(); err != nil {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func newClients(cfg *appconfig.Config) (map[string]*alertmanager.Client, []string, error) {
	names := make([]string, 0, len(cfg.Alertmanagers))
	for name := range cfg.Alertmanagers {
		names = append(names, name)
	}
	sort.Strings(names)

	clients := make(map[string]*alertmanager.Client, len(names))
	for _, name := range names {
		am := cfg.Alertmanagers[name]
		client, err := alertmanager.NewClientWithOptions(alertmanager.ClientOptions{
			BaseURL: am.URL,
			TLS: alertmanager.TLSOptions{
				SkipVerify: am.HTTP.TLS.SkipVerify,
				CAFile:     am.HTTP.TLS.CAFile,
				CertFile:   am.HTTP.TLS.CertFile,
				KeyFile:    am.HTTP.TLS.KeyFile,
			},
			Timeouts: alertmanager.TimeoutOptions{
				Request:        am.HTTP.Timeouts.Request.Duration,
				Dial:           am.HTTP.Timeouts.Dial.Duration,
				TLSHandshake:   am.HTTP.Timeouts.TLSHandshake.Duration,
				ResponseHeader: am.HTTP.Timeouts.ResponseHeader.Duration,
				IdleConn:       am.HTTP.Timeouts.IdleConn.Duration,
				ExpectContinue: am.HTTP.Timeouts.ExpectContinue.Duration,
			},
			Retry: alertmanager.RetryOptions{
				MaxAttempts: am.Retry.MaxAttempts,
				Backoff:     am.Retry.Backoff.Duration,
			},
			Pool: alertmanager.PoolOptions{
				MaxIdleConns:        am.Pool.MaxIdleConns,
				MaxIdleConnsPerHost: am.Pool.MaxIdleConnsPerHost,
				MaxConnsPerHost:     am.Pool.MaxConnsPerHost,
			},
		})
		if err != nil {
			return nil, nil, fmt.Errorf("create Alertmanager client %q: %w", name, err)
		}
		clients[name] = client
	}
	return clients, names, nil
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
