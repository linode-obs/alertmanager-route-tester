package alertmanager

import (
	"github.com/prometheus/alertmanager/dispatch"
	amlabels "github.com/prometheus/alertmanager/pkg/labels"
)

type MatcherFailure struct {
	Label    string `json:"label"`
	Operator string `json:"operator"`
	Expected string `json:"expected"`
	Actual   string `json:"actual"`
	Missing  bool   `json:"missing"`
}

type RouteMismatch struct {
	Route           *Route           `json:"route"`
	Depth           int              `json:"depth"`
	ParentReceivers []string         `json:"parent_receivers,omitempty"`
	FailedMatchers  []MatcherFailure `json:"failed_matchers"`
}

func FindRouteMismatches(labels map[string]string, config *Config) []RouteMismatch {
	tree, err := nativeRouteTreeForConfig(config)
	if err != nil {
		return nil
	}
	mismatches := make([]RouteMismatch, 0)
	findRouteMismatches(labels, tree, tree.root, 0, nil, &mismatches)
	return mismatches
}

func findRouteMismatches(labels map[string]string, tree *nativeRouteTree, route *dispatch.Route, depth int, parentReceivers []string, mismatches *[]RouteMismatch) {
	for _, child := range route.Routes {
		localRoute := tree.localRoutes[child]
		failures := failedRouteMatchers(labels, child.Matchers, localRoute)
		if len(failures) > 0 {
			*mismatches = append(*mismatches, RouteMismatch{
				Route:           localRoute,
				Depth:           depth,
				ParentReceivers: parentReceivers,
				FailedMatchers:  failures,
			})
			continue
		}

		childParents := parentReceivers
		if receiver := child.RouteOpts.Receiver; receiver != "" {
			childParents = append(append([]string(nil), parentReceivers...), receiver)
		}
		findRouteMismatches(labels, tree, child, depth+1, childParents, mismatches)
		if !child.Continue {
			break
		}
	}
}

func failedRouteMatchers(labels map[string]string, matchers amlabels.Matchers, route *Route) []MatcherFailure {
	failures := make([]MatcherFailure, 0)
	for _, matcher := range matchers {
		if !matcher.Matches(labels[matcher.Name]) {
			actual, found := labels[matcher.Name]
			expected := matcher.Value
			if matcher.Type == amlabels.MatchRegexp {
				if configuredPattern, ok := route.MatchRE[matcher.Name]; ok {
					expected = configuredPattern
				}
			}
			failures = append(failures, MatcherFailure{
				Label:    matcher.Name,
				Operator: matcher.Type.String(),
				Expected: expected,
				Actual:   actual,
				Missing:  !found,
			})
		}
	}
	return failures
}
