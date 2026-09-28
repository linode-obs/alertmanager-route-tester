package alertmanager

import "testing"

func TestFindRouteMismatchesExplainsMissingAndMismatchedLabels(t *testing.T) {
	config := &Config{Route: &Route{
		Receiver: "default",
		Routes: []*Route{{
			Receiver: "api-team",
			Match:    map[string]string{"severity": "critical", "service": "web"},
			MatchRE:  map[string]string{"job": "^api-.*"},
			Matchers: []string{`region=~"^(us|eu)-"`},
		}},
	}}
	labels := map[string]string{"service": "api", "job": "web", "region": "apac"}

	mismatches := FindRouteMismatches(labels, config)
	if len(mismatches) != 1 {
		t.Fatalf("FindRouteMismatches() returned %d routes, want 1", len(mismatches))
	}
	failures := mismatches[0].FailedMatchers
	if len(failures) != 4 {
		t.Fatalf("failed matchers = %#v, want four failures", failures)
	}

	byLabel := make(map[string]MatcherFailure, len(failures))
	for _, failure := range failures {
		byLabel[failure.Label] = failure
	}
	if failure := byLabel["severity"]; !failure.Missing || failure.Expected != "critical" {
		t.Errorf("severity failure = %#v, want missing critical label", failure)
	}
	if failure := byLabel["service"]; failure.Missing || failure.Actual != "api" || failure.Expected != "web" {
		t.Errorf("service failure = %#v, want actual api and expected web", failure)
	}
	if failure := byLabel["job"]; failure.Missing || failure.Actual != "web" || failure.Expected != "^api-.*" {
		t.Errorf("job failure = %#v, want actual web and regex ^api-.*", failure)
	}
	if failure := byLabel["region"]; failure.Missing || failure.Actual != "apac" || failure.Expected != "^(us|eu)-" {
		t.Errorf("region failure = %#v, want actual apac and regex", failure)
	}
}

func TestFindRouteMismatchesOnlyDescendsThroughMatchedRoutes(t *testing.T) {
	config := &Config{Route: &Route{
		Receiver: "default",
		Routes: []*Route{{
			Receiver: "platform",
			Match:    map[string]string{"team": "platform"},
			Routes: []*Route{{
				Receiver: "pagerduty",
				Match:    map[string]string{"severity": "critical"},
			}},
		}},
	}}

	mismatches := FindRouteMismatches(map[string]string{"team": "platform", "severity": "warning"}, config)
	if len(mismatches) != 1 || mismatches[0].Depth != 1 || mismatches[0].Route.Receiver != "pagerduty" {
		t.Fatalf("nested mismatches = %#v, want pagerduty subroute", mismatches)
	}
	if len(mismatches[0].ParentReceivers) != 1 || mismatches[0].ParentReceivers[0] != "platform" {
		t.Fatalf("parent receivers = %v, want [platform]", mismatches[0].ParentReceivers)
	}
}
