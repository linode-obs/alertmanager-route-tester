AI-generated content prepared on Will's behalf.

# OpenTelemetry setup

The build uses `otelc` to add HTTP spans and metrics. The app adds spans and metrics for configuration fetches, retries, cache access, and route results.

The default `service.name` is `alertmanager-route-tester`. The app sets `service.version` from build information. Set `OTEL_SERVICE_NAME` to override the service name. Set `OTEL_RESOURCE_ATTRIBUTES` to add or override resource attributes.

The app uses OTLP exporters and standard `OTEL_*` variables. The default protocol is `http/protobuf`. Set `OTEL_EXPORTER_OTLP_ENDPOINT` to your Collector endpoint. For example, use `http://localhost:4318` for a local Collector.

If your Collector listens for OTLP/gRPC on port 4317, set `OTEL_EXPORTER_OTLP_PROTOCOL=grpc`.

## Common environment variables

| Variable | Use |
| --- | --- |
| `OTEL_SERVICE_NAME` | Set the service name. |
| `OTEL_RESOURCE_ATTRIBUTES` | Add resource attributes or override defaults such as `service.version`. |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | Set the Collector endpoint. The default is `http://localhost:4318`. |
| `OTEL_EXPORTER_OTLP_PROTOCOL` | Set `http/protobuf` (default) or `grpc`. |
| `OTEL_EXPORTER_OTLP_HEADERS` | Set OTLP request headers. |
| `OTEL_TRACES_EXPORTER` | Select the traces exporter. The default is `otlp`. Set `none` to disable it. |
| `OTEL_METRICS_EXPORTER` | Select the metrics exporter. The default is `otlp`. Set `none` to disable it. |
| `OTEL_PROPAGATORS` | Select context propagators. The default is `tracecontext,baggage`. |
| `OTEL_TRACES_SAMPLER` | Select a trace sampler. The default is `parentbased_always_on`. |
| `OTEL_TRACES_SAMPLER_ARG` | Set a sampler argument, such as a trace sampling ratio. |
| `OTEL_GO_ENABLED_INSTRUMENTATIONS` | Select `otelc` instrumentations. The default is `nethttp`. |
| `OTEL_SDK_DISABLED` | Set `true` to disable telemetry. |


The app does not add alert labels, receiver names, request IDs, or URL credentials to telemetry attributes. Provider shutdown exports pending data when the process exits. Export errors do not change a successful CLI result.

## Prometheus `/metrics`

The same HTTP listener serves Prometheus text format on `/metrics`. The series correspond to the existing counters: `http_server_requests_total`, `alertmanager_config_fetches_total`, `alertmanager_failures_total`, `alertmanager_cache_accesses_total`, `alertmanager_retries_total`, and `route_evaluations_total`.

The Helm chart ServiceMonitor is optional and off by default, so clusters without the Prometheus Operator CRD still install. Enable `serviceMonitor` and set its labels to the cluster Prometheus selector, for example `prometheus: o11y-apps`.

## Kubernetes Helm values

The ATR chart accepts standard environment variables through `extraEnv`. A parent chart such as `o11y-helm-charts` can set the Collector endpoint in its values overlay.

```yaml
extraEnv:
  - name: OTEL_SERVICE_NAME
    value: alertmanager-route-tester
  - name: OTEL_EXPORTER_OTLP_ENDPOINT
    value: http://otel-collector.observability.svc.cluster.local:4317
  - name: OTEL_EXPORTER_OTLP_PROTOCOL
    value: grpc
  - name: OTEL_EXPORTER_OTLP_HEADERS
    valueFrom:
      secretKeyRef:
        name: otel-credentials
        key: headers
```

Create the `otel-credentials` Secret outside the chart. Do not store credentials in Helm values.

## Collector example

This Collector accepts OTLP over gRPC and HTTP. The `debug` exporter prints telemetry to the Collector output. Replace it with a backend exporter for persistent storage.

```yaml
receivers:
  otlp:
    protocols:
      grpc:
        endpoint: 0.0.0.0:4317
      http:
        endpoint: 0.0.0.0:4318

processors:
  memory_limiter:
    check_interval: 1s
    limit_mib: 256
  batch:

exporters:
  debug:
    verbosity: basic

service:
  pipelines:
    traces:
      receivers: [otlp]
      processors: [memory_limiter, batch]
      exporters: [debug]
    metrics:
      receivers: [otlp]
      processors: [memory_limiter, batch]
      exporters: [debug]
```

## Build-time instrumentation

The repository pins `otelc` v1.1.0 as a Go tool in `go.mod`. Its bundled rules add HTTP client and server spans with trace context propagation.

The app enables only `nethttp` instrumentation by default. Set `OTEL_GO_ENABLED_INSTRUMENTATIONS` to choose a different set. The standard `OTEL_*` variables configure exporters and resource attributes.
