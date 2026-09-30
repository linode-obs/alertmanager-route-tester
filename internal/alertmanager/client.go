package alertmanager

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"regexp"
	"regexp/syntax"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/alertmanager/dispatch"
	amlabels "github.com/prometheus/alertmanager/pkg/labels"
	"gopkg.in/yaml.v3"
)

const maxAlertmanagerResponseBodyBytes int64 = 10 << 20

var errResponseBodyTooLarge = errors.New("response body exceeds limit")

// ErrSampleGenerationTooLarge indicates the route tree exceeds the sample-generation limit.
var ErrSampleGenerationTooLarge = errors.New("route tree exceeds the sample-generation limit")

type Client struct {
	baseURL          string
	matcherMode      MatcherMode
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
	sampleAlertsMu       sync.Mutex
	sampleAlertsConfig   *Config
	sampleAlerts         []SampleAlert
	sampleAlertsReady    bool
}

type StatusResponse struct {
	ConfigYAML struct {
		Original string `json:"original"`
	} `json:"config"`
}

type Config struct {
	Route        *Route     `json:"route" yaml:"route"`
	Receivers    []Receiver `json:"receivers" yaml:"receivers"`
	nativeRoutes *nativeRouteTree
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
	BaseURL     string
	MatcherMode MatcherMode
	TLS         TLSOptions
	Timeouts    TimeoutOptions
	Retry       RetryOptions
	Pool        PoolOptions
}

// MatcherMode selects Alertmanager's parser for route matcher expressions.
type MatcherMode string

const (
	MatcherModeFallback   MatcherMode = "fallback"
	MatcherModeClassic    MatcherMode = "classic"
	MatcherModeUTF8Strict MatcherMode = "utf8-strict"
)

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
		matcherMode:      MatcherModeFallback,
		httpClient:       &http.Client{Transport: transport},
		retryMaxAttempts: 1,
		retryBackoff:     0,
	}
}

