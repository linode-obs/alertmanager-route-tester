package handler

import (
	"bytes"
	"encoding/json"
	"html/template"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/wbollock/alertmanager-route-tester/internal/alertmanager"
)

// loadTemplates parses the real templates relative to the package directory so
// tests exercise the same template files that production uses.
func loadTemplates(t *testing.T) *template.Template {
	t.Helper()
	tmpl, err := template.New("").ParseGlob("../../templates/*.html")
	if err != nil {
		t.Fatalf("failed to parse templates: %v", err)
	}
	return tmpl
}

func captureSlogOutput(t *testing.T, expectedMessages ...string) *bytes.Buffer {
	t.Helper()
	previous := slog.Default()
	var output bytes.Buffer
	slog.SetDefault(slog.New(slog.NewTextHandler(&output, nil)))
	t.Cleanup(func() {
		slog.SetDefault(previous)
		for _, message := range expectedMessages {
			if !strings.Contains(output.String(), message) {
				t.Errorf("log output = %q, want %q", output.String(), message)
			}
		}
	})
	return &output
}

// resultData mirrors the anonymous struct passed to result.html in HandleTest.
type resultData struct {
	Receiver                 string
	ReceiverConfig           *alertmanager.Receiver
	MatchedRoutes            []alertmanager.MatchedRoute
	MatchedReceivers         []string
	RouteSteps               []RouteStep
	ContinueCount            int
	FinalMatch               MatchSummary
	DefaultRoot              bool
	MatchedReceiverSummaries []ReceiverSummary
	RouteMismatches          []alertmanager.RouteMismatch
	Labels                   map[string]string
	Error                    string
}

func TestNewWithClientsFromFSLoadsTemplatesWithoutWorkingDirectory(t *testing.T) {
	templateFS := fstest.MapFS{
		"templates/index.html":  &fstest.MapFile{Data: []byte("{{define \"index.html\"}}index{{end}}")},
		"templates/result.html": &fstest.MapFile{Data: []byte("{{define \"result.html\"}}result{{end}}")},
	}

	h := NewWithClientsFromFS(nil, nil, "", templateFS)
	if h == nil || h.tmpl == nil {
		t.Fatal("NewWithClientsFromFS() returned a handler without templates")
	}
}

func TestIndexTemplateLabelsCachedConfig(t *testing.T) {
	tmpl := loadTemplates(t)
	data := struct {
		LabelSuggestions     []alertmanager.LabelSuggestion
		SampleAlerts         []alertmanager.SampleAlert
		SampleAlertsDeferred bool
		Config               *alertmanager.Config
		AlertmanagerURL      string
		AlertmanagerNames    []string
		SelectedAlertmanager string
		ConnectionStatus     bool
		ConnectionError      string
		ConfigCachedAt       time.Time
		ConfigWasCached      bool
	}{
		AlertmanagerURL:  "http://localhost:9093",
		ConnectionStatus: true,
		ConfigCachedAt:   time.Now(),
		ConfigWasCached:  true,
	}

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "index.html", data); err != nil {
		t.Fatalf("index.html failed to render: %v", err)
	}
	output := buf.String()
	if !strings.Contains(output, "Using cached configuration") {
		t.Fatal("cached config status is missing")
	}
	if !strings.Contains(output, "config-reload-error") || !strings.Contains(output, "Reload failed. Using cached configuration.") {
		t.Fatal("reload error feedback is missing")
	}
}

func TestHandleTestRejectsOversizedJSON(t *testing.T) {
	body := []byte(`{"labels":{"label":"` + strings.Repeat("x", int(maxTestRequestBodyBytes)) + `"}}`)
	request := httptest.NewRequest(http.MethodPost, "/test", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	(&Handler{}).HandleTest(response, request)

	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusRequestEntityTooLarge)
	}
}

func TestHandleTestRejectsOversizedJSONAfterFirstValue(t *testing.T) {
	body := []byte(`{"labels":{}}` + strings.Repeat(" ", int(maxTestRequestBodyBytes)))
	request := httptest.NewRequest(http.MethodPost, "/test", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	(&Handler{}).HandleTest(response, request)

	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusRequestEntityTooLarge)
	}
}

func TestHandleTestRejectsTrailingJSONValue(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/test", strings.NewReader(`{"labels":{}} {}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	(&Handler{}).HandleTest(response, request)

	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "Invalid JSON") {
		t.Fatalf("response = %d %q, want invalid JSON response", response.Code, response.Body.String())
	}
}

func TestHandleTestRejectsOversizedForm(t *testing.T) {
	body := strings.Repeat("label=x&", int(maxTestRequestBodyBytes/7)+1)
	request := httptest.NewRequest(http.MethodPost, "/test", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()

	(&Handler{}).HandleTest(response, request)

	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusRequestEntityTooLarge)
	}
}

