package alertmanager

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
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
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

const maxAlertmanagerResponseBodyBytes int64 = 10 << 20

var errResponseBodyTooLarge = errors.New("response body exceeds limit")

type Client struct {
	baseURL          string
	httpClient       *http.Client
	retryMaxAttempts int
	retryBackoff     time.Duration

	// Config cache
	cacheMu              sync.RWMutex
	cachedConfig         *Config
	cacheTimestamp       time.Time
	cacheGeneration      uint64
	configFetchOnce      sync.Once
	configFetchSemaphore chan struct{}
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
			InsecureSkipVerify: skipTLSVerify, // #nosec G402 -- TLS verification is explicitly configurable by the caller.
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
	return c.CheckConnectionContext(context.Background())
}

func (c *Client) CheckConnectionContext(ctx context.Context) error {
	var status StatusResponse
	var lastErr error

	for attempt := 1; attempt <= c.retryMaxAttempts; attempt++ {
		retryable, err := c.fetchStatus(ctx, &status)
		if err == nil {
			lastErr = nil
			break
		}
		lastErr = err
		if !retryable || attempt == c.retryMaxAttempts {
			break
		}
		if err := waitForRetry(ctx, c.retryBackoff); err != nil {
			return err
		}
	}

	return lastErr
}

func (c *Client) GetConfig() (*Config, error) {
	return c.GetConfigContext(context.Background())
}

func (c *Client) GetConfigContext(ctx context.Context) (*Config, error) {
	config, _, err := c.GetConfigWithStatusContext(ctx)
	return config, err
}

func (c *Client) GetConfigWithStatus() (*Config, bool, error) {
	return c.GetConfigWithStatusContext(context.Background())
}

func (c *Client) GetConfigWithStatusContext(ctx context.Context) (*Config, bool, error) {
	c.cacheMu.RLock()
	if c.cachedConfig != nil {
		config := c.cachedConfig
		c.cacheMu.RUnlock()
		return config, true, nil
	}
	c.cacheMu.RUnlock()

	if err := c.acquireConfigFetch(ctx); err != nil {
		return nil, false, err
	}
	defer c.releaseConfigFetch()

	// Another fetch may have populated the cache while this caller waited.
	c.cacheMu.RLock()
	if c.cachedConfig != nil {
		config := c.cachedConfig
		c.cacheMu.RUnlock()
		return config, true, nil
	}
	generation := c.cacheGeneration
	c.cacheMu.RUnlock()

	config, err := c.fetchConfig(ctx)
	if err != nil {
		return nil, false, err
	}
	c.publishConfig(config, generation)
	return config, false, nil
}

// RefreshConfig fetches a fresh configuration and replaces the cached value
// only after the fetch and parse succeed.
func (c *Client) RefreshConfig() (*Config, error) {
	return c.RefreshConfigContext(context.Background())
}

func (c *Client) RefreshConfigContext(ctx context.Context) (*Config, error) {
	if err := c.acquireConfigFetch(ctx); err != nil {
		return nil, err
	}
	defer c.releaseConfigFetch()

	c.cacheMu.RLock()
	generation := c.cacheGeneration
	c.cacheMu.RUnlock()

	config, err := c.fetchConfig(ctx)
	if err != nil {
		return nil, err
	}
	c.publishConfig(config, generation)
	return config, nil
}

