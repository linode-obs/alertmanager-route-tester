package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"go.opentelemetry.io/otel/codes"

	"github.com/wbollock/alertmanager-route-tester/internal/alertmanager"
	"github.com/wbollock/alertmanager-route-tester/internal/telemetry"
)

const maxTestRequestBodyBytes int64 = 1 << 20
const maxRouteMismatchDiagnostics = 100

type Handler struct {
	client              *alertmanager.Client
	clients             map[string]*alertmanager.Client
	alertmanagerNames   []string
	defaultAlertmanager string
	tmpl                *template.Template
}

type TestRequest struct {
	Labels       map[string]string `json:"labels"`
	Alertmanager string            `json:"alertmanager"`
}

type TestResponse struct {
	Receiver       string                      `json:"receiver"`
	ReceiverConfig *alertmanager.Receiver      `json:"receiver_config,omitempty"`
	MatchedRoutes  []alertmanager.MatchedRoute `json:"matched_routes"`
	Labels         map[string]string           `json:"labels"`
}

type RouteStep struct {
	Index          int
	Receiver       string
	Match          map[string]string
	MatchRE        map[string]string
	Matchers       []string
	MatcherResults []alertmanager.MatcherResult
	Continue       bool
	IsFinal        bool
	IsEffective    bool
	TypeLabel      string
	TypeIcon       string
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

type IndexData struct {
	LabelSuggestions     []alertmanager.LabelSuggestion
	SampleAlerts         []alertmanager.SampleAlert
	SampleAlertsDeferred bool
	Config               *alertmanager.Config
	AlertmanagerURL      string
	AlertmanagerNames    []string
	SelectedAlertmanager string
	ConnectionStatus     bool
	ConnectionError      string
	ConfigCachedAt       time.Time
	ConfigWasCached      bool
}

func New(client *alertmanager.Client) *Handler {
	return NewWithClients(map[string]*alertmanager.Client{"default": client}, []string{"default"}, "default")
}

func NewWithClients(clients map[string]*alertmanager.Client, names []string, defaultName string) *Handler {
	return NewWithClientsFromFS(clients, names, defaultName, os.DirFS("."))
}

func NewWithClientsFromFS(clients map[string]*alertmanager.Client, names []string, defaultName string, templateFS fs.FS) *Handler {
	tmpl := template.Must(template.New("").ParseFS(templateFS, "templates/*.html"))
	return &Handler{
		client:              clients[defaultName],
		clients:             clients,
		alertmanagerNames:   names,
		defaultAlertmanager: defaultName,
		tmpl:                tmpl,
	}
}

func (h *Handler) selectedClient(name string) (*alertmanager.Client, string, error) {
	if len(h.clients) == 0 && h.client != nil {
		return h.client, "default", nil
	}
	if name == "" {
		name = h.defaultAlertmanager
	}
	client, ok := h.clients[name]
	if !ok || client == nil {
		return nil, "", fmt.Errorf("unknown Alertmanager %q", name)
	}
	return client, name, nil
}

func (h *Handler) clientForRequest(r *http.Request) (*alertmanager.Client, string, error) {
	name := r.URL.Query().Get("alertmanager")
	if name == "" {
		name = r.FormValue("alertmanager")
	}
	return h.selectedClient(name)
}

func (h *Handler) HandleIndex(w http.ResponseWriter, r *http.Request) {
	client, selectedName, err := h.clientForRequest(r)
	if err != nil {
		defaultClient, _, defaultErr := h.selectedClient("")
		alertmanagerURL := ""
		if defaultErr == nil {
			alertmanagerURL = defaultClient.BaseURL()
		}
		data := IndexData{
			LabelSuggestions:     []alertmanager.LabelSuggestion{},
			SampleAlerts:         []alertmanager.SampleAlert{},
			AlertmanagerNames:    h.alertmanagerNames,
			SelectedAlertmanager: h.defaultAlertmanager,
			AlertmanagerURL:      alertmanagerURL,
			ConnectionError:      err.Error(),
		}
		if renderErr := h.tmpl.ExecuteTemplate(w, "index.html", data); renderErr != nil {
			http.Error(w, "Failed to render page", http.StatusInternalServerError)
		}
		return
	}

	config, configWasCached, err := client.GetConfigWithStatusContext(r.Context())
	connectionOK := err == nil
	configCachedAt := client.ConfigCachedAt()
	data := IndexData{
		LabelSuggestions:     []alertmanager.LabelSuggestion{},
		SampleAlerts:         []alertmanager.SampleAlert{},
		AlertmanagerNames:    h.alertmanagerNames,
		SelectedAlertmanager: selectedName,
		AlertmanagerURL:      client.BaseURL(),
		ConnectionStatus:     connectionOK,
		ConnectionError:      "",
		ConfigCachedAt:       configCachedAt,
		ConfigWasCached:      configWasCached,
	}
	if err != nil {
		slog.Error("error fetching config", "alertmanager", selectedName, "error", err)
		data.ConnectionError = err.Error()
	} else {
		data.Config = config
		data.LabelSuggestions = alertmanager.ExtractLabelSuggestions(config)
		data.SampleAlerts, data.SampleAlertsDeferred, err = client.GetSampleAlertsContext(r.Context(), config)
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return
		}
		if err != nil {
			slog.Error("error generating sample alerts", "error", err)
			data.SampleAlertsDeferred = true
		}
	}

	if err := h.tmpl.ExecuteTemplate(w, "index.html", data); err != nil {
		slog.Error("error rendering template", "error", err)
		http.Error(w, "Failed to render page", http.StatusInternalServerError)
	}
}

