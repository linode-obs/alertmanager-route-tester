package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

func decodeJSONRequest(w http.ResponseWriter, r *http.Request, target any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxTestRequestBodyBytes)
	decoder := json.NewDecoder(r.Body)
	err := decoder.Decode(target)
	if err == nil {
		var extra json.RawMessage
		err = decoder.Decode(&extra)
		if err == io.EOF {
			return true
		}
	}
	if requestBodyTooLarge(err) {
		http.Error(w, "Request body too large", http.StatusRequestEntityTooLarge)
	} else {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
	}
	return false
}

func (h *Handler) HandleConfigSource(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	client, _, err := h.clientForRequest(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	config, err := client.GetConfigContext(r.Context())
	if err != nil {
		http.Error(w, "Cannot load configuration", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"source": config.Source})
}

func (h *Handler) HandlePreviewDraft(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var request struct {
		Alertmanager string `json:"alertmanager"`
		Baseline     string `json:"baseline"`
		Draft        string `json:"draft"`
	}
	if !decodeJSONRequest(w, r, &request) {
		return
	}
	client, _, err := h.selectedClient(request.Alertmanager)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	config, err := client.ParseConfig(request.Draft)
	if err != nil || config.Route == nil {
		http.Error(w, "Invalid draft configuration", http.StatusBadRequest)
		return
	}
	receivers := make([]string, 0, len(config.Receivers))
	for _, receiver := range config.Receivers {
		receivers = append(receivers, receiver.Name)
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		Diff      string   `json:"diff"`
		Receivers []string `json:"receivers"`
	}{unifiedDiff(request.Baseline, request.Draft), receivers})
}

func configurationLines(text string) []string {
	if text == "" {
		return nil
	}
	result := strings.SplitAfter(text, "\n")
	if result[len(result)-1] == "" {
		result = result[:len(result)-1]
	}
	return result
}

// A single hunk bounds diff generation to linear time and memory.
func unifiedDiff(before, after string) string {
	if before == after {
		return ""
	}
	previous, proposed := configurationLines(before), configurationLines(after)
	prefix := 0
	for prefix < len(previous) && prefix < len(proposed) && previous[prefix] == proposed[prefix] {
		prefix++
	}
	suffix := 0
	for len(previous)-suffix > prefix && len(proposed)-suffix > prefix && previous[len(previous)-suffix-1] == proposed[len(proposed)-suffix-1] {
		suffix++
	}
	start := max(0, prefix-3)
	previousEnd, proposedEnd := len(previous)-suffix, len(proposed)-suffix
	context := min(3, suffix)
	previousCount, proposedCount := previousEnd+context-start, proposedEnd+context-start
	previousStart, proposedStart := start+1, start+1
	if previousCount == 0 {
		previousStart = 0
	}
	if proposedCount == 0 {
		proposedStart = 0
	}
	var diff strings.Builder
	fmt.Fprintf(&diff, "--- fetched\n+++ draft\n@@ -%d,%d +%d,%d @@\n", previousStart, previousCount, proposedStart, proposedCount)
	appendLine := func(marker byte, line string) {
		diff.WriteByte(marker)
		diff.WriteString(strings.TrimSuffix(line, "\n"))
		diff.WriteByte('\n')
		if !strings.HasSuffix(line, "\n") {
			diff.WriteString("\\ No newline at end of file\n")
		}
	}
	for _, line := range previous[start:prefix] {
		appendLine(' ', line)
	}
	for _, line := range previous[prefix:previousEnd] {
		appendLine('-', line)
	}
	for _, line := range proposed[prefix:proposedEnd] {
		appendLine('+', line)
	}
	for _, line := range previous[previousEnd : previousEnd+context] {
		appendLine(' ', line)
	}
	return diff.String()
}
