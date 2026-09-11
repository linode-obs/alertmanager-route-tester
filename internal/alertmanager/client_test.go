package alertmanager

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestFindMatchingRouteReturnsEmptySliceForRootFallback(t *testing.T) {
	receiver, matched, err := (&Client{}).FindMatchingRoute(
		map[string]string{"alertname": "unknown"},
		&Config{Route: &Route{Receiver: "default"}},
	)
	if err != nil {
		t.Fatalf("FindMatchingRoute() error = %v", err)
	}
	if receiver != "default" {
		t.Fatalf("receiver = %q, want default", receiver)
	}
	if matched == nil {
		t.Fatal("matched routes = nil, want an empty slice")
	}
	if len(matched) != 0 {
		t.Fatalf("matched routes = %d, want 0", len(matched))
	}
}

func TestReceiverlessContinueRouteInheritsParentReceiver(t *testing.T) {
	config := &Config{
		Route: &Route{
			Receiver: "default",
			Routes: []*Route{
				{Match: map[string]string{"severity": "warning"}, Receiver: "platform", Continue: true},
				{Match: map[string]string{"severity": "warning"}},
			},
		},
	}

	receiver, matched, err := (&Client{}).FindMatchingRoute(map[string]string{"severity": "warning"}, config)
	if err != nil {
		t.Fatalf("FindMatchingRoute() error = %v", err)
	}
	if receiver != "default" {
		t.Fatalf("receiver = %q, want inherited default", receiver)
	}
	if matched[0].ResolvedReceiver != "platform" || matched[1].ResolvedReceiver != "default" {
		t.Fatalf("resolved receivers = %#v, want platform and default", matched)
	}
	if len(matched) != 2 || !matched[0].IsEffective || !matched[1].IsEffective {
		t.Fatalf("matched routes = %#v, want both routes effective", matched)
	}
}

func TestMatchedRoutesMarkResolvedBranches(t *testing.T) {
	config := &Config{
		Route: &Route{
			Receiver: "default",
			Routes: []*Route{
				{
					Match:    map[string]string{"team": "platform"},
					Receiver: "platform",
					Continue: true,
					Routes: []*Route{
						{Match: map[string]string{"severity": "warning"}},
					},
				},
				{Match: map[string]string{"team": "platform"}, Receiver: "fallback"},
			},
		},
	}
	labels := map[string]string{"team": "platform", "severity": "warning"}

	receiver, matched, err := (&Client{}).FindMatchingRoute(labels, config)
	if err != nil {
		t.Fatalf("FindMatchingRoute() error = %v", err)
	}
	if receiver != "fallback" {
		t.Fatalf("receiver = %q, want fallback", receiver)
	}
	if len(matched) != 3 {
		t.Fatalf("matched routes = %d, want 3", len(matched))
	}
	if matched[0].IsEffective {
		t.Error("parent route should not be effective when its receiver-less child inherits the receiver")
	}
	if !matched[1].IsEffective {
		t.Error("receiver-less child should be effective after inheriting the parent receiver")
	}
	if !matched[2].IsEffective {
		t.Error("sibling fallback route should be effective")
	}
}

func TestGetConfigWithStatusReportsConcurrentCacheHit(t *testing.T) {
	fetchStarted := make(chan struct{})
	releaseFetch := make(chan struct{})
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			close(fetchStarted)
			<-releaseFetch
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"config":{"original":"route:\n  receiver: default\nreceivers:\n  - name: default\n"}}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, false)
	type result struct{ hit bool }
	results := make(chan result, 2)
	for i := 0; i < 2; i++ {
		go func() {
			_, hit, err := client.GetConfigWithStatus()
			if err != nil {
				t.Errorf("GetConfigWithStatus() error = %v", err)
			}
			results <- result{hit: hit}
		}()
	}
	<-fetchStarted
	close(releaseFetch)

	first := <-results
	second := <-results
	if first.hit == second.hit {
		t.Fatalf("cache-hit results = %v and %v, want one fetch and one cache hit", first.hit, second.hit)
	}
}

func TestCachedReaderDoesNotBlockDuringRefresh(t *testing.T) {
	refreshStarted := make(chan struct{})
	releaseRefresh := make(chan struct{})
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 2 {
			close(refreshStarted)
			<-releaseRefresh
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"config":{"original":"route:\n  receiver: default\nreceivers:\n  - name: default\n"}}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, false)
	if _, err := client.GetConfig(); err != nil {
		t.Fatalf("initial GetConfig() error = %v", err)
	}

	refreshResult := make(chan error, 1)
	go func() {
		_, err := client.RefreshConfig()
		refreshResult <- err
	}()
	<-refreshStarted

	readerResult := make(chan error, 1)
	go func() {
		config, err := client.GetConfig()
		if err == nil && (config == nil || config.Route == nil || config.Route.Receiver != "default") {
			err = fmt.Errorf("unexpected cached config: %#v", config)
		}
		readerResult <- err
	}()

	select {
	case err := <-readerResult:
		if err != nil {
			t.Fatalf("cached GetConfig() error = %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("cached GetConfig() blocked during refresh")
	}

	close(releaseRefresh)
	if err := <-refreshResult; err != nil {
		t.Fatalf("RefreshConfig() error = %v", err)
	}
}

func TestGetConfigSerializesConcurrentCacheMisses(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"config":{"original":"route:\n  receiver: default\nreceivers:\n  - name: default\n"}}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, false)
	const goroutines = 20
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := client.GetConfig(); err != nil {
				t.Errorf("GetConfig() error = %v", err)
			}
		}()
	}
	wg.Wait()

	if got := requests.Load(); got != 1 {
		t.Fatalf("status requests = %d, want 1", got)
	}
}