func TestHandleTestUsesSelectedAlertmanager(t *testing.T) {
	server := func(t *testing.T, receiver string) *httptest.Server {
		t.Helper()
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"config":{"original":"route:\n  receiver: ` + receiver + `\nreceivers:\n- name: ` + receiver + `\n"}}`))
		}))
	}
	production := server(t, "production-default")
	defer production.Close()
	staging := server(t, "staging-default")
	defer staging.Close()

	h := &Handler{
		clients: map[string]*alertmanager.Client{
			"production": alertmanager.NewClient(production.URL, false),
			"staging":    alertmanager.NewClient(staging.URL, false),
		},
		alertmanagerNames:   []string{"production", "staging"},
		defaultAlertmanager: "production",
		tmpl:                loadTemplates(t),
	}
	request := httptest.NewRequest(http.MethodPost, "/test?alertmanager=staging", bytes.NewBufferString(`{"labels":{"alertname":"Test"}}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	h.HandleTest(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var result TestResponse
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if result.Receiver != "staging-default" {
		t.Fatalf("receiver = %q, want staging-default", result.Receiver)
	}
}

func TestHandleTestUsesSelectedAlertmanagerFromForm(t *testing.T) {
	server := func(t *testing.T, receiver string) *httptest.Server {
		t.Helper()
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"config":{"original":"route:\n  receiver: ` + receiver + `\nreceivers:\n- name: ` + receiver + `\n"}}`))
		}))
	}
	production := server(t, "production-default")
	defer production.Close()
	staging := server(t, "staging-default")
	defer staging.Close()

	h := &Handler{
		clients: map[string]*alertmanager.Client{
			"production": alertmanager.NewClient(production.URL, false),
			"staging":    alertmanager.NewClient(staging.URL, false),
		},
		alertmanagerNames:   []string{"production", "staging"},
		defaultAlertmanager: "production",
		tmpl:                loadTemplates(t),
	}
	form := url.Values{
		"alertmanager": {"staging"},
		"alertname":    {"Test"},
	}
	request := httptest.NewRequest(http.MethodPost, "/test", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()

	h.HandleTest(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var result TestResponse
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if result.Receiver != "staging-default" {
		t.Fatalf("receiver = %q, want staging-default", result.Receiver)
	}
}

func TestHandleIndexRejectsUnknownAlertmanager(t *testing.T) {
	tmpl := loadTemplates(t)
	h := &Handler{
		clients:             map[string]*alertmanager.Client{"production": alertmanager.NewClient("http://127.0.0.1:1", false)},
		alertmanagerNames:   []string{"production"},
		defaultAlertmanager: "production",
		tmpl:                tmpl,
	}
	response := httptest.NewRecorder()

	h.HandleIndex(response, httptest.NewRequest(http.MethodGet, "/?alertmanager=missing", http.NoBody))

	body := response.Body.String()
	if response.Code != http.StatusOK || !strings.Contains(body, "unknown Alertmanager") || !strings.Contains(body, "missing") {
		t.Fatalf("response = %d %q, want unknown Alertmanager error", response.Code, body)
	}
	if !strings.Contains(body, "http://127.0.0.1:1") || !strings.Contains(body, `name="alertmanager" value="production"`) {
		t.Fatalf("response = %q, want default Alertmanager connection state", body)
	}
}

func TestHandleConfigLabelsRejectsNonGetRequests(t *testing.T) {
	h := &Handler{}
	request := httptest.NewRequest(http.MethodPost, "/config/labels", http.NoBody)
	response := httptest.NewRecorder()

	h.HandleConfigLabels(response, request)

	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleIndexShowsCachedStatusAfterInitialLoad(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"config":{"original":"route:\n  receiver: default\nreceivers:\n- name: default\n"}}`))
	}))
	defer server.Close()

	h := &Handler{client: alertmanager.NewClient(server.URL, false), tmpl: loadTemplates(t)}
	first := httptest.NewRecorder()
	h.HandleIndex(first, httptest.NewRequest(http.MethodGet, "/", http.NoBody))
	if first.Code != http.StatusOK || !strings.Contains(first.Body.String(), "Connected to") {
		t.Fatalf("first response = %d %q, want live connection status", first.Code, first.Body.String())
	}

	second := httptest.NewRecorder()
	h.HandleIndex(second, httptest.NewRequest(http.MethodGet, "/", http.NoBody))
	if second.Code != http.StatusOK || !strings.Contains(second.Body.String(), "Using cached configuration") {
		t.Fatalf("second response = %d %q, want cached status", second.Code, second.Body.String())
	}
	if requests != 1 {
		t.Fatalf("Alertmanager requests = %d, want one cached read", requests)
	}
}

func TestBuildRouteSummaryTreatsNestedParentAsTraceOnly(t *testing.T) {
	config := &alertmanager.Config{
		Receivers: []alertmanager.Receiver{
			{Name: "team-database"},
			{Name: "pagerduty-database"},
		},
	}
	matched := []alertmanager.MatchedRoute{
		{Route: &alertmanager.Route{Receiver: "team-database"}},
		{Route: &alertmanager.Route{Receiver: "pagerduty-database"}, Depth: 1, IsSubroute: true, IsEffective: true, ResolvedReceiver: "pagerduty-database"},
	}

	steps, receivers, continueCount, _ := buildRouteSummary(matched, "pagerduty-database", config)
	if got := strings.Join(receivers, ","); got != "pagerduty-database" {
		t.Fatalf("matched receivers = %q, want pagerduty-database", got)
	}
	if continueCount != 0 {
		t.Fatalf("continue count = %d, want 0", continueCount)
	}
	if len(steps) != 2 || steps[0].IsFinal || !steps[1].IsFinal {
		t.Fatalf("route steps = %#v, want only child final", steps)
	}

	summaries := buildReceiverSummaries(matched, "pagerduty-database", config)
	if len(summaries) != 1 || summaries[0].Name != "pagerduty-database" {
		t.Fatalf("receiver summaries = %#v, want only pagerduty-database", summaries)
	}
}

func TestIsDefaultRootRejectsEffectiveTopLevelMatch(t *testing.T) {
	config := &alertmanager.Config{Route: &alertmanager.Route{Receiver: "default"}}
	matched := []alertmanager.MatchedRoute{{Route: &alertmanager.Route{Match: map[string]string{"component": "infrastructure"}}, IsEffective: true, ResolvedReceiver: "default"}}

	if isDefaultRoot("default", matched, config) {
		t.Fatal("isDefaultRoot() = true, want false for an effective route match")
	}
}

func TestIsDefaultRootRejectsEffectiveNestedMatchUsingRootReceiver(t *testing.T) {
	config := &alertmanager.Config{Route: &alertmanager.Route{Receiver: "default"}}
	matched := []alertmanager.MatchedRoute{
		{Route: &alertmanager.Route{}, Depth: 0, ResolvedReceiver: "default"},
		{Route: &alertmanager.Route{}, Depth: 1, IsEffective: true, ResolvedReceiver: "default"},
	}

	if isDefaultRoot("default", matched, config) {
		t.Fatal("isDefaultRoot() = true, want false for an effective nested match")
	}
}

