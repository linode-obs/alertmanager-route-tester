package cli_test

import (
	"net/http"
	"net/http/httptest"
	"os"
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
	response, err := client.Get(url + "/api/v2/status")
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
		t.Skipf("Skipping integration test: Alertmanager is not running at %s", alertmanagerURL())
	}
}
