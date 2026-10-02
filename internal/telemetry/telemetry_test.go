package telemetry

import (
	"context"
	"testing"
)

func TestSetupUsesStandardServiceResource(t *testing.T) {
	tests := []struct {
		name        string
		serviceName string
		attributes  string
		wantName    string
		wantVersion string
	}{
		{
			name:        "application defaults",
			wantName:    "alertmanager-route-tester",
			wantVersion: "1.2.3",
		},
		{
			name:        "standard environment overrides",
			serviceName: "route-tester-prod",
			attributes:  "service.version=2.0.0,deployment.environment=production",
			wantName:    "route-tester-prod",
			wantVersion: "2.0.0",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("OTEL_TRACES_EXPORTER", "none")
			t.Setenv("OTEL_METRICS_EXPORTER", "none")
			t.Setenv("OTEL_SERVICE_NAME", test.serviceName)
			t.Setenv("OTEL_RESOURCE_ATTRIBUTES", test.attributes)

			providers, err := Setup(context.Background(), "1.2.3")
			if err != nil {
				t.Fatalf("Setup() error = %v", err)
			}
			defer providers.Shutdown(context.Background())

			attributes := make(map[string]string)
			for _, attribute := range providers.resource.Attributes() {
				attributes[string(attribute.Key)] = attribute.Value.AsString()
			}
			if got := attributes["service.name"]; got != test.wantName {
				t.Errorf("service.name = %q, want %q", got, test.wantName)
			}
			if got := attributes["service.version"]; got != test.wantVersion {
				t.Errorf("service.version = %q, want %q", got, test.wantVersion)
			}
			if test.attributes != "" && attributes["deployment.environment"] != "production" {
				t.Errorf("deployment.environment = %q, want production", attributes["deployment.environment"])
			}
		})
	}
}
