# Alertmanager Route Tester

Alertmanager Route Tester (ATR) is a web-app designed to help figure out where your Prometheus alerts will actually wind up given an Alertmanager configuration. It pulls Alertmanager's santizied configuration directly from an Alertmanager API and allows the user to see exactly what reciever(s) their alerts will route to.

## Usage

Point it at any Alertmanager instance:

```bash
go run . -alertmanager-url http://your-alertmanager:9093

# Skip TLS verification if needed
go run . -alertmanager-url https://alertmanager:9093 -skip-tls-verify
```

Build your alert using the UI or paste YAML Prometheus alert labels. Hit "Test Route" to see which receiver it matches and the full route path.

## Development

```bash
mise run # see all available mise tasks
mise run test   # run tests
mise run build  # build binary
mise run clean  # cleanup
```

### Local Development Environment

```bash
# locally, build and deploy ATR along with a local sample Alertmanager setup
mise run start
```

This command:

- Downloads and installs Alertmanager if needed
- Starts Alertmanager on http://localhost:9093
- Starts Route Tester on http://localhost:8080

## How It Works

1. Fetches your Alertmanager config via Alertmanager's API.
2. Parses routing rules and matchers.
3. Evaluates your alert labels against the route tree.
4. Shows exactly which receiver(s) and route path matched.

Supports both exact matches and regex matchers, nested routes, default root reciever, and continue behavior.

## Planned Features

The following features are planned for future releases:

### Configuration File Support

- **YAML Configuration**: Replace CLI arguments with a dedicated `config.yaml` file
- **TLS Settings**: Proper certificate configuration, custom CA support, mTLS authentication
- **Connection Options**: Timeouts, retry policies, connection pooling
- **Multiple Alertmanagers**: Support testing against multiple Alertmanager instances

### Offline Mode

- **Paste Config Support**: Allow users to paste their own `alertmanager.yml` configuration directly into the UI
- **Local Testing**: Test routing logic without connecting to a live Alertmanager instance
- **Config Validation**: Validate Alertmanager configurations before testing
- **Export/Import**: Save and load test configurations

### Enhanced UI

- **Configuration Editor**: Built-in syntax highlighting for Alertmanager configs
- **Bulk Testing**: Test multiple alerts at once
- **History**: Save and replay previous test scenarios
- **Export Results**: Generate reports of routing test results
- **Improved Multiple Receiver Display**: Clearer visualization when alerts match multiple receivers due to `continue: true` routes, showing the complete notification flow in an easier-to-understand format

## About This Project

Built with Claude (claude-sonnet-4.5). See [agents.md](agents.md) for development details and AI assistance information.

**Model:** Claude Sonnet 4.5 (github-copilot/claude-sonnet-4.5)  
**Skills Used:** None (standard library only)  
**Technologies:** Go 1.25, HTMX, mise
