AI-generated content prepared on Will's behalf.

# Alertmanager Route Tester Helm chart

The chart deploys the web UI as a ClusterIP Service. Ingress is off unless `ingress.enabled` is true. Configure an authenticated ingress or another access control layer before exposing the UI. The UI has no built-in authentication and can display receiver configuration returned by Alertmanager.

The chart creates a default-deny ingress NetworkPolicy. `networkPolicy.enabled` defaults to true and `networkPolicy.ingress` defaults to an empty list, which denies all ingress. Set `networkPolicy.ingress` to allow access from trusted workloads. NetworkPolicy enforcement depends on the cluster's CNI. Probes GET `/healthz`, which does not call Alertmanager.

Allow Traefik by copying this rule. The policy port is the chart Service port (`service.port`, default 8080). The container listens on that same port. Do not set a second port, such as the Ingress entrypoint port.

```yaml
service:
  port: 8080

networkPolicy:
  enabled: true
  ingress:
    - from:
        - namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: traefik
          podSelector:
            matchLabels:
              app.kubernetes.io/name: traefik
      ports:
        - protocol: TCP
          port: 8080
```

When `ingress.enabled` and `networkPolicy.enabled` are both true, set `ingress.networkPolicy.namespace` or `ingress.networkPolicy.podLabels` (or both). The chart appends an allow rule for that peer on `service.port`, in addition to any `networkPolicy.ingress` entries. It does not guess a controller namespace. The render fails when both are empty so the default-deny policy cannot silently block the ingress controller.

## Ingress

The chart can render a Traefik `IngressRoute`, a cert-manager `Certificate`, and an optional Traefik `TLSOption`. Nothing is rendered until `ingress.enabled` is true. `extraObjects` remains available for other manifests.

```yaml
ingress:
  enabled: true
  hostname: route-tester.example.com
  entryPoints:
    - websecure
  tls:
    enabled: true
    # secretName defaults to <release fullname>-tls when empty
    issuer:
      name: letsencrypt-prod
      kind: ClusterIssuer
  mtls:
    enabled: true
    clientAuthType: RequireAndVerifyClientCert
    caSecretName: route-tester-client-ca
  networkPolicy:
    namespace: traefik
    podLabels:
      app.kubernetes.io/name: traefik
```

`ingress.hostname` is required when ingress is enabled. The route matches `Host(hostname)` and forwards to the Service on `service.port`. `ingress.entryPoints` defaults to `websecure`.

`ingress.tls.enabled` defaults to true. A Certificate is rendered only when ingress is enabled, so the default is unused while ingress is off. The chart requires `ingress.tls.issuer.name` when TLS is enabled. `ingress.tls.issuer.kind` defaults to `ClusterIssuer`. `ingress.tls.secretName` defaults to the release fullname plus `-tls`.

`ingress.mtls.enabled` defaults to false. When it is true, the chart requires `ingress.mtls.caSecretName`, renders a `TLSOption` with `clientAuthType` (default `RequireAndVerifyClientCert`), and references that option from the IngressRoute. Create the client CA Secret in the release namespace outside the chart. The Secret must contain the CA under `tls.ca` or `ca.crt`.

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

`alertmanagers` is a list. Helm replaces the whole list when you override it. The map form is no longer merged; replace the whole list.

```yaml
service:
  port: 8080

alertmanagers:
  - name: production
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
              kubernetes.io/metadata.name: traefik
          podSelector:
            matchLabels:
              app.kubernetes.io/name: traefik
      ports:
        - protocol: TCP
          port: 8080

imagePullSecrets:
  - name: ghcr-pull
```

Create the referenced TLS Secret and image-pull Secret outside the chart. Keep private-key bytes out of chart values, and configure `cert_file` and `key_file` as paths to the mounted Secret files. Do not put credentials in Alertmanager URLs. The application logs the configured URL, which would expose URL credentials in logs.

An empty `image.tag` uses `Chart.appVersion`. Set `image.tag` to override that tag. `image.pullPolicy` defaults to `IfNotPresent`. The next release must bump `Chart.yaml` `version` and `appVersion` to the git tag without the `v` prefix. The GHCR package may still require the `imagePullSecrets` entry above unless its visibility is public.

`Chart.appVersion` `0.1.1` matches the published image. That image does not serve `/healthz` or `/metrics`. Build an image from this commit, or set `image.tag` to the next release, before upgrading a deployment that still runs `0.1.1`.

Install with a values file containing the Alertmanager endpoint and permitted NetworkPolicy sources:

```bash
helm upgrade --install alertmanager-route-tester \
  ./helm/alertmanager-route-tester \
  --namespace alertmanager-route-tester \
  --create-namespace \
  -f values.yaml
```

The app listens on `0.0.0.0` and `service.port` (default 8080). That value sets both the process listen address and the container port. The Service `targetPort` stays the named port `http`. Configuration changes update a pod-template checksum and restart the Deployment.

Requests are set so the scheduler can place the pod. Limits are omitted so a namespace LimitRange can apply its own and so the chart does not throttle CPU.
