package cli_test

import (
	"testing"

	"github.com/wbollock/alertmanager-route-tester/internal/alertmanager"
	"github.com/wbollock/alertmanager-route-tester/internal/cli"
)

// Simple tests that demonstrate the CLI mode uses exact same routing logic as web UI
// These tests verify against the sample Alertmanager config in testdata/alertmanager.yml

func TestSimpleCriticalRouting(t *testing.T) {
	// Test that critical alerts route to pagerduty-critical
	// Based on: testdata/alertmanager.yml line 12-14
	client := alertmanager.NewClient("http://localhost:9093", false)

	labels := map[string]string{
		"alertname": "HighCPU",
		"severity":  "critical",
	}

	result, err := cli.TestRouting(client, labels)
	if err != nil {
		t.Fatalf("Failed to test routing: %v", err)
	}

	if result.Error != "" {
		t.Fatalf("Routing test returned error: %s", result.Error)
	}

	// Should match the critical severity route
	if result.Receiver != "pagerduty-critical" {
		t.Errorf("Expected receiver 'pagerduty-critical', got '%s'", result.Receiver)
		t.Logf("Matched routes:")
		for i, route := range result.MatchedRoutes {
			t.Logf("  %d. receiver=%s match=%v", i+1, route.Receiver, route.Match)
		}
	}

	// Should have at least one matched route
	if len(result.MatchedRoutes) == 0 {
		t.Error("Expected to match at least one route, but matched none")
	}

	t.Logf("✓ Critical alert correctly routes to: %s", result.Receiver)
}

func TestSimpleDatabaseTeamRouting(t *testing.T) {
	// Test that database team alerts route correctly
	// Based on: testdata/alertmanager.yml line 63-72
	client := alertmanager.NewClient("http://localhost:9093", false)

	labels := map[string]string{
		"alertname": "PostgreSQLSlowQueries",
		"team":      "database",
	}

	result, err := cli.TestRouting(client, labels)
	if err != nil {
		t.Fatalf("Failed to test routing: %v", err)
	}

	if result.Error != "" {
		t.Fatalf("Routing test returned error: %s", result.Error)
	}

	// The final receiver should be team-database (or a child route)
	// Since the alert matches team:database and alertname matches PostgreSQL regex
	expectedReceivers := []string{"team-database", "slack-database-alerts"}

	matched := false
	for _, expected := range expectedReceivers {
		if result.Receiver == expected {
			matched = true
			break
		}
	}

	if !matched {
		t.Errorf("Expected receiver to be one of %v, got '%s'", expectedReceivers, result.Receiver)
		t.Logf("Matched routes:")
		for i, route := range result.MatchedRoutes {
			t.Logf("  %d. receiver=%s match=%v match_re=%v",
				i+1, route.Receiver, route.Match, route.MatchRE)
		}
	}

	// Verify the receiver config is retrieved
	if result.ReceiverConfig == nil {
		t.Error("Expected receiver configuration, got nil")
	} else {
		t.Logf("✓ Database team alert correctly routes to: %s", result.Receiver)
		t.Logf("  Receiver has %d webhook configs", len(result.ReceiverConfig.WebhookConfigs))
	}
}

func TestRoutingWithContinue(t *testing.T) {
	// Test that 'continue' routes work correctly - alerts match multiple routes
	// Based on: testdata/alertmanager.yml line 22-30
	// Warning alerts match severity:warning (continue=true) AND monitoring-team (continue=true)
	client := alertmanager.NewClient("http://localhost:9093", false)

	labels := map[string]string{
		"alertname": "DiskSpaceLow",
		"severity":  "warning",
		"team":      "monitoring",
	}

	result, err := cli.TestRouting(client, labels)
	if err != nil {
		t.Fatalf("Failed to test routing: %v", err)
	}

	if result.Error != "" {
		t.Fatalf("Routing test returned error: %s", result.Error)
	}

	// Should match multiple routes due to 'continue: true'
	if len(result.MatchedRoutes) < 2 {
		t.Errorf("Expected at least 2 matched routes (due to continue), got %d", len(result.MatchedRoutes))
	}

	// Final receiver should be monitoring-team (the last route with continue)
	if result.Receiver != "monitoring-team" {
		t.Errorf("Expected final receiver 'monitoring-team', got '%s'", result.Receiver)
	}

	t.Logf("✓ Warning alert matches %d routes (continue behavior works)", len(result.MatchedRoutes))
	for i, route := range result.MatchedRoutes {
		t.Logf("  %d. receiver=%s continue=%v", i+1, route.Receiver, route.Continue)
	}
}
