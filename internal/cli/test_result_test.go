package cli_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/linode-obs/alertmanager-route-tester/internal/alertmanager"
	"github.com/linode-obs/alertmanager-route-tester/internal/cli"
)

func TestMatchedRouteJSONUsesStableFieldNames(t *testing.T) {
	result := cli.TestResult{
		MatchedRoutes: []alertmanager.MatchedRoute{
			{
				Route:            &alertmanager.Route{Receiver: "pagerduty"},
				Depth:            1,
				ParentReceivers:  []string{"platform"},
				IsSubroute:       true,
				IsEffective:      true,
				ResolvedReceiver: "pagerduty",
			},
		},
	}

	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	output := string(encoded)
	for _, field := range []string{`"route"`, `"depth"`, `"parent_receivers"`, `"is_subroute"`, `"is_effective"`, `"resolved_receiver"`} {
		if !strings.Contains(output, field) {
			t.Errorf("JSON output %q does not contain %s", output, field)
		}
	}
}

func TestAlertmanagerURLUsesEnvironment(t *testing.T) {
	t.Setenv("ALERTMANAGER_URL", "http://127.0.0.1:19093")
	if got := alertmanagerURL(); got != "http://127.0.0.1:19093" {
		t.Fatalf("alertmanagerURL() = %q, want configured URL", got)
	}
}

func TestEmptyMatchedRoutesMarshalAsArray(t *testing.T) {
	result := cli.TestResult{MatchedRoutes: []alertmanager.MatchedRoute{}}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if !strings.Contains(string(encoded), `"matched_routes":[]`) {
		t.Fatalf("JSON output = %s, want an empty matched_routes array", encoded)
	}
}
