package alertmanager_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/wbollock/alertmanager-route-tester/internal/alertmanager"
	"gopkg.in/yaml.v3"
)

func TestRoutingMatchesAlertmanagerDeliveries(t *testing.T) {
	binary := alertmanagerBinary(t)
	deliveries := make(chan webhookDelivery, 100)
	webhook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Alerts []struct {
				Labels map[string]string `json:"labels"`
			} `json:"alerts"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, "invalid alertmanager notification", http.StatusBadRequest)
			return
		}
		alertNames := make([]string, 0, len(payload.Alerts))
		for _, alert := range payload.Alerts {
			alertNames = append(alertNames, alert.Labels["alertname"])
		}
		deliveries <- webhookDelivery{receiver: strings.TrimPrefix(r.URL.Path, "/"), alertNames: alertNames}
		w.WriteHeader(http.StatusOK)
	}))
	defer webhook.Close()

	config := parityConfig(webhook.URL)
	configYAML, err := yaml.Marshal(config)
	if err != nil {
		t.Fatalf("marshal Alertmanager config: %v", err)
	}
	baseURL := startAlertmanager(t, binary, configYAML)
	client := alertmanager.NewClient(baseURL, false)
	loadedConfig, err := client.GetConfig()
	if err != nil {
		t.Fatalf("load Alertmanager config: %v", err)
	}

	testCases := []struct {
		name   string
		labels map[string]string
	}{
		{
			name: "quoted label name and matcher whitespace",
			labels: map[string]string{
				"severity": "critical",
				"case":     "quoted",
			},
		},
		{
			name: "unicode label name and whitespace",
			labels: map[string]string{
				"alert résumé": "🔥 CPU failure",
			},
		},
		{
			name: "escaped matcher value",
			labels: map[string]string{
				"message": `quoted "text" and slash \\`,
			},
		},
		{
			name: "regex matcher",
			labels: map[string]string{
				"service": "api",
			},
		},
		{
			name: "continue sends to multiple receivers",
			labels: map[string]string{
				"severity": "warning",
				"team":     "platform",
			},
		},
		{
			name: "nested route",
			labels: map[string]string{
				"team":     "database",
				"severity": "critical",
			},
		},
		{
			name:   "default receiver",
			labels: map[string]string{"source": "unmatched"},
		},
	}

	for index, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			labels := cloneLabels(testCase.labels)
			labels["alertname"] = fmt.Sprintf("ATRParity%d", index)

			predicted, matchedRoutes, err := client.FindMatchingRoute(labels, loadedConfig)
			if err != nil {
				t.Fatalf("FindMatchingRoute(): %v", err)
			}
			predictedReceivers := predictedReceiverSet(predicted, matchedRoutes)

			postAlert(t, baseURL, labels)
			actualReceivers := waitForDeliveries(t, deliveries, labels["alertname"])
			if !sameReceiverSet(predictedReceivers, actualReceivers) {
				t.Fatalf("receiver set differs: ATR predicted %v, Alertmanager delivered to %v", predictedReceivers, actualReceivers)
			}
		})
	}
}

type webhookDelivery struct {
	receiver   string
	alertNames []string
}

type parityAlertmanagerConfig struct {
	Route     *alertmanager.Route `yaml:"route"`
	Receivers []parityReceiver    `yaml:"receivers"`
}

type parityReceiver struct {
	Name           string                       `yaml:"name"`
	WebhookConfigs []alertmanager.WebhookConfig `yaml:"webhook_configs"`
}

func parityConfig(webhookURL string) *parityAlertmanagerConfig {
	escapedValue := `quoted "text" and slash \\`
	return &parityAlertmanagerConfig{
		Route: &alertmanager.Route{
			Receiver:       "default",
			GroupBy:        []string{"alertname"},
			GroupWait:      "100ms",
			GroupInterval:  "1s",
			RepeatInterval: "1h",
			Routes: []*alertmanager.Route{
				{Receiver: "quoted", Matchers: []string{`  "severity" = "critical"  `, `case="quoted"`}},
				{Receiver: "unicode-space", Matchers: []string{`"alert résumé"="🔥 CPU failure"`}},
				{Receiver: "escaped", Matchers: []string{"message=" + strconv.Quote(escapedValue)}},
				{Receiver: "regex", Matchers: []string{`service=~"^(api|web)$"`}},
				{Receiver: "warning", Matchers: []string{`severity="warning"`}, Continue: true},
				{Receiver: "platform", Matchers: []string{`team="platform"`}},
				{
					Receiver: "database-parent",
					Matchers: []string{`team="database"`},
					Routes: []*alertmanager.Route{{
						Receiver: "database-critical",
						Matchers: []string{`severity="critical"`},
					}},
				},
			},
		},
		Receivers: []parityReceiver{
			{Name: "default", WebhookConfigs: []alertmanager.WebhookConfig{{URL: webhookURL + "/default"}}},
			{Name: "quoted", WebhookConfigs: []alertmanager.WebhookConfig{{URL: webhookURL + "/quoted"}}},
			{Name: "unicode-space", WebhookConfigs: []alertmanager.WebhookConfig{{URL: webhookURL + "/unicode-space"}}},
			{Name: "escaped", WebhookConfigs: []alertmanager.WebhookConfig{{URL: webhookURL + "/escaped"}}},
			{Name: "regex", WebhookConfigs: []alertmanager.WebhookConfig{{URL: webhookURL + "/regex"}}},
			{Name: "warning", WebhookConfigs: []alertmanager.WebhookConfig{{URL: webhookURL + "/warning"}}},
			{Name: "platform", WebhookConfigs: []alertmanager.WebhookConfig{{URL: webhookURL + "/platform"}}},
			{Name: "database-parent", WebhookConfigs: []alertmanager.WebhookConfig{{URL: webhookURL + "/database-parent"}}},
			{Name: "database-critical", WebhookConfigs: []alertmanager.WebhookConfig{{URL: webhookURL + "/database-critical"}}},
		},
	}
}

func alertmanagerBinary(t *testing.T) string {
	t.Helper()
	binary := os.Getenv("ALERTMANAGER_BINARY")
	if binary != "" {
		if _, err := os.Stat(binary); err != nil {
			t.Fatalf("Alertmanager binary from ALERTMANAGER_BINARY is unavailable at %s: %v", binary, err)
		}
	} else {
		_, source, _, ok := runtime.Caller(0)
		if !ok {
			t.Fatal("locate parity test source")
		}
		root := filepath.Clean(filepath.Join(filepath.Dir(source), "../.."))
		for _, candidate := range []string{
			filepath.Join(root, "bin", "alertmanager", "alertmanager"),
			filepath.Join(root, "alertmanager", "alertmanager"),
		} {
			if _, err := os.Stat(candidate); err == nil {
				binary = candidate
				break
			}
		}
		if binary == "" {
			t.Skipf("Alertmanager binary unavailable under %s", root)
		}
	}
	output, err := exec.Command(binary, "--version").CombinedOutput()
	if err != nil {
		t.Fatalf("read Alertmanager version: %v: %s", err, output)
	}
	if !bytes.Contains(output, []byte("version 0.27.0")) {
		t.Fatalf("parity tests require Alertmanager 0.27.0, got %s", output)
	}
	return binary
}

func startAlertmanager(t *testing.T, binary string, config []byte) string {
	t.Helper()
	workDir := t.TempDir()
	configPath := filepath.Join(workDir, "alertmanager.yml")
	if err := os.WriteFile(configPath, config, 0600); err != nil {
		t.Fatalf("write Alertmanager config: %v", err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve Alertmanager port: %v", err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("release Alertmanager port: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	command := exec.CommandContext(ctx, binary,
		"--config.file="+configPath,
		"--web.listen-address="+address,
		"--storage.path="+filepath.Join(workDir, "storage"),
		"--cluster.listen-address=",
	)
	logPath := filepath.Join(workDir, "alertmanager.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		cancel()
		t.Fatalf("create Alertmanager log: %v", err)
	}
	command.Stdout = logFile
	command.Stderr = logFile
	if err := command.Start(); err != nil {
		cancel()
		_ = logFile.Close()
		t.Fatalf("start Alertmanager: %v", err)
	}
	t.Cleanup(func() {
		cancel()
		_ = command.Wait()
		_ = logFile.Close()
		if t.Failed() {
			logs, _ := os.ReadFile(logPath)
			t.Logf("Alertmanager output:\\n%s", logs)
		}
	})

	baseURL := "http://" + address
	client := &http.Client{Timeout: time.Second}
	for attempt := 0; attempt < 100; attempt++ {
		response, err := client.Get(baseURL + "/api/v2/status")
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return baseURL
			}
		}
		if ctx.Err() != nil {
			t.Fatalf("Alertmanager did not start")
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("Alertmanager did not become ready")
	return ""
}

func postAlert(t *testing.T, baseURL string, labels map[string]string) {
	t.Helper()
	alert := []struct {
		Labels       map[string]string `json:"labels"`
		Annotations  map[string]string `json:"annotations"`
		StartsAt     time.Time         `json:"startsAt"`
		EndsAt       time.Time         `json:"endsAt"`
		GeneratorURL string            `json:"generatorURL"`
	}{{
		Labels:       labels,
		Annotations:  map[string]string{"summary": "Alertmanager parity test"},
		StartsAt:     time.Now().Add(-time.Second),
		EndsAt:       time.Now().Add(time.Hour),
		GeneratorURL: "http://localhost/atr-parity",
	}}
	body, err := json.Marshal(alert)
	if err != nil {
		t.Fatalf("marshal alert: %v", err)
	}
	request, err := http.NewRequest(http.MethodPost, baseURL+"/api/v2/alerts", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("create alert request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 5 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("send alert: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		responseBody, _ := io.ReadAll(response.Body)
		t.Fatalf("send alert status = %d: %s", response.StatusCode, responseBody)
	}
}

func waitForDeliveries(t *testing.T, deliveries <-chan webhookDelivery, alertName string) []string {
	t.Helper()
	actual := make(map[string]bool)
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	var quiet *time.Timer
	var quietChannel <-chan time.Time
	for {
		select {
		case delivery := <-deliveries:
			for _, deliveredAlertName := range delivery.alertNames {
				if deliveredAlertName == alertName {
					actual[delivery.receiver] = true
					if quiet == nil {
						quiet = time.NewTimer(500 * time.Millisecond)
						quietChannel = quiet.C
					} else {
						if !quiet.Stop() {
							select {
							case <-quiet.C:
							default:
							}
						}
						quiet.Reset(500 * time.Millisecond)
					}
				}
			}
		case <-quietChannel:
			return receiverSet(actual)
		case <-deadline.C:
			return receiverSet(actual)
		}
	}
}

func predictedReceiverSet(receiver string, matchedRoutes []alertmanager.MatchedRoute) []string {
	receivers := make(map[string]bool)
	for _, matched := range matchedRoutes {
		if !matched.IsEffective {
			continue
		}
		name := matched.ResolvedReceiver
		if name == "" && matched.Route != nil {
			name = matched.Route.Receiver
		}
		if name != "" {
			receivers[name] = true
		}
	}
	if len(receivers) == 0 && receiver != "" {
		receivers[receiver] = true
	}
	return receiverSet(receivers)
}

func receiverSet(receivers map[string]bool) []string {
	result := make([]string, 0, len(receivers))
	for receiver := range receivers {
		result = append(result, receiver)
	}
	sort.Strings(result)
	return result
}

func sameReceiverSet(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func cloneLabels(labels map[string]string) map[string]string {
	result := make(map[string]string, len(labels)+1)
	for key, value := range labels {
		result[key] = value
	}
	return result
}
