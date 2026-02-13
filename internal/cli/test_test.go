package cli_test

import (
	"testing"

	"github.com/wbollock/alertmanager-route-tester/internal/alertmanager"
	"github.com/wbollock/alertmanager-route-tester/internal/cli"
)

// These tests demonstrate how to use the CLI package to verify alert routing
// in Go tests. They use the exact same logic as the web UI.
//
// To run these tests, you need a running Alertmanager instance.
// Use: mise run test

func TestCriticalAlertsRouting(t *testing.T) {
	// This test verifies that critical alerts always go to the correct receiver
	client := alertmanager.NewClient("http://localhost:9093", false)

	testCases := []struct {
		name             string
		labels           map[string]string
		expectedReceiver string
		shouldMatch      bool
	}{
		{
			name: "critical severity should route to pagerduty-critical",
			labels: map[string]string{
				"alertname": "HighErrorRate",
				"severity":  "critical",
			},
			expectedReceiver: "pagerduty-critical",
			shouldMatch:      true,
		},
		{
			name: "critical security alert should route to pagerduty-security",
			labels: map[string]string{
				"alertname": "SecurityBreach",
				"category":  "security",
				"severity":  "critical",
			},
			expectedReceiver: "pagerduty-security",
			shouldMatch:      true,
		},
		{
			name: "warning severity should NOT route to pagerduty",
			labels: map[string]string{
				"alertname": "DiskSpaceLow",
				"severity":  "warning",
			},
			expectedReceiver: "slack-warnings",
			shouldMatch:      true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := cli.TestRouting(client, tc.labels)
			if err != nil {
				t.Fatalf("Failed to test routing: %v", err)
			}

			if result.Error != "" {
				t.Fatalf("Routing test returned error: %s", result.Error)
			}

			if result.Receiver != tc.expectedReceiver {
				t.Errorf("Expected receiver %q, got %q", tc.expectedReceiver, result.Receiver)
				t.Logf("Matched routes: %+v", result.MatchedRoutes)
			}

			if tc.shouldMatch && len(result.MatchedRoutes) == 0 {
				t.Error("Expected to match at least one route, but matched none")
			}
		})
	}
}

func TestTeamBasedRouting(t *testing.T) {
	// This test ensures team-based routing works correctly
	client := alertmanager.NewClient("http://localhost:9093", false)

	testCases := []struct {
		name             string
		labels           map[string]string
		expectedReceiver string
	}{
		{
			name: "database team alert",
			labels: map[string]string{
				"alertname": "SlowQueries",
				"team":      "database",
			},
			expectedReceiver: "team-database",
		},
		{
			name: "frontend team alert",
			labels: map[string]string{
				"alertname": "JSError",
				"team":      "frontend",
			},
			expectedReceiver: "team-frontend",
		},
		{
			name: "infrastructure west region",
			labels: map[string]string{
				"alertname": "HighCPU",
				"component": "infrastructure",
				"region":    "us-west-2",
			},
			expectedReceiver: "team-infra-west",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := cli.TestRouting(client, tc.labels)
			if err != nil {
				t.Fatalf("Failed to test routing: %v", err)
			}

			if result.Receiver != tc.expectedReceiver {
				t.Errorf("Expected receiver %q, got %q", tc.expectedReceiver, result.Receiver)
			}
		})
	}
}

func TestComplexRoutingWithContinue(t *testing.T) {
	// This test verifies that 'continue' routing works correctly
	// Some alerts should match multiple routes
	client := alertmanager.NewClient("http://localhost:9093", false)

	testCases := []struct {
		name              string
		labels            map[string]string
		expectedReceiver  string
		minMatchedRoutes  int
		routeDescriptions []string
	}{
		{
			name: "security alert with continue should match multiple routes",
			labels: map[string]string{
				"alertname": "SecurityIssue",
				"category":  "security",
				"severity":  "critical",
			},
			expectedReceiver: "pagerduty-security",
			minMatchedRoutes: 2, // Should match both security and critical routes
			routeDescriptions: []string{
				"security route (with continue)",
				"critical route",
			},
		},
		{
			name: "warning alert with continue",
			labels: map[string]string{
				"alertname": "DiskSpace",
				"severity":  "warning",
				"service":   "web",
			},
			expectedReceiver: "slack-warnings",
			minMatchedRoutes: 1,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := cli.TestRouting(client, tc.labels)
			if err != nil {
				t.Fatalf("Failed to test routing: %v", err)
			}

			if result.Receiver != tc.expectedReceiver {
				t.Errorf("Expected final receiver %q, got %q", tc.expectedReceiver, result.Receiver)
			}

			if len(result.MatchedRoutes) < tc.minMatchedRoutes {
				t.Errorf("Expected at least %d matched routes, got %d",
					tc.minMatchedRoutes, len(result.MatchedRoutes))
				t.Logf("Matched routes: %+v", result.MatchedRoutes)
			}
		})
	}
}