// TestHandleTestRendersErrorForMissingRootRoute verifies that a missing root
// route returns a rendered error response instead of attempting route matching.
func TestHandleTestRendersErrorForMissingRootRoute(t *testing.T) {
	captureSlogOutput(t, "alertmanager config route is nil")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/status" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"config":{"original":"receivers:\n  - name: default\n"}}`))
	}))
	defer server.Close()

	h := &Handler{
		client: alertmanager.NewClient(server.URL, false),
		tmpl:   loadTemplates(t),
	}
	request := httptest.NewRequest(http.MethodPost, "/test", bytes.NewBufferString(`{"labels":{"alertname":"MissingRoute"}}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("HX-Request", "true")
	response := httptest.NewRecorder()

	h.HandleTest(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if !bytes.Contains(response.Body.Bytes(), []byte("no route defined")) {
		t.Fatalf("response = %q, want missing-route error", response.Body.String())
	}
}

func TestHandleTestShowsMatcherDiagnosticsOnlyForRootFallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"config":{"original":"route:\n  receiver: default\n  routes:\n  - match:\n      severity: critical\n    receiver: pagerduty-critical\nreceivers:\n- name: default\n- name: pagerduty-critical\n"}}`))
	}))
	defer server.Close()

	for _, testCase := range []struct {
		name     string
		labels   map[string]string
		want     string
		dontWant string
	}{
		{name: "fallback", labels: map[string]string{"severity": "warning"}, want: `severity = &#34;critical&#34;`, dontWant: ""},
		{name: "matched route", labels: map[string]string{"severity": "critical"}, want: "Route result", dontWant: "Why no child route matched"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			h := &Handler{client: alertmanager.NewClient(server.URL, false), tmpl: loadTemplates(t)}
			body, err := json.Marshal(map[string]map[string]string{"labels": testCase.labels})
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPost, "/test", bytes.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("HX-Request", "true")
			response := httptest.NewRecorder()

			h.HandleTest(response, request)

			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d: %s", response.Code, http.StatusOK, response.Body.String())
			}
			output := response.Body.String()
			if !strings.Contains(output, testCase.want) {
				t.Errorf("output does not contain %q: %s", testCase.want, output)
			}
			if testCase.dontWant != "" && strings.Contains(output, testCase.dontWant) {
				t.Errorf("output contains %q: %s", testCase.dontWant, output)
			}
		})
	}
}

func TestHandleGenerateSampleAlertsUsesCachedConfig(t *testing.T) {
	originalConfig := strings.Join([]string{
		"route:",
		"  receiver: default",
		"  routes:",
		"  - receiver: slack-warnings",
		"    matchers:",
		"    - severity=\"warning\"",
		"    continue: true",
		"  - receiver: monitoring-team",
		"    matchers:",
		"    - team=\"monitoring\"",
		"receivers:",
		"- name: default",
		"- name: slack-warnings",
		"- name: monitoring-team",
	}, "\n")
	configJSON, err := json.Marshal(map[string]map[string]string{"config": {"original": originalConfig}})
	if err != nil {
		t.Fatalf("marshal status response: %v", err)
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/status" {
			http.NotFound(w, r)
			return
		}
		requests++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(configJSON)
	}))
	defer server.Close()

	client := alertmanager.NewClient(server.URL, false)
	if _, err := client.GetConfig(); err != nil {
		t.Fatalf("GetConfig(): %v", err)
	}
	h := &Handler{
		client:              client,
		clients:             map[string]*alertmanager.Client{"local": client},
		alertmanagerNames:   []string{"local"},
		defaultAlertmanager: "local",
		tmpl:                loadTemplates(t),
	}
	request := httptest.NewRequest(http.MethodGet, "/config/samples?alertmanager=local", nil)
	response := httptest.NewRecorder()

	h.HandleGenerateSampleAlerts(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", response.Code, http.StatusOK, response.Body.String())
	}
	for _, expected := range []string{"Multiple receivers", "Continue sends to 2 receivers.", "severity=warning", "team=monitoring", `data-label-key="severity"`, `data-label-value="warning"`} {
		if !strings.Contains(response.Body.String(), expected) {
			t.Errorf("sample response does not contain %q: %s", expected, response.Body.String())
		}
	}
	if requests != 1 {
		t.Fatalf("Alertmanager requests = %d, want cached config to avoid a second request", requests)
	}
}

func TestReloadConfigRefetchesConfig(t *testing.T) {
	captureSlogOutput(t, "reloading alertmanager config cache")
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/status" {
			http.NotFound(w, r)
			return
		}
		requests++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"config":{"original":"route:\n  receiver: default\nreceivers:\n  - name: default\n"}}`))
	}))
	defer server.Close()

	client := alertmanager.NewClient(server.URL, false)
	h := &Handler{client: client}
	req := httptest.NewRequest(http.MethodPost, "/config/reload", nil)
	response := httptest.NewRecorder()

	h.HandleReloadConfig(response, req)

	if response.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusSeeOther)
	}
	if requests != 1 {
		t.Fatalf("status requests after reload = %d, want 1", requests)
	}
	if client.ConfigCachedAt().IsZero() {
		t.Fatal("expected config to be cached after reload")
	}
	config, err := client.GetConfig()
	if err != nil {
		t.Fatalf("cached GetConfig() error = %v", err)
	}
	if config.Route == nil || config.Route.Receiver != "default" {
		t.Fatalf("cached route = %#v, want default root route", config.Route)
	}
	if requests != 1 {
		t.Fatalf("status requests after cache hit = %d, want 1", requests)
	}

	client.InvalidateConfig()
	if _, err := client.GetConfig(); err != nil {
		t.Fatalf("GetConfig() after invalidation error = %v", err)
	}
	if requests != 2 {
		t.Fatalf("status requests after invalidation = %d, want 2", requests)
	}
}

func TestReloadConfigReturnsFailureWhenFetchFails(t *testing.T) {
	captureSlogOutput(t, "error reloading alertmanager config")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()

	h := &Handler{client: alertmanager.NewClient(server.URL, false)}
	request := httptest.NewRequest(http.MethodPost, "/config/reload", nil)
	response := httptest.NewRecorder()

	h.HandleReloadConfig(response, request)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
}

func TestReloadConfigReturnsHXErrorTriggerWhenFetchFails(t *testing.T) {
	captureSlogOutput(t, "error reloading alertmanager config")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()

	h := &Handler{client: alertmanager.NewClient(server.URL, false)}
	request := httptest.NewRequest(http.MethodPost, "/config/reload", http.NoBody)
	request.Header.Set("HX-Request", "true")
	response := httptest.NewRecorder()

	h.HandleReloadConfig(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNoContent)
	}
	if got := response.Header().Get("HX-Trigger"); got != "config-reload-error" {
		t.Fatalf("HX-Trigger = %q, want config-reload-error", got)
	}
}

func TestReloadConfigPreservesCachedConfigWhenRefreshFails(t *testing.T) {
	captureSlogOutput(t, "error reloading alertmanager config")
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests == 1 {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"config":{"original":"route:\n  receiver: default\nreceivers:\n  - name: default\n"}}`))
			return
		}
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()

	client := alertmanager.NewClient(server.URL, false)
	if _, err := client.GetConfig(); err != nil {
		t.Fatalf("initial GetConfig() error = %v", err)
	}

	h := &Handler{client: client}
	request := httptest.NewRequest(http.MethodPost, "/config/reload", nil)
	response := httptest.NewRecorder()
	h.HandleReloadConfig(response, request)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
	config, err := client.GetConfig()
	if err != nil {
		t.Fatalf("cached GetConfig() error = %v", err)
	}
	if config.Route == nil || config.Route.Receiver != "default" {
		t.Fatalf("cached route = %#v, want default", config.Route)
	}
	if requests != 2 {
		t.Fatalf("status requests = %d, want 2", requests)
	}
}

