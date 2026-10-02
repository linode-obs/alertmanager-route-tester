package telemetry

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestRuntimeEnvironmentDefaultsAndSDKRestore(t *testing.T) {
	if os.Getenv("ATR_TELEMETRY_SDK_CHILD") != "" {
		if got := os.Getenv("OTEL_SDK_DISABLED"); got != "true" {
			t.Fatalf("OTEL_SDK_DISABLED before Setup() = %q, want true", got)
		}
		if got, want := os.Getenv("OTEL_GO_ENABLED_INSTRUMENTATIONS"), os.Getenv("ATR_EXPECTED_INSTRUMENTATIONS"); got != want {
			t.Fatalf("OTEL_GO_ENABLED_INSTRUMENTATIONS before Setup() = %q, want %q", got, want)
		}

		providers, err := Setup(context.Background(), "1.2.3")
		if err != nil {
			t.Fatalf("Setup() error = %v", err)
		}
		defer providers.Shutdown(context.Background())

		if got := os.Getenv("OTEL_SDK_DISABLED"); got != "false" {
			t.Fatalf("OTEL_SDK_DISABLED after Setup() = %q, want original false", got)
		}
		return
	}

	for _, test := range []struct {
		name        string
		selection   string
		wantEnabled string
	}{
		{name: "default selection", wantEnabled: "nethttp"},
		{name: "explicit selection", selection: "grpc", wantEnabled: "grpc"},
	} {
		t.Run(test.name, func(t *testing.T) {
			command := exec.Command(os.Args[0], "-test.run=^TestRuntimeEnvironmentDefaultsAndSDKRestore$")
			for _, entry := range os.Environ() {
				if strings.HasPrefix(entry, "ATR_TELEMETRY_SDK_CHILD=") ||
					strings.HasPrefix(entry, "ATR_EXPECTED_INSTRUMENTATIONS=") ||
					strings.HasPrefix(entry, "OTEL_SDK_DISABLED=") ||
					strings.HasPrefix(entry, "OTEL_GO_ENABLED_INSTRUMENTATIONS=") ||
					strings.HasPrefix(entry, "OTEL_TRACES_EXPORTER=") ||
					strings.HasPrefix(entry, "OTEL_METRICS_EXPORTER=") {
					continue
				}
				command.Env = append(command.Env, entry)
			}
			command.Env = append(command.Env,
				"ATR_TELEMETRY_SDK_CHILD=1",
				"ATR_EXPECTED_INSTRUMENTATIONS="+test.wantEnabled,
				"OTEL_SDK_DISABLED=false",
				"OTEL_TRACES_EXPORTER=none",
				"OTEL_METRICS_EXPORTER=none",
			)
			if test.selection != "" {
				command.Env = append(command.Env, "OTEL_GO_ENABLED_INSTRUMENTATIONS="+test.selection)
			}
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("child test failed: %v\n%s", err, output)
			}
		})
	}
}
