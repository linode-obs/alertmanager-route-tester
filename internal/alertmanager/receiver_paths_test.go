package alertmanager

import (
	"strings"
	"testing"
)

func TestReceiverPathsExplainAllBlockers(t *testing.T) {
	config := &Config{Route: &Route{Receiver: "default", Routes: []*Route{
		{Receiver: "stop", Match: map[string]string{"severity": "critical"}},
		{Receiver: "team", Match: map[string]string{"team": "platform"}, Routes: []*Route{
			{Receiver: "wanted", Match: map[string]string{"service": "api"}},
			{Match: map[string]string{"service": "metrics"}},
		}},
		{Receiver: "wanted", Match: map[string]string{"job": "salt"}},
	}}}
	paths := ExplainReceiverPaths(map[string]string{"severity": "warning", "team": "database", "job": "other"}, config, "wanted")
	if len(paths) != 2 {
		t.Fatalf("paths = %#v, want two", paths)
	}
	if paths[0].Path != "root/2/1" || !strings.Contains(paths[0].BlockedBy, "root/2") {
		t.Fatalf("ancestor blocker = %#v", paths[0])
	}
	if paths[0].Steps[1].FailedMatchers[0].Actual != "database" || paths[0].Steps[1].FailedMatchers[0].Missing {
		t.Fatalf("ancestor failure = %#v", paths[0].Steps[1])
	}
	if !paths[0].Steps[2].FailedMatchers[0].Missing || paths[0].Steps[2].Evaluated {
		t.Fatalf("skipped child = %#v", paths[0].Steps[2])
	}
	if paths[1].Steps[1].FailedMatchers[0].Expected != "salt" {
		t.Fatalf("second path = %#v", paths[1])
	}
	paths = ExplainReceiverPaths(map[string]string{"severity": "critical", "team": "platform", "service": "api", "job": "salt"}, config, "wanted")
	for _, path := range paths {
		if !strings.Contains(path.BlockedBy, "Earlier sibling root/1") || path.Delivered {
			t.Fatalf("sibling blocker = %#v", path)
		}
	}
	paths = ExplainReceiverPaths(map[string]string{"severity": "warning", "team": "platform", "service": "api"}, config, "wanted")
	if !paths[0].Delivered || paths[0].BlockedBy != "" {
		t.Fatalf("delivered path = %#v", paths[0])
	}
	paths = ExplainReceiverPaths(map[string]string{"team": "platform", "service": "metrics"}, config, "team")
	if len(paths) != 2 || paths[0].Delivered || !paths[1].Delivered {
		t.Fatalf("inherited paths = %#v", paths)
	}
	if got := ExplainReceiverPaths(nil, config, "unknown"); len(got) != 0 {
		t.Fatalf("unknown = %#v", got)
	}
	paths = ExplainReceiverPaths(nil, config, "default")
	if len(paths) != 1 || !paths[0].Delivered {
		t.Fatalf("root fallback = %#v", paths)
	}
}
