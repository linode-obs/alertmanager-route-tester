package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
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

	// CLI test mode
	if cfg.App.CLITestMode.Enabled {
		result, err := cli.TestRouting(client, cfg.App.CLITestMode.Labels)
		if err != nil {
			// Error is already included in result
		}

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

	log.Printf("Starting server on %s", cfg.App.Server.Listen)
	log.Printf("Using Alertmanager at %s", cfg.Alertmanager.URL)
	if err := http.ListenAndServe(cfg.App.Server.Listen, nil); err != nil {
		log.Fatal(err)
	}
}
