package alertmanager

import (
	"fmt"

	"github.com/prometheus/alertmanager/dispatch"
)

type ReceiverPathStep struct {
	Path           string           `json:"path"`
	Receiver       string           `json:"receiver"`
	Evaluated      bool             `json:"evaluated"`
	FailedMatchers []MatcherFailure `json:"failed_matchers,omitempty"`
}

type ReceiverPath struct {
	Matched           bool               `json:"matched"`
	StoppedByReceiver string             `json:"stopped_by_receiver,omitempty"`
	Path              string             `json:"path"`
	Delivered         bool               `json:"delivered"`
	BlockedBy         string             `json:"blocked_by,omitempty"`
	Steps             []ReceiverPathStep `json:"steps"`
}

func ExplainReceiverPaths(labels map[string]string, config *Config, receiver string) []ReceiverPath {
	tree, err := nativeRouteTreeForConfig(config)
	if err != nil || receiver == "" {
		return nil
	}
	delivered := map[*dispatch.Route]bool{}
	for _, route := range tree.root.Match(nativeLabels(labels)) {
		delivered[route] = true
	}
	names := map[*dispatch.Route]string{}
	checked := map[*dispatch.Route]bool{}
	blockers := map[*dispatch.Route]string{}
	stoppedBy := map[*dispatch.Route]string{}
	var visit func(*dispatch.Route, string, string, string)
	visit = func(route *dispatch.Route, name, blocker, stoppingReceiver string) {
		names[route] = name
		checked[route] = blocker == ""
		if blocker == "" && !routeMatchesLabels(labels, route.Matchers) {
			blocker = "Route conditions failed at " + name
		}
		blockers[route] = blocker
		stoppedBy[route] = stoppingReceiver
		childBlocker := blocker
		childStoppingReceiver := stoppingReceiver
		for index, child := range route.Routes {
			childName := fmt.Sprintf("%s/%d", name, index+1)
			visit(child, childName, childBlocker, childStoppingReceiver)
			if childBlocker == "" && routeMatchesLabels(labels, child.Matchers) && !child.Continue {
				childBlocker = fmt.Sprintf("Earlier sibling %s matched receiver %s with continue=false", childName, child.RouteOpts.Receiver)
				childStoppingReceiver = child.RouteOpts.Receiver
			}
		}
	}
	visit(tree.root, "root", "", "")
	paths := []ReceiverPath{}
	tree.root.Walk(func(route *dispatch.Route) {
		if route.RouteOpts.Receiver != receiver {
			return
		}
		path := ReceiverPath{Path: names[route], Delivered: delivered[route], BlockedBy: blockers[route], Matched: blockers[route] == "", StoppedByReceiver: stoppedBy[route]}
		if !path.Delivered && path.BlockedBy == "" {
			path.BlockedBy = "A matching child route selects another delivery path"
		}
		for _, parent := range tree.paths[route] {
			path.Steps = append(path.Steps, ReceiverPathStep{
				Path: names[parent], Receiver: parent.RouteOpts.Receiver, Evaluated: checked[parent],
				FailedMatchers: failedRouteMatchers(labels, parent.Matchers, tree.localRoutes[parent]),
			})
		}
		paths = append(paths, path)
	})
	return paths
}
