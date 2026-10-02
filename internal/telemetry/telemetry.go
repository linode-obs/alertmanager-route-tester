package telemetry

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"

	"go.opentelemetry.io/contrib/exporters/autoexport"
	"go.opentelemetry.io/contrib/propagators/autoprop"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// The app owns its SDK providers, so suppress otelc's injected initializer before main starts.
var (
	originalSDKDisabled, originalSDKDisabledSet = os.LookupEnv("OTEL_SDK_DISABLED")
	sdkInitSuppressionError                     = os.Setenv("OTEL_SDK_DISABLED", "true")
	instrumentationDefaultError                 error
)

func init() {
	if _, set := os.LookupEnv("OTEL_GO_ENABLED_INSTRUMENTATIONS"); !set {
		instrumentationDefaultError = os.Setenv("OTEL_GO_ENABLED_INSTRUMENTATIONS", "nethttp")
	}
}

type Providers struct {
	tracerProvider *sdktrace.TracerProvider
	meterProvider  *metric.MeterProvider
	resource       *resource.Resource
	shutdownOnce   sync.Once
	shutdownErr    error
}

func Setup(ctx context.Context, version string) (*Providers, error) {
	if sdkInitSuppressionError != nil {
		return nil, sdkInitSuppressionError
	}
	if instrumentationDefaultError != nil {
		return nil, instrumentationDefaultError
	}
	if originalSDKDisabledSet {
		if err := os.Setenv("OTEL_SDK_DISABLED", originalSDKDisabled); err != nil {
			return nil, err
		}
	} else if err := os.Unsetenv("OTEL_SDK_DISABLED"); err != nil {
		return nil, err
	}
	if strings.EqualFold(strings.TrimSpace(originalSDKDisabled), "true") {
		return &Providers{}, nil
	}

	serviceResource, err := resource.New(ctx,
		resource.WithAttributes(
			attribute.String("service.name", "alertmanager-route-tester"),
			attribute.String("service.version", version),
		),
		resource.WithFromEnv(),
		resource.WithTelemetrySDK(),
	)
	if err != nil {
		return nil, err
	}

	spanExporter, err := autoexport.NewSpanExporter(ctx)
	if err != nil {
		return nil, err
	}
	metricReader, err := autoexport.NewMetricReader(ctx)
	if err != nil {
		_ = spanExporter.Shutdown(ctx)
		return nil, err
	}

	tracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(spanExporter),
		sdktrace.WithResource(serviceResource),
	)
	meterProvider := metric.NewMeterProvider(
		metric.WithReader(metricReader),
		metric.WithResource(serviceResource),
	)

	otel.SetTracerProvider(tracerProvider)
	otel.SetMeterProvider(meterProvider)
	otel.SetTextMapPropagator(autoprop.NewTextMapPropagator())

	return &Providers{
		tracerProvider: tracerProvider,
		meterProvider:  meterProvider,
		resource:       serviceResource,
	}, nil
}

func (p *Providers) Shutdown(ctx context.Context) error {
	if p == nil {
		return nil
	}
	p.shutdownOnce.Do(func() {
		var shutdownErrors []error
		if p.tracerProvider != nil {
			shutdownErrors = append(shutdownErrors, p.tracerProvider.Shutdown(ctx))
		}
		if p.meterProvider != nil {
			shutdownErrors = append(shutdownErrors, p.meterProvider.Shutdown(ctx))
		}
		p.shutdownErr = errors.Join(shutdownErrors...)
	})
	return p.shutdownErr
}
