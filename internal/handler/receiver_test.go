package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/linode-obs/alertmanager-route-tester/internal/alertmanager"
)

func TestReceiverCheckGroupsRulesAndDelivery(t *testing.T) {
	failure := alertmanager.MatcherFailure{Label: "team", Operator: "=", Expected: "platform", Missing: true}
	paths := []alertmanager.ReceiverPath{
		{StoppedByReceiver: "earlier", Steps: []alertmanager.ReceiverPathStep{{FailedMatchers: []alertmanager.MatcherFailure{failure, failure}}}},
		{StoppedByReceiver: "earlier", Steps: []alertmanager.ReceiverPathStep{{FailedMatchers: []alertmanager.MatcherFailure{failure}}}},
	}
	check := receiverCheck(paths)
	if !check.HasRoutes || check.Delivered || len(check.StoppedBy) != 1 || check.StoppedBy[0] != "earlier" || len(check.Rules) != 1 || len(check.Rules[0]) != 1 {
		t.Fatalf("grouped explanation = %#v", check)
	}
	paths = append(paths, alertmanager.ReceiverPath{Delivered: true, Matched: true})
	if !receiverCheck(paths).Delivered {
		t.Fatal("delivered path must mark the receiver as reached")
	}
	if !receiverCheck([]alertmanager.ReceiverPath{{Matched: true}}).ChildSelected {
		t.Fatal("matched parent must identify child delivery")
	}
	if receiverCheck(nil).HasRoutes {
		t.Fatal("empty paths must not claim a route exists")
	}
}

func TestReceiverExplanationSummarizesSharedStops(t *testing.T) {
	draft := "route:\n  receiver: default\n  routes:\n  - receiver: stopped-here\n    match: {severity: critical}\n  - receiver: wanted\n    match: {team: platform}\n  - receiver: wanted\n    match: {team: database}\nreceivers:\n- name: default\n- name: stopped-here\n- name: wanted\n"
	body, err := json.Marshal(map[string]any{"labels": map[string]string{"severity": "critical"}, "target_receiver": "wanted", "draft": draft})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/test", strings.NewReader(string(body)))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("HX-Request", "true")
	response := httptest.NewRecorder()
	h := &Handler{client: alertmanager.NewClient("http://127.0.0.1:1", false), tmpl: loadTemplates(t)}
	h.HandleTest(response, request)
	if response.Code != 200 {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	html := response.Body.String()
	if count := strings.Count(html, `class="receiver-check-summary`); count != 1 {
		t.Fatalf("summaries = %d, want one shared explanation", count)
	}
	if strings.Contains(html, "root/") {
		t.Fatal("explanation exposes internal route identifiers")
	}
	if !strings.Contains(html, "Stopped by") || !strings.Contains(html, "stopped-here") {
		t.Fatal("explanation does not identify the blocking receiver")
	}
	if !strings.Contains(html, "platform") || !strings.Contains(html, "database") {
		t.Fatal("explanation discarded alternative label requirements")
	}
}
