package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/linode-obs/alertmanager-route-tester/internal/alertmanager"
)

func TestJSONContractRejectsInvalidLabels(t *testing.T) {
	for _, body := range []string{`null`, `{}`, `{"labels":null}`, `{"labels":{"severity":null}}`, `{"labels":{"": "critical"}}`, `{"labels":{"severity":4}}`, `{"labels":[]}`, `{"labels":{}} {}`} {
		t.Run(body, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/test", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			(&Handler{}).HandleTest(response, req)
			if response.Code != http.StatusBadRequest || response.Body.String() != "Invalid JSON\n" {
				t.Fatalf("response = %d %q, want 400 Invalid JSON", response.Code, response.Body.String())
			}
		})
	}
}

func TestJSONContractResponse(t *testing.T) {
	original := "route:\n  receiver: default\n  routes:\n  - receiver: critical\n    matchers: ['severity=critical']\nreceivers:\n- name: default\n- name: critical\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"config": map[string]string{"original": original}})
	}))
	defer server.Close()
	h := &Handler{clients: map[string]*alertmanager.Client{"local": alertmanager.NewClient(server.URL, false)}, defaultAlertmanager: "local", tmpl: loadTemplates(t)}
	for _, tc := range []struct {
		body     string
		receiver string
		count    int
	}{
		{`{"labels":{"severity":"critical"},"alertmanager":"local"}`, "critical", 1},
		{`{"labels":{}}`, "default", 0},
	} {
		request := httptest.NewRequest(http.MethodPost, "/test", strings.NewReader(tc.body))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		h.HandleTest(response, request)
		if response.Code != 200 || response.Header().Get("Content-Type") != "application/json" {
			t.Fatalf("response = %d %s", response.Code, response.Body.String())
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(response.Body.Bytes(), &fields); err != nil {
			t.Fatal(err)
		}
		if len(fields) != 4 {
			t.Fatalf("response fields = %v, want four fields", fields)
		}
		for _, name := range []string{"receiver", "receiver_config", "matched_routes", "labels"} {
			if _, ok := fields[name]; !ok {
				t.Errorf("missing field %s", name)
			}
		}
		var result TestResponse
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result.Receiver != tc.receiver || len(result.MatchedRoutes) != tc.count || string(fields["matched_routes"]) == "null" {
			t.Fatalf("result = %#v", result)
		}
		var req TestRequest
		_ = json.Unmarshal([]byte(tc.body), &req)
		if !reflect.DeepEqual(result.Labels, req.Labels) {
			t.Fatalf("labels = %v, want %v", result.Labels, req.Labels)
		}
		if tc.count > 0 {
			var routes []map[string]json.RawMessage
			_ = json.Unmarshal(fields["matched_routes"], &routes)
			for _, name := range []string{"route", "depth", "is_subroute", "is_effective", "resolved_receiver"} {
				if _, ok := routes[0][name]; !ok {
					t.Errorf("missing route field %s", name)
				}
			}
		}
	}
	request := httptest.NewRequest(http.MethodPost, "/test", strings.NewReader(`{"labels":{},"alertmanager":"missing"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	h.HandleTest(response, request)
	if response.Code != 400 || !strings.Contains(response.Body.String(), `unknown Alertmanager "missing"`) {
		t.Fatalf("unknown instance = %d %s", response.Code, response.Body.String())
	}
	response = httptest.NewRecorder()
	h.HandleTest(response, httptest.NewRequest(http.MethodGet, "/test", nil))
	if response.Code != 405 {
		t.Fatalf("GET status = %d", response.Code)
	}
}