func TestReloadConfigReturnsHXRefreshAfterSuccess(t *testing.T) {
	captureSlogOutput(t, "reloading alertmanager config cache")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"config":{"original":"route:\n  receiver: default\nreceivers:\n  - name: default\n"}}`))
	}))
	defer server.Close()

	h := &Handler{client: alertmanager.NewClient(server.URL, false)}
	request := httptest.NewRequest(http.MethodPost, "/config/reload", nil)
	request.Header.Set("HX-Request", "true")
	response := httptest.NewRecorder()

	h.HandleReloadConfig(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNoContent)
	}
	if got := response.Header().Get("HX-Refresh"); got != "true" {
		t.Fatalf("HX-Refresh = %q, want true", got)
	}
}

func TestResultTemplateRendersRootFallbackWithMatchedParent(t *testing.T) {
	tmpl := loadTemplates(t)
	data := resultData{
		Receiver:         "default",
		MatchedRoutes:    []alertmanager.MatchedRoute{{Route: &alertmanager.Route{Match: map[string]string{"component": "infrastructure"}}, IsEffective: true, ResolvedReceiver: "default"}},
		MatchedReceivers: []string{"default"},
		DefaultRoot:      false,
		Labels:           map[string]string{"component": "infrastructure"},
	}

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "result.html", data); err != nil {
		t.Fatalf("result.html failed to render root fallback: %v", err)
	}
	output := buf.String()
	if strings.Contains(output, "root route's receiver") {
		t.Fatal("effective route match should not show the root fallback notice")
	}
	if !strings.Contains(output, "Matched route rules") {
		t.Fatal("effective route match should show the matched route")
	}
}

func TestHandleTestReturnsNestedRouteJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"config":{"original":"route:\n  receiver: default\n  routes:\n  - match:\n      team: database\n    receiver: team-database\n    routes:\n    - match:\n        severity: critical\n      receiver: pagerduty-database\nreceivers:\n- name: default\n- name: team-database\n- name: pagerduty-database\n"}}`))
	}))
	defer server.Close()

	h := &Handler{client: alertmanager.NewClient(server.URL, false)}
	request := httptest.NewRequest(http.MethodPost, "/test", bytes.NewBufferString(`{"labels":{"team":"database","severity":"critical"}}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	h.HandleTest(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	var body struct {
		Receiver      string                      `json:"receiver"`
		MatchedRoutes []alertmanager.MatchedRoute `json:"matched_routes"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Receiver != "pagerduty-database" {
		t.Fatalf("receiver = %q, want pagerduty-database", body.Receiver)
	}
	if len(body.MatchedRoutes) != 2 || body.MatchedRoutes[1].Depth != 1 || !body.MatchedRoutes[1].IsEffective {
		t.Fatalf("matched routes = %#v, want nested effective child", body.MatchedRoutes)
	}
	if body.MatchedRoutes[1].ResolvedReceiver != "pagerduty-database" {
		t.Fatalf("resolved receiver = %q, want pagerduty-database", body.MatchedRoutes[1].ResolvedReceiver)
	}
}

func TestHandleTestReturnsEmptyMatchedRouteArrayForRootFallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"config":{"original":"route:\n  receiver: default\nreceivers:\n- name: default\n"}}`))
	}))
	defer server.Close()

	h := &Handler{client: alertmanager.NewClient(server.URL, false)}
	request := httptest.NewRequest(http.MethodPost, "/test", bytes.NewBufferString(`{"labels":{"alertname":"unknown"}}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()

	h.HandleTest(response, request)

	var body map[string]json.RawMessage
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if string(body["matched_routes"]) != "[]" {
		t.Fatalf("matched_routes = %s, want []", body["matched_routes"])
	}
}

