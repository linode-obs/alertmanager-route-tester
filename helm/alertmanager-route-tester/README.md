AI-generated content prepared on Will's behalf.

# Alertmanager Route Tester Helm chart

The chart deploys the web UI as a ClusterIP Service. It does not create an Ingress. Configure an authenticated ingress or another access control layer before exposing the UI. The UI has no built-in authentication and can display receiver configuration returned by Alertmanager.

The chart creates a default-deny ingress NetworkPolicy. Set `networkPolicy.ingress` to allow access from trusted workloads. NetworkPolicy enforcement depends on the cluster's CNI. Probes GET `/healthz`, which does not call Alertmanager.

## OpenTelemetry

Use `extraEnv` to set standard OTLP variables. Use `valueFrom.secretKeyRef` for headers stored in a Kubernetes Secret.

```yaml
extraEnv:
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

The chart does not install a Collector. See [OpenTelemetry setup](../../docs/opentelemetry.md) for common OTEL variables and a Collector example.

`/metrics` is served on the application port in Prometheus text format. The ServiceMonitor is off by default so clusters without the Prometheus Operator CRD still install. Set `serviceMonitor.labels` to the labels the cluster Prometheus selects.

```yaml
serviceMonitor:
  enabled: true
  labels:
    prometheus: o11y-apps
```

Use `extraObjects` to render additional Kubernetes objects with this release. Each entry is templated, so object names can use the release name and chart helpers. Helm replaces lists when a later values file defines `extraObjects`, so overlays must supply the complete list. Use external secret controllers or encrypted Secret resources instead of storing plaintext credentials in values.

```yaml
extraObjects:
  - apiVersion: v1
    kind: ConfigMap
    metadata:
      name: '{{ include "alertmanager-route-tester.fullname" . }}-custom'
    data:
      example: value
```

For automatic rollout after a mounted TLS Secret changes, use a cluster-wide [Stakater Reloader](https://docs.stakater.com/reloader/1.4/reference/annotations.html) installation and add its annotation to the Deployment metadata:

```yaml
deploymentAnnotations:
  secret.reloader.stakater.com/reload: alertmanager-tls
```

The chart does not install Reloader. Without a Secret-reloader controller, restart the Deployment after rotating TLS Secret data.

```yaml
alertmanagers:
  production:
    url: https://alerts.example.com
    http:
      tls:
        ca_file: /etc/alertmanager/tls/ca.crt
        cert_file: /etc/alertmanager/tls/tls.crt
        key_file: /etc/alertmanager/tls/tls.key

extraVolumes:
  - name: alertmanager-tls
    secret:
      secretName: alertmanager-tls

extraVolumeMounts:
  - name: alertmanager-tls
    mountPath: /etc/alertmanager/tls
    readOnly: true

networkPolicy:
  enabled: true
  ingress:
    - from:
        - namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: trusted-tools
          podSelector:
            matchLabels:
              app.kubernetes.io/name: approved-proxy
      ports:
        - protocol: TCP
          port: 8080

imagePullSecrets:
  - name: ghcr-pull
```

Create the referenced TLS Secret and image-pull Secret outside the chart. Keep private-key bytes out of chart values, and configure `cert_file` and `key_file` as paths to the mounted Secret files. Do not put credentials in Alertmanager URLs. The application logs the configured URL, which would expose URL credentials in logs.

An empty `image.tag` uses `Chart.appVersion`. Set `image.tag` to override that tag. `image.pullPolicy` defaults to `IfNotPresent`. The next release must bump `Chart.yaml` `version` and `appVersion` to the git tag without the `v` prefix. The GHCR package may still require the `imagePullSecrets` entry above unless its visibility is public.

Install with a values file containing the Alertmanager endpoint and permitted NetworkPolicy sources:

```bash
helm upgrade --install alertmanager-route-tester \
  ./helm/alertmanager-route-tester \
  --namespace alertmanager-route-tester \
  --create-namespace \
  -f values.yaml
```

The app listens on port 8080 in the container. `service.port` controls the ClusterIP Service port. Configuration changes update a pod-template checksum and restart the Deployment. The chart sets CPU and memory requests and does not set resource limits.
