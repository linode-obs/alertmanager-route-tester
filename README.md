# Alertmanager Route Tester

Figure out where your alerts actually go.

## What

Test Prometheus alerts against your Alertmanager routing config without sending real alerts. Build alerts with a UI or paste JSON, see which receiver catches them.

## Quick Start

```bash
mise run start
```

This single command:
- Downloads and installs Alertmanager if needed
- Starts Alertmanager on http://localhost:9093
- Starts Route Tester on http://localhost:8080
- Press Ctrl+C to stop everything

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

## About This Project

Built with Claude (claude-sonnet-4.5) from the following prompt:

> create a new project using Go and HTMX that acts as an alertmanager route test. it'll hook into an existing prometheus alertmanager endpoint like curl -L -s http://prometheus1.sea1.linode.com:9999/prometheus/alertmanager/api/v2/status | jq .config (https://prometheus.io/docs/alerting/latest/overview/) and allow the user to test where an alert would end up in that config, what exact reciever it'll hit. for now use a test alertmanager instance to test it locally. make the alertmanager instance variable with TLS settings. have it run locally using mise actions like mise tasks to set it up. have the local UI be very similar to alertmanager and provide the user some starting alert labels. i want to build up the alert with various labels, maybe suggest some starting ones from the loaded alertmanager config. but users should be able to customize the alert either by clicking in a nice alertmanager-like UI or copy pasting in a raw prometheus alert block. update claude.md and agents.md (point claude to agents) with simple instructions for building this project. make the readme pithy but helpful. use conventional commits in all cases. the primary function of this app is to help users deduce where their alerts end up in an alertmanager config

**Model:** Claude Sonnet 4.5 (github-copilot/claude-sonnet-4.5)  
**Skills Used:** None (standard library only)  
**Technologies:** Go 1.25, HTMX, mise