func TestGetConfigDoesNotPublishFetchStartedBeforeInvalidation(t *testing.T) {
	fetchStarted := make(chan struct{})
	releaseFetch := make(chan struct{})
	var requests atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			close(fetchStarted)
			<-releaseFetch
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"config":{"original":"route:\n  receiver: stale\nreceivers:\n  - name: stale\n"}}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, false)
	result := make(chan error, 1)
	go func() {
		_, err := client.GetConfig()
		result <- err
	}()

	<-fetchStarted
	invalidated := make(chan struct{})
	go func() {
		client.InvalidateConfig()
		close(invalidated)
	}()
	close(releaseFetch)
	if err := <-result; err != nil {
		t.Fatalf("initial GetConfig() error = %v", err)
	}
	<-invalidated

	if _, err := client.GetConfig(); err != nil {
		t.Fatalf("GetConfig() after invalidation error = %v", err)
	}
	if got := requests.Load(); got != 2 {
		t.Fatalf("status requests = %d, want 2", got)
	}
}

// ---------------------------------------------------------------------------
// matchesRoute tests
// ---------------------------------------------------------------------------

func TestMatchesRoute(t *testing.T) {
	tests := []struct {
		name     string
		labels   map[string]string
		route    *Route
		expected bool
	}{
		{
			name:     "exact match - single label",
			labels:   map[string]string{"severity": "critical"},
			route:    &Route{Match: map[string]string{"severity": "critical"}},
			expected: true,
		},
		{
			name:     "exact match - multiple labels",
			labels:   map[string]string{"severity": "critical", "team": "platform"},
			route:    &Route{Match: map[string]string{"severity": "critical", "team": "platform"}},
			expected: true,
		},
		{
			name:     "no match - wrong value",
			labels:   map[string]string{"severity": "warning"},
			route:    &Route{Match: map[string]string{"severity": "critical"}},
			expected: false,
		},
		{
			name:     "no match - missing label",
			labels:   map[string]string{"severity": "critical"},
			route:    &Route{Match: map[string]string{"severity": "critical", "team": "platform"}},
			expected: false,
		},
		{
			name:     "regex match - simple pattern",
			labels:   map[string]string{"service": "api"},
			route:    &Route{MatchRE: map[string]string{"service": "^(api|web)$"}},
			expected: true,
		},
		{
			name:     "regex match - complex pattern",
			labels:   map[string]string{"instance": "prod-server-01"},
			route:    &Route{MatchRE: map[string]string{"instance": "prod-.*"}},
			expected: true,
		},
		{
			name:     "regex no match",
			labels:   map[string]string{"service": "database"},
			route:    &Route{MatchRE: map[string]string{"service": "^(api|web)$"}},
			expected: false,
		},
		{
			name:     "regex - missing label fails",
			labels:   map[string]string{"alertname": "test"},
			route:    &Route{MatchRE: map[string]string{"service": "^api$"}},
			expected: false,
		},
		{
			name:   "combined match and match_re",
			labels: map[string]string{"severity": "critical", "service": "api"},
			route: &Route{
				Match:   map[string]string{"severity": "critical"},
				MatchRE: map[string]string{"service": "^(api|web)$"},
			},
			expected: true,
		},
		{
			name:     "combined - match fails",
			labels:   map[string]string{"severity": "warning", "service": "api"},
			route:    &Route{Match: map[string]string{"severity": "critical"}, MatchRE: map[string]string{"service": "^api$"}},
			expected: false,
		},
		{
			name:     "empty route matches everything",
			labels:   map[string]string{"alertname": "anything"},
			route:    &Route{},
			expected: true,
		},
		{
			name:     "matcher equals - match",
			labels:   map[string]string{"severity": "critical"},
			route:    &Route{Matchers: []string{`severity="critical"`}},
			expected: true,
		},
		{
			name:     "matcher equals - no match",
			labels:   map[string]string{"severity": "warning"},
			route:    &Route{Matchers: []string{`severity="critical"`}},
			expected: false,
		},
		{
			name:     "matcher not equals - match",
			labels:   map[string]string{"severity": "warning"},
			route:    &Route{Matchers: []string{`severity!="critical"`}},
			expected: true,
		},
		{
			name:     "matcher not equals - no match",
			labels:   map[string]string{"severity": "critical"},
			route:    &Route{Matchers: []string{`severity!="critical"`}},
			expected: false,
		},
		{
			name:     "matcher not equals - missing label is truthy",
			labels:   map[string]string{"alertname": "test"},
			route:    &Route{Matchers: []string{`severity!="critical"`}},
			expected: true,
		},
		{
			name:     "matcher regex - match",
			labels:   map[string]string{"service": "api"},
			route:    &Route{Matchers: []string{`service=~"^(api|web)$"`}},
			expected: true,
		},
		{
			name:     "matcher regex - no match",
			labels:   map[string]string{"service": "database"},
			route:    &Route{Matchers: []string{`service=~"^(api|web)$"`}},
			expected: false,
		},
		{
			name:     "matcher negative regex - match (not in set)",
			labels:   map[string]string{"service": "database"},
			route:    &Route{Matchers: []string{`service!~"^(api|web)$"`}},
			expected: true,
		},
		{
			name:     "matcher negative regex - no match (is in set)",
			labels:   map[string]string{"service": "api"},
			route:    &Route{Matchers: []string{`service!~"^(api|web)$"`}},
			expected: false,
		},
		{
			name:     "matcher negative regex - missing label is truthy",
			labels:   map[string]string{"alertname": "test"},
			route:    &Route{Matchers: []string{`service!~"^api$"`}},
			expected: true,
		},
		{
			name:   "multiple matchers - all must pass",
			labels: map[string]string{"severity": "critical", "team": "platform"},
			route: &Route{Matchers: []string{
				`severity="critical"`,
				`team="platform"`,
			}},
			expected: true,
		},
		{
			name:   "multiple matchers - one fails",
			labels: map[string]string{"severity": "warning", "team": "platform"},
			route: &Route{Matchers: []string{
				`severity="critical"`,
				`team="platform"`,
			}},
			expected: false,
		},
		{
			name:     "invalid matcher returns false",
			labels:   map[string]string{"severity": "critical"},
			route:    &Route{Matchers: []string{"not_a_valid_matcher"}},
			expected: false,
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

// ---------------------------------------------------------------------------
// parseMatcher tests
// ---------------------------------------------------------------------------

func TestParseMatcher(t *testing.T) {
	tests := []struct {
		input     string
		wantOK    bool
		wantLabel string
		wantOp    string
		wantValue string
	}{
		{`severity="critical"`, true, "severity", "=", "critical"},
		{`severity!="critical"`, true, "severity", "!=", "critical"},
		{`service=~"^api$"`, true, "service", "=~", "^api$"},
		{`service!~"^api$"`, true, "service", "!~", "^api$"},
		{`severity='critical'`, true, "severity", "=", "critical"},      // single-quote stripping
		{`severity=critical`, true, "severity", "=", "critical"},        // unquoted value
		{`  severity = "critical" `, true, "severity", "=", "critical"}, // whitespace
		{`notavalidmatcher`, false, "", "", ""},
		{`=value`, false, "", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, ok := parseMatcher(tt.input)
			if ok != tt.wantOK {
				t.Fatalf("parseMatcher(%q) ok = %v, want %v", tt.input, ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if got.Label != tt.wantLabel {
				t.Errorf("Label = %q, want %q", got.Label, tt.wantLabel)
			}
			if got.Operator != tt.wantOp {
				t.Errorf("Operator = %q, want %q", got.Operator, tt.wantOp)
			}
			if got.Value != tt.wantValue {
				t.Errorf("Value = %q, want %q", got.Value, tt.wantValue)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// FindMatchingRoute – comprehensive table-driven tests
// ---------------------------------------------------------------------------

// buildTestConfig returns a Config that mirrors testdata/alertmanager.yml so
// all routing scenarios can be tested offline.
func buildTestConfig() *Config {
	return &Config{
		Route: &Route{
			Receiver: "default",
			Routes: []*Route{
				// Critical → pagerduty-critical (no continue)
				{
					Matchers: []string{`severity="critical"`},
					Receiver: "pagerduty-critical",
				},
				// High priority → slack-high-priority (continue)
				{
					Matchers: []string{`severity="high"`},
					Receiver: "slack-high-priority",
					Continue: true,
				},
				// Warning → slack-warnings (continue)
				{
					Matchers: []string{`severity="warning"`},
					Receiver: "slack-warnings",
					Continue: true,
				},
				// Monitoring team → monitoring-team (continue)
				{
					Matchers: []string{`team="monitoring"`},
					Receiver: "monitoring-team",
					Continue: true,
				},
				// Infrastructure → regional sub-routes
				{
					Match:    map[string]string{"component": "infrastructure"},
					Receiver: "", // parent has no receiver of its own
					Routes: []*Route{
						{Match: map[string]string{"region": "us-east-1"}, Receiver: "team-infra-east"},
						{Match: map[string]string{"region": "us-west-2"}, Receiver: "team-infra-west"},
						{Match: map[string]string{"region": "eu-west-1"}, Receiver: "team-infra-eu"},
					},
				},
				// Platform team → service regex + environment sub-routes
				{
					Matchers: []string{`service=~"^(api|web|frontend)$"`},
					Receiver: "team-platform",
					Routes: []*Route{
						{
							Match:    map[string]string{"environment": "production", "severity": "critical"},
							Receiver: "pagerduty-platform",
						},
						{
							Match:    map[string]string{"environment": "production"},
							Receiver: "slack-platform-prod",
						},
						{
							Match:    map[string]string{"environment": "staging"},
							Receiver: "slack-platform-staging",
						},
					},
				},
				// Database team
				{
					Match:    map[string]string{"team": "database"},
					Receiver: "team-database",
					Routes: []*Route{
						{Match: map[string]string{"severity": "critical"}, Receiver: "pagerduty-database"},
						{MatchRE: map[string]string{"alertname": `^(PostgreSQL|MySQL|Redis).*`}, Receiver: "slack-database-alerts"},
					},
				},
				// Security (continue) + critical sub-route
				{
					Match:    map[string]string{"category": "security"},
					Receiver: "security-team",
					Continue: true,
					Routes: []*Route{
						{Match: map[string]string{"severity": "critical"}, Receiver: "pagerduty-security"},
					},
				},
				// Customer-facing
				{
					Match: map[string]string{"customer_facing": "true"},
					Routes: []*Route{
						{Match: map[string]string{"severity": "critical"}, Receiver: "pagerduty-customer"},
						{Match: map[string]string{"severity": "warning"}, Receiver: "slack-customer-alerts"},
					},
				},
				// Observability jobs
				{
					MatchRE:  map[string]string{"job": `^(prometheus|grafana|alertmanager)$`},
					Receiver: "team-observability",
					Routes: []*Route{
						{Match: map[string]string{"severity": "critical"}, Receiver: "pagerduty-observability"},
					},
				},
			},
		},
		Receivers: []Receiver{
			{Name: "default"},
			{Name: "pagerduty-critical"},
			{Name: "slack-high-priority"},
			{Name: "slack-warnings"},
			{Name: "monitoring-team"},
			{Name: "team-infra-east"},
			{Name: "team-infra-west"},
			{Name: "team-infra-eu"},
			{Name: "team-platform"},
			{Name: "pagerduty-platform"},
			{Name: "slack-platform-prod"},
			{Name: "slack-platform-staging"},
			{Name: "team-database"},
			{Name: "pagerduty-database"},
			{Name: "slack-database-alerts"},
			{Name: "security-team"},
			{Name: "pagerduty-security"},
			{Name: "pagerduty-customer"},
			{Name: "slack-customer-alerts"},
			{Name: "team-observability"},
			{Name: "pagerduty-observability"},
		},
	}
}

func TestFindMatchingRoute(t *testing.T) {
	config := &Config{
		Route: &Route{
			Receiver: "default",
			Routes: []*Route{
				{
					Matchers: []string{`severity="critical"`},
					Receiver: "pagerduty",
				},
				{
					Matchers: []string{`severity="warning"`},
					Receiver: "slack",
					Continue: true,
				},
				{
					Matchers: []string{`service=~"^(api|web)$"`},
					Receiver: "platform-team",
					Routes: []*Route{
						{
							Matchers: []string{`environment="production"`},
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
			name:             "critical alert routes to pagerduty",
			labels:           map[string]string{"alertname": "HighErrorRate", "severity": "critical"},
			expectedReceiver: "pagerduty",
			expectedRoutes:   1,
		},
		{
			name:             "warning alert routes to slack",
			labels:           map[string]string{"alertname": "HighLatency", "severity": "warning"},
			expectedReceiver: "slack",
			expectedRoutes:   1,
		},
		{
			name:             "api service routes to platform-team",
			labels:           map[string]string{"alertname": "APIDown", "service": "api"},
			expectedReceiver: "platform-team",
			expectedRoutes:   1,
		},
		{
			name:             "production api routes to pagerduty-platform",
			labels:           map[string]string{"alertname": "APIDown", "service": "api", "environment": "production"},
			expectedReceiver: "pagerduty-platform",
			expectedRoutes:   2,
		},
		{
			name:             "no match uses default",
			labels:           map[string]string{"alertname": "UnknownAlert"},
			expectedReceiver: "default",
			expectedRoutes:   0,
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

func TestFindMatchingRouteComprehensive(t *testing.T) {
	cfg := buildTestConfig()
	client := &Client{}

	tests := []struct {
		name             string
		labels           map[string]string
		expectedReceiver string
		minRoutes        int
		exactRoutes      int // -1 means don't check exact count
		// subroute context checks
		wantSubroute   bool // is the final matched route a subroute?
		wantParentName string
	}{
		// ── Simple top-level routes ───────────────────────────────────────
		{
			name:             "critical to pagerduty-critical",
			labels:           map[string]string{"alertname": "HighCPU", "severity": "critical"},
			expectedReceiver: "pagerduty-critical",
			exactRoutes:      1,
			wantSubroute:     false,
		},
		{
			name:             "high priority to slack-high-priority (continue)",
			labels:           map[string]string{"alertname": "DiskFull", "severity": "high"},
			expectedReceiver: "slack-high-priority",
			exactRoutes:      1,
			wantSubroute:     false,
		},
		{
			name:             "warning to slack-warnings (continue)",
			labels:           map[string]string{"alertname": "DiskFull", "severity": "warning"},
			expectedReceiver: "slack-warnings",
			exactRoutes:      1,
			wantSubroute:     false,
		},
		{
			name:             "no match falls back to default",
			labels:           map[string]string{"alertname": "RandomAlert", "foo": "bar"},
			expectedReceiver: "default",
			exactRoutes:      0,
		},
		// ── continue semantics ───────────────────────────────────────────
		{
			// warning + monitoring-team: warning route has continue=true, so evaluation
			// proceeds and also matches the monitoring-team route (also continue=true).
			// Last matching top-level route wins → monitoring-team.
			name:             "warning AND monitoring-team: both continue, last wins",
			labels:           map[string]string{"severity": "warning", "team": "monitoring"},
			expectedReceiver: "monitoring-team",
			exactRoutes:      -1, // only check minRoutes
			minRoutes:        2,
		},
		// ── Nested sub-routes (infrastructure → regional) ────────────────
		{
			name:             "infra us-east-1 subroute",
			labels:           map[string]string{"component": "infrastructure", "region": "us-east-1"},
			expectedReceiver: "team-infra-east",
			exactRoutes:      2, // parent infra route + child region route
			wantSubroute:     true,
		},
		{
			name:             "infra us-west-2 subroute",
			labels:           map[string]string{"component": "infrastructure", "region": "us-west-2"},
			expectedReceiver: "team-infra-west",
			exactRoutes:      2,
			wantSubroute:     true,
		},
		{
			name:             "infra eu-west-1 subroute",
			labels:           map[string]string{"component": "infrastructure", "region": "eu-west-1"},
			expectedReceiver: "team-infra-eu",
			exactRoutes:      2,
			wantSubroute:     true,
		},
		{
			// infrastructure match but no matching region → no child matched.
			// Parent has no receiver itself → falls back to root default.
			name:             "infra no matching region falls to default",
			labels:           map[string]string{"component": "infrastructure", "region": "ap-southeast-1"},
			expectedReceiver: "default",
			exactRoutes:      1, // only the parent infra route matched
		},
		// ── Platform team nested routes ───────────────────────────────────
		{
			name:             "platform api service only → team-platform",
			labels:           map[string]string{"service": "api"},
			expectedReceiver: "team-platform",
			exactRoutes:      1,
			wantSubroute:     false,
		},
		{
			name:             "platform api production → slack-platform-prod",
			labels:           map[string]string{"service": "api", "environment": "production"},
			expectedReceiver: "slack-platform-prod",
			exactRoutes:      2,
			wantSubroute:     true,
			wantParentName:   "team-platform",
		},
		{
			// The top-level severity=critical route (no continue) matches first and
			// stops evaluation before the platform sub-routes are reached.
			name:             "platform web production critical → blocked by top-level critical route",
			labels:           map[string]string{"service": "web", "environment": "production", "severity": "critical"},
			expectedReceiver: "pagerduty-critical",
			exactRoutes:      1,
			wantSubroute:     false,
		},
		{
			name:             "platform frontend staging → slack-platform-staging",
			labels:           map[string]string{"service": "frontend", "environment": "staging"},
			expectedReceiver: "slack-platform-staging",
			exactRoutes:      2,
			wantSubroute:     true,
		},
		// ── Database team ─────────────────────────────────────────────────
		{
			name:             "database team → team-database (no sub-match)",
			labels:           map[string]string{"team": "database"},
			expectedReceiver: "team-database",
			exactRoutes:      1,
		},
		{
			// The top-level severity=critical route (no continue) matches first and
			// stops evaluation before the database sub-routes are reached.
			name:             "database team critical → blocked by top-level critical route",
			labels:           map[string]string{"team": "database", "severity": "critical"},
			expectedReceiver: "pagerduty-critical",
			exactRoutes:      1,
			wantSubroute:     false,
		},
		{
			name:             "database team PostgreSQL alertname regex → slack-database-alerts",
			labels:           map[string]string{"team": "database", "alertname": "PostgreSQLSlowQueries"},
			expectedReceiver: "slack-database-alerts",
			exactRoutes:      2,
			wantSubroute:     true,
		},
		{
			name:             "database team MySQL regex match",
			labels:           map[string]string{"team": "database", "alertname": "MySQLReplicationLag"},
			expectedReceiver: "slack-database-alerts",
			exactRoutes:      2,
		},
		// ── Security (continue) ───────────────────────────────────────────
		{
			// The top-level severity=critical route (no continue) matches first and
			// stops evaluation before the security route is reached.
			name:             "security critical → blocked by top-level critical route",
			labels:           map[string]string{"category": "security", "severity": "critical"},
			expectedReceiver: "pagerduty-critical",
			exactRoutes:      1,
			wantSubroute:     false,
		},
		{
			// severity=warning (continue=true) + security (continue=true) both match;
			// last matching route wins → security-team.
			name:             "security non-critical → warning route + security-team (both continue)",
			labels:           map[string]string{"category": "security", "severity": "warning"},
			expectedReceiver: "security-team",
			exactRoutes:      2,
			wantSubroute:     false,
		},
		// ── Customer facing ───────────────────────────────────────────────
		{
			// The top-level severity=critical route (no continue) matches first and
			// stops evaluation before the customer-facing sub-routes are reached.
			name:             "customer-facing critical → blocked by top-level critical route",
			labels:           map[string]string{"customer_facing": "true", "severity": "critical"},
			expectedReceiver: "pagerduty-critical",
			exactRoutes:      1,
			wantSubroute:     false,
		},
		{
			// severity=warning (continue=true) matches, then customer_facing parent
			// also matches; sub-route severity=warning matches → slack-customer-alerts.
			// Total: 3 matched routes.
			name:             "customer-facing warning → slack-customer-alerts",
			labels:           map[string]string{"customer_facing": "true", "severity": "warning"},
			expectedReceiver: "slack-customer-alerts",
			exactRoutes:      3,
			wantSubroute:     true,
		},
		// ── Observability (match_re on job) ───────────────────────────────
		{
			name:             "prometheus job → team-observability",
			labels:           map[string]string{"job": "prometheus"},
			expectedReceiver: "team-observability",
			exactRoutes:      1,
		},
		{
			// The top-level severity=critical route (no continue) matches first and
			// stops evaluation before the observability sub-routes are reached.
			name:             "grafana job critical → blocked by top-level critical route",
			labels:           map[string]string{"job": "grafana", "severity": "critical"},
			expectedReceiver: "pagerduty-critical",
			exactRoutes:      1,
			wantSubroute:     false,
		},
		{
			name:             "alertmanager job → team-observability",
			labels:           map[string]string{"job": "alertmanager"},
			expectedReceiver: "team-observability",
			exactRoutes:      1,
		},
		{
			name:             "unknown job → default",
			labels:           map[string]string{"job": "myapp"},
			expectedReceiver: "default",
			exactRoutes:      0,
		},
		// ── Matchers with single-quote values ─────────────────────────────
		{
			name:             "matcher with unquoted value",
			labels:           map[string]string{"severity": "critical"},
			expectedReceiver: "pagerduty-critical",
			exactRoutes:      1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			receiver, matched, err := client.FindMatchingRoute(tt.labels, cfg)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if receiver != tt.expectedReceiver {
				t.Errorf("receiver = %q, want %q", receiver, tt.expectedReceiver)
				for i, mr := range matched {
					t.Logf("  matched[%d] receiver=%q depth=%d subroute=%v parents=%v",
						i, mr.Route.Receiver, mr.Depth, mr.IsSubroute, mr.ParentReceivers)
				}
			}

			if tt.exactRoutes >= 0 && len(matched) != tt.exactRoutes {
				t.Errorf("matched %d routes, want exactly %d", len(matched), tt.exactRoutes)
			}
			if tt.minRoutes > 0 && len(matched) < tt.minRoutes {
				t.Errorf("matched %d routes, want at least %d", len(matched), tt.minRoutes)
			}

			// Subroute context checks on the last matched route.
			if tt.wantSubroute && len(matched) > 0 {
				last := matched[len(matched)-1]
				if !last.IsSubroute {
					t.Errorf("last matched route IsSubroute = false, want true")
				}
				if tt.wantParentName != "" {
					found := false
					for _, p := range last.ParentReceivers {
						if p == tt.wantParentName {
							found = true
							break
						}
					}
					if !found {
						t.Errorf("expected parent receiver %q in chain %v", tt.wantParentName, last.ParentReceivers)
					}
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Subroute depth and parent-chain tests
// ---------------------------------------------------------------------------

func TestParentChainPreservesRepeatedReceivers(t *testing.T) {
	cfg := &Config{Route: &Route{
		Receiver: "default",
		Routes: []*Route{{
			Receiver: "team",
			Routes: []*Route{{
				Receiver: "team",
				Routes:   []*Route{{Receiver: "leaf"}},
			}},
		}},
	}}

	_, matched, err := (&Client{}).FindMatchingRoute(map[string]string{}, cfg)
	if err != nil {
		t.Fatalf("FindMatchingRoute() error = %v", err)
	}
	if len(matched) != 3 {
		t.Fatalf("matched routes = %d, want 3", len(matched))
	}
	if got := strings.Join(matched[2].ParentReceivers, " -> "); got != "team -> team" {
		t.Fatalf("parent receivers = %q, want team -> team", got)
	}
}

func TestSubrouteDepthAndParentChain(t *testing.T) {
	// Config with three nesting levels: root → L1 → L2
	cfg := &Config{
		Route: &Route{
			Receiver: "default",
			Routes: []*Route{
				{
					Match:    map[string]string{"team": "platform"},
					Receiver: "platform",
					Routes: []*Route{
						{
							Match:    map[string]string{"env": "prod"},
							Receiver: "platform-prod",
							Routes: []*Route{
								{
									Match:    map[string]string{"severity": "critical"},
									Receiver: "platform-prod-critical",
								},
							},
						},
					},
				},
			},
		},
	}

	client := &Client{}

	t.Run("three-level deep route has correct depth and parents", func(t *testing.T) {
		labels := map[string]string{"team": "platform", "env": "prod", "severity": "critical"}
		_, matched, err := client.FindMatchingRoute(labels, cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(matched) != 3 {
			t.Fatalf("expected 3 matched routes, got %d", len(matched))
		}

		// matched[0]: depth=0, not a subroute, no parents
		r0 := matched[0]
		if r0.Depth != 0 {
			t.Errorf("matched[0].Depth = %d, want 0", r0.Depth)
		}
		if r0.IsSubroute {
			t.Errorf("matched[0].IsSubroute = true, want false")
		}
		if len(r0.ParentReceivers) != 0 {
			t.Errorf("matched[0].ParentReceivers = %v, want []", r0.ParentReceivers)
		}

		// matched[1]: depth=1, subroute, parent = ["platform"]
		r1 := matched[1]
		if r1.Depth != 1 {
			t.Errorf("matched[1].Depth = %d, want 1", r1.Depth)
		}
		if !r1.IsSubroute {
			t.Errorf("matched[1].IsSubroute = false, want true")
		}
		if len(r1.ParentReceivers) != 1 || r1.ParentReceivers[0] != "platform" {
			t.Errorf("matched[1].ParentReceivers = %v, want [platform]", r1.ParentReceivers)
		}

		// matched[2]: depth=2, subroute, parent = ["platform", "platform-prod"]
		r2 := matched[2]
		if r2.Depth != 2 {
			t.Errorf("matched[2].Depth = %d, want 2", r2.Depth)
		}
		if !r2.IsSubroute {
			t.Errorf("matched[2].IsSubroute = false, want true")
		}
		if len(r2.ParentReceivers) != 2 {
			t.Fatalf("matched[2].ParentReceivers = %v, want len 2", r2.ParentReceivers)
		}
		if r2.ParentReceivers[0] != "platform" || r2.ParentReceivers[1] != "platform-prod" {
			t.Errorf("matched[2].ParentReceivers = %v, want [platform platform-prod]", r2.ParentReceivers)
		}
	})
}

// ---------------------------------------------------------------------------
// Config caching tests
// ---------------------------------------------------------------------------

func TestConfigCache(t *testing.T) {
	t.Run("InvalidateConfig clears cached config", func(t *testing.T) {
		c := &Client{}
		cfg := &Config{Route: &Route{Receiver: "default"}}

		// Manually populate the cache as GetConfig() would.
		c.cacheMu.Lock()
		c.cachedConfig = cfg
		c.cacheTimestamp = time.Now()
		c.cacheMu.Unlock()

		// Sanity: cache is set.
		c.cacheMu.RLock()
		if c.cachedConfig == nil {
			t.Fatal("expected cachedConfig to be set")
		}
		c.cacheMu.RUnlock()

		c.InvalidateConfig()

		c.cacheMu.RLock()
		defer c.cacheMu.RUnlock()
		if c.cachedConfig != nil {
			t.Error("expected cachedConfig to be nil after InvalidateConfig()")
		}
		if !c.cacheTimestamp.IsZero() {
			t.Error("expected cacheTimestamp to be zero after InvalidateConfig()")
		}
	})

	t.Run("ConfigCachedAt returns zero before any cache entry", func(t *testing.T) {
		c := &Client{}
		if !c.ConfigCachedAt().IsZero() {
			t.Error("expected zero time before any caching")
		}
	})

	t.Run("ConfigCachedAt returns non-zero after caching", func(t *testing.T) {
		c := &Client{}
		now := time.Now()
		c.cacheMu.Lock()
		c.cachedConfig = &Config{}
		c.cacheTimestamp = now
		c.cacheMu.Unlock()

		got := c.ConfigCachedAt()
		if got != now {
			t.Errorf("ConfigCachedAt() = %v, want %v", got, now)
		}
	})

	t.Run("concurrent access is safe", func(t *testing.T) {
		c := &Client{}
		var wg sync.WaitGroup
		const goroutines = 50

		for i := 0; i < goroutines; i++ {
			wg.Add(2)
			// Writer: toggles cache on and off.
			go func() {
				defer wg.Done()
				c.cacheMu.Lock()
				c.cachedConfig = &Config{}
				c.cacheTimestamp = time.Now()
				c.cacheMu.Unlock()
			}()
			// Reader: reads the cached time.
			go func() {
				defer wg.Done()
				_ = c.ConfigCachedAt()
			}()
		}

		// Intersperse invalidations.
		for i := 0; i < goroutines/5; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				c.InvalidateConfig()
			}()
		}

		wg.Wait() // should not race-detect
	})
}

// ---------------------------------------------------------------------------
// ExtractLabelKeys tests
// ---------------------------------------------------------------------------

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
				{
					Matchers: []string{`region="us-east-1"`},
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
		"region":      true,
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

// ---------------------------------------------------------------------------
// ExtractLabelSuggestions tests
// ---------------------------------------------------------------------------

func TestExtractLabelSuggestions(t *testing.T) {
	config := &Config{
		Route: &Route{
			Routes: []*Route{
				{
					Match:    map[string]string{"severity": "critical"},
					Receiver: "pagerduty",
				},
				{
					Match:    map[string]string{"severity": "warning"},
					Receiver: "slack",
				},
				{
					Matchers: []string{`team="platform"`},
					Receiver: "platform",
				},
				{
					// match_re values are NOT included in suggestions (not exact values)
					MatchRE:  map[string]string{"service": "^api$"},
					Receiver: "api",
				},
			},
		},
	}

	suggestions := ExtractLabelSuggestions(config)
	suggestMap := make(map[string][]string)
	for _, s := range suggestions {
		suggestMap[s.Key] = s.Values
	}

	// severity should have both "critical" and "warning"
	if vals, ok := suggestMap["severity"]; !ok {
		t.Error("expected 'severity' in suggestions")
	} else {
		valSet := make(map[string]bool)
		for _, v := range vals {
			valSet[v] = true
		}
		if !valSet["critical"] {
			t.Error("expected 'critical' in severity suggestions")
		}
		if !valSet["warning"] {
			t.Error("expected 'warning' in severity suggestions")
		}
	}

	// team should appear from matchers with = operator
	if _, ok := suggestMap["team"]; !ok {
		t.Error("expected 'team' in suggestions from matchers")
	}
}

// ---------------------------------------------------------------------------
// FindReceiverByName tests
// ---------------------------------------------------------------------------

func TestFindReceiverByName(t *testing.T) {
	config := &Config{
		Receivers: []Receiver{
			{Name: "default"},
			{Name: "pagerduty"},
			{Name: "slack"},
		},
	}
	client := &Client{}

	t.Run("found", func(t *testing.T) {
		r := client.FindReceiverByName("pagerduty", config)
		if r == nil {
			t.Fatal("expected non-nil receiver")
		}
		if r.Name != "pagerduty" {
			t.Errorf("got receiver %q, want %q", r.Name, "pagerduty")
		}
	})

	t.Run("not found returns nil", func(t *testing.T) {
		r := client.FindReceiverByName("nonexistent", config)
		if r != nil {
			t.Errorf("expected nil for unknown receiver, got %+v", r)
		}
	})
}

// ---------------------------------------------------------------------------
// Route with no receiver (parent-only match) falls back to root default
// ---------------------------------------------------------------------------

func TestParentRouteWithNoReceiverFallsToDefault(t *testing.T) {
	cfg := &Config{
		Route: &Route{
			Receiver: "root-default",
			Routes: []*Route{
				{
					// This parent route has no receiver of its own.
					Match: map[string]string{"component": "infra"},
					Routes: []*Route{
						{Match: map[string]string{"region": "us-east-1"}, Receiver: "infra-east"},
					},
				},
			},
		},
	}
	client := &Client{}

	t.Run("parent match + no child match → root default", func(t *testing.T) {
		labels := map[string]string{"component": "infra", "region": "unknown"}
		recv, matched, err := client.FindMatchingRoute(labels, cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if recv != "root-default" {
			t.Errorf("receiver = %q, want %q", recv, "root-default")
		}
		// The parent route itself matched (depth 0), but no child matched.
		if len(matched) != 1 {
			t.Errorf("expected 1 matched route (the parent), got %d", len(matched))
		}
	})

	t.Run("parent + child match → child receiver", func(t *testing.T) {
		labels := map[string]string{"component": "infra", "region": "us-east-1"}
		recv, matched, err := client.FindMatchingRoute(labels, cfg)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if recv != "infra-east" {
			t.Errorf("receiver = %q, want %q", recv, "infra-east")
		}
		if len(matched) != 2 {
			t.Errorf("expected 2 matched routes, got %d", len(matched))
		}
		if len(matched[1].ParentReceivers) != 1 || matched[1].ParentReceivers[0] != "root-default" {
			t.Errorf("child parent receivers = %v, want [root-default]", matched[1].ParentReceivers)
		}
	})
}

// ---------------------------------------------------------------------------
// Error cases
// ---------------------------------------------------------------------------

func TestFindMatchingRouteErrors(t *testing.T) {
	client := &Client{}

	t.Run("nil route config returns error", func(t *testing.T) {
		cfg := &Config{Route: nil}
		_, _, err := client.FindMatchingRoute(map[string]string{}, cfg)
		if err == nil {
			t.Error("expected error for nil route, got nil")
		}
	})
}
