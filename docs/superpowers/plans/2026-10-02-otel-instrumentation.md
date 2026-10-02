AI-generated content prepared on Will's behalf.

# OpenTelemetry instrumentation implementation plan

> **For agentic workers:** Implement this plan inline, one task at a time, using test-first cycles and verifying each task before proceeding.

**Goal:** Add OpenTelemetry tracing and metrics for Alertmanager Route Tester with standard runtime configuration and Helm/Kubernetes environment injection.

**Architecture:** Pin OpenTelemetry Go Compile-Time Instrumentation (`otelc`) v1.1.0 and use it for `net/http` spans and trace propagation. Use the OpenTelemetry API and SDK for app-specific spans, bounded metrics, resource identity, OTLP exporters, and shutdown. Add a generic `extraEnv` hook to the existing ATR Helm chart.

**Tech Stack:** Go 1.27.1, OpenTelemetry Go SDK and `autoexport`, `otelc` v1.1.0, Helm 3.

## Global Constraints

- Instrument incoming `net/http` handlers and outbound Alertmanager calls with `otelc`; do not overlap those paths with `otelhttp` wrappers.
- Pin the stable `otelc` release in `go.mod` and enable only `nethttp` instrumentation by default.
- Set `service.name` and `service.version`; honor standard `OTEL_*` configuration.
- Never emit alert labels, receiver names, request IDs, or credential-bearing URLs as telemetry attributes or metric dimensions.
- Keep metric dimensions to bounded categories.
- Shut down providers and exporters within the server shutdown budget; CLI mode must not require a Collector.
- Keep the ATR Helm chart Collector-agnostic. Pass OTEL variables through generic `extraEnv`, including Kubernetes `valueFrom` references.
- Keep the existing local `.gitignore` addition for `config.prod.local.yaml` in the final change.
- Make one final Conventional Commit only after tests pass, with the required AI disclosure in the commit body.

---

### Task 1: Build telemetry providers

**Files:**
- Create: `internal/telemetry/telemetry.go`
- Create: `internal/telemetry/telemetry_test.go`
- Create: `internal/telemetry/sdk_lifecycle_test.go`
- Create: `internal/telemetry/shutdown_test.go`
- Modify: `main.go`
- Modify: `main_test.go`
- Modify: `go.mod`, `go.sum`

**Interfaces:**
- `telemetry.Setup(ctx context.Context, version string) (*telemetry.Providers, error)` configures global tracer and meter providers from standard OTel environment variables, with default service name `alertmanager-route-tester` and the supplied build version.
- `(*telemetry.Providers).Shutdown(ctx context.Context) error` shuts down all owned providers and joins shutdown errors.

- [x] **Step 1: Write tests for default service identity, resource environment overrides, and provider shutdown. Use resource attributes and a local OTLP HTTP server to assert default identity and span flush.**
- [x] **Step 2: Run `go test ./internal/telemetry` and confirm the tests fail because the setup API does not exist.**
- [x] **Step 3: Suppress otelc's injected SDK initializer before `main`, restore the original `OTEL_SDK_DISABLED` setting in `Setup`, and configure one app-owned provider pipeline from standard OTEL variables.**
- [x] **Step 4: Add setup to app startup and shut providers down after successful CLI completion or graceful server stop. Keep the existing signal-driven server shutdown behavior.**
- [x] **Step 5: Run `go test ./internal/telemetry ./...` and confirm they pass.**

### Task 2: Instrument config fetch, retry, cache, and routing

**Files:**
- Create: `internal/telemetry/metrics.go`
- Create: `internal/telemetry/metrics_test.go`
- Create: `internal/telemetry/http.go`
- Create: `internal/telemetry/http_test.go`
- Modify: `internal/alertmanager/client.go`
- Modify: `internal/handler/handler.go`
- Modify: `internal/cli/test.go`
- Modify: `main.go`

**Interfaces:**
- `telemetry.MetricsHandler(next http.Handler) http.Handler` records bounded HTTP method and status-class outcomes.
- `telemetry.RecordConfigFetch`, `telemetry.RecordRetry`, `telemetry.RecordCache`, and `telemetry.RecordRoute` record bounded operation outcomes from the Alertmanager and handler packages.
- `telemetry.StartSpan(ctx, name)` returns a child context and span for app-specific operations.
- Instrumentation records only operation and outcome categories, never dynamic labels, receiver names, instance names, URLs, or error strings.

