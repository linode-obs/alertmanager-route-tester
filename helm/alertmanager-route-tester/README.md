AI-generated content prepared on Will's behalf.

# Alertmanager Route Tester Helm chart

The chart deploys the web UI as a ClusterIP Service. `ingress` is off by default and renders a standard Kubernetes `Ingress` (not a Traefik CRD) only when enabled. Configure an authenticated access layer before exposing the UI. The UI has no built-in authentication and can display receiver configuration returned by Alertmanager.

The chart creates a default-deny ingress NetworkPolicy. `networkPolicy.enabled` defaults to true and `networkPolicy.ingress` defaults to an empty list, which denies all ingress. Set `networkPolicy.ingress` to allow access from trusted workloads. NetworkPolicy enforcement depends on the cluster's CNI. Startup, readiness, and liveness probes GET `/healthz`, which does not call Alertmanager. The image must serve that path.

Allow Traefik by copying this rule. The policy port is the chart Service port (`service.port`, default 8080). The container listens on that same port. Do not set a second port, such as the Traefik entrypoint port.

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

When `ingress.enabled` and `networkPolicy.enabled` are both true, set `ingress.networkPolicy.namespace`, `ingress.networkPolicy.podLabels`, or both. The chart appends an allow rule for that peer on `service.port`, in addition to any `networkPolicy.ingress` entries. It does not guess a controller namespace. `podLabels` alone matches those labels in every namespace. A pod selector without a namespace selector would only see the release namespace, so a controller running elsewhere would stay blocked. Set `namespace` to limit the match. The render fails when both are empty so the default-deny policy cannot silently block Traefik.

## Ingress

`ingress` renders a plain Kubernetes `Ingress` with no controller- or cert-manager-specific logic baked in: the chart does not generate, infer, or merge any annotations, and it does not render a `Certificate` or any Traefik CRD (such as a `TLSOption`). Everything controller-specific is supplied through `ingress.annotations`, which is rendered as-is.

Nothing is rendered until `ingress.enabled` is true.

```yaml
ingress:
  enabled: true
  className: traefik
  hostname: route-tester.example.com
  annotations:
    cert-manager.io/cluster-issuer: letsencrypt-prod
    traefik.ingress.kubernetes.io/router.entrypoints: websecure
    traefik.ingress.kubernetes.io/router.tls: "true"
  tls:
    enabled: true
    # secretName defaults to <release fullname>-tls when empty
  networkPolicy:
    namespace: traefik
    podLabels:
      app.kubernetes.io/name: traefik
```

`ingress.hostname` is required when the Ingress is enabled. The rule matches that host and forwards to the Service on `service.port`. `ingress.className` defaults to `traefik` and sets `spec.ingressClassName`; override it for another controller.

`ingress.tls.enabled` defaults to true and renders the Ingress `spec.tls` block (host plus `ingress.tls.secretName`, which defaults to the release fullname plus `-tls`). It does not add any TLS-related annotations itself: for example, cert-manager only issues a Certificate for annotated Ingress objects, and Traefik only terminates TLS on a route when told to via its own annotation, so add whatever your controller and/or cert-manager require to `ingress.annotations` (as in the example above).

For mTLS (or any other feature that needs a separate CRD object, such as Traefik's `TLSOption`), create that object yourself via `extraObjects` (see below) and reference it from `ingress.annotations`. For example, with Traefik:

```yaml
ingress:
  enabled: true
  hostname: route-tester.example.com
  annotations:
    cert-manager.io/cluster-issuer: letsencrypt-prod
    traefik.ingress.kubernetes.io/router.entrypoints: websecure
    traefik.ingress.kubernetes.io/router.tls: "true"
    # safeNaming defaults to false on Traefik's kubernetesCRD provider, so cross-namespace
    # @kubernetescrd references from an Ingress use <namespace>-<name>@kubernetescrd unless the
    # cluster operator has explicitly enabled safeNaming (<namespace>_<name>@kubernetescrd).
    traefik.ingress.kubernetes.io/router.tls.options: default-route-tester-mtls@kubernetescrd

extraObjects:
  - apiVersion: traefik.io/v1alpha1
    kind: TLSOption
    metadata:
      name: '{{ include "alertmanager-route-tester.fullname" . }}-mtls'
    spec:
      clientAuth:
        secretNames:
          - route-tester-client-ca
        clientAuthType: RequireAndVerifyClientCert
```

Create the client CA Secret (`route-tester-client-ca` above) in the release namespace outside the chart. The Secret must contain the CA under `tls.ca` or `ca.crt`.


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

`/metrics` is served on the application port in Prometheus text format. The ServiceMonitor is off by default so clusters without the Prometheus Operator CRD still install. Set `serviceMonitor.labels` to the labels the cluster Prometheus selects. Enabling the ServiceMonitor does not change the NetworkPolicy. The default policy denies all ingress, so scrapes fail until `networkPolicy.ingress` allows the Prometheus pods on `service.port`.

```yaml
serviceMonitor:
  enabled: true
  labels:
    prometheus: o11y-apps

networkPolicy:
  enabled: true
  ingress:
    - from:
        - namespaceSelector:
            matchLabels:
              kubernetes.io/metadata.name: monitoring
          podSelector:
            matchLabels:
              app.kubernetes.io/name: prometheus
      ports:
        - protocol: TCP
          port: 8080
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

Install with a values file containing the Alertmanager endpoint and permitted NetworkPolicy sources:

```bash
helm upgrade --install alertmanager-route-tester \
  ./helm/alertmanager-route-tester \
  --namespace alertmanager-route-tester \
  --create-namespace \
  -f values.yaml
```

The app listens on `0.0.0.0` and `service.port` (default 8080). That value sets the process listen address, the container port, and the Service port. The Service `targetPort` stays the named port `http`. `service.port` must be from 1024 through 65535. The container runs as UID 65532 and drops all capabilities, so rendering fails for a privileged port. Configuration changes update a pod-template checksum and restart the Deployment.

Requests are set so the scheduler can place the pod. Limits are omitted so a namespace LimitRange can apply its own and so the chart does not throttle CPU.