func TestDefaultReceiverFallback(t *testing.T) {
	// This test verifies that alerts with no matching routes fall back to default receiver
	client := alertmanager.NewClient("http://localhost:9093", false)

	labels := map[string]string{
		"alertname": "UnknownAlert",
		"random":    "label",
		"priority":  "low",
	}

	result, err := cli.TestRouting(client, labels)
	if err != nil {
		t.Fatalf("Failed to test routing: %v", err)
	}

	if result.Receiver == "" {
		t.Error("Expected a default receiver, got empty string")
	}

	// The default receiver should be the root route's receiver
	if result.Receiver != "team-platform" {
		t.Logf("Warning: Default receiver is %q, expected 'team-platform'", result.Receiver)
	}

	t.Logf("Default receiver: %s", result.Receiver)
	t.Logf("Matched routes: %d", len(result.MatchedRoutes))
}

func TestReceiverConfiguration(t *testing.T) {
	// This test verifies that receiver configurations are correctly retrieved
	client := alertmanager.NewClient("http://localhost:9093", false)

	labels := map[string]string{
		"alertname": "TestAlert",
		"severity":  "critical",
	}

	result, err := cli.TestRouting(client, labels)
	if err != nil {
		t.Fatalf("Failed to test routing: %v", err)
	}

	if result.ReceiverConfig == nil {
		t.Fatal("Expected receiver configuration, got nil")
	}

	if result.ReceiverConfig.Name != result.Receiver {
		t.Errorf("Receiver config name %q doesn't match receiver %q",
			result.ReceiverConfig.Name, result.Receiver)
	}

	// Log receiver details for debugging
	t.Logf("Receiver: %s", result.ReceiverConfig.Name)
	t.Logf("Email configs: %d", len(result.ReceiverConfig.EmailConfigs))
	t.Logf("Slack configs: %d", len(result.ReceiverConfig.SlackConfigs))
	t.Logf("Pagerduty configs: %d", len(result.ReceiverConfig.PagerdutyConfigs))
	t.Logf("Webhook configs: %d", len(result.ReceiverConfig.WebhookConfigs))
}

func TestRegexMatching(t *testing.T) {
	// This test verifies regex-based routing (match_re)
	client := alertmanager.NewClient("http://localhost:9093", false)

	testCases := []struct {
		name             string
		labels           map[string]string
		expectedReceiver string
		shouldMatch      bool
	}{
		{
			name: "environment production should match regex",
			labels: map[string]string{
				"alertname":   "HighLatency",
				"service":     "web",
				"environment": "production",
			},
			expectedReceiver: "slack-platform-prod",
			shouldMatch:      true,
		},
		{
			name: "environment staging should match regex",
			labels: map[string]string{
				"alertname":   "HighLatency",
				"service":     "api",
				"environment": "staging",
			},
			expectedReceiver: "slack-platform-nonprod",
			shouldMatch:      true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := cli.TestRouting(client, tc.labels)
			if err != nil {
				t.Fatalf("Failed to test routing: %v", err)
			}

			if tc.shouldMatch {
				if result.Receiver != tc.expectedReceiver {
					t.Errorf("Expected receiver %q, got %q", tc.expectedReceiver, result.Receiver)
				}
			}
		})
	}
}

// BenchmarkRouting benchmarks the routing logic
func BenchmarkRouting(b *testing.B) {
	client := alertmanager.NewClient("http://localhost:9093", false)

	labels := map[string]string{
		"alertname": "HighCPU",
		"severity":  "critical",
		"service":   "web",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := cli.TestRouting(client, labels)
		if err != nil {
			b.Fatalf("Failed to test routing: %v", err)
		}
	}
}
