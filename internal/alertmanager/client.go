// ABOUTME: Client for interacting with Alertmanager API
// ABOUTME: Fetches configuration and provides types for alert routing

package alertmanager

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
)

type Client struct {
	baseURL    string
	httpClient *http.Client
}

type StatusResponse struct {
	Config Config `json:"config"`
}

type Config struct {
	Route     *Route     `json:"route"`
	Receivers []Receiver `json:"receivers"`
}

type Route struct {
	Receiver       string            `json:"receiver,omitempty"`
	Match          map[string]string `json:"match,omitempty"`
	MatchRE        map[string]string `json:"match_re,omitempty"`
	GroupBy        []string          `json:"group_by,omitempty"`
	Continue       bool              `json:"continue,omitempty"`
	Routes         []*Route          `json:"routes,omitempty"`
	GroupWait      string            `json:"group_wait,omitempty"`
	GroupInterval  string            `json:"group_interval,omitempty"`
	RepeatInterval string            `json:"repeat_interval,omitempty"`
}

type Receiver struct {
	Name string `json:"name"`
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

	return &status.Config, nil
}

// FindMatchingRoute determines which receiver an alert would match
func (c *Client) FindMatchingRoute(labels map[string]string, config *Config) (string, []*Route, error) {
	if config.Route == nil {
		return "", nil, fmt.Errorf("no route configuration found")
	}

	matchedRoutes := []*Route{}
	receiver := findMatchingRouteRecursive(labels, config.Route, &matchedRoutes)

	if receiver == "" && config.Route.Receiver != "" {
		receiver = config.Route.Receiver
		matchedRoutes = append([]*Route{config.Route}, matchedRoutes...)
	}

	return receiver, matchedRoutes, nil
}

func findMatchingRouteRecursive(labels map[string]string, route *Route, matchedRoutes *[]*Route) string {
	// Check if any child routes match
	for _, childRoute := range route.Routes {
		if matchesRoute(labels, childRoute) {
			*matchedRoutes = append(*matchedRoutes, childRoute)

			// Recursively check child routes first
			if receiver := findMatchingRouteRecursive(labels, childRoute, matchedRoutes); receiver != "" {
				return receiver
			}

			// If no child matched but this route has a receiver, use it
			if childRoute.Receiver != "" {
				return childRoute.Receiver
			}

			// If continue is false, stop checking other routes at this level
			if !childRoute.Continue {
				break
			}
		}
	}

	return ""
}

func matchesRoute(labels map[string]string, route *Route) bool {
	// Check exact matches
	for key, value := range route.Match {
		if labels[key] != value {
			return false
		}
	}

	// Check regex matches
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