func NewClientWithOptions(opts ClientOptions) (*Client, error) {
	if opts.BaseURL == "" {
		return nil, fmt.Errorf("base URL is required")
	}

	if opts.MatcherMode == "" {
		opts.MatcherMode = MatcherModeFallback
	}
	if _, err := matcherModeFeature(opts.MatcherMode); err != nil {
		return nil, err
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
		baseURL:     strings.TrimSuffix(opts.BaseURL, "/"),
		matcherMode: opts.MatcherMode,
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
	if config.Route == nil {
		return &config, nil
	}

	nativeConfig, err := loadNativeConfig(status.ConfigYAML.Original, c.matcherMode)
	if err != nil {
		return nil, fmt.Errorf("failed to parse Alertmanager routing config: %w", err)
	}
	config.nativeRoutes, err = newNativeRouteTree(nativeConfig.Route, config.Route)
	if err != nil {
		return nil, fmt.Errorf("failed to build Alertmanager routing tree: %w", err)
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

// MatcherResult records an alert label value that satisfies one route matcher.
type MatcherResult struct {
	Label    string `json:"-"`
	Operator string `json:"-"`
	Expected string `json:"-"`
	Actual   string `json:"-"`
	Missing  bool   `json:"-"`
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
	ResolvedReceiver string          `json:"resolved_receiver,omitempty"`
	MatcherResults   []MatcherResult `json:"-"`
}

// FindMatchingRoute determines which receiver an alert would match.
// It returns a flat, ordered list of MatchedRoute entries that captures the
// full parent→child traversal path so callers can reconstruct the chain.
func (c *Client) FindMatchingRoute(labels map[string]string, config *Config) (string, []MatchedRoute, error) {
	tree, err := nativeRouteTreeForConfig(config)
	if err != nil {
		return "", nil, err
	}

	routes := tree.root.Match(nativeLabels(labels))
	if len(routes) == 0 {
		return "", []MatchedRoute{}, nil
	}
	if len(routes) == 1 && routes[0] == tree.root {
		return tree.root.RouteOpts.Receiver, []MatchedRoute{}, nil
	}

	matched := make([]MatchedRoute, 0)
	seen := make(map[*Route]int, len(routes))
	for _, terminal := range routes {
		path := tree.paths[terminal]
		for index := 1; index < len(path); index++ {
			nativeRoute := path[index]
			localRoute := tree.localRoutes[nativeRoute]
			if matchedIndex, ok := seen[localRoute]; ok {
				if nativeRoute == terminal {
					matched[matchedIndex].IsEffective = true
				}
				continue
			}

			parentReceivers := make([]string, 0, index-1)
			for parentIndex := 1; parentIndex < index; parentIndex++ {
				if receiver := path[parentIndex].RouteOpts.Receiver; receiver != "" {
					parentReceivers = append(parentReceivers, receiver)
				}
			}
			depth := index - 1
			matcherResults := make([]MatcherResult, 0, len(nativeRoute.Matchers))
			for _, matcher := range nativeRoute.Matchers {
				actual, found := labels[matcher.Name]
				matcherResults = append(matcherResults, MatcherResult{
					Label:    matcher.Name,
					Operator: matcher.Type.String(),
					Expected: matcher.Value,
					Actual:   actual,
					Missing:  !found,
				})
			}
			matched = append(matched, MatchedRoute{
				Route:            localRoute,
				Depth:            depth,
				ParentReceivers:  parentReceivers,
				IsSubroute:       depth > 0,
				IsEffective:      nativeRoute == terminal,
				ResolvedReceiver: nativeRoute.RouteOpts.Receiver,
				MatcherResults:   matcherResults,
			})
			seen[localRoute] = len(matched) - 1
		}
	}

	return routes[len(routes)-1].RouteOpts.Receiver, matched, nil
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

type LabelSuggestion struct {
	Key    string
	Values []string
}

type SampleAlert struct {
	Name        string
	Description string
	Labels      map[string]string
}

const (
	maxAutomaticSampleAlertRoutes  = 5000
	maxOnDemandSampleAlertRoutes   = 10000
	maxSampleAlertCandidates       = 64
	maxSampleAlertFallbackAttempts = 100
)

// ExtractLabelKeys extracts all unique label keys from the route configuration.
func ExtractLabelKeys(config *Config) []string {
	tree, err := nativeRouteTreeForConfig(config)
	if err != nil {
		return nil
	}
	keys := make(map[string]bool)
	tree.root.Walk(func(route *dispatch.Route) {
		for _, matcher := range route.Matchers {
			keys[matcher.Name] = true
		}
	})

	result := make([]string, 0, len(keys))
	for key := range keys {
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}

// ExtractLabelSuggestions extracts label keys and their possible values from the route configuration.
func ExtractLabelSuggestions(config *Config) []LabelSuggestion {
	tree, err := nativeRouteTreeForConfig(config)
	if err != nil {
		return nil
	}
	labelValues := make(map[string]map[string]bool)
	tree.root.Walk(func(route *dispatch.Route) {
		for _, matcher := range route.Matchers {
			if matcher.Type != amlabels.MatchEqual {
				continue
			}
			if labelValues[matcher.Name] == nil {
				labelValues[matcher.Name] = make(map[string]bool)
			}
			labelValues[matcher.Name][matcher.Value] = true
		}
	})

	suggestions := make([]LabelSuggestion, 0, len(labelValues))
	for key, valuesMap := range labelValues {
		values := make([]string, 0, len(valuesMap))
		for value := range valuesMap {
			values = append(values, value)
		}
		sort.Strings(values)
		suggestions = append(suggestions, LabelSuggestion{Key: key, Values: values})
	}
	sort.Slice(suggestions, func(i, j int) bool {
		return suggestions[i].Key < suggestions[j].Key
	})
	return suggestions
}

// GenerateSampleAlerts creates examples that match the configured routes and root fallback.
func GenerateSampleAlerts(config *Config) []SampleAlert {
	samples, _ := GenerateSampleAlertsContext(context.Background(), config)
	return samples
}

// GenerateSampleAlertsContext creates examples while observing request cancellation.
func GenerateSampleAlertsContext(ctx context.Context, config *Config) ([]SampleAlert, error) {
	samples, deferred, err := generateSampleAlertsContext(ctx, config, maxOnDemandSampleAlertRoutes)
	if err != nil {
		return nil, err
	}
	if deferred {
		return nil, ErrSampleGenerationTooLarge
	}
	return samples, nil
}

func generateSampleAlertsContext(ctx context.Context, config *Config, routeLimit int) ([]SampleAlert, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if config == nil || config.Route == nil {
		return nil, false, nil
	}

	routeCount, err := routeTreeNodeCount(ctx, config.Route, routeLimit)
	if err != nil {
		return nil, false, err
	}
	if routeCount > routeLimit {
		return nil, true, nil
	}

	tree, err := nativeRouteTreeForConfig(config)
	if err != nil {
		return nil, false, nil
	}
	client := &Client{}
	candidates := make([]map[string]string, 0, maxSampleAlertCandidates)
	if err := collectRouteExamples(ctx, tree.root, map[string]string{}, &candidates); err != nil {
		return nil, false, err
	}

	singleReceiverSamples := make([]SampleAlert, 0, 2)
	var multipleReceiverSample SampleAlert
	hasMultipleReceiverSample := false
	seen := make(map[string]bool)
	for _, labels := range candidates {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		receiver, matched, err := client.FindMatchingRoute(labels, config)
		if err != nil || len(matched) == 0 {
			continue
		}
		fingerprint := sampleLabelsFingerprint(labels)
		if seen[fingerprint] {
			continue
		}
		seen[fingerprint] = true
		receivers := effectiveReceivers(receiver, matched)
		sample := SampleAlert{
			Description: "Routes to " + strings.Join(receivers, ", ") + ".",
			Labels:      labels,
		}
		if len(receivers) > 1 {
			if !hasMultipleReceiverSample {
				sample.Name = "Multiple receivers"
				sample.Description = fmt.Sprintf("Continue sends to %d receivers.", len(receivers))
				multipleReceiverSample = sample
				hasMultipleReceiverSample = true
			}
			continue
		}
		if len(singleReceiverSamples) < 2 {
			sample.Name = fmt.Sprintf("Matching alert %d", len(singleReceiverSamples)+1)
			singleReceiverSamples = append(singleReceiverSamples, sample)
		}
	}

	samples := make([]SampleAlert, 0, 3)
	if len(singleReceiverSamples) > 0 {
		samples = append(samples, singleReceiverSamples[0])
	}
	if hasMultipleReceiverSample {
		samples = append(samples, multipleReceiverSample)
	}
	if len(samples) < 2 && len(singleReceiverSamples) > 1 {
		samples = append(samples, singleReceiverSamples[1])
	}

	labelKeys := ExtractLabelKeys(config)
	for attempt := 0; attempt < maxSampleAlertFallbackAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		labels := fallbackLabels(labelKeys, attempt)
		receiver, matched, err := client.FindMatchingRoute(labels, config)
		if err != nil || len(matched) != 0 || receiver == "" {
			continue
		}
		samples = append(samples, SampleAlert{
			Name:        "Default receiver",
			Description: "No child route matched. Uses the root receiver, " + receiver + ".",
			Labels:      labels,
		})
		break
	}

	return samples, false, nil
}

func (c *Client) GetSampleAlerts(config *Config) ([]SampleAlert, bool) {
	samples, deferred, _ := c.GetSampleAlertsContext(context.Background(), config)
	return samples, deferred
}

func (c *Client) GetSampleAlertsContext(ctx context.Context, config *Config) ([]SampleAlert, bool, error) {
	return c.sampleAlertsForConfig(ctx, config, false)
}

func (c *Client) GenerateSampleAlerts(config *Config) []SampleAlert {
	samples, _ := c.GenerateSampleAlertsContext(context.Background(), config)
	return samples
}

func (c *Client) GenerateSampleAlertsContext(ctx context.Context, config *Config) ([]SampleAlert, error) {
	samples, deferred, err := c.sampleAlertsForConfig(ctx, config, true)
	if err != nil {
		return nil, err
	}
	if deferred {
		return nil, ErrSampleGenerationTooLarge
	}
	return samples, nil
}

func (c *Client) sampleAlertsForConfig(ctx context.Context, config *Config, generateLargeTree bool) ([]SampleAlert, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}

	c.sampleAlertsMu.Lock()
	if err := ctx.Err(); err != nil {
		c.sampleAlertsMu.Unlock()
		return nil, false, err
	}
	if c.sampleAlertsReady && config == c.sampleAlertsConfig {
		samples := cloneSampleAlerts(c.sampleAlerts)
		c.sampleAlertsMu.Unlock()
		return samples, false, nil
	}
	c.sampleAlertsMu.Unlock()

	routeLimit := maxAutomaticSampleAlertRoutes
	if generateLargeTree {
		routeLimit = maxOnDemandSampleAlertRoutes
	}
	samples, deferred, err := generateSampleAlertsContext(ctx, config, routeLimit)
	if err != nil || deferred {
		return nil, deferred, err
	}

	c.sampleAlertsMu.Lock()
	if err := ctx.Err(); err != nil {
		c.sampleAlertsMu.Unlock()
		return nil, false, err
	}
	if c.sampleAlertsReady && config == c.sampleAlertsConfig {
		samples = cloneSampleAlerts(c.sampleAlerts)
	} else {
		c.sampleAlertsConfig = config
		c.sampleAlerts = cloneSampleAlerts(samples)
		c.sampleAlertsReady = true
	}
	c.sampleAlertsMu.Unlock()
	return cloneSampleAlerts(samples), false, nil
}

func routeTreeNodeCount(ctx context.Context, route *Route, limit int) (int, error) {
	if route == nil {
		return 0, nil
	}
	count := 0
	pending := []*Route{route}
	for len(pending) > 0 {
		if err := ctx.Err(); err != nil {
			return count, err
		}
		current := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		count++
		if limit > 0 && count+len(pending)+len(current.Routes) > limit {
			return limit + 1, nil
		}
		pending = append(pending, current.Routes...)
	}
	return count, nil
}

func cloneSampleAlerts(samples []SampleAlert) []SampleAlert {
	if samples == nil {
		return nil
	}
	cloned := make([]SampleAlert, len(samples))
	for index, sample := range samples {
		cloned[index] = sample
		if sample.Labels == nil {
			continue
		}
		cloned[index].Labels = make(map[string]string, len(sample.Labels))
		for key, value := range sample.Labels {
			cloned[index].Labels[key] = value
		}
	}
	return cloned
}

func collectRouteExamples(ctx context.Context, route *dispatch.Route, labels map[string]string, candidates *[]map[string]string) error {
	continuingLabels := labels
	hasContinuingLabels := false
	for _, child := range route.Routes {
		if err := ctx.Err(); err != nil {
			return err
		}
		if len(*candidates) >= maxSampleAlertCandidates {
			return nil
		}
		candidate, ok, err := routeExampleLabels(ctx, continuingLabels, child)
		if err != nil {
			return err
		}
		if !ok && hasContinuingLabels {
			candidate, ok, err = routeExampleLabels(ctx, labels, child)
			if err != nil {
				return err
			}
		}
		if ok {
			*candidates = append(*candidates, candidate)
			if err := collectRouteExamples(ctx, child, candidate, candidates); err != nil {
				return err
			}
		}
		if child.Continue && ok {
			continuingLabels = candidate
			hasContinuingLabels = true
		} else if !child.Continue {
			continuingLabels = labels
			hasContinuingLabels = false
		}
	}
	return nil
}

func routeExampleLabels(ctx context.Context, parentLabels map[string]string, route *dispatch.Route) (map[string]string, bool, error) {
	labels := make(map[string]string, len(parentLabels))
	for key, value := range parentLabels {
		labels[key] = value
	}

	constraints := make(map[string][]*amlabels.Matcher)
	for _, matcher := range route.Matchers {
		constraints[matcher.Name] = append(constraints[matcher.Name], matcher)
	}

	for name, conditions := range constraints {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		if matchersMatch(labels[name], conditions) {
			continue
		}
		found := false
		candidates := []string{"alertmanager-route-tester-example", "example", "test", "critical", "warning", "production", "api", "x", "0", ""}
		for _, condition := range conditions {
			if err := ctx.Err(); err != nil {
				return nil, false, err
			}
			switch condition.Type {
			case amlabels.MatchEqual:
				candidates = append([]string{condition.Value}, candidates...)
			case amlabels.MatchRegexp:
				if candidate, ok := regexMatcherExample(condition.Value); ok {
					candidates = append([]string{candidate}, candidates...)
				}
			case amlabels.MatchNotEqual, amlabels.MatchNotRegexp:
			}
		}
		for _, candidate := range candidates {
			if err := ctx.Err(); err != nil {
				return nil, false, err
			}
			if matchersMatch(candidate, conditions) {
				labels[name] = candidate
				found = true
				break
			}
		}
		if !found {
			return nil, false, nil
		}
		if labels[name] == "" {
			delete(labels, name)
		}
	}

	return labels, true, nil
}

func regexMatcherExample(expression string) (string, bool) {
	parsed, err := syntax.Parse(expression, syntax.Perl)
	if err != nil {
		return "", false
	}
	candidate, ok := regexSyntaxExample(parsed)
	if !ok {
		return "", false
	}
	compiled, err := regexp.Compile(expression)
	return candidate, err == nil && compiled.MatchString(candidate)
}

func regexSyntaxExample(expression *syntax.Regexp) (string, bool) {
	switch expression.Op {
	case syntax.OpNoMatch:
		return "", false
	case syntax.OpEmptyMatch, syntax.OpBeginLine, syntax.OpEndLine, syntax.OpBeginText, syntax.OpEndText, syntax.OpWordBoundary, syntax.OpNoWordBoundary:
		return "", true
	case syntax.OpLiteral:
		return string(expression.Rune), true
	case syntax.OpCharClass:
		return regexClassExample(expression.Rune)
	case syntax.OpAnyChar, syntax.OpAnyCharNotNL:
		return "a", true
	case syntax.OpCapture:
		return regexSyntaxExample(expression.Sub[0])
	case syntax.OpStar, syntax.OpQuest:
		return "", true
	case syntax.OpPlus:
		return regexSyntaxExample(expression.Sub[0])
	case syntax.OpRepeat:
		unit, ok := regexSyntaxExample(expression.Sub[0])
		if !ok || (len(unit) > 0 && expression.Min > 128/len(unit)) {
			return "", false
		}
		return strings.Repeat(unit, expression.Min), true
	case syntax.OpConcat:
		var candidate strings.Builder
		for _, sub := range expression.Sub {
			part, ok := regexSyntaxExample(sub)
			if !ok {
				return "", false
			}
			candidate.WriteString(part)
		}
		return candidate.String(), true
	case syntax.OpAlternate:
		for _, sub := range expression.Sub {
			if candidate, ok := regexSyntaxExample(sub); ok {
				return candidate, true
			}
		}
	}
	return "", false
}

func regexClassExample(ranges []rune) (string, bool) {
	for _, candidate := range []rune{'0', 'a', 'A', '_', ' '} {
		for index := 0; index+1 < len(ranges); index += 2 {
			if candidate >= ranges[index] && candidate <= ranges[index+1] {
				return string(candidate), true
			}
		}
	}
	return "", false
}

func matchersMatch(value string, matchers []*amlabels.Matcher) bool {
	for _, matcher := range matchers {
		if !matcher.Matches(value) {
			return false
		}
	}
	return true
}

func effectiveReceivers(receiver string, matched []MatchedRoute) []string {
	receivers := make([]string, 0, len(matched))
	seen := make(map[string]bool)
	for _, route := range matched {
		if !route.IsEffective || route.ResolvedReceiver == "" || seen[route.ResolvedReceiver] {
			continue
		}
		receivers = append(receivers, route.ResolvedReceiver)
		seen[route.ResolvedReceiver] = true
	}
	if len(receivers) == 0 && receiver != "" {
		receivers = append(receivers, receiver)
	}
	return receivers
}

func fallbackLabels(keys []string, attempt int) map[string]string {
	if len(keys) == 0 {
		keys = []string{"alertname"}
	}
	labels := make(map[string]string, len(keys))
	for _, key := range keys {
		labels[key] = fmt.Sprintf("alertmanager-route-tester-unmatched-%d", attempt)
	}
	if _, ok := labels["alertname"]; !ok {
		labels["alertname"] = fmt.Sprintf("alertmanager-route-tester-unmatched-%d", attempt)
	}
	return labels
}

func sampleLabelsFingerprint(labels map[string]string) string {
	encoded, _ := json.Marshal(labels)
	return string(encoded)
}