func TestResultTemplateShowsRouteRulesLeadingToSelectedReceivers(t *testing.T) {
	tmpl := loadTemplates(t)
	tests := []struct {
		name                     string
		data                     resultData
		wantCounts               []string
		wantConditions           []string
		wantReceivers            []string
		wantPrimaryHeading       string
		wantPrimaryCount         string
		wantPrimaryReceivers     []string
		wantPrimaryFinalReceiver string
	}{
		{
			name: "nested rules select one receiver",
			data: resultData{
				Receiver: "team-platform",
				RouteSteps: []RouteStep{
					{Index: 1, Receiver: "region-router", Match: map[string]string{"cluster": "prod-west"}},
					{Index: 2, Receiver: "team-platform", Match: map[string]string{"team": "platform"}, IsFinal: true},
				},
				MatchedReceiverSummaries: []ReceiverSummary{{Name: "team-platform", IsFinal: true}},
			},
			wantCounts:               []string{"2 matched route rules", "1 selected receiver"},
			wantConditions:           []string{`cluster=&#34;prod-west&#34;`, `team=&#34;platform&#34;`},
			wantReceivers:            []string{"team-platform"},
			wantPrimaryHeading:       "Selected receiver",
			wantPrimaryCount:         "1 selected receiver",
			wantPrimaryReceivers:     []string{"team-platform"},
			wantPrimaryFinalReceiver: "team-platform",
		},
		{
			name: "continued rules select multiple receivers",
			data: resultData{
				Receiver: "monitoring-team",
				RouteSteps: []RouteStep{
					{Index: 1, Receiver: "slack-warnings", Matchers: []string{`severity="warning"`}, Continue: true, IsEffective: true},
					{Index: 2, Receiver: "monitoring-team", Matchers: []string{`team="monitoring"`}, Continue: true, IsEffective: true, IsFinal: true},
				},
				MatchedReceiverSummaries: []ReceiverSummary{
					{Name: "slack-warnings"},
					{Name: "monitoring-team", IsFinal: true},
				},
			},
			wantCounts:               []string{"2 matched route rules", "2 selected receivers"},
			wantConditions:           []string{`severity=&#34;warning&#34;`, `team=&#34;monitoring&#34;`},
			wantReceivers:            []string{"slack-warnings", "monitoring-team"},
			wantPrimaryHeading:       "Selected receivers",
			wantPrimaryCount:         "2 selected receivers",
			wantPrimaryReceivers:     []string{"slack-warnings", "monitoring-team"},
			wantPrimaryFinalReceiver: "monitoring-team",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := tmpl.ExecuteTemplate(&buf, "result.html", tt.data); err != nil {
				t.Fatalf("result.html failed to render: %v", err)
			}
			output := buf.String()
			primaryStart := strings.Index(output, `<div class="result-primary-outcome">`)
			detailsStart := strings.Index(output, `<details class="result-details route-matching-details">`)
			if primaryStart < 0 || detailsStart <= primaryStart {
				t.Fatal("primary receiver summary is missing or appears after route details")
			}
			primarySummary := output[primaryStart:detailsStart]
			if !strings.Contains(primarySummary, `<h3>`+tt.wantPrimaryHeading+`</h3>`) {
				t.Errorf("primary summary does not contain heading %q", tt.wantPrimaryHeading)
			}
			if !strings.Contains(primarySummary, `<span>`+tt.wantPrimaryCount+`</span>`) {
				t.Errorf("primary summary does not contain receiver count %q", tt.wantPrimaryCount)
			}
			primaryReceiversStart := strings.Index(primarySummary, `<div class="result-primary-receivers">`)
			if primaryReceiversStart < 0 {
				t.Fatal("primary receiver list is missing")
			}
			primaryReceiversEndOffset := strings.Index(primarySummary[primaryReceiversStart:], `</div>`)
			if primaryReceiversEndOffset < 0 {
				t.Fatal("primary receiver list is not closed")
			}
			primaryReceivers := primarySummary[primaryReceiversStart : primaryReceiversStart+primaryReceiversEndOffset]
			if got := strings.Count(primaryReceivers, `class="result-primary-receiver"`); got != len(tt.wantPrimaryReceivers) {
				t.Errorf("primary receiver count = %d, want %d", got, len(tt.wantPrimaryReceivers))
			}
			position := 0
			for _, receiver := range tt.wantPrimaryReceivers {
				marker := `class="result-primary-receiver">` + receiver
				relativePosition := strings.Index(primaryReceivers[position:], marker)
				if relativePosition < 0 {
					t.Errorf("primary summary does not contain receiver %q in order", receiver)
					break
				}
				position += relativePosition + len(marker)
			}
			if !strings.Contains(primaryReceivers, `class="result-primary-receiver">`+tt.wantPrimaryFinalReceiver+`<span class="result-primary-final">final</span>`) {
				t.Errorf("primary summary does not mark %q as final", tt.wantPrimaryFinalReceiver)
			}
			for _, want := range tt.wantCounts {
				if !strings.Contains(output, want) {
					t.Errorf("route flow does not contain %q", want)
				}
			}
			if !strings.Contains(output, `class="route-flow-arrow"`) {
				t.Error("route flow arrow is missing")
			}
			for _, receiver := range tt.wantReceivers {
				if !strings.Contains(output, `class="route-flow-receiver-name">`+receiver+`</span>`) {
					t.Errorf("route flow does not contain selected receiver %q", receiver)
				}
			}
			source := strings.Index(output, `class="route-flow-source"`)
			arrow := strings.Index(output, `class="route-flow-arrow"`)
			destination := strings.Index(output, `class="route-flow-destination"`)
			ladder := strings.Index(output, `class="route-ladder route-trace"`)
			if source < 0 || arrow <= source || destination <= arrow || ladder <= destination {
				t.Fatalf("route flow positions = source %d, arrow %d, destination %d, ladder %d; want rules → receivers → ladder", source, arrow, destination, ladder)
			}
			for _, condition := range tt.wantConditions {
				if !strings.Contains(output[source:arrow], condition) {
					t.Errorf("matched route rules do not contain condition %q", condition)
				}
			}
		})
	}
}

func TestResultTemplateRendersMatchedRoutes(t *testing.T) {
	tmpl := loadTemplates(t)

	route := &alertmanager.Route{
		Receiver: "pagerduty-critical",
		Match:    map[string]string{"severity": "critical"},
	}

	data := resultData{
		Receiver: "pagerduty-critical",
		MatchedRoutes: []alertmanager.MatchedRoute{
			{
				Route:           route,
				Depth:           0,
				ParentReceivers: nil,
				IsSubroute:      false,
			},
		},
		MatchedReceivers: []string{"pagerduty-critical"},
		RouteSteps: []RouteStep{
			{
				Index:    1,
				Receiver: "pagerduty-critical",
				Match:    map[string]string{"severity": "critical"},
				IsFinal:  true,
			},
		},
		FinalMatch: MatchSummary{
			Match: map[string]string{"severity": "critical"},
		},
		Labels: map[string]string{"severity": "critical", "alertname": "HighCPU"},
	}

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "result.html", data); err != nil {
		t.Fatalf("result.html failed to render: %v", err)
	}

	// Spot-check that the receiver name appears in the output.
	if !bytes.Contains(buf.Bytes(), []byte("pagerduty-critical")) {
		t.Error("expected receiver name 'pagerduty-critical' in rendered output")
	}
}

