package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/linode-obs/alertmanager-route-tester/internal/alertmanager"
)

func TestDraftPatchAppliesToItsBaseline(t *testing.T) {
	before := "route:\n  receiver: default\nreceivers:\n- name: default\n- name: oncall\n"
	after := strings.Replace(before, "receiver: default", "receiver: oncall", 1)
	h := &Handler{client: alertmanager.NewClient("http://127.0.0.1:1", false)}
	for _, target := range []string{"", "alertmanager.yml", "config/alert manager.yml", "configs/équipe.yml"} {
		t.Run(target, func(t *testing.T) {
			payload, err := json.Marshal(map[string]string{"baseline": before, "draft": after, "patch_target": target})
			if err != nil {
				t.Fatal(err)
			}
			response := httptest.NewRecorder()
			h.HandlePreviewDraft(response, httptest.NewRequest(http.MethodPost, "/config/draft", strings.NewReader(string(payload))))
			if response.Code != 200 {
				t.Fatalf("status = %d: %s", response.Code, response.Body.String())
			}
			var result struct {
				Patch string `json:"patch"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(result.Patch, "diff --git ") {
				t.Fatalf("missing Git patch: %q", result.Patch)
			}
			directory := t.TempDir()
			filename := target
			if filename == "" {
				filename = "alertmanager.yml"
			}
			file := filepath.Join(directory, filepath.FromSlash(filename))
			if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(file, []byte(before), 0600); err != nil {
				t.Fatal(err)
			}
			commands := []*exec.Cmd{exec.Command("git", "apply", "--check", "-"), exec.Command("git", "apply", "-")}
			for _, command := range commands {
				command.Dir = directory
				command.Stdin = strings.NewReader(result.Patch)
				if output, err := command.CombinedOutput(); err != nil {
					t.Fatalf("git apply: %v\n%s", err, output)
				}
			}
			root, err := os.OpenRoot(directory)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := root.Close(); err != nil {
					t.Error(err)
				}
			})
			contents, err := root.ReadFile(filename)
			if err != nil {
				t.Fatal(err)
			}
			if string(contents) != after {
				t.Fatalf("applied file = %q, want %q", contents, after)
			}
		})
	}
}

func TestDraftPatchIsEmptyWithoutChanges(t *testing.T) {
	source := "route:\n  receiver: default\nreceivers:\n- name: default\n"
	payload, err := json.Marshal(map[string]string{"baseline": source, "draft": source})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	h := &Handler{client: alertmanager.NewClient("http://127.0.0.1:1", false)}
	h.HandlePreviewDraft(response, httptest.NewRequest(http.MethodPost, "/config/draft", strings.NewReader(string(payload))))
	var result struct {
		Patch string `json:"patch"`
		Diff  string `json:"diff"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if response.Code != 200 || result.Patch != "" || result.Diff != "" {
		t.Fatalf("unchanged draft = %d %#v", response.Code, result)
	}
}

func TestDraftPatchRejectsUnsafeTargets(t *testing.T) {
	h := &Handler{client: alertmanager.NewClient("http://127.0.0.1:1", false)}
	for _, target := range []string{"../alertmanager.yml", "/etc/alertmanager.yml", "a/../../b", "C:/config.yml", "config\\alertmanager.yml", "a\nb.yml", "."} {
		payload, err := json.Marshal(map[string]string{"baseline": "", "draft": "route:\n  receiver: default\nreceivers:\n- name: default\n", "patch_target": target})
		if err != nil {
			t.Fatal(err)
		}
		response := httptest.NewRecorder()
		h.HandlePreviewDraft(response, httptest.NewRequest(http.MethodPost, "/config/draft", strings.NewReader(string(payload))))
		if response.Code != 400 {
			t.Errorf("target %q: status = %d, want 400", target, response.Code)
		}
	}
}
