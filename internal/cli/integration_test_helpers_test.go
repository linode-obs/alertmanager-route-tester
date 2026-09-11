package cli_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func alertmanagerURL() string {
	if url := os.Getenv("ALERTMANAGER_URL"); url != "" {
		return url
	}
	return "http://localhost:9093"
}

func alertmanagerAvailable(url string) bool {
	client := &http.Client{Timeout: 2 * time.Second}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, strings.TrimRight(url, "/")+"/api/v2/status", nil)
	if err != nil {
		return false
	}
	response, err := client.Do(req)
	if err != nil {
		return false
	}
	defer func() { _ = response.Body.Close() }()
	return response.StatusCode == http.StatusOK
}

func TestAlertmanagerAvailableRequiresSuccessfulStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	if alertmanagerAvailable(server.URL) {
		t.Fatal("alertmanagerAvailable() = true for a 503 response")
	}
}

func skipIntegration(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	if os.Getenv("SKIP_INTEGRATION_TESTS") != "" {
		t.Skip("Skipping integration tests (SKIP_INTEGRATION_TESTS is set)")
	}

	if !alertmanagerAvailable(alertmanagerURL()) {
		if os.Getenv("ALERTMANAGER_URL") != "" {
			t.Fatalf("Alertmanager is not available at %s", alertmanagerURL())
		}
		t.Skipf("Skipping integration test: Alertmanager is not running at %s", alertmanagerURL())
	}
}
