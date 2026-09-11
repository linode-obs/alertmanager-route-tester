package cli_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/wbollock/alertmanager-route-tester/internal/alertmanager"
	"github.com/wbollock/alertmanager-route-tester/internal/cli"
)

func TestMatchedRouteJSONUsesStableFieldNames(t *testing.T) {
	result := cli.TestResult{
		MatchedRoutes: []alertmanager.MatchedRoute{
			{
				Route:           &alertmanager.Route{Receiver: "pagerduty"},
				Depth:           1,
				ParentReceivers: []string{"platform"},
				IsSubroute:      true,
			},
		},
	}

	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	output := string(encoded)
	for _, field := range []string{`"route"`, `"depth"`, `"parent_receivers"`, `"is_subroute"`} {
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