// TestResultTemplateRendersSubroute verifies a nested route's matcher and receiver are rendered.
func TestResultTemplateRendersSubroute(t *testing.T) {
	tmpl := loadTemplates(t)

	parentRoute := &alertmanager.Route{
		Receiver: "team-database",
		Match:    map[string]string{"team": "database"},
	}
	childRoute := &alertmanager.Route{
		Receiver: "pagerduty-database",
		Match:    map[string]string{"severity": "critical"},
	}

	data := resultData{
		Receiver: "pagerduty-database",
		MatchedRoutes: []alertmanager.MatchedRoute{
			{Route: parentRoute, Depth: 0, IsSubroute: false},
			{Route: childRoute, Depth: 1, ParentReceivers: []string{"team-database"}, IsSubroute: true},
		},
		MatchedReceivers: []string{"pagerduty-database"},
		RouteSteps: []RouteStep{
			{Index: 1, Receiver: "team-database", Match: map[string]string{"team": "database"}, IsEffective: false},
			{Index: 2, Receiver: "pagerduty-database", Match: map[string]string{"severity": "critical"},
				Depth: 1, ParentReceivers: []string{"team-database"}, IsSubroute: true, IsEffective: true, IsFinal: true},
		},
		FinalMatch: MatchSummary{Match: map[string]string{"severity": "critical"}},
		Labels:     map[string]string{"team": "database", "severity": "critical"},
	}

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "result.html", data); err != nil {
		t.Fatalf("result.html failed to render with subroute data: %v", err)
	}

	out := buf.Bytes()
	if !bytes.Contains(out, []byte("pagerduty-database")) {
		t.Error("expected 'pagerduty-database' in rendered output")
	}
	if !bytes.Contains(out, []byte("team-database")) {
		t.Error("expected parent 'team-database' in rendered output")
	}
	if bytes.Contains(out, []byte("Multiple receivers will be notified")) {
		t.Error("nested parent and child should not trigger multiple-receiver notice")
	}
	if !bytes.Contains(out, []byte(`class="trace-match-status">MATCHED</span>`)) {
		t.Error("matched route status is missing")
	}
}

// TestResultTemplateRendersDefaultRoot verifies result.html renders correctly
// when no routes matched and the root default receiver is used.
func TestResultTemplateRendersDefaultRoot(t *testing.T) {
	tmpl := loadTemplates(t)

	data := resultData{
		Receiver:         "default",
		MatchedRoutes:    nil,
		MatchedReceivers: []string{"default"},
		DefaultRoot:      true,
		Labels:           map[string]string{"alertname": "Unknown"},
	}

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "result.html", data); err != nil {
		t.Fatalf("result.html failed to render with default root: %v", err)
	}
	output := buf.String()
	if !strings.Contains(output, "root receiver") || !strings.Contains(output, "result-default") {
		t.Fatalf("default root output = %q, want default notice and styling", output)
	}
	for _, expected := range []string{"0 matched route rules", "1 selected receiver", "No child route matched", `class="result-primary-receiver">default`} {
		if !strings.Contains(output, expected) {
			t.Errorf("default root result does not contain %q", expected)
		}
	}
	if strings.Contains(output, `class="route-outcome-flow"`) {
		t.Error("default root repeats its selected receiver in the route flow")
	}
}

func TestResultTemplateExplainsFailedRouteMatchers(t *testing.T) {
	tmpl := loadTemplates(t)
	data := resultData{
		Receiver:    "default",
		DefaultRoot: true,
		RouteMismatches: []alertmanager.RouteMismatch{{
			Route: &alertmanager.Route{Receiver: "pagerduty-critical"},
			FailedMatchers: []alertmanager.MatcherFailure{
				{Label: "severity", Operator: "=", Expected: "critical", Missing: true},
				{Label: "service", Operator: "=", Expected: "web", Actual: "api"},
			},
		}},
	}

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "result.html", data); err != nil {
		t.Fatalf("result.html failed to render route mismatches: %v", err)
	}
	output := buf.String()
	for _, expected := range []string{
		"Why no child route matched",
		"pagerduty-critical",
		`severity = &#34;critical&#34;`,
		"label is missing",
		`service = &#34;web&#34;`,
		`actual value <code>&#34;api&#34;</code>`,
	} {
		if !strings.Contains(output, expected) {
			t.Errorf("route mismatch output does not contain %q: %s", expected, output)
		}
	}
}

func TestResultTemplatePlacesLongRouteTraceBeforeSecondaryDetails(t *testing.T) {
	tmpl := loadTemplates(t)
	config := &alertmanager.Receiver{
		Name:           "pagerduty-platform",
		WebhookConfigs: []alertmanager.WebhookConfig{{URL: "http://webhook.example.test"}},
	}
	steps := []RouteStep{
		{Index: 1, Receiver: "region-router", Match: map[string]string{"cluster": "prod-west"}, IsSubroute: false},
		{Index: 2, Receiver: "team-platform", Match: map[string]string{"team": "platform"}, IsSubroute: true, Depth: 1},
		{Index: 3, Receiver: "api-platform", Match: map[string]string{"service": "api"}, IsSubroute: true, Depth: 2},
		{Index: 4, Receiver: "slack-platform-prod", Match: map[string]string{"environment": "production"}, IsSubroute: true, Depth: 3},
		{Index: 5, Receiver: "pagerduty-platform", Match: map[string]string{"severity": "critical"}, IsSubroute: true, Depth: 4, IsFinal: true, IsEffective: true},
	}
	data := resultData{
		Receiver:       "pagerduty-platform",
		ReceiverConfig: config,
		MatchedRoutes: []alertmanager.MatchedRoute{{
			Route:            &alertmanager.Route{Receiver: "pagerduty-platform"},
			IsEffective:      true,
			ResolvedReceiver: "pagerduty-platform",
		}},
		RouteSteps: steps,
		Labels: map[string]string{
			"cluster":     "prod-west",
			"team":        "platform",
			"service":     "api",
			"environment": "production",
			"severity":    "critical",
		},
	}

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "result.html", data); err != nil {
		t.Fatalf("result.html failed to render long route: %v", err)
	}
	output := buf.String()
	tracePosition := strings.Index(output, `class="route-ladder route-trace"`)
	receiverConfigPosition := strings.Index(output, `class="receiver-config`)
	if tracePosition < 0 || receiverConfigPosition < 0 || tracePosition > receiverConfigPosition {
		t.Fatalf("route ladder position = %d, receiver config position = %d, want ladder before secondary details", tracePosition, receiverConfigPosition)
	}
	if !strings.Contains(output, `class="result-heading"`) || !strings.Contains(output, "Route result") || !strings.Contains(output, "5 matched route rules") {
		t.Fatal("route result summary is missing its heading or route count")
	}
	if !strings.Contains(output, `class="trace-match-status">MATCHED</span>`) || !strings.Contains(output, "Match conditions") || strings.Contains(strings.ToLower(output), "deliver") {
		t.Fatal("long route ladder output is missing clear match status")
	}
	trace := output[tracePosition:receiverConfigPosition]
	lastStepPosition := -1
	for _, receiver := range []string{"region-router", "team-platform", "api-platform", "slack-platform-prod", "pagerduty-platform"} {
		position := strings.Index(trace, `class="trace-receiver">`+receiver+`</span>`)
		if position <= lastStepPosition {
			t.Fatalf("route step %q position = %d, want it after position %d", receiver, position, lastStepPosition)
		}
		lastStepPosition = position
	}
	matchPosition := strings.Index(trace, `<span class="trace-kv">cluster=prod-west</span>`)
	receiverPosition := strings.Index(trace, `<span class="trace-receiver">region-router</span>`)
	if matchPosition < 0 || receiverPosition < 0 || matchPosition > receiverPosition {
		t.Fatalf("first route matcher position = %d, receiver position = %d, want matcher first", matchPosition, receiverPosition)
	}
	if !strings.Contains(output, `<details class="result-details">`) {
		t.Fatal("secondary receiver details are not collapsible")
	}
}

