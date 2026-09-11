package alertmanager

import (
	"testing"
)

// TestEdgeCases covers additional edge cases and corner scenarios in routing
func TestEdgeCases(t *testing.T) {
	tests := []struct {
		name             string
		config           *Config
		labels           map[string]string
		expectedReceiver string
		expectedMatches  int
	}{
		{
			name: "empty labels match empty route",
			config: &Config{
				Route: &Route{
					Receiver: "default",
					Routes: []*Route{
						{Receiver: "catch-all"},
					},
				},
			},
			labels:           map[string]string{},
			expectedReceiver: "catch-all",
			expectedMatches:  1,
		},
		{
			name: "nil match and matchRE matches everything",
			config: &Config{
				Route: &Route{
					Receiver: "default",
					Routes: []*Route{
						{Receiver: "matches-all"},
					},
				},
			},
			labels:           map[string]string{"any": "label"},
			expectedReceiver: "matches-all",
			expectedMatches:  1,
		},
		{
			name: "route with only continue=true and no receiver",
			config: &Config{
				Route: &Route{
					Receiver: "default",
					Routes: []*Route{
						{
							Match:    map[string]string{"severity": "warning"},
							Continue: true,
						},
						{
							Match:    map[string]string{"team": "platform"},
							Receiver: "platform",
						},
					},
				},
			},
			labels:           map[string]string{"severity": "warning", "team": "platform"},
			expectedReceiver: "platform",
			expectedMatches:  2,
		},
		{
			name: "deeply nested routes (4 levels)",
			config: &Config{
				Route: &Route{
					Receiver: "default",
					Routes: []*Route{
						{
							Match:    map[string]string{"l1": "true"},
							Receiver: "level1",
							Routes: []*Route{
								{
									Match:    map[string]string{"l2": "true"},
									Receiver: "level2",
									Routes: []*Route{
										{
											Match:    map[string]string{"l3": "true"},
											Receiver: "level3",
											Routes: []*Route{
												{
													Match:    map[string]string{"l4": "true"},
													Receiver: "level4",
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
			labels:           map[string]string{"l1": "true", "l2": "true", "l3": "true", "l4": "true"},
			expectedReceiver: "level4",
			expectedMatches:  4,
		},
		{
			name: "multiple continue routes at same level",
			config: &Config{
				Route: &Route{
					Receiver: "default",
					Routes: []*Route{
						{
							Match:    map[string]string{"severity": "critical"},
							Receiver: "route1",
							Continue: true,
						},
						{
							Match:    map[string]string{"severity": "critical"},
							Receiver: "route2",
							Continue: true,
						},
						{
							Match:    map[string]string{"severity": "critical"},
							Receiver: "route3",
						},
					},
				},
			},
			labels:           map[string]string{"severity": "critical"},
			expectedReceiver: "route3",
			expectedMatches:  3,
		},
		{
			name: "regex matching with special characters",
			config: &Config{
				Route: &Route{
					Receiver: "default",
					Routes: []*Route{
						{
							MatchRE:  map[string]string{"service": `api\.(v1|v2)\.prod`},
							Receiver: "api-prod",
						},
					},
				},
			},
			labels:           map[string]string{"service": "api.v1.prod"},
			expectedReceiver: "api-prod",
			expectedMatches:  1,
		},
		{
			name: "matcher with unquoted value",
			config: &Config{
				Route: &Route{
					Receiver: "default",
					Routes: []*Route{
						{
							Matchers: []string{`severity=critical`},
							Receiver: "pagerduty",
						},
					},
				},
			},
			labels:           map[string]string{"severity": "critical"},
			expectedReceiver: "pagerduty",
			expectedMatches:  1,
		},
		{
			name: "matcher with single quotes",
			config: &Config{
				Route: &Route{
					Receiver: "default",
					Routes: []*Route{
						{
							Matchers: []string{`severity='critical'`},
							Receiver: "pagerduty",
						},
					},
				},
			},
			labels:           map[string]string{"severity": "critical"},
			expectedReceiver: "pagerduty",
			expectedMatches:  1,
		},
		{
			name: "negative matcher - label absent",
			config: &Config{
				Route: &Route{
					Receiver: "default",
					Routes: []*Route{
						{
							Matchers: []string{`severity!="critical"`},
							Receiver: "non-critical",
						},
					},
				},
			},
			labels:           map[string]string{"alertname": "test"},
			expectedReceiver: "non-critical",
			expectedMatches:  1,
		},
		{
			name: "negative regex matcher - no match",
			config: &Config{
				Route: &Route{
					Receiver: "default",
					Routes: []*Route{
						{
							Matchers: []string{`env!~"^(prod|production)$"`},
							Receiver: "non-prod",
						},
					},
				},
			},
			labels:           map[string]string{"env": "staging"},
			expectedReceiver: "non-prod",
			expectedMatches:  1,
		},
		{
			name: "combined match, match_re, and matchers",
			config: &Config{
				Route: &Route{
					Receiver: "default",
					Routes: []*Route{
						{
							Match:    map[string]string{"team": "platform"},
							MatchRE:  map[string]string{"service": "^api"},
							Matchers: []string{`severity="critical"`},
							Receiver: "platform-critical",
						},
					},
				},
			},
			labels:           map[string]string{"team": "platform", "service": "api-gateway", "severity": "critical"},
			expectedReceiver: "platform-critical",
			expectedMatches:  1,
		},
		{
			name: "partial match fails (all conditions must match)",
			config: &Config{
				Route: &Route{
					Receiver: "default",
					Routes: []*Route{
						{
							Match:    map[string]string{"team": "platform"},
							MatchRE:  map[string]string{"service": "^api"},
							Matchers: []string{`severity="critical"`},
							Receiver: "platform-critical",
						},
					},
				},
			},
			labels:           map[string]string{"team": "platform", "service": "api-gateway"},
			expectedReceiver: "default",
			expectedMatches:  0,
		},
		{
			name: "empty string label value matches",
			config: &Config{
				Route: &Route{
					Receiver: "default",
					Routes: []*Route{
						{
							Match:    map[string]string{"label": ""},
							Receiver: "empty-match",
						},
					},
				},
			},
			labels:           map[string]string{"label": ""},
			expectedReceiver: "empty-match",
			expectedMatches:  1,
		},
		{
			name: "label with special characters in value",
			config: &Config{
				Route: &Route{
					Receiver: "default",
					Routes: []*Route{
						{
							Match:    map[string]string{"annotation": "https://example.com/alert?id=123&severity=high"},
							Receiver: "special-chars",
						},
					},
				},
			},
			labels:           map[string]string{"annotation": "https://example.com/alert?id=123&severity=high"},
			expectedReceiver: "special-chars",
			expectedMatches:  1,
		},
		{
			name: "route with all timing fields",
			config: &Config{
				Route: &Route{
					Receiver: "default",
					Routes: []*Route{
						{
							Match:          map[string]string{"team": "platform"},
							Receiver:       "platform",
							GroupWait:      "30s",
							GroupInterval:  "5m",
							RepeatInterval: "4h",
						},
					},
				},
			},
			labels:           map[string]string{"team": "platform"},
			expectedReceiver: "platform",
			expectedMatches:  1,
		},
		{
			name: "route with group_by field",
			config: &Config{
				Route: &Route{
					Receiver: "default",
					Routes: []*Route{
						{
							Match:    map[string]string{"alertname": "HighCPU"},
							Receiver: "cpu-alerts",
							GroupBy:  []string{"instance", "job"},
						},
					},
				},
			},
			labels:           map[string]string{"alertname": "HighCPU"},
			expectedReceiver: "cpu-alerts",
			expectedMatches:  1,
		},
	}

	client := &Client{}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			receiver, matched, err := client.FindMatchingRoute(tt.labels, tt.config)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if receiver != tt.expectedReceiver {
				t.Errorf("receiver = %q, want %q", receiver, tt.expectedReceiver)
				for i, mr := range matched {
					t.Logf("  matched[%d] receiver=%q depth=%d", i, mr.Route.Receiver, mr.Depth)
				}
			}

			if len(matched) != tt.expectedMatches {
				t.Errorf("matched %d routes, want %d", len(matched), tt.expectedMatches)
			}
		})
	}
}

// TestMatcherEdgeCases tests edge cases in matcher parsing and evaluation
func TestMatcherEdgeCases(t *testing.T) {
	tests := []struct {
		name           string
		matcher        string
		labels         map[string]string
		expectedMatch  bool
		expectedParsed bool
	}{
		{
			name:           "matcher with spaces around operator",
			matcher:        `  severity  =  "critical"  `,
			labels:         map[string]string{"severity": "critical"},
			expectedMatch:  true,
			expectedParsed: true,
		},
		{
			name:           "matcher with value containing equals",
			matcher:        `query="SELECT * FROM users WHERE id=123"`,
			labels:         map[string]string{"query": "SELECT * FROM users WHERE id=123"},
			expectedMatch:  true,
			expectedParsed: true,
		},
		{
			name:           "matcher with value containing regex special chars",
			matcher:        `hostname=~".*\.example\.com$"`,
			labels:         map[string]string{"hostname": "api.example.com"},
			expectedMatch:  true,
			expectedParsed: true,
		},
		{
			name:           "invalid matcher - no operator",
			matcher:        `invalidmatcher`,
			labels:         map[string]string{},
			expectedMatch:  false,
			expectedParsed: false,
		},
		{
			name:           "invalid matcher - starts with operator",
			matcher:        `="value"`,
			labels:         map[string]string{},
			expectedMatch:  false,
			expectedParsed: false,
		},
		{
			name:           "matcher with unicode characters",
			matcher:        `description="🔥 Critical Alert 🚨"`,
			labels:         map[string]string{"description": "🔥 Critical Alert 🚨"},
			expectedMatch:  true,
			expectedParsed: true,
		},
		{
			name:           "negative matcher matches when label missing",
			matcher:        `optional_label!="value"`,
			labels:         map[string]string{"other": "label"},
			expectedMatch:  true,
			expectedParsed: true,
		},
		{
			name:           "regex matcher with anchors",
			matcher:        `service=~"^(api|web|worker)$"`,
			labels:         map[string]string{"service": "api"},
			expectedMatch:  true,
			expectedParsed: true,
		},
		{
			name:           "regex matcher partial match fails with anchors",
			matcher:        `service=~"^(api|web)$"`,
			labels:         map[string]string{"service": "api-gateway"},
			expectedMatch:  false,
			expectedParsed: true,
		},
		{
			name:           "negative regex matcher with missing label",
			matcher:        `env!~"^prod"`,
			labels:         map[string]string{},
			expectedMatch:  true,
			expectedParsed: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test parsing
			parsed, ok := parseMatcher(tt.matcher)
			if ok != tt.expectedParsed {
				t.Errorf("parseMatcher ok = %v, want %v", ok, tt.expectedParsed)
				return
			}

			if !ok {
				return
			}

			// Test matching
			route := &Route{
				Matchers: []string{tt.matcher},
			}

			matched := matchesRoute(tt.labels, route)
			if matched != tt.expectedMatch {
				t.Errorf("matchesRoute = %v, want %v (parsed: %+v)", matched, tt.expectedMatch, parsed)
			}
		})
	}
}

// TestContinueSemantics tests the behavior of continue flag in routing
func TestContinueSemantics(t *testing.T) {
	tests := []struct {
		name            string
		config          *Config
		labels          map[string]string
		expectedFinal   string
		expectedMatches []string // ordered list of matched receiver names
	}{
		{
			name: "continue=true allows next route to match",
			config: &Config{
				Route: &Route{
					Receiver: "default",
					Routes: []*Route{
						{
							Match:    map[string]string{"severity": "warning"},
							Receiver: "first",
							Continue: true,
						},
						{
							Match:    map[string]string{"team": "platform"},
							Receiver: "second",
						},
					},
				},
			},
			labels:          map[string]string{"severity": "warning", "team": "platform"},
			expectedFinal:   "second",
			expectedMatches: []string{"first", "second"},
		},
		{
			name: "continue=false stops evaluation",
			config: &Config{
				Route: &Route{
					Receiver: "default",
					Routes: []*Route{
						{
							Match:    map[string]string{"severity": "critical"},
							Receiver: "first",
							Continue: false,
						},
						{
							Match:    map[string]string{"severity": "critical"},
							Receiver: "second",
						},
					},
				},
			},
			labels:          map[string]string{"severity": "critical"},
			expectedFinal:   "first",
			expectedMatches: []string{"first"},
		},
		{
			name: "multiple continue=true routes",
			config: &Config{
				Route: &Route{
					Receiver: "default",
					Routes: []*Route{
						{
							Match:    map[string]string{"env": "prod"},
							Receiver: "r1",
							Continue: true,
						},
						{
							Match:    map[string]string{"env": "prod"},
							Receiver: "r2",
							Continue: true,
						},
						{
							Match:    map[string]string{"env": "prod"},
							Receiver: "r3",
							Continue: true,
						},
						{
							Match:    map[string]string{"env": "prod"},
							Receiver: "r4",
						},
					},
				},
			},
			labels:          map[string]string{"env": "prod"},
			expectedFinal:   "r4",
			expectedMatches: []string{"r1", "r2", "r3", "r4"},
		},
		{
			name: "continue in nested route",
			config: &Config{
				Route: &Route{
					Receiver: "default",
					Routes: []*Route{
						{
							Match:    map[string]string{"team": "platform"},
							Receiver: "platform",
							Routes: []*Route{
								{
									Match:    map[string]string{"severity": "warning"},
									Receiver: "platform-warning",
									Continue: true,
								},
								{
									Match:    map[string]string{"severity": "warning"},
									Receiver: "platform-warning-escalation",
								},
							},
						},
					},
				},
			},
			labels:          map[string]string{"team": "platform", "severity": "warning"},
			expectedFinal:   "platform-warning-escalation",
			expectedMatches: []string{"platform", "platform-warning", "platform-warning-escalation"},
		},
	}

	client := &Client{}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			receiver, matched, err := client.FindMatchingRoute(tt.labels, tt.config)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if receiver != tt.expectedFinal {
				t.Errorf("final receiver = %q, want %q", receiver, tt.expectedFinal)
			}

			if len(matched) != len(tt.expectedMatches) {
				t.Errorf("matched %d routes, want %d", len(matched), len(tt.expectedMatches))
			}

			for i, expectedRcv := range tt.expectedMatches {
				if i >= len(matched) {
					break
				}
				if matched[i].Route.Receiver != expectedRcv {
					t.Errorf("matched[%d].Receiver = %q, want %q", i, matched[i].Route.Receiver, expectedRcv)
				}
			}
		})
	}
}
