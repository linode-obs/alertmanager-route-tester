package telemetry_test

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	collectormetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"

	"github.com/linode-obs/alertmanager-route-tester/internal/handler"
)

const instrumentedAppPath = "../../bin/alertmanager-route-tester"

type telemetryCapture struct {
	mu                      sync.Mutex
	traceRequests           []*collectortrace.ExportTraceServiceRequest
	metricRequests          []*collectormetrics.ExportMetricsServiceRequest
	alertmanagerTraceParent string
	alertmanagerRequests    int
}

func requireInstrumentedApp(t *testing.T) {
	t.Helper()
	if _, err := os.Stat(instrumentedAppPath); err != nil {
		if os.IsNotExist(err) {
			t.Skip("instrumented app binary is unavailable")
		}
		t.Fatalf("check instrumented app binary: %v", err)
	}
}

func newOTLPCollector(t *testing.T, capture *telemetryCapture) *httptest.Server {
	t.Helper()
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read OTLP request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		switch r.URL.Path {
		case "/v1/traces":
			exported := new(collectortrace.ExportTraceServiceRequest)
			if err := proto.Unmarshal(body, exported); err != nil {
				t.Errorf("decode OTLP traces: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			capture.mu.Lock()
			capture.traceRequests = append(capture.traceRequests, exported)
			capture.mu.Unlock()
		case "/v1/metrics":
			exported := new(collectormetrics.ExportMetricsServiceRequest)
			if err := proto.Unmarshal(body, exported); err != nil {
				t.Errorf("decode OTLP metrics: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			capture.mu.Lock()
			capture.metricRequests = append(capture.metricRequests, exported)
			capture.mu.Unlock()
		default:
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(collector.Close)
	return collector
}

func newAlertmanagerServer(t *testing.T, capture *telemetryCapture, receiverName, labelValue string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/status" {
			t.Errorf("Alertmanager path = %q, want /api/v2/status", r.URL.Path)
		}
		call := capture.recordAlertmanagerCall(r)
		if call > 1 {
			connection, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Errorf("hijack Alertmanager connection: %v", err)
				return
			}
			_ = connection.Close()
			return
		}
		_, _ = fmt.Fprintf(w, `{"config":{"original":"route:\n  receiver: default\n  routes:\n  - receiver: %s\n    match:\n      secret_label: %s\nreceivers:\n- name: default\n- name: %s\n"}}`, receiverName, labelValue, receiverName)
	}))
	t.Cleanup(server.Close)
	return server
}

func (capture *telemetryCapture) recordAlertmanagerCall(r *http.Request) int {
	capture.mu.Lock()
	defer capture.mu.Unlock()
	capture.alertmanagerRequests++
	if capture.alertmanagerRequests == 1 {
		capture.alertmanagerTraceParent = r.Header.Get("traceparent")
	}
	return capture.alertmanagerRequests
}

func authenticatedAlertmanagerURL(t *testing.T, serverURL string) (*url.URL, string) {
	t.Helper()
	target, err := url.Parse(serverURL)
	if err != nil {
		t.Fatal(err)
	}
	var authBytes [16]byte
	if _, err := rand.Read(authBytes[:]); err != nil {
		t.Fatal(err)
	}
	authValue := hex.EncodeToString(authBytes[:])
	target.User = url.UserPassword("route-user", authValue)
	return target, authValue
}

func availableAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return address
}

func writeAppConfig(t *testing.T, listenAddress, alertmanagerURL string) string {
	t.Helper()
	config := fmt.Sprintf(`alertmanager-route-tester:
  server:
    enabled: true
    listen: %q
alertmanagers:
  default:
    url: %q
    retry:
      max_attempts: 1
`, listenAddress, alertmanagerURL)
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	return configPath
}

type runningApp struct {
	command         *exec.Cmd
	address         string
	output          lockedBuffer
	processFinished bool
}

