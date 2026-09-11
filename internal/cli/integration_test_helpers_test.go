package cli_test

import (
	"net/http"
	"os"
	"testing"
)

func alertmanagerURL() string {
	if url := os.Getenv("ALERTMANAGER_URL"); url != "" {
		return url
	}
	return "http://localhost:9093"
}

func skipIntegration(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}
	if os.Getenv("SKIP_INTEGRATION_TESTS") != "" {
		t.Skip("Skipping integration tests (SKIP_INTEGRATION_TESTS is set)")
	}

	response, err := http.Get(alertmanagerURL() + "/api/v2/status")
	if err != nil {
		t.Skipf("Skipping integration test: Alertmanager is not running at %s", alertmanagerURL())
	}
	response.Body.Close()
}