func TestResultTemplateLabelsNativeMatchersAsConditions(t *testing.T) {
	tmpl := loadTemplates(t)
	data := resultData{
		Receiver: "monitoring-team",
		RouteSteps: []RouteStep{
			{Index: 1, Receiver: "slack-warnings", Matchers: []string{`severity="warning"`}, Continue: true, IsEffective: true},
			{Index: 2, Receiver: "monitoring-team", Matchers: []string{`team="monitoring"`}, Continue: true, IsEffective: true, IsFinal: true},
		},
		MatchedReceivers: []string{"slack-warnings", "monitoring-team"},
	}

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "result.html", data); err != nil {
		t.Fatalf("result.html failed to render native matchers: %v", err)
	}
	output := buf.String()
	for _, expected := range []string{
		"Match conditions",
		`severity=&#34;warning&#34;`,
		`team=&#34;monitoring&#34;`,
		`class="trace-match-status">MATCHED</span>`,
	} {
		if !strings.Contains(output, expected) {
			t.Errorf("native matcher output does not contain %q", expected)
		}
	}
	if strings.Contains(strings.ToLower(output), "deliver") {
		t.Error("route ladder output contains unclear delivery wording")
	}
}

func TestIndexTemplateIncludesShareLinkControls(t *testing.T) {
	tmpl := loadTemplates(t)
	data := struct {
		LabelSuggestions     []alertmanager.LabelSuggestion
		SampleAlerts         []alertmanager.SampleAlert
		SampleAlertsDeferred bool
		Config               *alertmanager.Config
		AlertmanagerURL      string
		AlertmanagerNames    []string
		SelectedAlertmanager string
		ConnectionStatus     bool
		ConnectionError      string
		ConfigCachedAt       time.Time
		ConfigWasCached      bool
	}{}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "index.html", data); err != nil {
		t.Fatalf("index.html failed to render: %v", err)
	}
	output := buf.String()
	if !strings.Contains(output, "Share link") {
		t.Fatal("share link control is missing")
	}
	if !strings.Contains(output, "loadSharedLabels") {
		t.Fatal("share link loading behavior is missing")
	}
}

func TestIndexTemplateIncludesRedesignControls(t *testing.T) {
	tmpl := loadTemplates(t)
	data := struct {
		LabelSuggestions     []alertmanager.LabelSuggestion
		SampleAlerts         []alertmanager.SampleAlert
		SampleAlertsDeferred bool
		Config               *alertmanager.Config
		AlertmanagerURL      string
		AlertmanagerNames    []string
		SelectedAlertmanager string
		ConnectionStatus     bool
		ConnectionError      string
		ConfigCachedAt       time.Time
		ConfigWasCached      bool
	}{AlertmanagerURL: "http://localhost:9093", ConnectionStatus: true, ConfigCachedAt: time.Now(), SampleAlertsDeferred: true}

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "index.html", data); err != nil {
		t.Fatalf("index.html failed to render: %v", err)
	}

	output := buf.String()
	for _, expected := range []string{
		`src="/static/htmx.min.js"`,
		`class="app-headerbar"`,
		`class="theme-switch"`,
		`aria-label="Test the alert route"`,
		`aria-live="polite"`,
		`textContent = key`,
		`loadSampleAlertFromCard`,
		`Generate examples`,
		`hx-get="/config/samples?alertmanager=`,
	} {
		if !strings.Contains(output, expected) {
			t.Errorf("rendered index template does not contain %q", expected)
		}
	}
}

func TestIndexTemplateExplainsEmptyLabelsAreAddedBelow(t *testing.T) {
	tmpl := loadTemplates(t)
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "index.html", IndexData{}); err != nil {
		t.Fatalf("index.html failed to render: %v", err)
	}

	output := buf.String()
	containerStart := strings.Index(output, `<div id="current-labels" class="current-labels">`)
	if containerStart < 0 {
		t.Fatal("initial labels area is missing")
	}
	containerEndOffset := strings.Index(output[containerStart:], `</div>`)
	if containerEndOffset < 0 {
		t.Fatal("initial labels area is not closed")
	}
	containerMarkup := output[containerStart : containerStart+containerEndOffset]
	if !strings.Contains(containerMarkup, `<p class="no-labels-text">No labels added. Add labels below, then test the route.</p>`) {
		t.Fatal("initial labels area does not render its empty-state message")
	}
}

func TestIndexTemplateUsesInfoIconsForAllHelpTips(t *testing.T) {
	tmpl := loadTemplates(t)
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "index.html", IndexData{}); err != nil {
		t.Fatalf("index.html failed to render: %v", err)
	}

	output := buf.String()
	for _, tooltip := range []string{
		`data-tooltip="A route matches only when its labels match." aria-label="Alert label help: A route matches only when its labels match." tabindex="0">ⓘ</span>`,
		`data-tooltip="Use one key: value pair per line." aria-label="Paste labels as YAML help: Use one key: value pair per line." tabindex="0">ⓘ</span>`,
	} {
		if !strings.Contains(output, tooltip) {
			t.Errorf("index help tooltip does not render its info icon: %q", tooltip)
		}
	}
}

