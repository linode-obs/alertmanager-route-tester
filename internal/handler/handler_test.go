package handler

import (
	"bytes"
	"encoding/json"
	"html/template"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wbollock/alertmanager-route-tester/internal/alertmanager"
)

// loadTemplates parses the real templates relative to the package directory so
// tests exercise the same template files that production uses.
func loadTemplates(t *testing.T) *template.Template {
	t.Helper()
	funcMap := template.FuncMap{
		"json": func(v interface{}) template.JS {
			b, _ := json.Marshal(v)
			return template.JS(b)
		},
	}
	tmpl, err := template.New("").Funcs(funcMap).ParseGlob("../../templates/*.html")
	if err != nil {
		t.Fatalf("failed to parse templates: %v", err)
	}
	return tmpl
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
	Labels                   map[string]string
	Error                    string
}

// TestResultTemplateRendersMatchedRoutes verifies that result.html renders
// without error when MatchedRoutes is []alertmanager.MatchedRoute.
//
// This guards against template field regressions such as accessing ".Receiver"
// directly on a MatchedRoute (should be ".Route.Receiver").
func TestHandleTestRendersErrorForMissingRootRoute(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/status" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"configYAML":{"original":"receivers:\n  - name: default\n"}}`))
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

func TestReloadConfigRefetchesConfig(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/status" {
			http.NotFound(w, r)
			return
		}
		requests++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"configYAML":{"original":"route:\n  receiver: default\nreceivers:\n  - name: default\n"}}`))
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
		t.Fatalf("status requests = %d, want 1", requests)
	}
	if client.ConfigCachedAt().IsZero() {
		t.Fatal("expected config to be cached after reload")
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

// TestResultTemplateRendersSubroute verifies result.html renders correctly when
// a MatchedRoute carries subroute context (Depth > 0, ParentReceivers set).
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
		MatchedReceivers: []string{"team-database", "pagerduty-database"},
		RouteSteps: []RouteStep{
			{Index: 1, Receiver: "team-database", Match: map[string]string{"team": "database"}},
			{Index: 2, Receiver: "pagerduty-database", Match: map[string]string{"severity": "critical"},
				Depth: 1, ParentReceivers: []string{"team-database"}, IsSubroute: true, IsFinal: true},
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
}
