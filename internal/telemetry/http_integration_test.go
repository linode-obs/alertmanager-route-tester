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

	"github.com/wbollock/alertmanager-route-tester/internal/handler"
	collectormetrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"
)

func TestHTTPTraceConnectsRequestToAlertmanager(t *testing.T) {
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

	const (
		labelValue   = "private-label-value"
		receiverName = "private-receiver-name"
		requestID    = "private-request-id"
	)

	var (
		mu                      sync.Mutex
		traceRequests           []*collectortrace.ExportTraceServiceRequest
		metricRequests          []*collectormetrics.ExportMetricsServiceRequest
		alertmanagerTraceParent string
		alertmanagerRequests    int
	)
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
			mu.Lock()
			traceRequests = append(traceRequests, exported)
			mu.Unlock()
		case "/v1/metrics":
			exported := new(collectormetrics.ExportMetricsServiceRequest)
			if err := proto.Unmarshal(body, exported); err != nil {
				t.Errorf("decode OTLP metrics: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			mu.Lock()
			metricRequests = append(metricRequests, exported)
			mu.Unlock()
		default:
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer collector.Close()

	alertmanagerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/status" {
			t.Errorf("Alertmanager path = %q, want /api/v2/status", r.URL.Path)
		}
		mu.Lock()
		alertmanagerRequests++
		call := alertmanagerRequests
		if call == 1 {
			alertmanagerTraceParent = r.Header.Get("traceparent")
		}
		mu.Unlock()
		if call > 1 {
			connection, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Errorf("hijack Alertmanager connection: %v", err)
				return
			}
			_ = connection.Close()
			return
		}
		_, _ = fmt.Fprint(w, `{"config":{"original":"route:\n  receiver: default\n  routes:\n  - receiver: private-receiver-name\n    match:\n      secret_label: private-label-value\nreceivers:\n- name: default\n- name: private-receiver-name\n"}}`)
	}))
	defer alertmanagerServer.Close()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	appAddress := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}

	alertmanagerURL, err := url.Parse(alertmanagerServer.URL)
	if err != nil {
		t.Fatal(err)
	}
	var authBytes [16]byte
	if _, err := rand.Read(authBytes[:]); err != nil {
		t.Fatal(err)
	}
	authValue := hex.EncodeToString(authBytes[:])
	alertmanagerURL.User = url.UserPassword("route-user", authValue)
	config := fmt.Sprintf(`alertmanager-route-tester:
  server:
    enabled: true
    listen: %q
alertmanagers:
  default:
    url: %q
    retry:
      max_attempts: 1
`, appAddress, alertmanagerURL.String())
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(configPath, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}

	command := exec.Command(binaryPath, "-config", configPath)
	command.Env = cleanOpenTelemetryEnvironment(os.Environ())
	command.Env = append(command.Env,
		"OTEL_EXPORTER_OTLP_ENDPOINT="+collector.URL,
		"OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf",
		"OTEL_TRACES_EXPORTER=otlp",
		"OTEL_METRICS_EXPORTER=otlp",
	)
	var output lockedBuffer
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Start(); err != nil {
		t.Fatalf("start app: %v", err)
	}
	processFinished := false
	defer func() {
		if !processFinished {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	}()

	if err := waitForHTTPServer(appAddress); err != nil {
		t.Fatalf("app did not start: %v\n%s", err, output.String())
	}

	request, err := http.NewRequest(
		http.MethodPost,
		"http://"+appAddress+"/test",
		strings.NewReader(`{"labels":{"secret_label":"private-label-value"}}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Request-ID", requestID)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("app request error = %v\n%s", err, output.String())
	}
	var result handler.TestResponse
	if response.StatusCode != http.StatusOK {
		_ = response.Body.Close()
		t.Fatalf("app response status = %d, want %d\n%s", response.StatusCode, http.StatusOK, output.String())
	}
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		_ = response.Body.Close()
		t.Fatalf("app response JSON error = %v", err)
	}
	_ = response.Body.Close()
	if result.Receiver != receiverName {
		t.Fatalf("receiver = %q, want %q", result.Receiver, receiverName)
	}

	reloadRequest, err := http.NewRequest(http.MethodPost, "http://"+appAddress+"/config/reload", nil)
	if err != nil {
		t.Fatal(err)
	}
	reloadResponse, err := http.DefaultClient.Do(reloadRequest)
	if err != nil {
		t.Fatalf("config reload request error = %v", err)
	}
	_ = reloadResponse.Body.Close()
	if reloadResponse.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("config reload status = %d, want %d", reloadResponse.StatusCode, http.StatusServiceUnavailable)
	}

	if err := command.Process.Signal(os.Interrupt); err != nil {
		t.Fatalf("signal app shutdown: %v", err)
	}
	wait := make(chan error, 1)
	go func() { wait <- command.Wait() }()
	select {
	case err := <-wait:
		processFinished = true
		if err != nil {
			t.Fatalf("app shutdown error = %v\n%s", err, output.String())
		}
	case <-time.After(12 * time.Second):
		_ = command.Process.Kill()
		processFinished = true
		<-wait
		t.Fatalf("app did not shut down within its grace period\n%s", output.String())
	}

	mu.Lock()
	defer mu.Unlock()
	if alertmanagerTraceParent == "" {
		t.Fatal("Alertmanager request did not receive trace context")
	}

	var serverSpan, fetchSpan, alertmanagerSpan *tracepb.Span
	var spans []*tracepb.Span
	var tracePayloads [][]byte
	for _, exported := range traceRequests {
		payload, err := proto.Marshal(exported)
		if err != nil {
			t.Fatalf("marshal trace payload: %v", err)
		}
		tracePayloads = append(tracePayloads, payload)
		for _, resourceSpans := range exported.ResourceSpans {
			for _, scopeSpans := range resourceSpans.ScopeSpans {
				spans = append(spans, scopeSpans.Spans...)
				for _, span := range scopeSpans.Spans {
					if span.Kind == tracepb.Span_SPAN_KIND_SERVER && spanAttribute(span, "url.path") == "/test" {
						serverSpan = span
					}
				}
			}
		}
	}
	if serverSpan != nil {
		for _, span := range spans {
			if span.Name == "alertmanager.config.fetch" && bytes.Equal(span.ParentSpanId, serverSpan.SpanId) {
				fetchSpan = span
				break
			}
		}
	}
	if fetchSpan != nil {
		for _, span := range spans {
			if span.Kind == tracepb.Span_SPAN_KIND_CLIENT &&
				bytes.Equal(span.ParentSpanId, fetchSpan.SpanId) &&
				strings.HasSuffix(spanAttribute(span, "url.full"), "/api/v2/status") {
				alertmanagerSpan = span
				break
			}
		}
	}
	if serverSpan == nil || fetchSpan == nil || alertmanagerSpan == nil {
		t.Fatalf("expected incoming, config-fetch, and Alertmanager spans, got %d trace exports\n%s", len(traceRequests), output.String())
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

	sensitiveValues := []string{labelValue, receiverName, requestID, authValue, alertmanagerURL.User.String()}
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
	if !containsMetric(metricRequests, "http.server.requests") {
		t.Error("http.server.requests metric is missing")
	}
	if !containsMetric(metricRequests, "route.evaluations") {
		t.Error("route.evaluations metric is missing")
	}
}

func cleanOpenTelemetryEnvironment(environment []string) []string {
	clean := make([]string, 0, len(environment))
	for _, entry := range environment {
		if strings.HasPrefix(entry, "OTEL_") || strings.HasPrefix(entry, "ATR_OTEL_TEST_BINARY=") {
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