func (h *Handler) HandleGenerateSampleAlerts(w http.ResponseWriter, r *http.Request) {
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
		slog.Error("error loading config for sample alerts", "error", err)
		http.Error(w, "Failed to load Alertmanager config", http.StatusServiceUnavailable)
		return
	}

	sampleAlerts, err := client.GenerateSampleAlertsContext(r.Context(), config)
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return
	}
	if errors.Is(err, alertmanager.ErrSampleGenerationTooLarge) {
		if renderErr := h.tmpl.ExecuteTemplate(w, "sample-alert-generation-limit", nil); renderErr != nil {
			slog.Error("error rendering sample generation limit", "error", renderErr)
			http.Error(w, "Failed to render quick examples", http.StatusInternalServerError)
		}
		return
	}
	if err != nil {
		slog.Error("error generating sample alerts", "error", err)
		http.Error(w, "Failed to generate quick examples", http.StatusInternalServerError)
		return
	}

	data := struct {
		SampleAlerts []alertmanager.SampleAlert
	}{SampleAlerts: sampleAlerts}
	if err := h.tmpl.ExecuteTemplate(w, "sample-alert-cards", data); err != nil {
		slog.Error("error rendering sample alerts", "error", err)
		http.Error(w, "Failed to render quick examples", http.StatusInternalServerError)
	}
}

