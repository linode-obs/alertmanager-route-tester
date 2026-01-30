# Alertmanager Route Tester

Figure out where your alerts actually go.

## What

Test Prometheus alerts against your Alertmanager routing config without sending real alerts. Build alerts with a UI or paste JSON, see which receiver catches them.

## Quick Start

```bash
# Setup local Alertmanager for testing
mise run setup
mise run alertmanager  # separate terminal

# Run the tester
mise run dev
```

Visit http://localhost:8080

## Usage

Point it at any Alertmanager instance:

```bash
go run . -alertmanager-url http://your-alertmanager:9093

# Skip TLS verification if needed
go run . -alertmanager-url https://alertmanager:9093 -skip-tls-verify
```

Build your alert using the UI or paste JSON labels. Hit "Test Route" to see which receiver it matches and the full route path.

## Development

```bash
mise run test   # run tests
mise run build  # build binary
mise run clean  # cleanup
```

## How It Works

1. Fetches your Alertmanager config via API
2. Parses routing rules and matchers
3. Evaluates your alert labels against the route tree
4. Shows exactly which receiver and route path matched

Supports both exact matches and regex matchers, nested routes, and continue behavior.
