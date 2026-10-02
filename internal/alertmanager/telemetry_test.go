package alertmanager

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestZZConfigFetchRecordsFailureRetryAndCacheOutcomes(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	spanRecorder := tracetest.NewSpanRecorder()
	tracerProvider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spanRecorder))
	otel.SetMeterProvider(provider)
	otel.SetTracerProvider(tracerProvider)
	defer provider.Shutdown(context.Background())
	defer tracerProvider.Shutdown(context.Background())

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if requests.Add(1) == 1 {
			http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
			return
		}
		_, _ = fmt.Fprint(w, `{"config":{"original":"route:\n  receiver: default\nreceivers:\n  - name: default\n"}}`)
	}))
	defer server.Close()

	client, err := NewClientWithOptions(ClientOptions{
		BaseURL: server.URL,
		Retry:   RetryOptions{MaxAttempts: 2},
	})
	if err != nil {
		t.Fatalf("NewClientWithOptions() error = %v", err)
	}

	if _, cached, err := client.GetConfigWithStatusContext(context.Background()); err != nil || cached {
		t.Fatalf("first GetConfigWithStatusContext() = cached %t, error %v, want cache miss and success", cached, err)
	}
	if _, cached, err := client.GetConfigWithStatusContext(context.Background()); err != nil || !cached {
		t.Fatalf("second GetConfigWithStatusContext() = cached %t, error %v, want cache hit and success", cached, err)
	}
	if _, err := client.RefreshConfigContext(context.Background()); err != nil {
		t.Fatalf("RefreshConfigContext() error = %v", err)
	}
	if got := requests.Load(); got != 3 {
		t.Fatalf("HTTP requests = %d, want one failed request, one retry, and one refresh", got)
	}

	var data metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &data); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	values := map[string]map[string]int64{
		"alertmanager.failures":       {"": 1},
		"alertmanager.retries":        {"": 1},
		"alertmanager.cache.accesses": {"miss": 1, "hit": 1},
	}
	for _, scope := range data.ScopeMetrics {
		for _, metric := range scope.Metrics {
			outcomes, ok := values[metric.Name]
			if !ok {
				continue
			}
			sum, ok := metric.Data.(metricdata.Sum[int64])
			if !ok {
				t.Fatalf("%s data type = %T, want Sum[int64]", metric.Name, metric.Data)
			}
			for _, point := range sum.DataPoints {
				outcome := ""
				for _, attribute := range point.Attributes.ToSlice() {
					if string(attribute.Key) != "outcome" {
						t.Errorf("%s attribute %q is not bounded outcome", metric.Name, attribute.Key)
						continue
					}
					outcome = attribute.Value.AsString()
				}
				if got := outcomes[outcome]; got != point.Value {
					t.Errorf("%s outcome %q value = %d, want %d", metric.Name, outcome, point.Value, got)
				}
				delete(outcomes, outcome)
			}
			delete(values, metric.Name)
		}
	}
	if len(values) != 0 {
		t.Errorf("missing metric data: %v", values)
	}

	wantSpans := map[string]bool{
		"alertmanager.config.fetch":   false,
		"alertmanager.config.retry":   false,
		"alertmanager.config.refresh": false,
	}
	for _, span := range spanRecorder.Ended() {
		if _, ok := wantSpans[span.Name()]; ok {
			wantSpans[span.Name()] = true
		}
	}
	for name, found := range wantSpans {
		if !found {
			t.Errorf("span %q is missing", name)
		}
	}
}
