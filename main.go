package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/wbollock/alertmanager-route-tester/internal/alertmanager"
	"github.com/wbollock/alertmanager-route-tester/internal/cli"
	"github.com/wbollock/alertmanager-route-tester/internal/handler"
)

func main() {
	var (
		listenAddr      = flag.String("listen", ":8080", "Address to listen on")
		alertmanagerURL = flag.String("alertmanager-url", "", "Alertmanager API URL (required)")
		skipTLSVerify   = flag.Bool("skip-tls-verify", false, "Skip TLS certificate verification")
		testMode        = flag.Bool("test", false, "Run in test mode (CLI) instead of web server mode")
		labelsJSON      = flag.String("labels", "", "JSON object of alert labels (test mode only)")
		outputFormat    = flag.String("format", "simple", "Output format: json or simple (test mode only)")
	)
	flag.Parse()

	if *alertmanagerURL == "" {
		fmt.Fprintln(os.Stderr, "Error: -alertmanager-url is required")
		flag.Usage()
		os.Exit(1)
	}

	client := alertmanager.NewClient(*alertmanagerURL, *skipTLSVerify)

	// CLI test mode
	if *testMode {
		if *labelsJSON == "" {
			fmt.Fprintln(os.Stderr, "Error: -labels is required in test mode")
			fmt.Fprintln(os.Stderr, "Example: -labels '{\"alertname\":\"HighCPU\",\"severity\":\"critical\"}'")
			os.Exit(1)
		}

		var labels map[string]string
		if err := json.Unmarshal([]byte(*labelsJSON), &labels); err != nil {
			fmt.Fprintf(os.Stderr, "Error parsing labels JSON: %v\n", err)
			os.Exit(1)
		}

		result, err := cli.TestRouting(client, labels)
		if err != nil {
			// Error is already included in result
		}

		format := cli.OutputFormat(strings.ToLower(*outputFormat))
		if err := cli.PrintResult(result, format); err != nil {
			os.Exit(1)
		}

		if result.Error != "" {
			os.Exit(1)
		}
		return
	}

	// Web server mode
	h := handler.New(client)

	http.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))
	http.HandleFunc("/", h.HandleIndex)
	http.HandleFunc("/test", h.HandleTest)
	http.HandleFunc("/config/labels", h.HandleConfigLabels)

	log.Printf("Starting server on %s", *listenAddr)
	log.Printf("Using Alertmanager at %s", *alertmanagerURL)
	if err := http.ListenAndServe(*listenAddr, nil); err != nil {
		log.Fatal(err)
	}
}
