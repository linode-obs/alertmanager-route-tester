package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/wbollock/alertmanager-route-tester/internal/alertmanager"
)

// TestResult represents the output of a route test
type TestResult struct {
	Receiver       string                      `json:"receiver"`
	ReceiverConfig *alertmanager.Receiver      `json:"receiver_config,omitempty"`
	MatchedRoutes  []alertmanager.MatchedRoute `json:"matched_routes"`
	Labels         map[string]string           `json:"labels"`
	Error          string                      `json:"error,omitempty"`
}

// TestRouting tests alert routing using the exact same logic as the web UI
// This function is designed to be used in Go tests
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

	receiver, matchedRoutes, err := client.FindMatchingRoute(labels, config)
	if err != nil {
		return &TestResult{
			Labels: labels,
			Error:  fmt.Sprintf("Error finding route: %v", err),
		}, err
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
