package alertmanager

import (
	"reflect"
	"testing"
)

func TestSuggestLabelsFiltersRoutePaths(t *testing.T) {
	config := &Config{Route: &Route{Receiver: "default", Routes: []*Route{
		{Receiver: "db", Match: map[string]string{"team": "database"}, Routes: []*Route{{Match: map[string]string{"service": "postgres"}}}},
		{Receiver: "api", Match: map[string]string{"team": "platform", "service": "api"}},
		{Receiver: "db", Match: map[string]string{"team": "database"}, Matchers: []string{`job=~"salt.*"`, `job!="salt"`}},
	}}}
	suggestions := SuggestLabels(config, map[string]string{"team": "database"})
	values := map[string][]string{}
	for _, suggestion := range suggestions {
		values[suggestion.Key] = suggestion.Values
	}
	if !reflect.DeepEqual(values["service"], []string{"postgres"}) {
		t.Fatalf("service suggestions = %v", values["service"])
	}
	if !reflect.DeepEqual(values["team"], []string{"database"}) {
		t.Fatalf("team suggestions = %v", values["team"])
	}
	for _, job := range values["job"] {
		if job == "salt" || job == "salt.*" {
			t.Fatalf("suggested nonmatching job %q", job)
		}
	}
	if got := SuggestLabels(config, map[string]string{"team": "unknown"}); len(got) != 0 {
		t.Fatalf("unrelated suggestions = %#v", got)
	}
}
