package alertmanager

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

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
	t.Cleanup(func() {
		if err := provider.Shutdown(context.Background()); err != nil {
			t.Errorf("meter provider shutdown error = %v", err)
		}
	})
	t.Cleanup(func() {
		if err := tracerProvider.Shutdown(context.Background()); err != nil {
			t.Errorf("tracer provider shutdown error = %v", err)
		}
	})

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		request := requests.Add(1)
		if request == 1 || request == 4 {
			http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
			return
		}
		_, _ = fmt.Fprint(w, `{"config":{"original":"route:\n  receiver: default\nreceivers:\n  - name: default\n"}}`)
	}))
	defer server.Close()

	client, err := NewClientWithOptions(ClientOptions{
		BaseURL: server.URL,
		Retry:   RetryOptions{MaxAttempts: 2, Backoff: time.Millisecond},
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
	if err := client.CheckConnectionContext(context.Background()); err != nil {
		t.Fatalf("CheckConnectionContext() error = %v", err)
	}
	if got := requests.Load(); got != 5 {
		t.Fatalf("HTTP requests = %d, want one failed fetch, one retry, one refresh, one failed connection check, and one retry", got)
	}

	var data metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &data); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}
	values := map[string]map[string]int64{
		"alertmanager.failures":       {"": 2},
		"alertmanager.retries":        {"": 2},
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
		"alertmanager.retry":          false,
		"alertmanager.config.refresh": false,
	}
	retrySpans := 0
	for _, span := range spanRecorder.Ended() {
		if _, ok := wantSpans[span.Name()]; ok {
			wantSpans[span.Name()] = true
		}
		if span.Name() == "alertmanager.retry" {
			retrySpans++
		}
	}
	for name, found := range wantSpans {
		if !found {
			t.Errorf("span %q is missing", name)
		}
	}
	if retrySpans != 2 {
		t.Errorf("alertmanager.retry spans = %d, want 2", retrySpans)
	}
}
