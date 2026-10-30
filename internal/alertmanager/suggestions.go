package alertmanager

import (
	"sort"

	"github.com/prometheus/alertmanager/dispatch"
	amlabels "github.com/prometheus/alertmanager/pkg/labels"
)

func SuggestLabels(config *Config, entered map[string]string) []LabelSuggestion {
	tree, err := nativeRouteTreeForConfig(config)
	if err != nil {
		return nil
	}
	values := map[string]map[string]bool{}
	tree.root.Walk(func(route *dispatch.Route) {
		constraints := map[string][]*amlabels.Matcher{}
		for _, parent := range tree.paths[route] {
			for _, matcher := range parent.Matchers {
				if value, found := entered[matcher.Name]; found && !matcher.Matches(value) {
					return
				}
				constraints[matcher.Name] = append(constraints[matcher.Name], matcher)
			}
		}
		for name, matchers := range constraints {
			if values[name] == nil {
				values[name] = map[string]bool{}
			}
			for _, matcher := range matchers {
				candidate := ""
				switch matcher.Type {
				case amlabels.MatchEqual:
					candidate = matcher.Value
				case amlabels.MatchRegexp:
					var ok bool
					candidate, ok = regexMatcherExample(matcher.Value)
					if !ok {
						continue
					}
				case amlabels.MatchNotEqual, amlabels.MatchNotRegexp:
					continue
				}
				if matchersMatch(candidate, matchers) {
					values[name][candidate] = true
				}
			}
		}
	})
	result := make([]LabelSuggestion, 0, len(values))
	for name, candidates := range values {
		suggestion := LabelSuggestion{Key: name, Values: []string{}}
		for value := range candidates {
			suggestion.Values = append(suggestion.Values, value)
		}
		sort.Strings(suggestion.Values)
		result = append(result, suggestion)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Key < result[j].Key })
	return result
}
