package alertmanager

import (
	"fmt"
	"sort"

	"github.com/prometheus/alertmanager/dispatch"
)

type ReceiverPathStep struct {
	Path           string           `json:"path"`
	Receiver       string           `json:"receiver"`
	Evaluated      bool             `json:"evaluated"`
	FailedMatchers []MatcherFailure `json:"failed_matchers,omitempty"`
}

type ReceiverPath struct {
	Matched            bool               `json:"matched"`
	StoppedByReceivers []string           `json:"stopped_by_receivers,omitempty"`
	Path               string             `json:"path"`
	Delivered          bool               `json:"delivered"`
	BlockedBy          string             `json:"blocked_by,omitempty"`
	Steps              []ReceiverPathStep `json:"steps"`
}

func ExplainReceiverPaths(labels map[string]string, config *Config, receiver string) []ReceiverPath {
	tree, err := nativeRouteTreeForConfig(config)
	if err != nil || receiver == "" {
		return nil
	}
	evaluation := evaluateRoutePaths(labels, tree)
	paths := []ReceiverPath{}
	tree.root.Walk(func(route *dispatch.Route) {
		if route.RouteOpts.Receiver != receiver {
			return
		}
		path := ReceiverPath{Path: evaluation.names[route], Delivered: evaluation.delivered[route], BlockedBy: evaluation.blockers[route], Matched: evaluation.blockers[route] == "", StoppedByReceivers: evaluation.stoppedBy[route]}
		if !path.Delivered && path.BlockedBy == "" {
			path.BlockedBy = "A matching child route selects another delivery path"
		}
		for _, parent := range tree.paths[route] {
			path.Steps = append(path.Steps, ReceiverPathStep{
				Path: evaluation.names[parent], Receiver: parent.RouteOpts.Receiver, Evaluated: evaluation.checked[parent],
				FailedMatchers: failedRouteMatchers(labels, parent.Matchers, tree.localRoutes[parent]),
			})
		}
		paths = append(paths, path)
	})
	return paths
}

type routePathEvaluation struct {
	delivered map[*dispatch.Route]bool
	names     map[*dispatch.Route]string
	checked   map[*dispatch.Route]bool
	blockers  map[*dispatch.Route]string
	stoppedBy map[*dispatch.Route][]string
}

func evaluateRoutePaths(labels map[string]string, tree *nativeRouteTree) routePathEvaluation {
	evaluation := routePathEvaluation{
		delivered: map[*dispatch.Route]bool{},
		names:     map[*dispatch.Route]string{},
		checked:   map[*dispatch.Route]bool{},
		blockers:  map[*dispatch.Route]string{},
		stoppedBy: map[*dispatch.Route][]string{},
	}
	terminalReceivers := map[*dispatch.Route]map[string]bool{}
	for _, terminal := range tree.root.Match(nativeLabels(labels)) {
		evaluation.delivered[terminal] = true
		receiver := terminal.RouteOpts.Receiver
		if receiver == "" {
			continue
		}
		for _, route := range tree.paths[terminal] {
			if terminalReceivers[route] == nil {
				terminalReceivers[route] = map[string]bool{}
			}
			terminalReceivers[route][receiver] = true
		}
	}
	var visit func(*dispatch.Route, string, string, []string)
	visit = func(route *dispatch.Route, name, blocker string, stoppingReceivers []string) {
		evaluation.names[route] = name
		evaluation.checked[route] = blocker == ""
		if blocker == "" && !routeMatchesLabels(labels, route.Matchers) {
			blocker = "Route conditions failed at " + name
		}
		evaluation.blockers[route] = blocker
		evaluation.stoppedBy[route] = stoppingReceivers
		childBlocker := blocker
		childStoppingReceivers := stoppingReceivers
		for index, child := range route.Routes {
			childName := fmt.Sprintf("%s/%d", name, index+1)
			visit(child, childName, childBlocker, childStoppingReceivers)
			if childBlocker == "" && routeMatchesLabels(labels, child.Matchers) && !child.Continue {
				childBlocker = fmt.Sprintf("Earlier sibling %s matched with continue=false", childName)
				childStoppingReceivers = sortedReceiverNames(terminalReceivers[child])
				if len(childStoppingReceivers) == 0 && child.RouteOpts.Receiver != "" {
					childStoppingReceivers = []string{child.RouteOpts.Receiver}
				}
			}
		}
	}
	visit(tree.root, "root", "", nil)
	return evaluation
}

func sortedReceiverNames(receivers map[string]bool) []string {
	names := make([]string, 0, len(receivers))
	for name := range receivers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
