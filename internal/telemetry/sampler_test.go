package telemetry

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/trace"
)

func TestSetupUsesStandardSamplerSetting(t *testing.T) {
	t.Setenv("OTEL_TRACES_EXPORTER", "none")
	t.Setenv("OTEL_METRICS_EXPORTER", "none")
	t.Setenv("OTEL_TRACES_SAMPLER", "always_off")

	providers, err := Setup(context.Background(), "1.2.3")
	if err != nil {
		t.Fatalf("Setup() error = %v", err)
	}
	t.Cleanup(func() {
		if err := providers.Shutdown(context.Background()); err != nil {
			t.Errorf("provider shutdown error = %v", err)
		}
	})

	_, span := providers.tracerProvider.Tracer("sampler-test").Start(context.Background(), "unsampled")
	if span.IsRecording() {
		t.Fatal("span is recording with OTEL_TRACES_SAMPLER=always_off")
	}
	span.End()

	if span.SpanContext().TraceFlags()&trace.FlagsSampled != 0 {
		t.Fatal("span context is sampled with OTEL_TRACES_SAMPLER=always_off")
	}
}
