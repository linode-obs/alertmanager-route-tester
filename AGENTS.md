# Agent Instructions

## Key Points

- Use `mise run start` to test locally
- Standard library only, no external frameworks
- Route matching logic is in `internal/alertmanager/client.go`
- All tests must pass before committing
- Follow TDD when adding features

## Quick Start

```bash
# Start everything (Alertmanager + Route Tester)
mise run start
```

This will:
1. Download and install Alertmanager if needed
2. Start Alertmanager on :9093
3. Start the Route Tester on :8080
4. Display clickable URLs

Press Ctrl+C to stop all services.

## Individual Commands

```bash
mise run setup         # Download Alertmanager binary
mise run alertmanager  # Run only Alertmanager
mise run dev          # Run only Route Tester (requires Alertmanager running)
mise run test         # Run tests
mise run build        # Build binary
mise run clean        # Clean up bin/ and data/
```

## Project Structure

- `main.go` - HTTP server entrypoint (supports both web and CLI modes via config)
- `internal/alertmanager/` - API client and route matching engine
- `internal/cli/` - CLI test mode (same routing logic as web UI)
- `internal/handler/` - HTTP handlers for UI and API
- `templates/` - HTMX templates
- `static/` - CSS files
- `testdata/` - Sample Alertmanager config for local testing

## Technologies

- Go 1.25 (standard library only, no frameworks)
- HTMX for dynamic UI
- mise for task running

## Testing

Tests use real Alertmanager routing logic without mocks. Run with:

```bash
mise run test
```

## Connecting to Real Alertmanager

Update `config.yaml` with your Alertmanager URL and TLS settings, then run:

```bash
go run .
```

## CLI Test Mode

The tool supports a CLI mode that uses the exact same routing logic as the web UI. This is perfect for writing Go tests to verify alert routing behavior.

### Usage

```yaml
alertmanager-route-tester:
  server:
    enabled: false
  cli-test-mode:
    enabled: true # set true to run in CLI test mode instead of web server mode
    format: "json"
    labels:
      alertname: "HighCPU"
      severity: "critical"
```

### Example Output

Simple format:
```
Labels: map[alertname:HighCPU severity:critical]
Receiver: pagerduty-critical
Matched Routes:
  1. receiver=pagerduty-critical match=map[severity:critical]

Receiver Configuration:
  Name: pagerduty-critical
  Pagerduty Configs: 1
```

JSON format:
```json
{
  "receiver": "pagerduty-critical",
  "matched_routes": [
    {
      "route": {
        "receiver": "pagerduty-critical",
        "match": {
          "severity": "critical"
        }
      },
      "depth": 0,
      "is_subroute": false,
      "is_effective": true
    }
  ],
  "labels": {
    "alertname": "HighCPU",
    "severity": "critical"
  }
}
```

The `matched_routes` entries include the matched route under `route` plus nested-route metadata. This replaces the previous top-level `receiver` and `match` fields.

### Using CLI Mode in Go Tests

The CLI mode is designed to be used in Go tests to ensure 100% correct routing behavior. See `internal/cli/test_test.go` for comprehensive examples.

```go
package mytest

import (
	"testing"
	"github.com/wbollock/alertmanager-route-tester/internal/alertmanager"
	"github.com/wbollock/alertmanager-route-tester/internal/cli"
)

func TestCriticalAlertsAlwaysRouteToPagerDuty(t *testing.T) {
	client := alertmanager.NewClient("http://localhost:9093", false)
	
	labels := map[string]string{
		"alertname": "HighErrorRate",
		"severity":  "critical",
	}
	
	result, err := cli.TestRouting(client, labels)
	if err != nil {
		t.Fatalf("Failed to test routing: %v", err)
	}
	
	if result.Receiver != "pagerduty-critical" {
		t.Errorf("Expected pagerduty-critical, got %s", result.Receiver)
	}
}
```

### Test Categories

The example tests in `internal/cli/test_test.go` cover:

1. Critical alert routing - Ensures critical alerts go to PagerDuty
2. Team-based routing - Verifies team labels route to correct receivers
3. Complex routing with continue - Tests routes that match multiple rules
4. Default receiver fallback - Confirms unmatched alerts use default
5. Receiver configuration - Validates receiver configs are retrieved
6. Regex matching - Tests match_re patterns work correctly
