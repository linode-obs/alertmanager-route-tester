package alertmanager

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

type Client struct {
	baseURL          string
	httpClient       *http.Client
	retryMaxAttempts int
	retryBackoff     time.Duration

	// Config cache
	cacheMu        sync.RWMutex
	cachedConfig   *Config
	cacheTimestamp time.Time
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
	Matchers       []string          `json:"matchers,omitempty" yaml:"matchers,omitempty"`
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

type ClientOptions struct {
	BaseURL  string
	TLS      TLSOptions
	Timeouts TimeoutOptions
	Retry    RetryOptions
	Pool     PoolOptions
}

type TLSOptions struct {
	SkipVerify bool
	CAFile     string
	CertFile   string
	KeyFile    string
}

type TimeoutOptions struct {
	Request        time.Duration
	Dial           time.Duration
	TLSHandshake   time.Duration
	ResponseHeader time.Duration
	IdleConn       time.Duration
	ExpectContinue time.Duration
}

type RetryOptions struct {
	MaxAttempts int
	Backoff     time.Duration
}

type PoolOptions struct {
	MaxIdleConns        int
	MaxIdleConnsPerHost int
	MaxConnsPerHost     int
}

func NewClient(baseURL string, skipTLSVerify bool) *Client {
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: skipTLSVerify,
		},
	}

	return &Client{
		baseURL:          strings.TrimSuffix(baseURL, "/"),
		httpClient:       &http.Client{Transport: transport},
		retryMaxAttempts: 1,
		retryBackoff:     0,
	}
}

func NewClientWithOptions(opts ClientOptions) (*Client, error) {
	if opts.BaseURL == "" {
		return nil, fmt.Errorf("base URL is required")
	}

	tlsConfig, err := buildTLSConfig(opts.TLS)
	if err != nil {
		return nil, err
	}

	transport := &http.Transport{
		TLSClientConfig:       tlsConfig,
		MaxIdleConns:          opts.Pool.MaxIdleConns,
		MaxIdleConnsPerHost:   opts.Pool.MaxIdleConnsPerHost,
		MaxConnsPerHost:       opts.Pool.MaxConnsPerHost,
		IdleConnTimeout:       opts.Timeouts.IdleConn,
		TLSHandshakeTimeout:   opts.Timeouts.TLSHandshake,
		ResponseHeaderTimeout: opts.Timeouts.ResponseHeader,
		ExpectContinueTimeout: opts.Timeouts.ExpectContinue,
		DialContext: (&net.Dialer{
			Timeout:   opts.Timeouts.Dial,
			KeepAlive: 30 * time.Second,
		}).DialContext,
	}

	retryMax := opts.Retry.MaxAttempts
	if retryMax < 1 {
		retryMax = 1
	}

	return &Client{
		baseURL: strings.TrimSuffix(opts.BaseURL, "/"),
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   opts.Timeouts.Request,
		},
		retryMaxAttempts: retryMax,
		retryBackoff:     opts.Retry.Backoff,
	}, nil
}

func (c *Client) BaseURL() string {
	return c.baseURL
}

func (c *Client) CheckConnection() error {
	var status StatusResponse
	var lastErr error

	for attempt := 1; attempt <= c.retryMaxAttempts; attempt++ {
		retryable, err := c.fetchStatus(&status)
		if err == nil {
			lastErr = nil
			break
		}
		lastErr = err
		if !retryable || attempt == c.retryMaxAttempts {
			break
		}
		if c.retryBackoff > 0 {
			time.Sleep(c.retryBackoff)
		}
	}

	return lastErr
}

