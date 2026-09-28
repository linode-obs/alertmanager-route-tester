package alertmanager

import "sort"

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
	if config == nil || config.Route == nil {
		return nil
	}
	mismatches := make([]RouteMismatch, 0)
	findRouteMismatches(labels, config.Route, 0, nil, config.Route.Receiver, &mismatches)
	return mismatches
}

func findRouteMismatches(labels map[string]string, route *Route, depth int, parentReceivers []string, inheritedReceiver string, mismatches *[]RouteMismatch) {
	for _, child := range route.Routes {
		if child == nil {
			continue
		}
		failures := failedRouteMatchers(labels, child)
		if len(failures) > 0 {
			*mismatches = append(*mismatches, RouteMismatch{
				Route:           child,
				Depth:           depth,
				ParentReceivers: parentReceivers,
				FailedMatchers:  failures,
			})
			continue
		}

		resolvedReceiver := child.Receiver
		if resolvedReceiver == "" {
			resolvedReceiver = inheritedReceiver
		}
		childParents := parentReceivers
		if resolvedReceiver != "" {
			childParents = make([]string, len(parentReceivers)+1)
			copy(childParents, parentReceivers)
			childParents[len(parentReceivers)] = resolvedReceiver
		}
		findRouteMismatches(labels, child, depth+1, childParents, resolvedReceiver, mismatches)
		if !child.Continue {
			break
		}
	}
}

func failedRouteMatchers(labels map[string]string, route *Route) []MatcherFailure {
	failures := make([]MatcherFailure, 0)
	keys := make([]string, 0, len(route.Match))
	for key := range route.Match {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if labels[key] != route.Match[key] {
			failures = append(failures, matcherFailure(labels, key, "=", route.Match[key]))
		}
	}

	keys = keys[:0]
	for key := range route.MatchRE {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		matched, err := matchesRegex(route.MatchRE[key], labels[key])
		if err != nil || !matched {
			failures = append(failures, matcherFailure(labels, key, "=~", route.MatchRE[key]))
		}
	}

	for _, matcher := range route.Matchers {
		if !matchesMatcher(labels, matcher) {
			parsed, ok := parseMatcher(matcher)
			if !ok {
				failures = append(failures, MatcherFailure{Expected: matcher})
				continue
			}
			failures = append(failures, matcherFailure(labels, parsed.Label, parsed.Operator, parsed.Value))
		}
	}
	return failures
}

func matcherFailure(labels map[string]string, label, operator, expected string) MatcherFailure {
	actual, found := labels[label]
	return MatcherFailure{
		Label:    label,
		Operator: operator,
		Expected: expected,
		Actual:   actual,
		Missing:  !found,
	}
}
