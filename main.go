package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/wbollock/alertmanager-route-tester/internal/alertmanager"
	"github.com/wbollock/alertmanager-route-tester/internal/cli"
	appconfig "github.com/wbollock/alertmanager-route-tester/internal/config"
	"github.com/wbollock/alertmanager-route-tester/internal/handler"
)

//go:embed templates/*.html static/*
var assets embed.FS

func main() {
	configPath := flag.String("config", "config.yaml", "Path to configuration file")
	alertmanagerName := flag.String("alertmanager", "", "Alertmanager instance name for CLI test mode")
	labelsJSON := flag.String("labels-json", "", "Alert labels as a JSON object for CLI test mode")
	flag.Parse()

	loadConfig := appconfig.Load
	if *labelsJSON != "" {
		loadConfig = appconfig.LoadForCLI
	}
	cfg, err := loadConfig(*configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}

	clients, names, err := newClients(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
	cliMode := cfg.App.CLITestMode.Enabled || *labelsJSON != ""
	requestedAlertmanager := alertmanagerRequest(cliMode, *alertmanagerName)
	defaultName, err := selectAlertmanager(names, requestedAlertmanager)
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
	client := clients[defaultName]

	version, revision, modified, goVersion := buildInfo()
	slog.Info("startup",
		"listen", cfg.App.Server.Listen,
		"alertmanagers", names,
		"server_enabled", cfg.ServerEnabled(),
		"cli_test_enabled", cfg.App.CLITestMode.Enabled,
		"version", version,
		"vcs_revision", revision,
		"vcs_modified", modified,
		"go_version", goVersion,
		"config_path", *configPath,
	)

	// CLI test mode
	if cliMode {
		if len(cfg.App.CLITestMode.Suite) > 0 {
			if *labelsJSON != "" {
				fmt.Fprintln(os.Stderr, "--labels-json cannot be used with cli-test-mode.suite")
				os.Exit(1)
			}
			suite := make([]cli.SuiteCase, 0, len(cfg.App.CLITestMode.Suite))
			for _, testCase := range cfg.App.CLITestMode.Suite {
				suite = append(suite, cli.SuiteCase{
					Name:              testCase.Name,
					Labels:            testCase.Labels,
					ExpectedReceivers: testCase.ExpectedReceivers,
				})
			}
			results := cli.RunSuite(client, suite)
			format := cli.OutputFormat(strings.ToLower(cfg.App.CLITestMode.Format))
			if err := cli.PrintSuiteResults(results, format); err != nil {
				fmt.Fprintln(os.Stderr, err.Error())
				os.Exit(1)
			}
			for _, result := range results {
				if !result.Passed {
					os.Exit(1)
				}
			}
			return
		}

		labels := cfg.App.CLITestMode.Labels
		if *labelsJSON != "" {
			labels, err = parseLabelsJSON(*labelsJSON)
			if err != nil {
				fmt.Fprintln(os.Stderr, err.Error())
				os.Exit(1)
			}
		}
		if len(labels) == 0 {
			fmt.Fprintln(os.Stderr, "at least one CLI label is required in CLI test mode")
			os.Exit(1)
		}
		result, _ := cli.TestRouting(client, labels)

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
	h := handler.NewWithClientsFromFS(clients, names, defaultName, assets)

	http.Handle("/static/", http.FileServer(http.FS(assets)))
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
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)

	listenerConfig := net.ListenConfig{}
	listener, err := listenerConfig.Listen(context.Background(), "tcp", server.Addr)
	if err != nil {
		slog.Error("server failed to start", "error", err)
		os.Exit(1)
	}
	if err := serve(server, listener, signals); err != nil {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func alertmanagerRequest(cliMode bool, requested string) string {
	if cliMode {
		return requested
	}
	return ""
}

func selectAlertmanager(names []string, requested string) (string, error) {
	if requested == "" {
		return names[0], nil
	}
	for _, name := range names {
		if name == requested {
			return name, nil
		}
	}
	return "", fmt.Errorf("unknown Alertmanager %q", requested)
}

func parseLabelsJSON(input string) (map[string]string, error) {
	var values map[string]*string
	if err := json.Unmarshal([]byte(input), &values); err != nil {
		return nil, fmt.Errorf("invalid labels JSON: %w", err)
	}
	if values == nil {
		return nil, fmt.Errorf("invalid labels JSON: expected an object")
	}

	labels := make(map[string]string, len(values))
	for name, value := range values {
		if value == nil {
			return nil, fmt.Errorf("invalid labels JSON: label %q must be a string", name)
		}
		labels[name] = *value
	}
	return labels, nil
}

func serve(server *http.Server, listener net.Listener, signals <-chan os.Signal) error {
	serverContext, cancelServerContext := context.WithCancel(context.Background())
	defer cancelServerContext()
	server.BaseContext = func(net.Listener) context.Context {
		return serverContext
	}

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- server.Serve(listener)
	}()

	select {
	case err := <-serveErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-signals:
		cancelServerContext()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return server.Shutdown(ctx)
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