func (c *Client) GetConfig() (*Config, error) {
	// Hold the cache lock through a miss so concurrent fetches and invalidation
	// cannot publish stale configuration.
	c.cacheMu.Lock()
	defer c.cacheMu.Unlock()

	if c.cachedConfig != nil {
		return c.cachedConfig, nil
	}

	// Slow path: fetch fresh config.
	var status StatusResponse
	var lastErr error

	for attempt := 1; attempt <= c.retryMaxAttempts; attempt++ {
		retryable, err := c.fetchStatus(&status)
		if err == nil {
			lastErr = nil
			break
		}
		lastErr = err
		if !retryable || attempt == c.retryMaxAttempts {
			break
		}
		if c.retryBackoff > 0 {
			time.Sleep(c.retryBackoff)
		}
	}
	if lastErr != nil {
		return nil, lastErr
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

	// Store in cache.
	c.cachedConfig = &config
	c.cacheTimestamp = time.Now()

	return &config, nil
}

// InvalidateConfig clears the cached configuration so the next call to
// GetConfig() fetches a fresh copy from Alertmanager.
func (c *Client) InvalidateConfig() {
	c.cacheMu.Lock()
	c.cachedConfig = nil
	c.cacheTimestamp = time.Time{}
	c.cacheMu.Unlock()
}

// ConfigCachedAt returns the time the configuration was last fetched from
// Alertmanager, or the zero time if nothing has been cached yet.
func (c *Client) ConfigCachedAt() time.Time {
	c.cacheMu.RLock()
	defer c.cacheMu.RUnlock()
	return c.cacheTimestamp
}

func (c *Client) fetchStatus(status *StatusResponse) (bool, error) {
	resp, err := c.httpClient.Get(c.baseURL + "/api/v2/status")
	if err != nil {
		return true, fmt.Errorf("failed to fetch status: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		retryable := resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= http.StatusInternalServerError
		return retryable, fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, string(body))
	}

	if err := json.NewDecoder(resp.Body).Decode(status); err != nil {
		return true, fmt.Errorf("failed to decode response: %w", err)
	}

	return false, nil
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

func buildTLSConfig(opts TLSOptions) (*tls.Config, error) {
	tlsConfig := &tls.Config{
		InsecureSkipVerify: opts.SkipVerify,
	}

	if opts.CAFile != "" {
		caCert, err := os.ReadFile(opts.CAFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read CA file: %w", err)
		}
		certPool, err := x509.SystemCertPool()
		if err != nil || certPool == nil {
			certPool = x509.NewCertPool()
		}
		if !certPool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("failed to parse CA file")
		}
		tlsConfig.RootCAs = certPool
	}

	if opts.CertFile != "" || opts.KeyFile != "" {
		cert, err := tls.LoadX509KeyPair(opts.CertFile, opts.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("failed to load client certificate: %w", err)
		}
		tlsConfig.Certificates = []tls.Certificate{cert}
	}

	return tlsConfig, nil
}

// MatchedRoute represents a route that matched an alert, along with its position
// in the route tree expressed as a parent→child ancestry path.
type MatchedRoute struct {
	// Route is the matched route node.
	Route *Route `json:"route"`
	// Depth is 0 for direct children of the root route, 1 for their children, etc.
	Depth int `json:"depth"`
	// ParentReceivers is the ordered list of ancestor receivers from the root
	// down to (but not including) this route.  Empty for top-level routes.
	ParentReceivers []string `json:"parent_receivers,omitempty"`
	// IsSubroute is true when Depth > 0, i.e. this route was reached by
	// entering a nested `routes:` block of a parent that also matched.
	IsSubroute bool `json:"is_subroute"`
	// IsEffective is true when this route supplies a receiver for its matched branch.
	IsEffective bool `json:"is_effective"`
}

// FindMatchingRoute determines which receiver an alert would match.
// It returns a flat, ordered list of MatchedRoute entries that captures the
// full parent→child traversal path so callers can reconstruct the chain.
func (c *Client) FindMatchingRoute(labels map[string]string, config *Config) (string, []MatchedRoute, error) {
	if config.Route == nil {
		return "", nil, fmt.Errorf("no route configuration found")
	}

	matched := []MatchedRoute{}
	receiver := findMatchingRouteRecursive(labels, config.Route, &matched, 0, nil)

	// If no specific routes matched, use the root route's receiver as default
	if receiver == "" && config.Route.Receiver != "" {
		receiver = config.Route.Receiver
	}

	return receiver, matched, nil
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

func findMatchingRouteRecursive(labels map[string]string, route *Route, matched *[]MatchedRoute, depth int, parentReceivers []string) string {
	var finalReceiver string

	for _, childRoute := range route.Routes {
		if matchesRoute(labels, childRoute) {
			matchIndex := len(*matched)
			mr := MatchedRoute{
				Route:           childRoute,
				Depth:           depth,
				ParentReceivers: parentReceivers,
				IsSubroute:      depth > 0,
			}
			*matched = append(*matched, mr)

			// Build the ancestry list for this route's own children.
			// Only include non-empty receiver names.
			childParents := parentReceivers
			if childRoute.Receiver != "" {
				childParents = make([]string, len(parentReceivers)+1)
				copy(childParents, parentReceivers)
				childParents[len(parentReceivers)] = childRoute.Receiver
			}

			// Recursively check child routes first
			if receiver := findMatchingRouteRecursive(labels, childRoute, matched, depth+1, childParents); receiver != "" {
				finalReceiver = receiver
			} else if childRoute.Receiver != "" {
				// No child matched but this route has a receiver
				finalReceiver = childRoute.Receiver
				(*matched)[matchIndex].IsEffective = true
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

	for _, matcher := range route.Matchers {
		if !matchesMatcher(labels, matcher) {
			return false
		}
	}

	return true
}

type parsedMatcher struct {
	Label    string
	Operator string
	Value    string
}

func matchesMatcher(labels map[string]string, matcher string) bool {
	parsed, ok := parseMatcher(matcher)
	if !ok {
		return false
	}

	value, hasLabel := labels[parsed.Label]

	switch parsed.Operator {
	case "=":
		return hasLabel && value == parsed.Value
	case "!=":
		return !hasLabel || value != parsed.Value
	case "=~":
		if !hasLabel {
			return false
		}
		matched, err := regexp.MatchString(parsed.Value, value)
		return err == nil && matched
	case "!~":
		if !hasLabel {
			return true
		}
		matched, err := regexp.MatchString(parsed.Value, value)
		return err == nil && !matched
	default:
		return false
	}
}

var matcherPattern = regexp.MustCompile(`^\s*([^=!~\s]+)\s*(=~|!~|=|!=)\s*(.+?)\s*$`)

func parseMatcher(input string) (parsedMatcher, bool) {
	matches := matcherPattern.FindStringSubmatch(input)
	if len(matches) != 4 {
		return parsedMatcher{}, false
	}

	value := strings.TrimSpace(matches[3])
	if len(value) >= 2 {
		if (value[0] == '"' && value[len(value)-1] == '"') || (value[0] == '\'' && value[len(value)-1] == '\'') {
			value = value[1 : len(value)-1]
		}
	}

	return parsedMatcher{
		Label:    matches[1],
		Operator: matches[2],
		Value:    value,
	}, true
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
	for _, matcher := range route.Matchers {
		parsed, ok := parseMatcher(matcher)
		if ok {
			keys[parsed.Label] = true
		}
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

	for _, matcher := range route.Matchers {
		parsed, ok := parseMatcher(matcher)
		if !ok {
			continue
		}
		if parsed.Operator != "=" {
			continue
		}
		if labelValues[parsed.Label] == nil {
			labelValues[parsed.Label] = make(map[string]bool)
		}
		labelValues[parsed.Label][parsed.Value] = true
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
