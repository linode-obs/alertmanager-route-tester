package cli

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/wbollock/alertmanager-route-tester/internal/alertmanager"
)

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
	if !strings.Contains(string(output), "[subroute of platform -> platform-prod]") {
		t.Fatalf("output = %q, want full ancestry", output)
	}
}
