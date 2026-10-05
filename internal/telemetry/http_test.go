package telemetry

import (
	"bytes"
	"context"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestHTTPAndApplicationMetricsUseBoundedOutcomes(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	otel.SetMeterProvider(provider)
	t.Cleanup(func() {
		if err := provider.Shutdown(context.Background()); err != nil {
			t.Errorf("meter provider shutdown error = %v", err)
		}
	})

	var serverOutput bytes.Buffer
	server := httptest.NewUnstartedServer(MetricsHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/missing":
			http.Error(w, "not found", http.StatusNotFound)
		case "/informational":
			w.WriteHeader(http.StatusEarlyHints)
			w.WriteHeader(http.StatusNotFound)
			w.WriteHeader(http.StatusInternalServerError)
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	})))
	server.Config.ErrorLog = log.New(&serverOutput, "", 0)
	server.Start()
	defer server.Close()

	for _, request := range []struct {
		method string
		path   string
	}{
		{method: http.MethodGet, path: "/ok"},
		{method: "PURGE", path: "/missing"},
		{method: http.MethodPost, path: "/informational"},
	} {
		req, err := http.NewRequest(request.method, server.URL+request.path, nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := server.Client().Do(req)
		if err != nil {
			t.Fatalf("request %s %s error = %v", request.method, request.path, err)
		}
		if request.path == "/informational" && response.StatusCode != http.StatusNotFound {
			t.Errorf("informational response final status = %d, want %d", response.StatusCode, http.StatusNotFound)
		}
		if err := response.Body.Close(); err != nil {
			t.Errorf("close %s response: %v", request.path, err)
		}
	}
	if strings.Contains(serverOutput.String(), "superfluous response.WriteHeader") {
		t.Errorf("HTTP server logged a repeated final status: %s", serverOutput.String())
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
		"http.server.requests":        {"GET/2xx": 1, "OTHER/4xx": 1, "POST/4xx": 1},
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
