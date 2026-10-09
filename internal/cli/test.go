package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"go.opentelemetry.io/otel/codes"

	"github.com/linode-obs/alertmanager-route-tester/internal/alertmanager"
	"github.com/linode-obs/alertmanager-route-tester/internal/telemetry"
)

// TestResult represents the output of a route test
type TestResult struct {
	Receiver       string                      `json:"receiver"`
	ReceiverConfig *alertmanager.Receiver      `json:"receiver_config,omitempty"`
	MatchedRoutes  []alertmanager.MatchedRoute `json:"matched_routes"`
	Labels         map[string]string           `json:"labels"`
	Error          string                      `json:"error,omitempty"`
}

type SuiteCase struct {
	Name              string
	Labels            map[string]string
	ExpectedReceivers []string
}

type SuiteResult struct {
	Name              string   `json:"name"`
	Passed            bool     `json:"passed"`
	ExpectedReceivers []string `json:"expected_receivers"`
	ActualReceivers   []string `json:"actual_receivers"`
	Error             string   `json:"error,omitempty"`
}

// TestRouting tests alert routing using the exact same logic as the web UI
// This function is designed to be used in Go tests
func RunSuite(client *alertmanager.Client, cases []SuiteCase) []SuiteResult {
	results := make([]SuiteResult, 0, len(cases))
	for _, testCase := range cases {
		result, err := TestRouting(client, testCase.Labels)
		suiteResult := SuiteResult{
			Name:              testCase.Name,
			ExpectedReceivers: sortedReceivers(testCase.ExpectedReceivers),
		}
		if err != nil {
			suiteResult.Error = err.Error()
		} else {
			suiteResult.ActualReceivers = receiversForResult(result)
			suiteResult.Passed = sameReceiverSet(suiteResult.ExpectedReceivers, suiteResult.ActualReceivers)
		}
		results = append(results, suiteResult)
	}
	return results
}

func receiversForResult(result *TestResult) []string {
	receivers := make([]string, 0, len(result.MatchedRoutes))
	seen := make(map[string]bool)
	for _, matched := range result.MatchedRoutes {
		if !matched.IsEffective {
			continue
		}
		receiver := matched.ResolvedReceiver
		if receiver == "" && matched.Route != nil {
			receiver = matched.Route.Receiver
		}
		if receiver != "" && !seen[receiver] {
			receivers = append(receivers, receiver)
			seen[receiver] = true
		}
	}
	if len(receivers) == 0 && result.Receiver != "" {
		receivers = append(receivers, result.Receiver)
	}
	return sortedReceivers(receivers)
}

func sortedReceivers(receivers []string) []string {
	unique := make(map[string]bool, len(receivers))
	result := make([]string, 0, len(receivers))
	for _, receiver := range receivers {
		if !unique[receiver] {
			result = append(result, receiver)
			unique[receiver] = true
		}
	}
	sort.Strings(result)
	return result
}

func sameReceiverSet(expected, actual []string) bool {
	if len(expected) != len(actual) {
		return false
	}
	for i := range expected {
		if expected[i] != actual[i] {
			return false
		}
	}
	return true
}

func TestRouting(client *alertmanager.Client, labels map[string]string) (*TestResult, error) {
	config, err := client.GetConfig()
	if err != nil {
		return &TestResult{
			Labels: labels,
			Error:  fmt.Sprintf("Failed to fetch config: %v", err),
		}, err
	}

	if config == nil {
		err := fmt.Errorf("alertmanager returned empty config")
		return &TestResult{
			Labels: labels,
			Error:  err.Error(),
		}, err
	}

	if config.Route == nil {
		err := fmt.Errorf("alertmanager config has no route defined")
		return &TestResult{
			Labels: labels,
			Error:  err.Error(),
		}, err
	}

	routeContext, span := telemetry.StartSpan(context.Background(), "alertmanager.route.evaluate")
	defer span.End()

	receiver, matchedRoutes, err := client.FindMatchingRoute(labels, config)
	if err != nil {
		span.SetStatus(codes.Error, "route evaluation failed")
		telemetry.RecordRoute(routeContext, telemetry.RouteFailed)
		return &TestResult{
			Labels: labels,
			Error:  fmt.Sprintf("Error finding route: %v", err),
		}, err
	}
	if receiver == "" {
		telemetry.RecordRoute(routeContext, telemetry.RouteUnmatched)
	} else {
		telemetry.RecordRoute(routeContext, telemetry.RouteMatched)
	}

	var receiverConfig *alertmanager.Receiver
	if receiver != "" {
		receiverConfig = client.FindReceiverByName(receiver, config)
	}

	return &TestResult{
		Receiver:       receiver,
		ReceiverConfig: receiverConfig,
		MatchedRoutes:  matchedRoutes,
		Labels:         labels,
	}, nil
}

