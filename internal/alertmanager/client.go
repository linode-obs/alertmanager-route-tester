package alertmanager

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Client struct {
	baseURL    string
	httpClient *http.Client
}

type StatusResponse struct {
	ConfigYAML struct {
		Original string `json:"original"`
	} `json:"config"`
}

type Config struct {
	Route     *Route     `json:"route" yaml:"route"`
	Receivers []Receiver `json:"receivers" yaml:"receivers"`
}

type Route struct {
	Receiver       string            `json:"receiver,omitempty" yaml:"receiver,omitempty"`
	Match          map[string]string `json:"match,omitempty" yaml:"match,omitempty"`
	MatchRE        map[string]string `json:"match_re,omitempty" yaml:"match_re,omitempty"`
	GroupBy        []string          `json:"group_by,omitempty" yaml:"group_by,omitempty"`
	Continue       bool              `json:"continue,omitempty" yaml:"continue,omitempty"`
	Routes         []*Route          `json:"routes,omitempty" yaml:"routes,omitempty"`
	GroupWait      string            `json:"group_wait,omitempty" yaml:"group_wait,omitempty"`
	GroupInterval  string            `json:"group_interval,omitempty" yaml:"group_interval,omitempty"`
	RepeatInterval string            `json:"repeat_interval,omitempty" yaml:"repeat_interval,omitempty"`
}

type Receiver struct {
	Name             string            `json:"name" yaml:"name"`
	RawConfig        string            `json:"-"` // Store raw YAML config
	EmailConfigs     []EmailConfig     `json:"email_configs,omitempty" yaml:"email_configs,omitempty"`
	SlackConfigs     []SlackConfig     `json:"slack_configs,omitempty" yaml:"slack_configs,omitempty"`
	PagerdutyConfigs []PagerdutyConfig `json:"pagerduty_configs,omitempty" yaml:"pagerduty_configs,omitempty"`
	WebhookConfigs   []WebhookConfig   `json:"webhook_configs,omitempty" yaml:"webhook_configs,omitempty"`
	OpsGenieConfigs  []OpsGenieConfig  `json:"opsgenie_configs,omitempty" yaml:"opsgenie_configs,omitempty"`
}

type EmailConfig struct {
	To        string `json:"to,omitempty" yaml:"to,omitempty"`
	From      string `json:"from,omitempty" yaml:"from,omitempty"`
	Subject   string `json:"subject,omitempty" yaml:"subject,omitempty"`
	Smarthost string `json:"smarthost,omitempty" yaml:"smarthost,omitempty"`
}

type SlackConfig struct {
	APIURL   string `json:"api_url,omitempty" yaml:"api_url,omitempty"`
	Channel  string `json:"channel,omitempty" yaml:"channel,omitempty"`
	Username string `json:"username,omitempty" yaml:"username,omitempty"`
	Title    string `json:"title,omitempty" yaml:"title,omitempty"`
	Text     string `json:"text,omitempty" yaml:"text,omitempty"`
}

type PagerdutyConfig struct {
	ServiceKey  string `json:"service_key,omitempty" yaml:"service_key,omitempty"`
	RoutingKey  string `json:"routing_key,omitempty" yaml:"routing_key,omitempty"`
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
}

type WebhookConfig struct {
	URL          string `json:"url,omitempty" yaml:"url,omitempty"`
	Title        string `json:"title,omitempty" yaml:"title,omitempty"`
	SendResolved bool   `json:"send_resolved,omitempty" yaml:"send_resolved,omitempty"`
	MaxAlerts    int    `json:"max_alerts,omitempty" yaml:"max_alerts,omitempty"`
}

type OpsGenieConfig struct {
	APIKey      string `json:"api_key,omitempty" yaml:"api_key,omitempty"`
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
}

func NewClient(baseURL string, skipTLSVerify bool) *Client {
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: skipTLSVerify,
		},
	}

	return &Client{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		httpClient: &http.Client{
			Transport: transport,
		},
	}
}

func (c *Client) BaseURL() string {
	return c.baseURL
}

