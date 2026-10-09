package telemetry

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

const instrumentationName = "github.com/linode-obs/alertmanager_route_tester"

type RouteResult uint8

const (
	RouteMatched RouteResult = iota
	RouteUnmatched
	RouteFailed
)

var (
	tracer = otel.Tracer(instrumentationName)
	meter  = otel.Meter(instrumentationName)

	httpRequests, _         = meter.Int64Counter("http.server.requests")
	configFetches, _        = meter.Int64Counter("alertmanager.config.fetches")
	alertmanagerFailures, _ = meter.Int64Counter("alertmanager.failures")
	cacheAccesses, _        = meter.Int64Counter("alertmanager.cache.accesses")
	retries, _              = meter.Int64Counter("alertmanager.retries")
	routeEvaluations, _     = meter.Int64Counter("route.evaluations")
)

func StartSpan(ctx context.Context, name string) (context.Context, trace.Span) {
	return tracer.Start(ctx, name)
}

func RecordHTTPRequest(ctx context.Context, method string, statusCode int) {
	method = boundedMethod(method)
	statusClass := "other"
	switch statusCode / 100 {
	case 1:
		statusClass = "1xx"
	case 2:
		statusClass = "2xx"
	case 3:
		statusClass = "3xx"
	case 4:
		statusClass = "4xx"
	case 5:
		statusClass = "5xx"
	}
	httpRequests.Add(ctx, 1, metric.WithAttributes(
		attribute.String("method", method),
		attribute.String("status_class", statusClass),
	))
}

func boundedMethod(method string) string {
	switch method {
	case "CONNECT", "DELETE", "GET", "HEAD", "OPTIONS", "PATCH", "POST", "PUT", "TRACE":
		return method
	default:
		return "OTHER"
	}
}

func RecordConfigFetch(ctx context.Context, failed bool) {
	outcome := "success"
	if failed {
		outcome = "failure"
	}
	configFetches.Add(ctx, 1, metric.WithAttributes(attribute.String("outcome", outcome)))
}

func RecordAlertmanagerFailure(ctx context.Context) {
	alertmanagerFailures.Add(ctx, 1)
}

func RecordCache(ctx context.Context, hit bool) {
	outcome := "miss"
	if hit {
		outcome = "hit"
	}
	cacheAccesses.Add(ctx, 1, metric.WithAttributes(attribute.String("outcome", outcome)))
}

func RecordRetry(ctx context.Context) {
	retries.Add(ctx, 1)
}

func RecordRoute(ctx context.Context, result RouteResult) {
	outcome := "failed"
	switch result {
	case RouteMatched:
		outcome = "matched"
	case RouteUnmatched:
		outcome = "unmatched"
	case RouteFailed:
		outcome = "failed"
	}
	routeEvaluations.Add(ctx, 1, metric.WithAttributes(attribute.String("outcome", outcome)))
}
