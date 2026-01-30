// ABOUTME: HTTP handlers for web UI and API endpoints
// ABOUTME: Serves HTMX-based interface for testing alert routing

package handler

import (
	"encoding/json"
	"html/template"
	"log"
	"net/http"
	"strings"

	"github.com/wbollock/alertmanager-route-tester/internal/alertmanager"
)

type Handler struct {
	client *alertmanager.Client
	tmpl   *template.Template
}

type TestRequest struct {
	Labels map[string]string `json:"labels"`
}

type TestResponse struct {
	Receiver      string                `json:"receiver"`
	MatchedRoutes []*alertmanager.Route `json:"matched_routes"`
	Labels        map[string]string     `json:"labels"`
}

func New(client *alertmanager.Client) *Handler {
	tmpl := template.Must(template.ParseGlob("templates/*.html"))
	return &Handler{
		client: client,
		tmpl:   tmpl,
	}
}

func (h *Handler) HandleIndex(w http.ResponseWriter, r *http.Request) {
	config, err := h.client.GetConfig()
	if err != nil {
		log.Printf("Error fetching config: %v", err)
		http.Error(w, "Failed to fetch Alertmanager config", http.StatusInternalServerError)
		return
	}

	labelKeys := alertmanager.ExtractLabelKeys(config)

	data := struct {
		LabelKeys []string
		Config    *alertmanager.Config
	}{
		LabelKeys: labelKeys,
		Config:    config,
	}

	if err := h.tmpl.ExecuteTemplate(w, "index.html", data); err != nil {
		log.Printf("Error rendering template: %v", err)
		http.Error(w, "Failed to render page", http.StatusInternalServerError)
	}
}

func (h *Handler) HandleTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var labels map[string]string

	// Check if this is form data or JSON
	contentType := r.Header.Get("Content-Type")
	if strings.Contains(contentType, "application/json") {
		var req TestRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}
		labels = req.Labels
	} else {
		// Parse form data
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Invalid form data", http.StatusBadRequest)
			return
		}

		labels = make(map[string]string)
		for key, values := range r.Form {
			if len(values) > 0 && values[0] != "" {
				labels[key] = values[0]
			}
		}
	}

	config, err := h.client.GetConfig()
	if err != nil {
		log.Printf("Error fetching config: %v", err)
		http.Error(w, "Failed to fetch config", http.StatusInternalServerError)
		return
	}

	receiver, matchedRoutes, err := h.client.FindMatchingRoute(labels, config)
	if err != nil {
		log.Printf("Error finding route: %v", err)
		http.Error(w, "Failed to find matching route", http.StatusInternalServerError)
		return
	}

	// Check if HTMX request
	if r.Header.Get("HX-Request") == "true" {
		data := struct {
			Receiver      string
			MatchedRoutes []*alertmanager.Route
			Labels        map[string]string
		}{
			Receiver:      receiver,
			MatchedRoutes: matchedRoutes,
			Labels:        labels,
		}
		if err := h.tmpl.ExecuteTemplate(w, "result.html", data); err != nil {
			log.Printf("Error rendering result: %v", err)
			http.Error(w, "Failed to render result", http.StatusInternalServerError)
		}
		return
	}

	// Return JSON for API requests
	response := TestResponse{
		Receiver:      receiver,
		MatchedRoutes: matchedRoutes,
		Labels:        labels,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func (h *Handler) HandleConfigLabels(w http.ResponseWriter, r *http.Request) {
	config, err := h.client.GetConfig()
	if err != nil {
		log.Printf("Error fetching config: %v", err)
		http.Error(w, "Failed to fetch config", http.StatusInternalServerError)
		return
	}

	labelKeys := alertmanager.ExtractLabelKeys(config)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(labelKeys)
}
