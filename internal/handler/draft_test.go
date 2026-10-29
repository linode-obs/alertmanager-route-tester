package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/linode-obs/alertmanager-route-tester/internal/alertmanager"
)

func TestDraftPreviewDiffAndValidation(t *testing.T) {
	client := alertmanager.NewClient("http://127.0.0.1:1", false)
	h := &Handler{client: client}
	source := "route:\n  receiver: fetched\nreceivers:\n- name: fetched\n"
	draft := strings.ReplaceAll(source, "fetched", "proposed")
	body, _ := json.Marshal(map[string]string{"baseline": source, "draft": draft})
	request := httptest.NewRequest(http.MethodPost, "/config/draft", strings.NewReader(string(body)))
	response := httptest.NewRecorder()
	h.HandlePreviewDraft(response, request)
	if response.Code != 200 {
		t.Fatalf("preview = %d %s", response.Code, response.Body.String())
	}
	var result struct {
		Diff      string   `json:"diff"`
		Receivers []string `json:"receivers"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"--- fetched\n+++ draft\n", "-  receiver: fetched", "+  receiver: proposed"} {
		if !strings.Contains(result.Diff, text) {
			t.Fatalf("diff %q omits %q", result.Diff, text)
		}
	}
	if len(result.Receivers) != 1 || result.Receivers[0] != "proposed" {
		t.Fatalf("receivers = %v", result.Receivers)
	}
	for _, invalid := range []string{"[", "route:\n  receiver: missing\nreceivers: []\n", "route: {}\nsecret: PRIVATE_VALUE"} {
		body, _ := json.Marshal(map[string]string{"baseline": source, "draft": invalid})
		response = httptest.NewRecorder()
		h.HandlePreviewDraft(response, httptest.NewRequest(http.MethodPost, "/config/draft", strings.NewReader(string(body))))
		if response.Code != 400 || strings.Contains(response.Body.String(), "PRIVATE_VALUE") {
			t.Fatalf("invalid draft = %d %s", response.Code, response.Body.String())
		}
	}
	response = httptest.NewRecorder()
	h.HandlePreviewDraft(response, httptest.NewRequest(http.MethodPost, "/config/draft", strings.NewReader(strings.Repeat(" ", 1<<20)+"{}")))
	if response.Code != 413 {
		t.Fatalf("oversized draft = %d", response.Code)
	}
}

func TestUnifiedDiffHandlesContextAndMissingNewlines(t *testing.T) {
	for _, tc := range []struct{ before, after, want string }{
		{"same\n", "same\n", ""},
		{"old\n", "new\n", "--- fetched\n+++ draft\n@@ -1,1 +1,1 @@\n-old\n+new\n"},
		{"", "new\n", "--- fetched\n+++ draft\n@@ -0,0 +1,1 @@\n+new\n"},
		{"old\n", "", "--- fetched\n+++ draft\n@@ -1,1 +0,0 @@\n-old\n"},
		{"same", "same\n", "--- fetched\n+++ draft\n@@ -1,1 +1,1 @@\n-same\n\\ No newline at end of file\n+same\n"},
	} {
		if got := unifiedDiff(tc.before, tc.after); got != tc.want {
			t.Errorf("diff = %q, want %q", got, tc.want)
		}
	}
}

func TestDraftRouteUsesSelectedMatcherMode(t *testing.T) {
	client, err := alertmanager.NewClientWithOptions(alertmanager.ClientOptions{BaseURL: "http://127.0.0.1:1", MatcherMode: alertmanager.MatcherModeUTF8Strict})
	if err != nil {
		t.Fatal(err)
	}
	h := &Handler{client: client, tmpl: loadTemplates(t)}
	for _, tc := range []struct {
		draft    string
		status   int
		receiver string
	}{
		{"route:\n  receiver: proposed\nreceivers:\n- name: proposed\n", 200, "proposed"},
		{"route:\n  receiver: proposed\n  routes:\n  - matchers: ['message=\"a\\qb\"']\nreceivers:\n- name: proposed\n", 400, ""},
	} {
		body, _ := json.Marshal(map[string]any{"labels": map[string]string{}, "draft": tc.draft})
		request := httptest.NewRequest(http.MethodPost, "/test", strings.NewReader(string(body)))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		h.HandleTest(response, request)
		if response.Code != tc.status {
			t.Fatalf("status = %d, want %d: %s", response.Code, tc.status, response.Body.String())
		}
		if tc.status == 200 {
			var result map[string]any
			_ = json.Unmarshal(response.Body.Bytes(), &result)
			if result["receiver"] != tc.receiver || result["draft"] != true {
				t.Fatalf("draft route result = %v", result)
			}
		}
	}
}
