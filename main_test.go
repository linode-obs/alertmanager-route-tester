package main

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	appconfig "github.com/wbollock/alertmanager-route-tester/internal/config"
)

func TestAlertmanagerRequestOnlyAppliesInCLIMode(t *testing.T) {
	if got := alertmanagerRequest(false, "staging"); got != "" {
		t.Fatalf("web alertmanager request = %q, want empty", got)
	}
	if got := alertmanagerRequest(true, "staging"); got != "staging" {
		t.Fatalf("CLI alertmanager request = %q, want staging", got)
	}
}

func TestSelectAlertmanager(t *testing.T) {
	name, err := selectAlertmanager([]string{"production", "staging"}, "staging")
	if err != nil {
		t.Fatalf("selectAlertmanager() error = %v", err)
	}
	if name != "staging" {
		t.Fatalf("selectAlertmanager() = %q, want staging", name)
	}

	if _, err := selectAlertmanager([]string{"production"}, "missing"); err == nil {
		t.Fatal("selectAlertmanager() error = nil, want unknown instance error")
	}
}

func TestNewClientsApplyPerInstanceMatcherMode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"config":{"original":"route:\n  receiver: default\n  routes:\n  - receiver: api\n    matchers:\n    - 'message=\"a\\qb\"'\nreceivers:\n- name: default\n- name: api\n"}}`))
	}))
	defer server.Close()

	clients, _, err := newClients(&appconfig.Config{Alertmanagers: map[string]appconfig.AlertmanagerConfig{
		"strict": {URL: server.URL, MatcherMode: "utf8-strict"},
	}})
	if err != nil {
		t.Fatalf("newClients() error = %v", err)
	}
	if _, err := clients["strict"].GetConfig(); err == nil {
		t.Fatal("strict-mode GetConfig() error = nil, want classic-only matcher rejection")
	}
}

func TestRouteLadderPreviewUsesButtonGroupSemantics(t *testing.T) {
	preview, err := os.ReadFile(filepath.Join("docs", "design-reference", "issue-14-route-ladder", "route-ladder-preview.html"))
	if err != nil {
		t.Fatalf("read route ladder preview: %v", err)
	}
	markup := string(preview)
	if !strings.Contains(markup, `<div class="layout-tabs" role="group" aria-label="Layout variations">`) {
		t.Fatal("layout controls are not exposed as a labeled button group")
	}
	if strings.Contains(markup, `role="tablist"`) || strings.Contains(markup, `role="tab"`) {
		t.Fatal("layout controls expose tab semantics without tab keyboard behavior")
	}
}

func TestParseLabelsJSON(t *testing.T) {
	labels, err := parseLabelsJSON(`{"severity":"critical","team":"platform"}`)
	if err != nil {
		t.Fatalf("parseLabelsJSON() error = %v", err)
	}
	if labels["severity"] != "critical" || labels["team"] != "platform" {
		t.Fatalf("parseLabelsJSON() = %#v, want parsed labels", labels)
	}

	var object map[string]string
	if err := json.Unmarshal([]byte(`{"severity":"critical"}`), &object); err != nil {
		t.Fatal(err)
	}
	if _, err := parseLabelsJSON(`[]`); err == nil {
		t.Fatal("parseLabelsJSON() error = nil, want object validation error")
	}
}

func TestParseLabelsJSONRejectsNullValues(t *testing.T) {
	if _, err := parseLabelsJSON(`{"severity":null}`); err == nil {
		t.Fatal("parseLabelsJSON() error = nil, want null label rejection")
	}
}

func TestServeCancelsHandlerContextsOnSignal(t *testing.T) {
	requestStarted := make(chan struct{})
	requestCanceled := make(chan struct{})
	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			close(requestStarted)
			<-r.Context().Done()
			close(requestCanceled)
		}),
		ReadHeaderTimeout: time.Second,
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	signals := make(chan os.Signal, 1)
	serveResult := make(chan error, 1)
	go func() {
		serveResult <- serve(server, listener, signals)
	}()

	requestResult := make(chan error, 1)
	go func() {
		response, err := http.Get("http://" + listener.Addr().String())
		if response != nil {
			_ = response.Body.Close()
		}
		requestResult <- err
	}()
	select {
	case <-requestStarted:
	case <-time.After(time.Second):
		t.Fatal("request did not reach handler")
	}

	signals <- os.Interrupt
	select {
	case <-requestCanceled:
	case <-time.After(time.Second):
		t.Fatal("handler context was not canceled")
	}
	select {
	case err := <-serveResult:
		if err != nil {
			t.Fatalf("serve() error = %v, want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("serve() did not shut down after signal")
	}
	<-requestResult
}

func TestCleanAlertmanagerDataRemovesVersionBackups(t *testing.T) {
	repoRoot, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	tempRoot := t.TempDir()
	files := []string{
		"bin/alertmanager/alertmanager",
		"bin/alertmanager-123456789/alertmanager",
		"bin/alertmanager-install.A1B2C3/alertmanager",
		"bin/alertmanager-route-tester",
		"data/alertmanager/chunks",
		"data/alertmanager-test/chunks",
	}
	for _, path := range files {
		fullPath := filepath.Join(tempRoot, path)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fullPath, []byte("generated"), 0600); err != nil {
			t.Fatal(err)
		}
	}

	// #nosec G204 -- The test invokes the repository's cleanup script on a temporary fixture tree.
	command := exec.Command("bash", filepath.Join(repoRoot, "scripts", "clean-alertmanager-data.sh"))
	command.Dir = tempRoot
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("clean-alertmanager-data.sh failed: %v\n%s", err, output)
	}
	for _, path := range []string{
		"bin/alertmanager",
		"bin/alertmanager-123456789",
		"bin/alertmanager-install.A1B2C3",
		"data/alertmanager",
		"data/alertmanager-test",
	} {
		if _, err := os.Stat(filepath.Join(tempRoot, path)); !os.IsNotExist(err) {
			t.Errorf("generated path %q remains after cleanup, stat error = %v", path, err)
		}
	}
	if _, err := os.Stat(filepath.Join(tempRoot, "bin", "alertmanager-route-tester")); err != nil {
		t.Errorf("application binary was removed by Alertmanager cleanup: %v", err)
	}
}

func TestServeShutsDownOnSignal(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	server := &http.Server{
		Handler:           http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}),
		ReadHeaderTimeout: time.Second,
	}
	signals := make(chan os.Signal, 1)
	result := make(chan error, 1)
	go func() {
		result <- serve(server, listener, signals)
	}()

	signals <- os.Interrupt

	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("serve() error = %v, want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("serve() did not shut down after signal")
	}
}