func PrintSuiteResults(results []SuiteResult, format OutputFormat) error {
	switch format {
	case OutputFormatJSON:
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(results)
	case OutputFormatSimple:
		for _, result := range results {
			status := "FAIL"
			if result.Passed {
				status = "PASS"
			}
			if _, err := fmt.Fprintf(os.Stdout, "%s %s: expected=%v actual=%v", status, result.Name, result.ExpectedReceivers, result.ActualReceivers); err != nil {
				return err
			}
			if result.Error != "" {
				if _, err := fmt.Fprintf(os.Stdout, " error=%s", result.Error); err != nil {
					return err
				}
			}
			if _, err := fmt.Fprintln(os.Stdout); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("unknown output format: %s", format)
	}
}

// OutputFormat represents the output format
type OutputFormat string

const (
	OutputFormatJSON   OutputFormat = "json"
	OutputFormatSimple OutputFormat = "simple"
)

// PrintResult prints the test result in the specified format
func PrintResult(result *TestResult, format OutputFormat) error {
	switch format {
	case OutputFormatJSON:
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(result)

	case OutputFormatSimple:
		if result.Error != "" {
			fmt.Fprintf(os.Stderr, "Error: %s\n", result.Error)
			return fmt.Errorf("%s", result.Error)
		}

		fmt.Printf("Labels: %v\n", result.Labels)
		fmt.Printf("Receiver: %s\n", result.Receiver)

		if len(result.MatchedRoutes) > 0 {
			fmt.Printf("Matched Routes:\n")
			for i, mr := range result.MatchedRoutes {
				route := mr.Route
				resolvedReceiver := mr.ResolvedReceiver
				if resolvedReceiver == "" {
					resolvedReceiver = route.Receiver
				}
				fmt.Printf("  %d. ", i+1)
				if !mr.IsEffective {
					fmt.Printf("[trace-only] ")
				}
				if mr.IsSubroute {
					if len(mr.ParentReceivers) > 0 {
						fmt.Printf("[subroute of %s] ", strings.Join(mr.ParentReceivers, " -> "))
					} else {
						fmt.Printf("[subroute] ")
					}
				}
				if resolvedReceiver != "" {
					fmt.Printf("receiver=%s ", resolvedReceiver)
				}
				if len(route.Match) > 0 {
					fmt.Printf("match=%v ", route.Match)
				}
				if len(route.MatchRE) > 0 {
					fmt.Printf("match_re=%v ", route.MatchRE)
				}
				if len(route.Matchers) > 0 {
					fmt.Printf("matchers=%v ", route.Matchers)
				}
				if len(route.GroupBy) > 0 {
					fmt.Printf("group_by=%v ", route.GroupBy)
				}
				if route.GroupWait != "" {
					fmt.Printf("group_wait=%s ", route.GroupWait)
				}
				if route.GroupInterval != "" {
					fmt.Printf("group_interval=%s ", route.GroupInterval)
				}
				if route.RepeatInterval != "" {
					fmt.Printf("repeat_interval=%s ", route.RepeatInterval)
				}
				if route.Continue {
					fmt.Printf("continue=true")
				}
				fmt.Println()
			}
		}

		if result.ReceiverConfig != nil {
			fmt.Printf("\nReceiver Configuration:\n")
			fmt.Printf("  Name: %s\n", result.ReceiverConfig.Name)
			if len(result.ReceiverConfig.EmailConfigs) > 0 {
				fmt.Printf("  Email Configs: %d\n", len(result.ReceiverConfig.EmailConfigs))
			}
			if len(result.ReceiverConfig.SlackConfigs) > 0 {
				fmt.Printf("  Slack Configs: %d\n", len(result.ReceiverConfig.SlackConfigs))
			}
			if len(result.ReceiverConfig.PagerdutyConfigs) > 0 {
				fmt.Printf("  Pagerduty Configs: %d\n", len(result.ReceiverConfig.PagerdutyConfigs))
			}
			if len(result.ReceiverConfig.WebhookConfigs) > 0 {
				fmt.Printf("  Webhook Configs: %d\n", len(result.ReceiverConfig.WebhookConfigs))
			}
			if len(result.ReceiverConfig.OpsGenieConfigs) > 0 {
				fmt.Printf("  OpsGenie Configs: %d\n", len(result.ReceiverConfig.OpsGenieConfigs))
			}
		}

		return nil

	default:
		return fmt.Errorf("unknown output format: %s", format)
	}
}