- [x] **Step 1: Write tests that exercise real config cache hits/misses, failed Alertmanager fetches, retries, and route results, then collect exported spans and metric data to assert names and permitted attributes.**
- [x] **Step 2: Run focused telemetry and Alertmanager tests and confirm the new assertions fail before instrumentation exists.**
- [x] **Step 3: Add spans around config fetches, retry attempts, cache refreshes, and route evaluation. Set error status without recording raw error text.**
- [x] **Step 4: Add counters for HTTP status classes, fetch outcomes, cache hits/misses, retry attempts, and route outcomes using fixed-category attributes.**
- [x] **Step 5: Re-run focused tests and `go test ./internal/alertmanager ./internal/handler ./internal/cli`.**

### Task 3: Pin and exercise compile-time HTTP instrumentation

**Files:**
- Modify: `go.mod`, `go.sum`
- Create: `internal/telemetry/http_integration_test.go`
- Modify: `.mise.toml`
- Modify: `AGENTS.md`
- Modify: `README.md`
- Modify: `internal/cli/README.md`
- Create: `scripts/otelc-go`
- Modify: `Dockerfile`
- Modify: `.dockerignore`
- Modify: `.github/workflows/go-test.yaml`
- Modify: `.github/workflows/release.yaml`
- Modify: `.goreleaser.yaml`

**Interfaces:**
- All shipped binaries use `otelc` v1.1.0 and its embedded instrumentation rules.
- The integration test reads an instrumented app path from `ATR_OTEL_TEST_BINARY`, runs a real request, and receives exported OTLP from a local HTTP server.

- [x] **Step 1: Keep `otelc` v1.1.0 pinned as a Go tool and default `OTEL_GO_ENABLED_INSTRUMENTATIONS` to `nethttp` without overriding an explicit selection.**
- [x] **Step 2: Add an end-to-end test that launches an app binary, posts alert labels through the real HTTP handler, captures OTLP, and rejects sensitive values.**
- [x] **Step 3: Build the app with plain `go build`, run the end-to-end test, and confirm it fails because HTTP spans are missing.**
- [x] **Step 4: Route local build, development, test, Docker, CI, and GoReleaser builds through `otelc`; keep cross-platform build flags and release metadata intact.**
- [x] **Step 5: Build the app with `go tool otelc go build` and re-run the end-to-end test.**

### Task 4: Expose Collector configuration in Helm and docs

**Files:**
- Modify: `helm/alertmanager-route-tester/values.yaml`
- Modify: `helm/alertmanager-route-tester/templates/deployment.yaml`
- Modify: `helm/alertmanager-route-tester/README.md`
- Modify: `scripts/test-helm-chart.sh`
- Create: `docs/opentelemetry.md`
- Modify: `README.md`
- Modify: `.gitignore`

**Interfaces:**
- `.Values.extraEnv` is a list of Kubernetes container environment variable entries rendered under the app container and supports `valueFrom`.
- `docs/opentelemetry.md` documents default identity, supported standard environment variables, sensitive-data exclusions, shutdown, and an OTLP Collector example.

- [x] **Step 1: Add Helm assertions for `extraEnv` values and a `valueFrom.secretKeyRef` entry; run the chart script and confirm it fails because the env block is absent.**
- [x] **Step 2: Add the generic `extraEnv` value in `values.yaml` and render it under the app container's `env` field.**
- [x] **Step 3: Document a Kubernetes Collector endpoint example and a minimal Collector OTLP traces/metrics pipeline without embedding credentials.**
- [x] **Step 4: Ignore `config.prod.local.yaml`, `.otelc-build/`, `.otelc-build.lock`, and generated `otelc.runtime.go` while preserving existing entries.**
- [x] **Step 5: Run `bash scripts/test-helm-chart.sh`, inspect rendered Helm output, and run Markdown checks available in the repository.**

### Task 5: Full verification and commit

**Files:**
- Verify all changed files and the complete worktree diff.

- [x] **Step 1: Run `ATR_OTEL_TEST_BINARY=bin/alertmanager-route-tester go test -race ./...`. The suite skips live Alertmanager parity tests when the pinned binary is unavailable.**
- [x] **Step 2: Run `mise run build`, the Helm chart test script, and a Docker build for at least the native architecture.**
- [x] **Step 3: Review the diff for telemetry data leaks, unbounded metric dimensions, uninstrumented build paths, and unrelated changes.**
- [ ] **Step 4: Run `git status` and stage only the reviewed task files.**
- [ ] **Step 5: Create one Conventional Commit with the subject `feat(otel): add OpenTelemetry instrumentation` and the disclosure line `AI-generated content prepared on Will's behalf.` in its body.**
