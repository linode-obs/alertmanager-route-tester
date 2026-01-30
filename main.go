// ABOUTME: Main entry point for alertmanager route tester web application
// ABOUTME: Serves HTMX UI and provides API endpoints for testing alert routing

package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/wbollock/alertmanager-route-tester/internal/alertmanager"
	"github.com/wbollock/alertmanager-route-tester/internal/handler"
)

func main() {
	var (
		listenAddr      = flag.String("listen", ":8080", "Address to listen on")
		alertmanagerURL = flag.String("alertmanager-url", "", "Alertmanager API URL (required)")
		skipTLSVerify   = flag.Bool("skip-tls-verify", false, "Skip TLS certificate verification")
	)
	flag.Parse()

	if *alertmanagerURL == "" {
		fmt.Fprintln(os.Stderr, "Error: -alertmanager-url is required")
		flag.Usage()
		os.Exit(1)
	}

	client := alertmanager.NewClient(*alertmanagerURL, *skipTLSVerify)
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