func (c *Client) acquireConfigFetch(ctx context.Context) error {
	c.configFetchOnce.Do(func() {
		c.configFetchSemaphore = make(chan struct{}, 1)
	})

	select {
	case c.configFetchSemaphore <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *Client) releaseConfigFetch() {
	<-c.configFetchSemaphore
}

func (c *Client) fetchConfig(ctx context.Context) (*Config, error) {
	var status StatusResponse
	var lastErr error

	for attempt := 1; attempt <= c.retryMaxAttempts; attempt++ {
		retryable, err := c.fetchStatus(ctx, &status)
		if err == nil {
			lastErr = nil
			break
		}
		lastErr = err
		if !retryable || attempt == c.retryMaxAttempts {
			break
		}
		if err := waitForRetry(ctx, c.retryBackoff); err != nil {
			return nil, err
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}

	var config Config
	if err := yaml.Unmarshal([]byte(status.ConfigYAML.Original), &config); err != nil {
		return nil, fmt.Errorf("failed to parse YAML config: %w", err)
	}

	if err := c.extractRawReceiverConfigs(&config, status.ConfigYAML.Original); err != nil {
		return nil, fmt.Errorf("failed to extract receiver configs: %w", err)
	}

	return &config, nil
}

func waitForRetry(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return nil
		}
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (c *Client) publishConfig(config *Config, generation uint64) {
	c.cacheMu.Lock()
	defer c.cacheMu.Unlock()
	if generation != c.cacheGeneration {
		return
	}
	c.cachedConfig = config
	c.cacheTimestamp = time.Now()
}

// InvalidateConfig clears the cached configuration so the next call to
// GetConfig() fetches a fresh copy from Alertmanager.
func (c *Client) InvalidateConfig() {
	c.cacheMu.Lock()
	c.cachedConfig = nil
	c.cacheTimestamp = time.Time{}
	c.cacheGeneration++
	c.cacheMu.Unlock()
}

// ConfigCachedAt returns the time the configuration was last fetched from
// Alertmanager, or the zero time if nothing has been cached yet.
func (c *Client) ConfigCachedAt() time.Time {
	c.cacheMu.RLock()
	defer c.cacheMu.RUnlock()
	return c.cacheTimestamp
}

func (c *Client) fetchStatus(ctx context.Context, status *StatusResponse) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/v2/status", nil)
	if err != nil {
		return true, fmt.Errorf("failed to create status request: %w", err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return true, fmt.Errorf("failed to fetch status: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	body, err := readResponseBody(resp.Body)
	if err != nil {
		return !errors.Is(err, errResponseBodyTooLarge), fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		retryable := resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= http.StatusInternalServerError
		return retryable, fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, string(body))
	}

	if err := json.Unmarshal(body, status); err != nil {
		return true, fmt.Errorf("failed to decode response: %w", err)
	}

	return false, nil
}

func readResponseBody(reader io.Reader) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(reader, maxAlertmanagerResponseBodyBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maxAlertmanagerResponseBodyBytes {
		return nil, fmt.Errorf("%w of %d bytes", errResponseBodyTooLarge, maxAlertmanagerResponseBodyBytes)
	}
	return body, nil
}

// extractRawReceiverConfigs extracts the YAML for each receiver from the
// structured Alertmanager response instead of relying on line indentation.
func (c *Client) extractRawReceiverConfigs(config *Config, originalYAML string) error {
	var document yaml.Node
	if err := yaml.Unmarshal([]byte(originalYAML), &document); err != nil {
		return err
	}
	if len(document.Content) == 0 {
		return nil
	}

	receiversNode := yamlMappingValue(document.Content[0], "receivers")
	if receiversNode == nil || receiversNode.Kind != yaml.SequenceNode {
		return nil
	}

	for i := range config.Receivers {
		receiver := &config.Receivers[i]
		for _, receiverNode := range receiversNode.Content {
			if receiverNode.Kind != yaml.MappingNode {
				continue
			}
			nameNode := yamlMappingValue(receiverNode, "name")
			if nameNode == nil || nameNode.Value != receiver.Name {
				continue
			}

			receiverSequence := yaml.Node{
				Kind:    yaml.SequenceNode,
				Content: []*yaml.Node{receiverNode},
			}
			rawConfig, err := yaml.Marshal(&receiverSequence)
			if err != nil {
				return err
			}
			receiver.RawConfig = strings.TrimSpace(string(rawConfig))
			break
		}
	}

	return nil
}

func yamlMappingValue(mapping *yaml.Node, name string) *yaml.Node {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == name {
			return mapping.Content[i+1]
		}
	}
	return nil
}

func buildTLSConfig(opts TLSOptions) (*tls.Config, error) {
	tlsConfig := &tls.Config{
		InsecureSkipVerify: opts.SkipVerify, // #nosec G402 -- TLS verification is explicitly configurable by the caller.
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
	// ResolvedReceiver is the receiver after applying parent inheritance.
	ResolvedReceiver string `json:"resolved_receiver,omitempty"`
}

// FindMatchingRoute determines which receiver an alert would match.
// It returns a flat, ordered list of MatchedRoute entries that captures the
// full parent→child traversal path so callers can reconstruct the chain.
func (c *Client) FindMatchingRoute(labels map[string]string, config *Config) (string, []MatchedRoute, error) {
	if config.Route == nil {
		return "", nil, fmt.Errorf("no route configuration found")
	}

	matched := []MatchedRoute{}
	receiver := findMatchingRouteRecursive(labels, config.Route, &matched, 0, nil, config.Route.Receiver)

	// If no specific routes matched, use the root route's receiver as default
	if receiver == "" && config.Route.Receiver != "" {
		receiver = config.Route.Receiver
	}

	return receiver, matched, nil
}

// FindReceiverByName finds a receiver configuration by name
func (c *Client) FindReceiverByName(name string, config *Config) *Receiver {
	for i := range config.Receivers {
		if config.Receivers[i].Name == name {
			return &config.Receivers[i]
		}
	}
	return nil
}

func findMatchingRouteRecursive(labels map[string]string, route *Route, matched *[]MatchedRoute, depth int, parentReceivers []string, inheritedReceiver string) string {
	var finalReceiver string

	for _, childRoute := range route.Routes {
		if matchesRoute(labels, childRoute) {
			receiverForRoute := childRoute.Receiver
			if receiverForRoute == "" {
				receiverForRoute = inheritedReceiver
			}

			matchIndex := len(*matched)
			mr := MatchedRoute{
				Route:            childRoute,
				Depth:            depth,
				ParentReceivers:  parentReceivers,
				IsSubroute:       depth > 0,
				ResolvedReceiver: receiverForRoute,
			}
			*matched = append(*matched, mr)

			// Build the ancestry list for this route's own children.
			// Only include non-empty receiver names.
			childParents := parentReceivers
			if receiverForRoute != "" {
				childParents = make([]string, len(parentReceivers)+1)
				copy(childParents, parentReceivers)
				childParents[len(parentReceivers)] = receiverForRoute
			}

			// Recursively check child routes first.
			if receiver := findMatchingRouteRecursive(labels, childRoute, matched, depth+1, childParents, receiverForRoute); receiver != "" {
				finalReceiver = receiver
			} else if receiverForRoute != "" {
				// No child matched, so this route inherits or supplies the receiver.
				finalReceiver = receiverForRoute
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
		matched, err := matchesRegex(pattern, labels[key])
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

	value := labels[parsed.Label]

	switch parsed.Operator {
	case "=":
		return value == parsed.Value
	case "!=":
		return value != parsed.Value
	case "=~":
		matched, err := matchesRegex(parsed.Value, value)
		return err == nil && matched
	case "!~":
		matched, err := matchesRegex(parsed.Value, value)
		return err == nil && !matched
	default:
		return false
	}
}

func matchesRegex(pattern, value string) (bool, error) {
	return regexp.MatchString("^(?:"+pattern+")$", value)
}

var matcherPattern = regexp.MustCompile(`^\s*([^=!~\s]+)\s*(=~|!~|=|!=)\s*(.*?)\s*$`)

func parseMatcher(input string) (parsedMatcher, bool) {
	matches := matcherPattern.FindStringSubmatch(input)
	if len(matches) != 4 {
		return parsedMatcher{}, false
	}

	value := strings.TrimSpace(matches[3])
	if len(value) >= 2 {
		if value[0] == '"' && value[len(value)-1] == '"' {
			unquoted, err := unquoteMatcherValue(value)
			if err != nil {
				return parsedMatcher{}, false
			}
			value = unquoted
		} else if value[0] == '\'' && value[len(value)-1] == '\'' {
			value = value[1 : len(value)-1]
		}
	}

	return parsedMatcher{
		Label:    matches[1],
		Operator: matches[2],
		Value:    value,
	}, true
}

func unquoteMatcherValue(value string) (string, error) {
	rawValue := value[1 : len(value)-1]
	if !utf8.ValidString(rawValue) {
		return "", fmt.Errorf("matcher value is not valid UTF-8")
	}

	var builder strings.Builder
	escaped := false
	for i, r := range rawValue {
		if escaped {
			escaped = false
			switch r {
			case 'n':
				builder.WriteByte('\n')
			case '\\', '"':
				builder.WriteRune(r)
			default:
				builder.WriteByte('\\')
				builder.WriteRune(r)
			}
			continue
		}

		switch r {
		case '\\':
			if i < len(rawValue)-1 {
				escaped = true
				continue
			}
			builder.WriteByte('\\')
		case '"':
			return "", fmt.Errorf("matcher value contains unescaped double quote")
		default:
			builder.WriteRune(r)
		}
	}

	return builder.String(), nil
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
	rng := rand.New(rand.NewSource(time.Now().UnixNano())) // #nosec G404 -- sample alert generation does not require cryptographic randomness.

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
