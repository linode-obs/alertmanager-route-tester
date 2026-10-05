package telemetry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
)

func TestShutdownExportsEndedSpans(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/traces" {
			t.Errorf("OTLP path = %q, want /v1/traces", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("OTLP method = %q, want POST", r.Method)
		}
		requests.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	t.Setenv("OTEL_TRACES_EXPORTER", "otlp")
	t.Setenv("OTEL_METRICS_EXPORTER", "none")
	t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "http/protobuf")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", server.URL)
	t.Setenv("OTEL_SERVICE_NAME", "")
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "")

	providers, err := Setup(context.Background(), "1.2.3")
	if err != nil {
		t.Fatalf("Setup() error = %v", err)
	}

	_, span := otel.Tracer("shutdown-test").Start(context.Background(), "shutdown flush")
	span.End()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := providers.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("OTLP trace requests = %d, want one flushed batch", got)
	}
	if err := providers.Shutdown(ctx); err != nil {
		t.Fatalf("second Shutdown() error = %v, want nil", err)
	}
}
