package telemetry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestHTTPAndApplicationMetricsUseBoundedOutcomes(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	otel.SetMeterProvider(provider)
	defer provider.Shutdown(context.Background())

	handler := MetricsHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/ok", nil),
		httptest.NewRequest("PURGE", "/missing", nil),
	} {
		handler.ServeHTTP(httptest.NewRecorder(), request)
	}

	ctx := context.Background()
	RecordConfigFetch(ctx, true)
	RecordAlertmanagerFailure(ctx)
	RecordCache(ctx, true)
	RecordCache(ctx, false)
	RecordRetry(ctx)
	RecordRoute(ctx, RouteMatched)
	RecordRoute(ctx, RouteUnmatched)
	RecordRoute(ctx, RouteFailed)

	var data metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &data); err != nil {
		t.Fatalf("Collect() error = %v", err)
	}

	want := map[string]map[string]int64{
		"http.server.requests":        {"GET/2xx": 1, "OTHER/4xx": 1},
		"alertmanager.config.fetches": {"failure": 1},
		"alertmanager.failures":       {"": 1},
		"alertmanager.cache.accesses": {"hit": 1, "miss": 1},
		"alertmanager.retries":        {"": 1},
		"route.evaluations":           {"matched": 1, "unmatched": 1, "failed": 1},
	}
	for _, scope := range data.ScopeMetrics {
		for _, instrument := range scope.Metrics {
			outcomes, ok := want[instrument.Name]
			if !ok {
				continue
			}
			sum, ok := instrument.Data.(metricdata.Sum[int64])
			if !ok {
				t.Fatalf("%s data type = %T, want Sum[int64]", instrument.Name, instrument.Data)
			}
			for _, point := range sum.DataPoints {
				attributes := make(map[string]string)
				for _, attribute := range point.Attributes.ToSlice() {
					attributes[string(attribute.Key)] = attribute.Value.AsString()
				}
				outcome := attributes["outcome"]
				if instrument.Name == "http.server.requests" {
					if len(attributes) != 2 {
						t.Errorf("HTTP metric attributes = %v, want method and status class", attributes)
					}
					outcome = attributes["method"] + "/" + attributes["status_class"]
				} else if outcome != "" && len(attributes) != 1 {
					t.Errorf("%s attributes = %v, want only outcome", instrument.Name, attributes)
				}
				if got := outcomes[outcome]; got != point.Value {
					t.Errorf("%s outcome %q value = %d, want %d", instrument.Name, outcome, point.Value, got)
				}
				delete(outcomes, outcome)
			}
			delete(want, instrument.Name)
		}
	}
	if len(want) != 0 {
		t.Errorf("missing metric data: %v", want)
	}
}
