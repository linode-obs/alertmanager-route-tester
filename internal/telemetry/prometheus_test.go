package telemetry

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestPrometheusHandlerExposesRecordedCounters(t *testing.T) {
	if os.Getenv("ATR_PROMETHEUS_METRICS_CHILD") == "" {
		runTelemetryChild(t, "TestPrometheusHandlerExposesRecordedCounters", []string{
			"ATR_PROMETHEUS_METRICS_CHILD=1",
			"OTEL_SDK_DISABLED=false",
			"OTEL_TRACES_EXPORTER=none",
			"OTEL_METRICS_EXPORTER=none",
		})
		return
	}

	providers, err := Setup(context.Background(), "test")
	if err != nil {
		t.Fatalf("Setup() error = %v", err)
	}
	t.Cleanup(func() {
		if err := providers.Shutdown(context.Background()); err != nil {
			t.Errorf("Shutdown() error = %v", err)
		}
	})

	ctx := context.Background()
	RecordHTTPRequest(ctx, http.MethodGet, http.StatusOK)
	RecordConfigFetch(ctx, false)
	RecordRoute(ctx, RouteMatched)

	body, contentType, status := scrapeMetrics(t, providers)
	if status != http.StatusOK {
		t.Errorf("GET /metrics status = %d, want %d", status, http.StatusOK)
	}
	if !strings.Contains(contentType, "text/plain") {
		t.Errorf("GET /metrics Content-Type = %q, want text/plain", contentType)
	}
	for _, name := range []string{
		"http_server_requests_total",
		"alertmanager_config_fetches_total",
		"route_evaluations_total",
	} {
		if !strings.Contains(body, name) {
			t.Errorf("GET /metrics body missing %q\n%s", name, body)
		}
	}
}

func TestPrometheusHandlerWhenSDKDisabled(t *testing.T) {
	if os.Getenv("ATR_PROMETHEUS_DISABLED_CHILD") == "" {
		runTelemetryChild(t, "TestPrometheusHandlerWhenSDKDisabled", []string{
			"ATR_PROMETHEUS_DISABLED_CHILD=1",
			"OTEL_SDK_DISABLED=true",
			"OTEL_TRACES_EXPORTER=none",
			"OTEL_METRICS_EXPORTER=none",
		})
		return
	}

	providers, err := Setup(context.Background(), "test")
	if err != nil {
		t.Fatalf("Setup() error = %v", err)
	}
	t.Cleanup(func() {
		if err := providers.Shutdown(context.Background()); err != nil {
			t.Errorf("Shutdown() error = %v", err)
		}
	})

	ctx := context.Background()
	RecordHTTPRequest(ctx, http.MethodGet, http.StatusOK)
	RecordConfigFetch(ctx, false)
	RecordRoute(ctx, RouteMatched)

	body, contentType, status := scrapeMetrics(t, providers)
	if status != http.StatusOK {
		t.Errorf("GET /metrics status = %d, want %d", status, http.StatusOK)
	}
	if !strings.Contains(contentType, "text/plain") {
		t.Errorf("GET /metrics Content-Type = %q, want text/plain", contentType)
	}
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		t.Errorf("GET /metrics disabled exposition line = %q, want empty exposition", line)
	}
}

func scrapeMetrics(t *testing.T, providers *Providers) (body string, contentType string, status int) {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle("/metrics", providers.PrometheusHandler())
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	response, err := http.Get(server.URL + "/metrics")
	if err != nil {
		t.Fatalf("GET /metrics error = %v", err)
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read /metrics body: %v", err)
	}
	return string(payload), response.Header.Get("Content-Type"), response.StatusCode
}

func runTelemetryChild(t *testing.T, testName string, extraEnv []string) {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable() error = %v", err)
	}
	command := exec.Command(executable, "-test.run=^"+testName+"$")
	command.Env = telemetryChildEnv(extraEnv)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("child %s failed: %v\n%s", testName, err, output)
	}
}

func telemetryChildEnv(extra []string) []string {
	skip := make(map[string]bool, len(extra))
	for _, entry := range extra {
		key, _, ok := strings.Cut(entry, "=")
		if ok {
			skip[key] = true
		}
	}
	env := make([]string, 0, len(os.Environ())+len(extra))
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if skip[key] {
			continue
		}
		env = append(env, entry)
	}
	return append(env, extra...)
}