func (c *Client) GetConfig() (*Config, error) {
	resp, err := c.httpClient.Get(c.baseURL + "/api/v2/status")
	if err != nil {
		return nil, fmt.Errorf("failed to fetch status: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, string(body))
	}

	var status StatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	// Parse the YAML configuration
	var config Config
	if err := yaml.Unmarshal([]byte(status.ConfigYAML.Original), &config); err != nil {
		return nil, fmt.Errorf("failed to parse YAML config: %w", err)
	}

	// Extract raw receiver configurations from the original YAML
	if err := c.extractRawReceiverConfigs(&config, status.ConfigYAML.Original); err != nil {
		return nil, fmt.Errorf("failed to extract receiver configs: %w", err)
	}

	return &config, nil
}

// extractRawReceiverConfigs extracts the raw YAML for each receiver
func (c *Client) extractRawReceiverConfigs(config *Config, originalYAML string) error {
	lines := strings.Split(originalYAML, "\n")

	for i := range config.Receivers {
		receiver := &config.Receivers[i]

		// Find the receiver block in the original YAML
		receiverStartLine := -1
		for j, line := range lines {
			if strings.Contains(line, "- name: '"+receiver.Name+"'") ||
				strings.Contains(line, "- name: "+receiver.Name) ||
				strings.Contains(line, "- name: \""+receiver.Name+"\"") {
				receiverStartLine = j
				break
			}
		}

		if receiverStartLine == -1 {
			continue
		}

		// Extract the receiver block
		var receiverLines []string
		receiverLines = append(receiverLines, "- name: "+receiver.Name)

		// Find the end of this receiver block
		for j := receiverStartLine + 1; j < len(lines); j++ {
			line := lines[j]

			// If we hit another receiver or the end, stop
			if strings.HasPrefix(line, "- name:") ||
				(strings.HasPrefix(line, "templates:") && !strings.HasPrefix(line, "  ")) {
				break
			}

			// Add lines that belong to this receiver
			if strings.HasPrefix(line, "  ") || strings.TrimSpace(line) == "" {
				receiverLines = append(receiverLines, line)
			} else {
				break
			}
		}

		receiver.RawConfig = strings.Join(receiverLines, "\n")
	}

	return nil
}

// FindMatchingRoute determines which receiver an alert would match
func (c *Client) FindMatchingRoute(labels map[string]string, config *Config) (string, []*Route, error) {
	if config.Route == nil {
		return "", nil, fmt.Errorf("no route configuration found")
	}

	matchedRoutes := []*Route{}
	receiver := findMatchingRouteRecursive(labels, config.Route, &matchedRoutes)

	// If no specific routes matched, use the root route's receiver as default
	if receiver == "" && config.Route.Receiver != "" {
		receiver = config.Route.Receiver
	}

	return receiver, matchedRoutes, nil
}

// FindReceiverByName finds a receiver configuration by name
func (c *Client) FindReceiverByName(name string, config *Config) *Receiver {
	for _, receiver := range config.Receivers {
		if receiver.Name == name {
			return &receiver
		}
	}
	return nil
}

func findMatchingRouteRecursive(labels map[string]string, route *Route, matchedRoutes *[]*Route) string {
	var finalReceiver string

	for _, childRoute := range route.Routes {
		if matchesRoute(labels, childRoute) {
			*matchedRoutes = append(*matchedRoutes, childRoute)

			// Recursively check child routes first
			if receiver := findMatchingRouteRecursive(labels, childRoute, matchedRoutes); receiver != "" {
				finalReceiver = receiver
			} else if childRoute.Receiver != "" {
				// No child matched but this route has a receiver
				finalReceiver = childRoute.Receiver
			}

			// If continue is false, stop checking other routes at this level
			if !childRoute.Continue {
				break
			}
		}
	}

	return finalReceiver
}

func matchesRoute(labels map[string]string, route *Route) bool {
	for key, value := range route.Match {
		if labels[key] != value {
			return false
		}
	}

	for key, pattern := range route.MatchRE {
		labelValue, ok := labels[key]
		if !ok {
			return false
		}
		matched, err := regexp.MatchString(pattern, labelValue)
		if err != nil || !matched {
			return false
		}
	}

	return true
}

type LabelSuggestion struct {
	Key    string
	Values []string
}

type SampleAlert struct {
	Name        string
	Description string
	Labels      map[string]string
}

// ExtractLabelKeys extracts all unique label keys from the route configuration
func ExtractLabelKeys(config *Config) []string {
	keys := make(map[string]bool)
	extractFromRoute(config.Route, keys)

	result := make([]string, 0, len(keys))
	for key := range keys {
		result = append(result, key)
	}
	return result
}

// ExtractLabelSuggestions extracts label keys and their possible values from the route configuration
func ExtractLabelSuggestions(config *Config) []LabelSuggestion {
	labelValues := make(map[string]map[string]bool)
	extractLabelValuesFromRoute(config.Route, labelValues)

	suggestions := make([]LabelSuggestion, 0, len(labelValues))
	for key, valuesMap := range labelValues {
		values := make([]string, 0, len(valuesMap))
		for value := range valuesMap {
			values = append(values, value)
		}
		suggestions = append(suggestions, LabelSuggestion{
			Key:    key,
			Values: values,
		})
	}
	return suggestions
}

func extractFromRoute(route *Route, keys map[string]bool) {
	if route == nil {
		return
	}

	for key := range route.Match {
		keys[key] = true
	}
	for key := range route.MatchRE {
		keys[key] = true
	}

	for _, child := range route.Routes {
		extractFromRoute(child, keys)
	}
}

func extractLabelValuesFromRoute(route *Route, labelValues map[string]map[string]bool) {
	if route == nil {
		return
	}

	for key, value := range route.Match {
		if labelValues[key] == nil {
			labelValues[key] = make(map[string]bool)
		}
		labelValues[key][value] = true
	}

	for _, child := range route.Routes {
		extractLabelValuesFromRoute(child, labelValues)
	}
}

// GenerateSampleAlerts creates example alerts based on the configuration
func GenerateSampleAlerts(config *Config) []SampleAlert {
	// Alerts that WILL match specific routes
	matchingAlerts := []SampleAlert{
		{
			Name:        "Critical Alert",
			Description: "Routes to pagerduty-critical (severity:critical stops routing)",
			Labels: map[string]string{
				"alertname": "HighErrorRate",
				"severity":  "critical",
			},
		},
		{
			Name:        "Critical Security Alert",
			Description: "Routes to security-team (continue) + pagerduty-security",
			Labels: map[string]string{
				"alertname": "CriticalSecurityBreach",
				"category":  "security",
				"severity":  "critical",
			},
		},
		{
			Name:        "Database Team Alert",
			Description: "Routes to team-database (team match)",
			Labels: map[string]string{
				"alertname": "PostgreSQLSlowQueries",
				"team":      "database",
			},
		},
		{
			Name:        "Production Web Service",
			Description: "Routes to slack-platform-prod (service + env match)",
			Labels: map[string]string{
				"alertname":   "HighLatency",
				"service":     "web",
				"environment": "production",
			},
		},
		{
			Name:        "Warning Alert",
			Description: "Routes to slack-warnings (severity:warning continues)",
			Labels: map[string]string{
				"alertname": "DiskSpaceLow",
				"severity":  "warning",
			},
		},
		{
			Name:        "Infrastructure US West",
			Description: "Routes to team-infra-west (component + region)",
			Labels: map[string]string{
				"alertname": "HighCPUUsage",
				"component": "infrastructure",
				"region":    "us-west-2",
			},
		},
	}

	// Alerts that WON'T match any specific route (goes to root route's default receiver)
	nonMatchingAlerts := []SampleAlert{
		{
			Name:        "Truly Unrouted Alert",
			Description: "No specific route matches - uses root route's default receiver",
			Labels: map[string]string{
				"alertname": "RandomSystemInfo",
				"priority":  "low",      // doesn't match severity routes
				"source":    "external", // doesn't match any service/component routes
			},
		},
		{
			Name:        "Unknown Service Alert",
			Description: "No specific route matches - uses root route's default receiver",
			Labels: map[string]string{
				"alertname": "UnknownIssue",
				"level":     "notice", // doesn't match severity routes
				"system":    "legacy", // doesn't match component routes
			},
		},
	}

	// Seed random number generator
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))

	// Shuffle and pick 2 from matching alerts
	rng.Shuffle(len(matchingAlerts), func(i, j int) {
		matchingAlerts[i], matchingAlerts[j] = matchingAlerts[j], matchingAlerts[i]
	})

	// Shuffle and pick 1 from non-matching alerts
	rng.Shuffle(len(nonMatchingAlerts), func(i, j int) {
		nonMatchingAlerts[i], nonMatchingAlerts[j] = nonMatchingAlerts[j], nonMatchingAlerts[i]
	})

	// Combine: 2 matching + 1 non-matching
	samples := make([]SampleAlert, 0, 3)
	if len(matchingAlerts) >= 2 {
		samples = append(samples, matchingAlerts[0], matchingAlerts[1])
	}
	if len(nonMatchingAlerts) > 0 {
		samples = append(samples, nonMatchingAlerts[0])
	}

	// Shuffle final order so non-matching isn't always last
	rng.Shuffle(len(samples), func(i, j int) {
		samples[i], samples[j] = samples[j], samples[i]
	})

	return samples
}
