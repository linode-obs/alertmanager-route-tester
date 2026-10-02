package telemetry

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
)

func TestSetupUsesStandardPropagatorSetting(t *testing.T) {
	t.Setenv("OTEL_TRACES_EXPORTER", "none")
	t.Setenv("OTEL_METRICS_EXPORTER", "none")
	t.Setenv("OTEL_PROPAGATORS", "none")

	providers, err := Setup(context.Background(), "1.2.3")
	if err != nil {
		t.Fatalf("Setup() error = %v", err)
	}
	defer providers.Shutdown(context.Background())

	if fields := otel.GetTextMapPropagator().Fields(); len(fields) != 0 {
		t.Fatalf("propagator fields = %v, want no propagation fields", fields)
	}
}
