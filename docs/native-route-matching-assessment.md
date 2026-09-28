# Native Alertmanager route matching

This assessment covers issue #16 against Alertmanager v0.27.0, the version in `.mise.toml` and the CI workflow.

## Native behavior

`config.Load` parses and validates a complete Alertmanager configuration. The `config.Route` type supports legacy `match` and `match_re` fields, plus the current `matchers` field. Its matcher parser uses Alertmanager's compatibility parser. [Source: `config/config.go`](https://github.com/prometheus/alertmanager/blob/v0.27.0/config/config.go)

`dispatch.NewRoute` builds a route tree from `config.Route`. It merges all three matcher forms, compiles regular expressions, and inherits receiver and grouping options from parent routes. [Source: `dispatch/route.go`](https://github.com/prometheus/alertmanager/blob/v0.27.0/dispatch/route.go)

`(*dispatch.Route).Match` checks matchers at each route, then visits child routes from left to right. It stops at the first matching child without `continue`. It returns terminal matching routes, not the full ancestry path. [Source: `dispatch/route.go`](https://github.com/prometheus/alertmanager/blob/v0.27.0/dispatch/route.go)

Each route's matcher set requires every matcher to pass. Missing labels have an empty value. Regex matchers use whole-value matching. [Source: `pkg/labels/matcher.go`](https://github.com/prometheus/alertmanager/blob/v0.27.0/pkg/labels/matcher.go)

## Preserving ATR's trace

`dispatch.Route` exposes its children and resolved `RouteOpts.Receiver`, but its parent pointer is private. ATR cannot read ancestry directly from a returned match.

ATR can preserve its trace by walking the native route tree once and recording each node's path and matching ATR config node. For each terminal node returned by `Match`, ATR can add its ancestors as trace-only entries and mark the terminal node effective. Use `RouteOpts.Receiver` for inherited receivers. Keep the current route order when building the trace.

The parity tests in `internal/alertmanager/parity_test.go` provide a real Alertmanager check for this mapping. Add trace assertions for nested routes and `continue` before replacing the current evaluator.

## Dependency and version impact

The application currently depends only on `gopkg.in/yaml.v3` for YAML parsing. Importing `config` and `dispatch` adds `github.com/prometheus/alertmanager` as an application dependency. Alertmanager v0.27.0's config package uses `gopkg.in/yaml.v2`, Prometheus common packages, and Alertmanager matcher packages. Its module also declares many server and integration dependencies. The exact dependency graph for these imports must be measured before adoption. [Source: Alertmanager v0.27.0 `go.mod`](https://github.com/prometheus/alertmanager/blob/v0.27.0/go.mod)

`config.Load` uses YAML v2 and strict decoding. ATR currently parses the Alertmanager status response with YAML v3. Using both parsers can create differences in accepted input. Alertmanager v0.27.0 declares Go 1.21, below the project's Go 1.27.1 version. Pin the Go module to the bundled Alertmanager release, then run the parity tests on each version change.

## Recommendation

The native API offers the strongest match with Alertmanager's routing behavior. It is not a drop-in replacement for ATR's trace, and it adds a large dependency boundary that conflicts with the current standard-library and YAML v3 constraint.

Keep the current evaluator and the real-delivery parity tests unless Will approves that dependency change. If approved, use `config.Load` and `dispatch.NewRoute(...).Match(...)` as the routing source of truth, then build the trace from the returned terminal routes and their recorded paths.
