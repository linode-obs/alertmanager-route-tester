package telemetry_test

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIRoutingSucceedsWithoutCollector(t *testing.T) {
	binary := os.Getenv("ATR_OTEL_TEST_BINARY")
	if binary == "" {
		t.Skip("ATR_OTEL_TEST_BINARY is not set")
	}
	binaryPath, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(binaryPath); err != nil {
		t.Fatalf("instrumented app binary is unavailable: %v", err)
	}

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
    format: simple
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

	command := exec.Command(binaryPath, "-config", configPath)
	command.Env = cleanOpenTelemetryEnvironment(os.Environ())
	command.Env = append(command.Env,
		"OTEL_EXPORTER_OTLP_ENDPOINT=http://"+unavailableCollector,
		"OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf",
		"OTEL_EXPORTER_OTLP_TIMEOUT=200",
		"OTEL_TRACES_EXPORTER=otlp",
		"OTEL_METRICS_EXPORTER=otlp",
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("CLI returned an error without a Collector: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "Receiver: production") {
		t.Fatalf("CLI output = %q, want production receiver", output)
	}
}
