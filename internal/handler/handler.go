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
	Receiver       string                 `json:"receiver"`
	ReceiverConfig *alertmanager.Receiver `json:"receiver_config,omitempty"`
	MatchedRoutes  []*alertmanager.Route  `json:"matched_routes"`
	Labels         map[string]string      `json:"labels"`
}

func New(client *alertmanager.Client) *Handler {
	funcMap := template.FuncMap{
		"json": func(v interface{}) template.JS {
			b, _ := json.Marshal(v)
			return template.JS(b)
		},
	}
	tmpl := template.Must(template.New("").Funcs(funcMap).ParseGlob("templates/*.html"))
	return &Handler{
		client: client,
		tmpl:   tmpl,
	}
}

func (h *Handler) HandleIndex(w http.ResponseWriter, r *http.Request) {
	config, err := h.client.GetConfig()
	connectionOK := err == nil

	if err != nil {
		log.Printf("Error fetching config: %v", err)
		data := struct {
			LabelSuggestions []alertmanager.LabelSuggestion
			SampleAlerts     []alertmanager.SampleAlert
			Config           *alertmanager.Config
			AlertmanagerURL  string
			ConnectionStatus bool
			ConnectionError  string
		}{
			LabelSuggestions: []alertmanager.LabelSuggestion{},
			SampleAlerts:     []alertmanager.SampleAlert{},
			Config:           nil,
			AlertmanagerURL:  h.client.BaseURL(),
			ConnectionStatus: false,
			ConnectionError:  err.Error(),
		}
		if err := h.tmpl.ExecuteTemplate(w, "index.html", data); err != nil {
			log.Printf("Error rendering template: %v", err)
			http.Error(w, "Failed to render page", http.StatusInternalServerError)
		}
		return
	}

	labelSuggestions := alertmanager.ExtractLabelSuggestions(config)
	sampleAlerts := alertmanager.GenerateSampleAlerts(config)

	data := struct {
		LabelSuggestions []alertmanager.LabelSuggestion
		SampleAlerts     []alertmanager.SampleAlert
		Config           *alertmanager.Config
		AlertmanagerURL  string
		ConnectionStatus bool
		ConnectionError  string
	}{
		LabelSuggestions: labelSuggestions,
		SampleAlerts:     sampleAlerts,
		Config:           config,
		AlertmanagerURL:  h.client.BaseURL(),
		ConnectionStatus: connectionOK,
		ConnectionError:  "",
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

	contentType := r.Header.Get("Content-Type")
	if strings.Contains(contentType, "application/json") {
		var req TestRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}
		labels = req.Labels
	} else {
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
	var receiver string
	var receiverConfig *alertmanager.Receiver
	var matchedRoutes []*alertmanager.Route
	var errorMsg string

	if err != nil {
		log.Printf("Error fetching config: %v", err)
		errorMsg = "Failed to fetch Alertmanager config: " + err.Error()
	} else if config == nil {
		log.Printf("Config is nil")
		errorMsg = "Alertmanager returned empty config"
	} else if config.Route == nil {
		log.Printf("Config route is nil. Config: %+v", config)
		errorMsg = "Alertmanager config has no route defined. Check your alertmanager.yml"
	} else {
		receiver, matchedRoutes, err = h.client.FindMatchingRoute(labels, config)
		if err != nil {
			log.Printf("Error finding route: %v", err)
			errorMsg = "Error finding matching route: " + err.Error()
		} else if receiver != "" {
			receiverConfig = h.client.FindReceiverByName(receiver, config)
		}
	}

	if r.Header.Get("HX-Request") == "true" {
		data := struct {
			Receiver       string
			ReceiverConfig *alertmanager.Receiver
			MatchedRoutes  []*alertmanager.Route
			Labels         map[string]string
			Error          string
		}{
			Receiver:       receiver,
			ReceiverConfig: receiverConfig,
			MatchedRoutes:  matchedRoutes,
			Labels:         labels,
			Error:          errorMsg,
		}
		if err := h.tmpl.ExecuteTemplate(w, "result.html", data); err != nil {
			log.Printf("Error rendering result: %v", err)
			http.Error(w, "Failed to render result", http.StatusInternalServerError)
		}
		return
	}

	if errorMsg != "" {
		http.Error(w, errorMsg, http.StatusInternalServerError)
		return
	}

	response := TestResponse{
		Receiver:       receiver,
		ReceiverConfig: receiverConfig,
		MatchedRoutes:  matchedRoutes,
		Labels:         labels,
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
