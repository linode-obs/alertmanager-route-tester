package cli

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/wbollock/alertmanager-route-tester/internal/alertmanager"
)

func TestPrintResultShowsGenericSubrouteMarkerWithoutAncestry(t *testing.T) {
	result := &TestResult{
		Receiver: "regional",
		MatchedRoutes: []alertmanager.MatchedRoute{
			{Route: &alertmanager.Route{}, Depth: 1, IsSubroute: true, ResolvedReceiver: "regional"},
		},
	}

	output := captureSimpleOutput(t, result)
	if !strings.Contains(output, "[subroute]") || !strings.Contains(output, "receiver=regional") {
		t.Fatalf("output = %q, want generic marker and resolved receiver", output)
	}
}

func TestPrintResultShowsRouteDetails(t *testing.T) {
	result := &TestResult{
		MatchedRoutes: []alertmanager.MatchedRoute{{
			Route: &alertmanager.Route{
				Receiver:       "leaf",
				Matchers:       []string{`severity="critical"`},
				GroupBy:        []string{"alertname"},
				GroupWait:      "30s",
				GroupInterval:  "5m",
				RepeatInterval: "4h",
			},
		}},
	}

	output := captureSimpleOutput(t, result)
	for _, expected := range []string{
		`matchers=[severity="critical"]`,
		"group_by=[alertname]",
		"group_wait=30s",
		"group_interval=5m",
		"repeat_interval=4h",
	} {
		if !strings.Contains(output, expected) {
			t.Errorf("output = %q, want %q", output, expected)
		}
	}
}

func TestPrintResultShowsFullSubrouteAncestry(t *testing.T) {
	result := &TestResult{
		Receiver: "leaf",
		MatchedRoutes: []alertmanager.MatchedRoute{
			{
				Route:           &alertmanager.Route{Receiver: "leaf"},
				Depth:           2,
				ParentReceivers: []string{"platform", "platform-prod"},
				IsSubroute:      true,
			},
		},
	}

	output := captureSimpleOutput(t, result)
	if !strings.Contains(output, "[trace-only] [subroute of platform -> platform-prod]") {
		t.Fatalf("output = %q, want full ancestry", output)
	}
}

func captureSimpleOutput(t *testing.T, result *TestResult) string {
	t.Helper()
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() error = %v", err)
	}
	originalStdout := os.Stdout
	os.Stdout = write
	defer func() { os.Stdout = originalStdout }()

	if err := PrintResult(result, OutputFormatSimple); err != nil {
		t.Fatalf("PrintResult() error = %v", err)
	}
	if err := write.Close(); err != nil {
		t.Fatalf("write.Close() error = %v", err)
	}

	output, err := io.ReadAll(read)
	if err != nil {
		t.Fatalf("io.ReadAll() error = %v", err)
	}
	return string(output)
}
