AI-generated content prepared on Will's behalf.

# CLI Test Mode Examples

This directory contains examples of using the CLI test mode in Go tests.

## Running the Examples

Start Alertmanager first:
```bash
mise run start
```

Then run the tests:
```bash
go test -v ./internal/cli/...
```

## Test Files

- `simple_test.go` - Simple, focused examples showing exact routing behavior
- `test_test.go` - Comprehensive test suite covering all routing scenarios

## Quick CLI Usage

Enable test mode in `config.yaml`:

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

## YAML Test Suite

CLI mode can run named routing cases from the application YAML file:

```yaml
alertmanager-route-tester:
  server:
    enabled: false
  cli-test-mode:
    enabled: true
    format: simple
    suite:
      - name: critical alert
        labels:
          alertname: HighErrorRate
          severity: critical
        expected_receivers:
          - pagerduty-critical
      - name: monitoring warning
        labels:
          severity: warning
          team: monitoring
        expected_receivers:
          - monitoring-team
          - slack-warnings
```

Run the suite with `go tool otelc go run . -config config.yaml`. The command prints each case result and exits nonzero if a case fails. Receiver order does not affect comparisons.

## Key Features

1. Exact same logic - CLI mode uses identical routing logic as web UI
2. Testable - Perfect for Go tests to verify routing behavior
3. Two output formats - Simple (human-readable) and JSON (machine-readable)
4. No mocks - Tests against real Alertmanager configuration
