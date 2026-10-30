package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestParseLabelsYAML(t *testing.T) {
	for _, tc := range []struct {
		body   string
		status int
		labels map[string]string
	}{
		{`"message": "a: b\n\"quote\"\\path"`, 200, map[string]string{"message": "a: b\n\"quote\"\\path"}},
		{"severity: critical\ncount: 2\nempty: ''\n日本語: 値", 200, map[string]string{"severity": "critical", "count": "2", "empty": "", "日本語": "値"}},
		{"{}", 200, map[string]string{}},
		{"a: [b]", 400, nil}, {"a: null", 400, nil}, {"a: x\na: y", 400, nil},
		{"a: x\n---\nb: y", 400, nil}, {"", 400, nil}, {"[a, b]", 400, nil}, {"a: [", 400, nil},
		{"a: &x value\nb: *x", 400, nil}, {"'': value", 400, nil},
		{"a: " + strings.Repeat("x", 1<<20), 413, nil},
	} {
		name := tc.body
		if len(name) > 40 {
			name = name[:40]
		}
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/labels/parse", strings.NewReader(tc.body))
			response := httptest.NewRecorder()
			HandleParseLabels(response, request)
			if response.Code != tc.status {
				t.Fatalf("status = %d, want %d: %s", response.Code, tc.status, response.Body.String())
			}
			if tc.status != 200 {
				return
			}
			var data struct {
				Labels map[string]string `json:"labels"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &data); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(data.Labels, tc.labels) {
				t.Fatalf("labels = %#v, want %#v", data.Labels, tc.labels)
			}
		})
	}
	response := httptest.NewRecorder()
	HandleParseLabels(response, httptest.NewRequest(http.MethodGet, "/labels/parse", nil))
	if response.Code != 405 {
		t.Fatalf("GET status = %d", response.Code)
	}
}
