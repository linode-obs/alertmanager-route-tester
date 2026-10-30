package handler

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"

	"gopkg.in/yaml.v3"

	"github.com/linode-obs/alertmanager-route-tester/internal/alertmanager"
)

func HandleParseLabels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxTestRequestBodyBytes)
	contents, err := io.ReadAll(r.Body)
	if err != nil {
		if requestBodyTooLarge(err) {
			http.Error(w, "Request body too large", http.StatusRequestEntityTooLarge)
		} else {
			http.Error(w, "Cannot read YAML labels", http.StatusBadRequest)
		}
		return
	}
	decoder := yaml.NewDecoder(bytes.NewReader(contents))
	var document yaml.Node
	err = decoder.Decode(&document)
	if err == nil {
		var extra yaml.Node
		err = decoder.Decode(&extra)
		switch err {
		case io.EOF:
			err = nil
		case nil:
			http.Error(w, "Use one YAML labels object", http.StatusBadRequest)
			return
		}
	}
	if err != nil {
		http.Error(w, "Invalid YAML labels", http.StatusBadRequest)
		return
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		http.Error(w, "Labels must be a YAML object", http.StatusBadRequest)
		return
	}
	labels := make(map[string]string)
	entries := document.Content[0].Content
	for index := 0; index < len(entries); index += 2 {
		name, value := entries[index], entries[index+1]
		_, duplicate := labels[name.Value]
		if name.Kind != yaml.ScalarNode || name.Tag != "!!str" || name.Value == "" || value.Kind != yaml.ScalarNode || value.Tag == "!!null" || duplicate {
			http.Error(w, "Labels must have unique non-empty names and scalar values", http.StatusBadRequest)
			return
		}
		labels[name.Value] = value.Value
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"labels": labels})
}

func (h *Handler) HandleLabelSuggestions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var request TestRequest
	if !decodeJSONRequest(w, r, &request, maxTestRequestBodyBytes) {
		return
	}
	client, _, err := h.selectedClient(request.Alertmanager)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	config, err := client.GetConfigContext(r.Context())
	if err != nil {
		http.Error(w, "Cannot load route suggestions", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(alertmanager.SuggestLabels(config, request.Labels))
}
