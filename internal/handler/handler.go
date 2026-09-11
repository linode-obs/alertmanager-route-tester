package handler

import (
	"encoding/json"
	"html/template"
	"log/slog"
	"net/http"
	"strings"
	"time"

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
	Receiver       string                      `json:"receiver"`
	ReceiverConfig *alertmanager.Receiver      `json:"receiver_config,omitempty"`
	MatchedRoutes  []alertmanager.MatchedRoute `json:"matched_routes"`
	Labels         map[string]string           `json:"labels"`
}

type RouteStep struct {
	Index     int
	Receiver  string
	Match     map[string]string
	MatchRE   map[string]string
	Matchers  []string
	Continue  bool
	IsFinal   bool
	TypeLabel string
	TypeIcon  string
	// Subroute context
	Depth           int
	ParentReceivers []string
	IsSubroute      bool
}

type MatchSummary struct {
	Match    map[string]string
	MatchRE  map[string]string
	Matchers []string
}

type ReceiverSummary struct {
	Name       string
	Config     *alertmanager.Receiver
	TypeLabels []string
	IsFinal    bool
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
	configCachedAt := h.client.ConfigCachedAt()

	if err != nil {
		slog.Error("error fetching config", "error", err)
		data := struct {
			LabelSuggestions []alertmanager.LabelSuggestion
			SampleAlerts     []alertmanager.SampleAlert
			Config           *alertmanager.Config
			AlertmanagerURL  string
			ConnectionStatus bool
			ConnectionError  string
			ConfigCachedAt   time.Time
		}{
			LabelSuggestions: []alertmanager.LabelSuggestion{},
			SampleAlerts:     []alertmanager.SampleAlert{},
			Config:           nil,
			AlertmanagerURL:  h.client.BaseURL(),
			ConnectionStatus: false,
			ConnectionError:  err.Error(),
			ConfigCachedAt:   configCachedAt,
		}
		if err := h.tmpl.ExecuteTemplate(w, "index.html", data); err != nil {
			slog.Error("error rendering template", "error", err)
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
		ConfigCachedAt   time.Time
	}{
		LabelSuggestions: labelSuggestions,
		SampleAlerts:     sampleAlerts,
		Config:           config,
		AlertmanagerURL:  h.client.BaseURL(),
		ConnectionStatus: connectionOK,
		ConnectionError:  "",
		ConfigCachedAt:   configCachedAt,
	}

	if err := h.tmpl.ExecuteTemplate(w, "index.html", data); err != nil {
		slog.Error("error rendering template", "error", err)
		http.Error(w, "Failed to render page", http.StatusInternalServerError)
	}
}

// HandleReloadConfig refreshes the cached config, then redirects back to the
// index page when the refresh succeeds.
func (h *Handler) HandleReloadConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	slog.Info("reloading alertmanager config cache")
	h.client.InvalidateConfig()
	if _, err := h.client.GetConfig(); err != nil {
		slog.Error("error reloading alertmanager config", "error", err)
		http.Error(w, "Failed to reload Alertmanager config", http.StatusServiceUnavailable)
		return
	}

	// If this is an HTMX request, return a small snippet so the page can
	// refresh itself without a full reload.
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Refresh", "true")
		w.WriteHeader(http.StatusNoContent)
		return
	}

	http.Redirect(w, r, "/", http.StatusSeeOther)
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
	var matchedRoutes []alertmanager.MatchedRoute
	var errorMsg string

	if err != nil {
		slog.Error("error fetching config", "error", err)
		errorMsg = "Failed to fetch Alertmanager config: " + err.Error()
	} else if config == nil {
		slog.Error("alertmanager config is nil")
		errorMsg = "Alertmanager returned empty config"
	} else if config.Route == nil {
		slog.Error("alertmanager config route is nil", "config", config)
		errorMsg = "Alertmanager config has no route defined. Check your alertmanager.yml"
	} else {
		receiver, matchedRoutes, err = h.client.FindMatchingRoute(labels, config)
		if err != nil {
			slog.Error("error finding route", "error", err)
			errorMsg = "Error finding matching route: " + err.Error()
		} else if receiver != "" {
			receiverConfig = h.client.FindReceiverByName(receiver, config)
		}
	}

	if r.Header.Get("HX-Request") == "true" {
		routeSteps, matchedReceivers, continueCount, finalMatch := buildRouteSummary(matchedRoutes, receiver, config)
		matchedReceiverSummaries := buildReceiverSummaries(matchedRoutes, receiver, config)
		data := struct {
			Receiver                 string
			ReceiverConfig           *alertmanager.Receiver
			MatchedRoutes            []alertmanager.MatchedRoute
			MatchedReceivers         []string
			RouteSteps               []RouteStep
			ContinueCount            int
			FinalMatch               MatchSummary
			DefaultRoot              bool
			MatchedReceiverSummaries []ReceiverSummary
			Labels                   map[string]string
			Error                    string
		}{
			Receiver:                 receiver,
			ReceiverConfig:           receiverConfig,
			MatchedRoutes:            matchedRoutes,
			MatchedReceivers:         matchedReceivers,
			RouteSteps:               routeSteps,
			ContinueCount:            continueCount,
			FinalMatch:               finalMatch,
			DefaultRoot:              isDefaultRoot(receiver, matchedRoutes, config),
			MatchedReceiverSummaries: matchedReceiverSummaries,
			Labels:                   labels,
			Error:                    errorMsg,
		}
		if err := h.tmpl.ExecuteTemplate(w, "result.html", data); err != nil {
			slog.Error("error rendering result", "error", err)
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
	_ = json.NewEncoder(w).Encode(response)
}

func (h *Handler) HandleConfigLabels(w http.ResponseWriter, r *http.Request) {
	config, err := h.client.GetConfig()
	if err != nil {
		slog.Error("error fetching config", "error", err)
		http.Error(w, "Failed to fetch config", http.StatusInternalServerError)
		return
	}

	labelKeys := alertmanager.ExtractLabelKeys(config)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(labelKeys)
}

func buildRouteSummary(matchedRoutes []alertmanager.MatchedRoute, finalReceiver string, config *alertmanager.Config) ([]RouteStep, []string, int, MatchSummary) {
	steps := make([]RouteStep, 0, len(matchedRoutes))
	receivers := make([]string, 0, len(matchedRoutes))
	seen := make(map[string]bool)
	continueCount := 0
	receiverMap := map[string]*alertmanager.Receiver{}
	finalMatch := MatchSummary{}

	if config != nil {
		for i := range config.Receivers {
			receiver := &config.Receivers[i]
			receiverMap[receiver.Name] = receiver
		}
	}

	for i, mr := range matchedRoutes {
		route := mr.Route
		if route.Continue {
			continueCount++
		}

		typeLabel, typeIcon := "", ""
		if route.Receiver != "" {
			if receiverCfg := receiverMap[route.Receiver]; receiverCfg != nil {
				typeLabel, typeIcon = receiverType(receiverCfg)
			}
		}

		step := RouteStep{
			Index:           i + 1,
			Receiver:        route.Receiver,
			Match:           route.Match,
			MatchRE:         route.MatchRE,
			Matchers:        route.Matchers,
			Continue:        route.Continue,
			TypeLabel:       typeLabel,
			TypeIcon:        typeIcon,
			Depth:           mr.Depth,
			ParentReceivers: mr.ParentReceivers,
			IsSubroute:      mr.IsSubroute,
		}
		steps = append(steps, step)

		if isEffectiveMatch(matchedRoutes, i) && route.Receiver != "" && !seen[route.Receiver] {
			receivers = append(receivers, route.Receiver)
			seen[route.Receiver] = true
		}
	}

	if finalReceiver != "" {
		for i := len(steps) - 1; i >= 0; i-- {
			if steps[i].Receiver == finalReceiver {
				steps[i].IsFinal = true
				route := matchedRoutes[i].Route
				finalMatch.Match = route.Match
				finalMatch.MatchRE = route.MatchRE
				finalMatch.Matchers = route.Matchers
				break
			}
		}
		if len(receivers) == 0 {
			receivers = append(receivers, finalReceiver)
		}
	}

	return steps, receivers, continueCount, finalMatch
}

func receiverType(receiver *alertmanager.Receiver) (string, string) {
	switch {
	case len(receiver.SlackConfigs) > 0:
		return "slack_configs", "💬"
	case len(receiver.PagerdutyConfigs) > 0:
		return "pagerduty_configs", "🚨"
	case len(receiver.WebhookConfigs) > 0:
		return "webhook_configs", "🔗"
	case len(receiver.EmailConfigs) > 0:
		return "email_configs", "✉️"
	case len(receiver.OpsGenieConfigs) > 0:
		return "opsgenie_configs", "🧭"
	default:
		return "", ""
	}
}

func buildReceiverSummaries(matchedRoutes []alertmanager.MatchedRoute, finalReceiver string, config *alertmanager.Config) []ReceiverSummary {
	if config == nil {
		return nil
	}

	receiverMap := map[string]*alertmanager.Receiver{}
	for i := range config.Receivers {
		receiver := &config.Receivers[i]
		receiverMap[receiver.Name] = receiver
	}

	summaries := []ReceiverSummary{}
	seen := map[string]bool{}
	for i, mr := range matchedRoutes {
		route := mr.Route
		if !isEffectiveMatch(matchedRoutes, i) || route.Receiver == "" || seen[route.Receiver] {
			continue
		}
		receiver := receiverMap[route.Receiver]
		summaries = append(summaries, ReceiverSummary{
			Name:       route.Receiver,
			Config:     receiver,
			TypeLabels: receiverTypeLabels(receiver),
			IsFinal:    route.Receiver == finalReceiver,
		})
		seen[route.Receiver] = true
	}

	if finalReceiver != "" && !seen[finalReceiver] {
		receiver := receiverMap[finalReceiver]
		summaries = append(summaries, ReceiverSummary{
			Name:       finalReceiver,
			Config:     receiver,
			TypeLabels: receiverTypeLabels(receiver),
			IsFinal:    true,
		})
	}

	return summaries
}

func isEffectiveMatch(matchedRoutes []alertmanager.MatchedRoute, index int) bool {
	return index >= 0 && index < len(matchedRoutes) && matchedRoutes[index].IsEffective
}

func isDefaultRoot(receiver string, matchedRoutes []alertmanager.MatchedRoute, config *alertmanager.Config) bool {
	if config == nil || config.Route == nil || receiver == "" || receiver != config.Route.Receiver {
		return false
	}
	for _, matched := range matchedRoutes {
		if matched.Route != nil && matched.Route.Receiver == receiver {
			return false
		}
	}
	return true
}

func receiverTypeLabels(receiver *alertmanager.Receiver) []string {
	if receiver == nil {
		return nil
	}

	labels := []string{}
	if count := len(receiver.EmailConfigs); count > 0 {
		labels = append(labels, "email_configs ("+itoa(count)+")")
	}
	if count := len(receiver.SlackConfigs); count > 0 {
		labels = append(labels, "slack_configs ("+itoa(count)+")")
	}
	if count := len(receiver.PagerdutyConfigs); count > 0 {
		labels = append(labels, "pagerduty_configs ("+itoa(count)+")")
	}
	if count := len(receiver.WebhookConfigs); count > 0 {
		labels = append(labels, "webhook_configs ("+itoa(count)+")")
	}
	if count := len(receiver.OpsGenieConfigs); count > 0 {
		labels = append(labels, "opsgenie_configs ("+itoa(count)+")")
	}
	return labels
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	buf := [20]byte{}
	i := len(buf)
	for value > 0 {
		i--
		buf[i] = byte('0' + value%10)
		value /= 10
	}
	return string(buf[i:])
}