func startInstrumentedApp(t *testing.T, address, configPath, collectorURL string) *runningApp {
	t.Helper()
	command := exec.Command(instrumentedAppPath, "-config", configPath) // #nosec G204 -- configPath is created in t.TempDir.
	command.Env = cleanOpenTelemetryEnvironment(os.Environ())
	command.Env = append(command.Env,
		"OTEL_EXPORTER_OTLP_ENDPOINT="+collectorURL,
		"OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf",
		"OTEL_TRACES_EXPORTER=otlp",
		"OTEL_METRICS_EXPORTER=otlp",
	)
	app := &runningApp{command: command, address: address}
	command.Stdout = &app.output
	command.Stderr = &app.output
	if err := command.Start(); err != nil {
		t.Fatalf("start app: %v", err)
	}
	t.Cleanup(app.cleanup)
	if err := waitForHTTPServer(address); err != nil {
		t.Fatalf("app did not start: %v\n%s", err, app.output.String())
	}
	return app
}

func (app *runningApp) testRoute(t *testing.T, labelValue, requestID, receiverName string) {
	t.Helper()
	request, err := http.NewRequest(
		http.MethodPost,
		"http://"+app.address+"/test",
		strings.NewReader(fmt.Sprintf(`{"labels":{"secret_label":%q}}`, labelValue)),
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Request-ID", requestID)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("app request error = %v\n%s", err, app.output.String())
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("app response status = %d, want %d\n%s", response.StatusCode, http.StatusOK, app.output.String())
	}
	var result handler.TestResponse
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatalf("app response JSON error = %v", err)
	}
	if result.Receiver != receiverName {
		t.Fatalf("receiver = %q, want %q", result.Receiver, receiverName)
	}
}

func (app *runningApp) reloadConfig(t *testing.T) {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, "http://"+app.address+"/config/reload", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("config reload request error = %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("config reload status = %d, want %d", response.StatusCode, http.StatusServiceUnavailable)
	}
}

func (app *runningApp) stop(t *testing.T) {
	t.Helper()
	if err := app.command.Process.Signal(os.Interrupt); err != nil {
		t.Fatalf("signal app shutdown: %v", err)
	}
	wait := make(chan error, 1)
	go func() { wait <- app.command.Wait() }()
	select {
	case err := <-wait:
		app.processFinished = true
		if err != nil {
			t.Fatalf("app shutdown error = %v\n%s", err, app.output.String())
		}
	case <-time.After(12 * time.Second):
		_ = app.command.Process.Kill()
		app.processFinished = true
		<-wait
		t.Fatalf("app did not shut down within its grace period\n%s", app.output.String())
	}
}

func (app *runningApp) cleanup() {
	if app.processFinished {
		return
	}
	_ = app.command.Process.Kill()
	_ = app.command.Wait()
	app.processFinished = true
}

func (capture *telemetryCapture) assertTraceLink(t *testing.T, output string) [][]byte {
	t.Helper()
	requests, traceParent := capture.traceSnapshot()
	if traceParent == "" {
		t.Fatal("Alertmanager request did not receive trace context")
	}
	spans, payloads := collectTraceSpans(t, requests)
	serverSpan := findSpan(spans, func(span *tracepb.Span) bool {
		return span.Kind == tracepb.Span_SPAN_KIND_SERVER && spanAttribute(span, "url.path") == "/test"
	})
	fetchSpan := findSpan(spans, func(span *tracepb.Span) bool {
		return serverSpan != nil && span.Name == "alertmanager.config.fetch" && bytes.Equal(span.ParentSpanId, serverSpan.SpanId)
	})
	alertmanagerSpan := findSpan(spans, func(span *tracepb.Span) bool {
		return fetchSpan != nil && span.Kind == tracepb.Span_SPAN_KIND_CLIENT &&
			bytes.Equal(span.ParentSpanId, fetchSpan.SpanId) &&
			strings.HasSuffix(spanAttribute(span, "url.full"), "/api/v2/status")
	})
	if serverSpan == nil || fetchSpan == nil || alertmanagerSpan == nil {
		t.Fatalf("expected incoming, config-fetch, and Alertmanager spans, got %d trace exports\n%s", len(requests), output)
	}
	if !bytes.Equal(serverSpan.TraceId, alertmanagerSpan.TraceId) {
		t.Fatal("incoming request and Alertmanager request have different trace IDs")
	}
	if !bytes.Equal(fetchSpan.ParentSpanId, serverSpan.SpanId) {
		t.Fatal("config-fetch span is not a child of the incoming request span")
	}
	if !bytes.Equal(alertmanagerSpan.ParentSpanId, fetchSpan.SpanId) {
		t.Fatal("Alertmanager span is not a child of the config-fetch span")
	}
	return payloads
}

