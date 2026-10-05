package telemetry_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestCLIRoutingSucceedsWithoutCollector(t *testing.T) {
	requireInstrumentedApp(t)

	alertmanagerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"config":{"original":"route:\n  receiver: default\n  routes:\n  - receiver: production\n    match:\n      severity: critical\nreceivers:\n- name: default\n- name: production\n"}}`)
	}))
	defer alertmanagerServer.Close()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	unavailableCollector := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}

	config := fmt.Sprintf(`alertmanager-route-tester:
  server:
    enabled: false
  cli-test-mode:
    enabled: true
    format: json
    labels:
      severity: critical
alertmanagers:
  test:
    url: %q
    retry:
      max_attempts: 1
`, alertmanagerServer.URL)
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}

	command := exec.Command("../../bin/alertmanager-route-tester", "-config", configPath) // #nosec G204 -- configPath is created in t.TempDir.
	command.Env = cleanOpenTelemetryEnvironment(os.Environ())
	command.Env = append(command.Env,
		"OTEL_EXPORTER_OTLP_ENDPOINT=http://"+unavailableCollector,
		"OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf",
		"OTEL_EXPORTER_OTLP_TIMEOUT=200",
		"OTEL_TRACES_EXPORTER=otlp",
		"OTEL_METRICS_EXPORTER=otlp",
	)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		t.Fatalf("CLI returned an error without a Collector: %v\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
	}
	var result struct {
		Receiver string `json:"receiver"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("CLI stdout is not valid JSON: %v\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
	}
	if result.Receiver != "production" {
		t.Fatalf("CLI receiver = %q, want production", result.Receiver)
	}
}
