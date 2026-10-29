package handler

import (
	"fmt"
	"sort"
	"strings"

	"github.com/linode-obs/alertmanager-route-tester/internal/alertmanager"
)

type ReceiverCheck struct {
	HasRoutes     bool
	Delivered     bool
	ChildSelected bool
	StoppedBy     []string
	Rules         [][]alertmanager.MatcherFailure
}

func receiverCheck(paths []alertmanager.ReceiverPath) ReceiverCheck {
	result := ReceiverCheck{HasRoutes: len(paths) > 0}
	stops := map[string]bool{}
	rules := map[string]bool{}
	for _, path := range paths {
		result.Delivered = result.Delivered || path.Delivered
		result.ChildSelected = result.ChildSelected || (path.Matched && !path.Delivered)
		for _, receiver := range path.StoppedByReceivers {
			stops[receiver] = true
		}
		unique := map[alertmanager.MatcherFailure]bool{}
		for _, step := range path.Steps {
			for _, failure := range step.FailedMatchers {
				unique[failure] = true
			}
		}
		failures := make([]alertmanager.MatcherFailure, 0, len(unique))
		for failure := range unique {
			failures = append(failures, failure)
		}
		sort.Slice(failures, func(i, j int) bool { return matcherFailureKey(failures[i]) < matcherFailureKey(failures[j]) })
		var key strings.Builder
		for _, failure := range failures {
			key.WriteString(matcherFailureKey(failure))
		}
		if len(failures) > 0 && !rules[key.String()] {
			rules[key.String()] = true
			result.Rules = append(result.Rules, failures)
		}
	}
	for receiver := range stops {
		result.StoppedBy = append(result.StoppedBy, receiver)
	}
	sort.Strings(result.StoppedBy)
	return result
}

func matcherFailureKey(failure alertmanager.MatcherFailure) string {
	return fmt.Sprintf("%q|%q|%q|%q|%t;", failure.Label, failure.Operator, failure.Expected, failure.Actual, failure.Missing)
}
