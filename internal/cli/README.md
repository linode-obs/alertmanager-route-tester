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

```bash
# Test a critical alert
go run . -alertmanager-url http://localhost:9093 -test \
  -labels '{"alertname":"HighCPU","severity":"critical"}'

# Output:
# Labels: map[alertname:HighCPU severity:critical]
# Receiver: pagerduty-critical
# Matched Routes:
#   1. receiver=pagerduty-critical match=map[severity:critical]

# Test with JSON output
go run . -alertmanager-url http://localhost:9093 -test \
  -labels '{"alertname":"HighCPU","severity":"critical"}' \
  -format json
```

## Key Features

1. **Exact Same Logic** - CLI mode uses identical routing logic as web UI
2. **Testable** - Perfect for Go tests to verify routing behavior
3. **Two Output Formats** - Simple (human-readable) and JSON (machine-readable)
4. **No Mocks** - Tests against real Alertmanager configuration
