package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/linode-obs/alertmanager-route-tester/internal/alertmanager"
)

func TestZZHandleTestRecordsRouteOutcomeWithoutSensitiveData(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	meterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	spanRecorder := tracetest.NewSpanRecorder()
	tracerProvider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spanRecorder))
	otel.SetMeterProvider(meterProvider)
	otel.SetTracerProvider(tracerProvider)
	t.Cleanup(func() {
		if err := meterProvider.Shutdown(context.Background()); err != nil {
			t.Errorf("meter provider shutdown error = %v", err)
		}
	})
	t.Cleanup(func() {
		if err := tracerProvider.Shutdown(context.Background()); err != nil {
			t.Errorf("tracer provider shutdown error = %v", err)
		}
	})

	const sensitiveValue = "private-label-value"
	const sensitiveReceiver = "private-receiver-name"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"config":{"original":"route:\n  receiver: default\n  routes:\n  - receiver: private-receiver-name\n    match:\n      secret_label: private-label-value\nreceivers:\n- name: default\n- name: private-receiver-name\n"}}`))
	}))
	defer server.Close()

	client := alertmanager.NewClient(server.URL, false)
	templates := fstest.MapFS{
		"templates/index.html":  &fstest.MapFile{Data: []byte("{{define \"index.html\"}}index{{end}}")},
		"templates/result.html": &fstest.MapFile{Data: []byte("{{define \"result.html\"}}result{{end}}")},
	}
	handler := NewWithClientsFromFS(
		map[string]*alertmanager.Client{"default": client},
		[]string{"default"},
		"default",
		templates,
	)

	request := httptest.NewRequest(http.MethodPost, "/test", strings.NewReader(`{"labels":{"secret_label":"private-label-value"}}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.HandleTest(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("HandleTest() status = %d, want %d: %s", response.Code, http.StatusOK, response.Body.String())
	}
	var result TestResponse
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatalf("response JSON error = %v", err)
	}
	if result.Receiver != sensitiveReceiver {
		t.Fatalf("route receiver = %q, want %q", result.Receiver, sensitiveReceiver)
	}

	var data metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &data); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	foundRouteMetric := false
	for _, scope := range data.ScopeMetrics {
		for _, metric := range scope.Metrics {
			if metric.Name != "route.evaluations" {
				continue
			}
			foundRouteMetric = true
			sum, ok := metric.Data.(metricdata.Sum[int64])
			if !ok {
				t.Fatalf("route.evaluations data type = %T, want Sum[int64]", metric.Data)
			}
			if len(sum.DataPoints) != 1 || sum.DataPoints[0].Value != 1 {
				t.Fatalf("route.evaluations data points = %#v, want one matched result", sum.DataPoints)
			}
			attributes := sum.DataPoints[0].Attributes.ToSlice()
			if len(attributes) != 1 || attributes[0].Key != "outcome" || attributes[0].Value.AsString() != "matched" {
				t.Fatalf("route.evaluations attributes = %#v, want outcome=matched", attributes)
			}
		}
	}
	if !foundRouteMetric {
		t.Fatal("route.evaluations metric is missing")
	}

	foundRouteSpan := false
	for _, span := range spanRecorder.Ended() {
		if span.Name() != "alertmanager.route.evaluate" {
			continue
		}
		foundRouteSpan = true
		for _, attribute := range span.Attributes() {
			if attribute.Value.AsString() == sensitiveValue || attribute.Value.AsString() == sensitiveReceiver {
				t.Errorf("route span contains sensitive value in %q", attribute.Key)
			}
		}
	}
	if !foundRouteSpan {
		t.Fatal("alertmanager.route.evaluate span is missing")
	}
}
