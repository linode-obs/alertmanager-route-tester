package cli

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wbollock/alertmanager-route-tester/internal/alertmanager"
	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestZZTestRoutingRecordsRouteOutcome(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	meterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	spanRecorder := tracetest.NewSpanRecorder()
	tracerProvider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spanRecorder))
	otel.SetMeterProvider(meterProvider)
	otel.SetTracerProvider(tracerProvider)
	defer meterProvider.Shutdown(context.Background())
	defer tracerProvider.Shutdown(context.Background())

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, `{"config":{"original":"route:\n  receiver: default\n  routes:\n  - receiver: production\n    match:\n      severity: critical\nreceivers:\n- name: default\n- name: production\n"}}`)
	}))
	defer server.Close()

	result, err := TestRouting(alertmanager.NewClient(server.URL, false), map[string]string{"severity": "critical"})
	if err != nil {
		t.Fatalf("TestRouting() error = %v", err)
	}
	if result.Receiver != "production" {
		t.Fatalf("receiver = %q, want production", result.Receiver)
	}

	var data metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &data); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	foundMetric := false
	for _, scope := range data.ScopeMetrics {
		for _, metric := range scope.Metrics {
			if metric.Name != "route.evaluations" {
				continue
			}
			foundMetric = true
			sum, ok := metric.Data.(metricdata.Sum[int64])
			if !ok || len(sum.DataPoints) != 1 || sum.DataPoints[0].Value != 1 {
				t.Fatalf("route.evaluations = %#v, want one route evaluation", metric.Data)
			}
		}
	}
	if !foundMetric {
		t.Fatal("route.evaluations metric is missing")
	}

	foundSpan := false
	for _, span := range spanRecorder.Ended() {
		if span.Name() == "alertmanager.route.evaluate" {
			foundSpan = true
		}
	}
	if !foundSpan {
		t.Fatal("alertmanager.route.evaluate span is missing")
	}
}
