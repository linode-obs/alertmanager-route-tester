# Native Alertmanager route matching

This assessment covers issue #16 against Alertmanager v0.33.0, the version pinned in `.mise.toml`, CI, and the real-delivery parity test.

## Native behavior

`config.Load` parses and validates a complete Alertmanager configuration. The `config.Route` type supports legacy `match` and `match_re` fields, plus the current `matchers` field. Its matcher parser uses Alertmanager's compatibility parser. [Source: `config/config.go`](https://github.com/prometheus/alertmanager/blob/v0.33.0/config/config.go)

`dispatch.NewRoute` builds a route tree from `config.Route`. It merges all three matcher forms, compiles regular expressions, and inherits receiver and grouping options from parent routes. [Source: `dispatch/route.go`](https://github.com/prometheus/alertmanager/blob/v0.33.0/dispatch/route.go)

`(*dispatch.Route).Match` checks matchers at each route, then visits child routes from left to right. It stops at the first matching child without `continue`. It returns terminal matching routes, not the full ancestry path. [Source: `dispatch/route.go`](https://github.com/prometheus/alertmanager/blob/v0.33.0/dispatch/route.go)

Each route's matcher set requires every matcher to pass. Missing labels have an empty value. Regex matchers use whole-value matching. [Source: `pkg/labels/matcher.go`](https://github.com/prometheus/alertmanager/blob/v0.33.0/pkg/labels/matcher.go)

## Preserving ATR's trace

`dispatch.Route` exposes its children, `RouteOpts.Receiver`, and a traversal index. It does not expose its parent pointer. ATR cannot read ancestry directly from a returned match.

ATR can preserve its trace by pairing native and UI route nodes during a tree walk. Record each node's path. For each terminal node returned by `Match`, add its ancestors as trace-only entries and mark the terminal node effective. Use `RouteOpts.Receiver` for inherited receivers. Keep the current route order when building the trace.

The parity tests in `internal/alertmanager/parity_test.go` provide a real Alertmanager check for this mapping. Add trace assertions for nested routes and `continue` before replacing the current evaluator.

## Dependency and version impact

The application previously depended only on `gopkg.in/yaml.v3` for YAML parsing. The native route packages add Alertmanager and Prometheus common as direct dependencies. The updated module graph has 3 direct requirements, 83 indirect requirements, and 217 modules from `go list -m all`. Alertmanager v0.33.0 also declares server and integration dependencies. [Source: Alertmanager v0.33.0 `go.mod`](https://github.com/prometheus/alertmanager/blob/v0.33.0/go.mod)

`config.Load` uses YAML v2 and strict decoding. ATR still parses the Alertmanager status response with YAML v3. Using both parsers can create differences in accepted input. Alertmanager v0.33.0 declares Go 1.25.0, below the project's Go 1.27.1 version. The project pins the Go module and test binary to the same release. Run the parity tests on each version change.

I did not generate a vendor directory, so its disk size is unknown. Go vendoring would store source for the imported package graph, not the Alertmanager binary. It would not remove dependency update work.

## Recommendation

The native API offers the strongest match with Alertmanager's routing behavior. It adds a third-party dependency and uses YAML v2 internally. ATR can retain its trace by pairing native routes with the UI route tree and marking the returned terminal routes effective.

Use `config.Load` and `dispatch.NewRoute(...).Match(...)` as the routing source of truth. Keep the real-delivery parity tests as the upgrade check for Alertmanager v0.33.0 and later versions.