func TestIndexTemplateLabelsInputAndOutputPanes(t *testing.T) {
	tmpl := loadTemplates(t)
	data := IndexData{AlertmanagerURL: "http://localhost:9093"}

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "index.html", data); err != nil {
		t.Fatalf("index.html failed to render: %v", err)
	}

	output := buf.String()
	for _, label := range []string{
		`<span class="workspace-pane-label">ALERT INPUT</span>`,
		`<span class="workspace-pane-label">ROUTING OUTPUT</span>`,
	} {
		if !strings.Contains(output, label) {
			t.Errorf("workspace does not label pane %q", label)
		}
	}
}

func TestResultTemplateHighlightsReceiverAndCollapsesRouteExplanation(t *testing.T) {
	tmpl := loadTemplates(t)
	data := resultData{
		Receiver: "pagerduty-platform",
		RouteSteps: []RouteStep{
			{Index: 1, Receiver: "region-router", Match: map[string]string{"cluster": "prod-west"}},
			{Index: 2, Receiver: "pagerduty-platform", Match: map[string]string{"severity": "critical"}, IsFinal: true},
		},
		MatchedReceivers:         []string{"pagerduty-platform"},
		MatchedReceiverSummaries: []ReceiverSummary{{Name: "pagerduty-platform", IsFinal: true}},
	}

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "result.html", data); err != nil {
		t.Fatalf("result.html failed to render: %v", err)
	}

	output := buf.String()
	detailsStart := strings.Index(output, `<details class="result-details route-matching-details">`)
	if detailsStart < 0 {
		t.Fatal("route explanation is not inside a disclosure")
	}
	if !strings.Contains(output[:detailsStart], "ROUTING OUTPUT") || !strings.Contains(output[:detailsStart], "Selected receiver") || !strings.Contains(output[:detailsStart], `class="result-primary-receiver">pagerduty-platform`) {
		t.Fatal("routing output does not show the selected receiver before route details")
	}
	if !strings.Contains(output, `<summary>Expand: How routing matched (2 matched route rules)</summary>`) {
		t.Fatal("route disclosure does not explain what expands")
	}
	detailsTagEnd := strings.Index(output[detailsStart:], ">") + detailsStart
	if detailsTagEnd <= detailsStart || strings.Contains(output[detailsStart:detailsTagEnd], " open") {
		t.Fatal("route disclosure is open by default")
	}
	condition := strings.Index(output, `cluster=&#34;prod-west&#34;`)
	ladder := strings.Index(output, `class="route-ladder route-trace"`)
	if condition < detailsStart || ladder < detailsStart {
		t.Fatal("matched conditions and route ladder must be inside the collapsed disclosure")
	}
}

func TestResultTemplateUsesInfoIconForRouteResultHelp(t *testing.T) {
	tmpl := loadTemplates(t)
	data := resultData{Receiver: "default", DefaultRoot: true}

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "result.html", data); err != nil {
		t.Fatalf("result.html failed to render: %v", err)
	}

	if !strings.Contains(buf.String(), `<span class="help-tip" role="img" data-tooltip="Alertmanager uses matched route rules to select the receiver or receivers." aria-label="Route result help: Alertmanager uses matched route rules to select the receiver or receivers." tabindex="0">ⓘ</span>`) {
		t.Fatal("route result help does not render an info icon with its tooltip")
	}
}

func TestResultTemplateUsesInfoIconsForRouteDetails(t *testing.T) {
	tmpl := loadTemplates(t)
	data := resultData{
		Receiver: "pagerduty-platform",
		MatchedRoutes: []alertmanager.MatchedRoute{
			{Route: &alertmanager.Route{Receiver: "team-platform"}},
			{Route: &alertmanager.Route{Receiver: "pagerduty-platform"}, Depth: 1, IsSubroute: true},
		},
		RouteSteps: []RouteStep{
			{Index: 1, Receiver: "team-platform"},
			{Index: 2, Receiver: "pagerduty-platform", Depth: 1, IsSubroute: true, IsFinal: true},
		},
		MatchedReceivers: []string{"pagerduty-platform"},
	}

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "result.html", data); err != nil {
		t.Fatalf("result.html failed to render: %v", err)
	}

	output := buf.String()
	for _, tooltip := range []string{
		`data-tooltip="These rules match the alert labels." aria-label="Matched route rules help: These rules match the alert labels." tabindex="0">ⓘ</span>`,
		`data-tooltip="This shows the matching condition and receiver for each route." aria-label="Route ladder help: This shows the matching condition and receiver for each route." tabindex="0">ⓘ</span>`,
		`data-tooltip="This rule is inside the parent rule." aria-label="Nested subroute: This rule is inside the parent rule." tabindex="0">subroute (level 1)</span>`,
	} {
		if !strings.Contains(output, tooltip) {
			t.Errorf("result help tooltip does not render consistently: %q", tooltip)
		}
	}
}

func TestResultTemplateIncludesRedesignHelp(t *testing.T) {
	tmpl := loadTemplates(t)
	data := resultData{
		Receiver: "pagerduty-database",
		MatchedRoutes: []alertmanager.MatchedRoute{
			{Route: &alertmanager.Route{Receiver: "team-database"}},
			{Route: &alertmanager.Route{Receiver: "pagerduty-database"}, Depth: 1, IsSubroute: true, IsEffective: true, ParentReceivers: []string{"team-database"}},
		},
		RouteSteps: []RouteStep{
			{Index: 1, Receiver: "team-database"},
			{Index: 2, Receiver: "pagerduty-database", Depth: 1, IsSubroute: true, IsEffective: true, ParentReceivers: []string{"team-database"}, IsFinal: true},
		},
		MatchedReceivers: []string{"team-database", "pagerduty-database"},
		Labels:           map[string]string{"team": "database"},
	}

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, "result.html", data); err != nil {
		t.Fatalf("result.html failed to render: %v", err)
	}

	output := buf.String()
	for _, expected := range []string{
		`class="route-ladder route-trace"`,
		`trace-final`,
		`trace-subroute`,
		`data-tooltip="Alertmanager uses matched route rules to select the receiver or receivers."`,
		`2 matched route rules`,
		`Each receiver gets a copy.`,
	} {
		if !strings.Contains(output, expected) {
			t.Errorf("rendered result template does not contain %q", expected)
		}
	}
}
