AI-generated content prepared on Will's behalf.

# OpenTelemetry instrumentation design

## Goal

Instrument Alertmanager Route Tester with OpenTelemetry traces and metrics, while keeping telemetry safe for operational use and easy to configure in Kubernetes. The implementation follows issue #20 and uses compile-time `otelc` instrumentation for HTTP traffic.

## Design

Pin `otelc` v1.1.0 as a Go tool in `go.mod` and use its bundled HTTP instrumentation rules. Set `OTEL_GO_ENABLED_INSTRUMENTATIONS` to `nethttp` by default, while honoring an explicit environment override. Use the pinned tool in local, container, and CI build paths. Do not add `otelhttp` wrappers to the same paths, to avoid duplicate HTTP spans.

Use the OpenTelemetry API for application-specific instrumentation. Add focused spans around configuration fetches, retry attempts, cache refreshes, and route evaluation. Add metrics for HTTP request outcomes, Alertmanager failures, cache hits and misses, and routing outcomes. Metric dimensions use bounded categories only. Never record alert labels, receiver names, request IDs, or credential-bearing URLs as telemetry attributes or metric dimensions.

Set `service.name` to `alertmanager-route-tester` and `service.version` from build information by default. Honor standard `OTEL_*` settings for service identity, resources, sampling, and OTLP exporters. Do not bake a Collector endpoint into the app or chart.

Add a generic `extraEnv` option to the ATR Helm chart so operators can set standard OpenTelemetry variables, including values sourced from Kubernetes Secrets. Document a representative in-cluster Collector endpoint and a minimal Collector OTLP pipeline. The app chart will not deploy a Collector or assume the layout of `o11y-helm-charts`.

Ensure the telemetry providers and exporters are shut down within the existing graceful shutdown budget. Keep CLI behavior usable without requiring a Collector.

## Verification

- Test default service identity and standard environment configuration.
- Verify incoming HTTP and outgoing Alertmanager spans share a trace ID and have the expected parent-child relationship.
- Verify custom spans and metrics use the expected names and bounded attributes.
- Verify alert labels, receiver names, request IDs, and URL credentials are absent from exported telemetry.
- Render the Helm chart with `extraEnv`, including `valueFrom`, and assert the environment is present in the Deployment.
- Run Go tests, the full `mise run test` workflow, Helm chart validation, and the normal build.

## Scope

The integration changes this repository's Go build and ATR Helm chart. It does not edit the separate `~/repos/forks/o-h-c` repository. That chart can provide its own Collector endpoint and credentials through the generic Helm environment hook.
