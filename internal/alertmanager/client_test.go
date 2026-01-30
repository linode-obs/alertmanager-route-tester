// ABOUTME: Tests for Alertmanager client and route matching functionality
// ABOUTME: Validates correct receiver resolution based on alert labels

package alertmanager

import (
	"testing"
)

func TestMatchesRoute(t *testing.T) {
	tests := []struct {
		name     string
		labels   map[string]string
		route    *Route
		expected bool
	}{
		{
			name: "exact match - single label",
			labels: map[string]string{
				"severity": "critical",
			},
			route: &Route{
				Match: map[string]string{
					"severity": "critical",
				},
			},
			expected: true,
		},
		{
			name: "exact match - multiple labels",
			labels: map[string]string{
				"severity": "critical",
				"team":     "platform",
			},
			route: &Route{
				Match: map[string]string{
					"severity": "critical",
					"team":     "platform",
				},
			},
			expected: true,
		},
		{
			name: "no match - wrong value",
			labels: map[string]string{
				"severity": "warning",
			},
			route: &Route{
				Match: map[string]string{
					"severity": "critical",
				},
			},
			expected: false,
		},
		{
			name: "no match - missing label",
			labels: map[string]string{
				"severity": "critical",
			},
			route: &Route{
				Match: map[string]string{
					"severity": "critical",
					"team":     "platform",
				},
			},
			expected: false,
		},
		{
			name: "regex match - simple pattern",
			labels: map[string]string{
				"service": "api",
			},
			route: &Route{
				MatchRE: map[string]string{
					"service": "^(api|web)$",
				},
			},
			expected: true,
		},
		{
			name: "regex match - complex pattern",
			labels: map[string]string{
				"instance": "prod-server-01",
			},
			route: &Route{
				MatchRE: map[string]string{
					"instance": "prod-.*",
				},
			},
			expected: true,
		},
		{
			name: "regex no match",
			labels: map[string]string{
				"service": "database",
			},
			route: &Route{
				MatchRE: map[string]string{
					"service": "^(api|web)$",
				},
			},
			expected: false,
		},
		{
			name: "combined match and match_re",
			labels: map[string]string{
				"severity": "critical",
				"service":  "api",
			},
			route: &Route{
				Match: map[string]string{
					"severity": "critical",
				},
				MatchRE: map[string]string{
					"service": "^(api|web)$",
				},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := matchesRoute(tt.labels, tt.route)
			if result != tt.expected {
				t.Errorf("matchesRoute() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestFindMatchingRoute(t *testing.T) {
	config := &Config{
		Route: &Route{
			Receiver: "default",
			Routes: []*Route{
				{
					Match: map[string]string{
						"severity": "critical",
					},
					Receiver: "pagerduty",
				},
				{
					Match: map[string]string{
						"severity": "warning",
					},
					Receiver: "slack",
					Continue: true,
				},
				{
					MatchRE: map[string]string{
						"service": "^(api|web)$",
					},
					Receiver: "platform-team",
					Routes: []*Route{
						{
							Match: map[string]string{
								"environment": "production",
							},
							Receiver: "pagerduty-platform",
						},
					},
				},
			},
		},
		Receivers: []Receiver{
			{Name: "default"},
			{Name: "pagerduty"},
			{Name: "slack"},
			{Name: "platform-team"},
			{Name: "pagerduty-platform"},
		},
	}

	client := &Client{}

	tests := []struct {
		name             string
		labels           map[string]string
		expectedReceiver string
		expectedRoutes   int
	}{
		{
			name: "critical alert routes to pagerduty",
			labels: map[string]string{
				"alertname": "HighErrorRate",
				"severity":  "critical",
			},
			expectedReceiver: "pagerduty",
			expectedRoutes:   1,
		},
		{
			name: "warning alert routes to slack",
			labels: map[string]string{
				"alertname": "HighLatency",
				"severity":  "warning",
			},
			expectedReceiver: "slack",
			expectedRoutes:   1,
		},
		{
			name: "api service routes to platform-team",
			labels: map[string]string{
				"alertname": "APIDown",
				"service":   "api",
			},
			expectedReceiver: "platform-team",
			expectedRoutes:   1,
		},
		{
			name: "production api routes to pagerduty-platform",
			labels: map[string]string{
				"alertname":   "APIDown",
				"service":     "api",
				"environment": "production",
			},
			expectedReceiver: "pagerduty-platform",
			expectedRoutes:   2,
		},
		{
			name: "no match uses default",
			labels: map[string]string{
				"alertname": "UnknownAlert",
			},
			expectedReceiver: "default",
			expectedRoutes:   1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			receiver, matchedRoutes, err := client.FindMatchingRoute(tt.labels, config)
			if err != nil {
				t.Fatalf("FindMatchingRoute() error = %v", err)
			}

			if receiver != tt.expectedReceiver {
				t.Errorf("FindMatchingRoute() receiver = %v, want %v", receiver, tt.expectedReceiver)
			}

			if len(matchedRoutes) != tt.expectedRoutes {
				t.Errorf("FindMatchingRoute() matched %d routes, want %d", len(matchedRoutes), tt.expectedRoutes)
			}
		})
	}
}

func TestExtractLabelKeys(t *testing.T) {
	config := &Config{
		Route: &Route{
			Match: map[string]string{
				"severity": "critical",
			},
			Routes: []*Route{
				{
					Match: map[string]string{
						"team": "platform",
					},
				},
				{
					MatchRE: map[string]string{
						"service": "^api$",
					},
					Routes: []*Route{
						{
							Match: map[string]string{
								"environment": "production",
							},
						},
					},
				},
			},
		},
	}

	keys := ExtractLabelKeys(config)

	expectedKeys := map[string]bool{
		"severity":    true,
		"team":        true,
		"service":     true,
		"environment": true,
	}

	if len(keys) != len(expectedKeys) {
		t.Errorf("ExtractLabelKeys() returned %d keys, want %d", len(keys), len(expectedKeys))
	}

	for _, key := range keys {
		if !expectedKeys[key] {
			t.Errorf("ExtractLabelKeys() returned unexpected key: %s", key)
		}
	}
}