// HandleReloadConfig refreshes the cached config, then redirects back to the
// index page when the refresh succeeds.
func (h *Handler) HandleReloadConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	client, selectedName, err := h.clientForRequest(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	slog.Info("reloading alertmanager config cache", "alertmanager", selectedName)
	if _, err := client.RefreshConfigContext(r.Context()); err != nil {
		slog.Error("error reloading alertmanager config", "error", err)
		if r.Header.Get("HX-Request") == "true" {
			w.Header().Set("HX-Trigger", "config-reload-error")
			w.WriteHeader(http.StatusNoContent)
			return
		}
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

	http.Redirect(w, r, "/?alertmanager="+url.QueryEscape(selectedName), http.StatusSeeOther)
}

func (h *Handler) HandleTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxTestRequestBodyBytes)

	var labels map[string]string
	requestedAlertmanager := r.URL.Query().Get("alertmanager")

	contentType := r.Header.Get("Content-Type")
	if strings.Contains(contentType, "application/json") {
		var req TestRequest
		decoder := json.NewDecoder(r.Body)
		if err := decoder.Decode(&req); err != nil {
			if requestBodyTooLarge(err) {
				http.Error(w, "Request body too large", http.StatusRequestEntityTooLarge)
			} else {
				http.Error(w, "Invalid JSON", http.StatusBadRequest)
			}
			return
		}
		var extra json.RawMessage
		if err := decoder.Decode(&extra); err != io.EOF {
			if requestBodyTooLarge(err) {
				http.Error(w, "Request body too large", http.StatusRequestEntityTooLarge)
			} else {
				http.Error(w, "Invalid JSON", http.StatusBadRequest)
			}
			return
		}
		labels = req.Labels
		if requestedAlertmanager == "" {
			requestedAlertmanager = req.Alertmanager
		}
	} else {
		if err := r.ParseForm(); err != nil {
			if requestBodyTooLarge(err) {
				http.Error(w, "Request body too large", http.StatusRequestEntityTooLarge)
			} else {
				http.Error(w, "Invalid form data", http.StatusBadRequest)
			}
			return
		}

		labels = make(map[string]string)
		for key, values := range r.Form {
			if key == "alertmanager" {
				continue
			}
			if len(values) > 0 && values[0] != "" {
				labels[key] = values[0]
			}
		}
		if requestedAlertmanager == "" {
			requestedAlertmanager = r.FormValue("alertmanager")
		}
	}

	client, _, clientErr := h.selectedClient(requestedAlertmanager)
	if clientErr != nil {
		http.Error(w, clientErr.Error(), http.StatusBadRequest)
		return
	}
	config, err := client.GetConfigContext(r.Context())
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
		routeContext, span := telemetry.StartSpan(r.Context(), "alertmanager.route.evaluate")
		receiver, matchedRoutes, err = client.FindMatchingRoute(labels, config)
		if err != nil {
			span.SetStatus(codes.Error, "route evaluation failed")
			telemetry.RecordRoute(routeContext, telemetry.RouteFailed)
			slog.Error("error finding route", "error", err)
			errorMsg = "Error finding matching route: " + err.Error()
		} else if receiver != "" {
			telemetry.RecordRoute(routeContext, telemetry.RouteMatched)
			receiverConfig = client.FindReceiverByName(receiver, config)
		} else {
			telemetry.RecordRoute(routeContext, telemetry.RouteUnmatched)
		}
		span.End()
	}

	if r.Header.Get("HX-Request") == "true" {
		routeSteps, matchedReceivers, continueCount, finalMatch := buildRouteSummary(matchedRoutes, receiver, config)
		matchedReceiverSummaries := buildReceiverSummaries(matchedRoutes, receiver, config)
		defaultRoot := isDefaultRoot(receiver, matchedRoutes, config)
		routeMismatches, routeMismatchesOmitted := alertmanager.FindRouteMismatchesWithLimit(labels, config, maxRouteMismatchDiagnostics)
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
			RouteMismatches          []alertmanager.RouteMismatch
			RouteMismatchesOmitted   int
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
			DefaultRoot:              defaultRoot,
			MatchedReceiverSummaries: matchedReceiverSummaries,
			RouteMismatches:          routeMismatches,
			RouteMismatchesOmitted:   routeMismatchesOmitted,
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
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	client, _, clientErr := h.clientForRequest(r)
	if clientErr != nil {
		http.Error(w, clientErr.Error(), http.StatusBadRequest)
		return
	}
	config, err := client.GetConfigContext(r.Context())
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
		resolvedReceiver := matchedReceiver(mr)
		if route.Continue {
			continueCount++
		}

		typeLabel, typeIcon := "", ""
		if resolvedReceiver != "" {
			if receiverCfg := receiverMap[resolvedReceiver]; receiverCfg != nil {
				typeLabel, typeIcon = receiverType(receiverCfg)
			}
		}

		step := RouteStep{
			Index:           i + 1,
			Receiver:        resolvedReceiver,
			Match:           route.Match,
			MatchRE:         route.MatchRE,
			Matchers:        route.Matchers,
			MatcherResults:  mr.MatcherResults,
			Continue:        route.Continue,
			IsEffective:     mr.IsEffective,
			TypeLabel:       typeLabel,
			TypeIcon:        typeIcon,
			Depth:           mr.Depth,
			ParentReceivers: mr.ParentReceivers,
			IsSubroute:      mr.IsSubroute,
		}
		steps = append(steps, step)

		if isEffectiveMatch(matchedRoutes, i) && resolvedReceiver != "" && !seen[resolvedReceiver] {
			receivers = append(receivers, resolvedReceiver)
			seen[resolvedReceiver] = true
		}
	}

	if finalReceiver != "" {
		if len(receivers) == 0 {
			receivers = append(receivers, finalReceiver)
		}
		if len(receivers) == 1 {
			for i := len(steps) - 1; i >= 0; i-- {
				if isEffectiveMatch(matchedRoutes, i) && steps[i].Receiver == finalReceiver {
					steps[i].IsFinal = true
					route := matchedRoutes[i].Route
					finalMatch.Match = route.Match
					finalMatch.MatchRE = route.MatchRE
					finalMatch.Matchers = route.Matchers
					break
				}
			}
		}
	}

	return steps, receivers, continueCount, finalMatch
}

func requestBodyTooLarge(err error) bool {
	var maxBytesError *http.MaxBytesError
	return errors.As(err, &maxBytesError)
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
		resolvedReceiver := matchedReceiver(mr)
		if !isEffectiveMatch(matchedRoutes, i) || resolvedReceiver == "" || seen[resolvedReceiver] {
			continue
		}
		receiver := receiverMap[resolvedReceiver]
		summaries = append(summaries, ReceiverSummary{
			Name:       resolvedReceiver,
			Config:     receiver,
			TypeLabels: receiverTypeLabels(receiver),
		})
		seen[resolvedReceiver] = true
	}

	if finalReceiver != "" && !seen[finalReceiver] {
		receiver := receiverMap[finalReceiver]
		summaries = append(summaries, ReceiverSummary{
			Name:       finalReceiver,
			Config:     receiver,
			TypeLabels: receiverTypeLabels(receiver),
		})
	}
	if len(summaries) == 1 && summaries[0].Name == finalReceiver {
		summaries[0].IsFinal = true
	}

	return summaries
}

func isEffectiveMatch(matchedRoutes []alertmanager.MatchedRoute, index int) bool {
	return index >= 0 && index < len(matchedRoutes) && matchedRoutes[index].IsEffective
}

func matchedReceiver(matched alertmanager.MatchedRoute) string {
	if matched.ResolvedReceiver != "" {
		return matched.ResolvedReceiver
	}
	if matched.Route == nil {
		return ""
	}
	return matched.Route.Receiver
}

func isDefaultRoot(receiver string, matchedRoutes []alertmanager.MatchedRoute, config *alertmanager.Config) bool {
	if config == nil || config.Route == nil || receiver == "" || receiver != config.Route.Receiver {
		return false
	}
	for _, matched := range matchedRoutes {
		if matched.IsEffective {
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
