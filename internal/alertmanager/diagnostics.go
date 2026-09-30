package alertmanager

import (
	"regexp"

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
	mismatches, _ := FindRouteMismatchesWithLimit(labels, config, 0)
	return mismatches
}

// FindRouteMismatchesWithLimit returns at most limit diagnostics and counts the rest. A limit less than one is unlimited.
func FindRouteMismatchesWithLimit(labels map[string]string, config *Config, limit int) ([]RouteMismatch, int) {
	tree, err := nativeRouteTreeForConfig(config)
	if err != nil {
		return nil, 0
	}
	mismatches := make([]RouteMismatch, 0)
	omitted := 0
	findRouteMismatches(labels, tree, tree.root, 0, nil, limit, &mismatches, &omitted)
	return mismatches, omitted
}

func findRouteMismatches(labels map[string]string, tree *nativeRouteTree, route *dispatch.Route, depth int, parentReceivers []string, limit int, mismatches *[]RouteMismatch, omitted *int) {
	for _, child := range route.Routes {
		localRoute := tree.localRoutes[child]
		if !routeMatchesLabels(labels, child.Matchers) {
			if limit > 0 && len(*mismatches) >= limit {
				*omitted++
			} else {
				*mismatches = append(*mismatches, RouteMismatch{
					Route:           localRoute,
					Depth:           depth,
					ParentReceivers: parentReceivers,
					FailedMatchers:  failedRouteMatchers(labels, child.Matchers, localRoute),
				})
			}
			continue
		}

		childParents := parentReceivers
		if receiver := child.RouteOpts.Receiver; receiver != "" {
			childParents = append(append([]string(nil), parentReceivers...), receiver)
		}
		findRouteMismatches(labels, tree, child, depth+1, childParents, limit, mismatches, omitted)
		if !child.Continue {
			break
		}
	}
}

func routeMatchesLabels(labels map[string]string, matchers amlabels.Matchers) bool {
	for _, matcher := range matchers {
		if !matcher.Matches(labels[matcher.Name]) {
			return false
		}
	}
	return true
}

func failedRouteMatchers(labels map[string]string, matchers amlabels.Matchers, route *Route) []MatcherFailure {
	failures := make([]MatcherFailure, 0)
	for _, matcher := range matchers {
		if !matcher.Matches(labels[matcher.Name]) {
			actual, found := labels[matcher.Name]
			expected := matcher.Value
			if matcher.Type == amlabels.MatchRegexp {
				if configuredPattern, ok := route.MatchRE[matcher.Name]; ok {
					legacyPattern, err := regexp.Compile("^(?:" + configuredPattern + ")$")
					if err == nil && matcher.Value == legacyPattern.String() {
						expected = configuredPattern
					}
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