func (capture *telemetryCapture) assertNoSensitiveTelemetry(t *testing.T, tracePayloads [][]byte, sensitiveValues []string) {
	t.Helper()
	_, metricRequests := capture.telemetrySnapshot()
	for _, payload := range tracePayloads {
		for _, sensitive := range sensitiveValues {
			if bytes.Contains(payload, []byte(sensitive)) {
				t.Errorf("exported trace payload contains sensitive value %q", sensitive)
			}
		}
	}
	for _, exported := range metricRequests {
		payload, err := proto.Marshal(exported)
		if err != nil {
			t.Fatalf("marshal metric payload: %v", err)
		}
		for _, sensitive := range sensitiveValues {
			if bytes.Contains(payload, []byte(sensitive)) {
				t.Errorf("exported metric payload contains sensitive value %q", sensitive)
			}
		}
	}
}

func (capture *telemetryCapture) assertRequiredMetrics(t *testing.T) {
	t.Helper()
	_, metricRequests := capture.telemetrySnapshot()
	for _, name := range []string{"http.server.requests", "route.evaluations"} {
		if !containsMetric(metricRequests, name) {
			t.Errorf("%s metric is missing", name)
		}
	}
}

func (capture *telemetryCapture) traceSnapshot() ([]*collectortrace.ExportTraceServiceRequest, string) {
	capture.mu.Lock()
	defer capture.mu.Unlock()
	return append([]*collectortrace.ExportTraceServiceRequest(nil), capture.traceRequests...), capture.alertmanagerTraceParent
}

func (capture *telemetryCapture) telemetrySnapshot() ([]*collectortrace.ExportTraceServiceRequest, []*collectormetrics.ExportMetricsServiceRequest) {
	capture.mu.Lock()
	defer capture.mu.Unlock()
	traces := append([]*collectortrace.ExportTraceServiceRequest(nil), capture.traceRequests...)
	metrics := append([]*collectormetrics.ExportMetricsServiceRequest(nil), capture.metricRequests...)
	return traces, metrics
}

func collectTraceSpans(t *testing.T, requests []*collectortrace.ExportTraceServiceRequest) ([]*tracepb.Span, [][]byte) {
	t.Helper()
	var spans []*tracepb.Span
	var payloads [][]byte
	for _, exported := range requests {
		payload, err := proto.Marshal(exported)
		if err != nil {
			t.Fatalf("marshal trace payload: %v", err)
		}
		payloads = append(payloads, payload)
		for _, resourceSpans := range exported.ResourceSpans {
			for _, scopeSpans := range resourceSpans.ScopeSpans {
				spans = append(spans, scopeSpans.Spans...)
			}
		}
	}
	return spans, payloads
}

func findSpan(spans []*tracepb.Span, match func(*tracepb.Span) bool) *tracepb.Span {
	for _, span := range spans {
		if match(span) {
			return span
		}
	}
	return nil
}

func cleanOpenTelemetryEnvironment(environment []string) []string {
	clean := make([]string, 0, len(environment))
	for _, entry := range environment {
		if strings.HasPrefix(entry, "OTEL_") {
			continue
		}
		clean = append(clean, entry)
	}
	return clean
}

func waitForHTTPServer(address string) error {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		connection, err := net.DialTimeout("tcp", address, 100*time.Millisecond)
		if err == nil {
			_ = connection.Close()
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return fmt.Errorf("timed out waiting for %s", address)
}

func spanAttribute(span *tracepb.Span, name string) string {
	for _, attribute := range span.Attributes {
		if attribute.Key == name && attribute.Value.GetStringValue() != "" {
			return attribute.Value.GetStringValue()
		}
	}
	return ""
}

func containsMetric(requests []*collectormetrics.ExportMetricsServiceRequest, name string) bool {
	for _, request := range requests {
		for _, resource := range request.ResourceMetrics {
			for _, scope := range resource.ScopeMetrics {
				for _, metric := range scope.Metrics {
					if metric.Name == name {
						return true
					}
				}
			}
		}
	}
	return false
}

type lockedBuffer struct {
	mu sync.Mutex
	bytes.Buffer
}

func (b *lockedBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.Write(data)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.Buffer.String()
}
