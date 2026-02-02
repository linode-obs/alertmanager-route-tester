# Building alertmanager-route-tester

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

- `main.go` - HTTP server and CLI flags
- `internal/alertmanager/` - API client and route matching engine
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

```bash
go run . -alertmanager-url https://your-alertmanager:9093 -skip-tls-verify
```
