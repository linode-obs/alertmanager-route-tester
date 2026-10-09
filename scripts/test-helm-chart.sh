#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
chart="$root/helm/alertmanager-route-tester"

if ! grep -Fq 'needs: goreleaser' "$root/.github/workflows/release.yaml"; then
  echo "container publishing must wait for release validation" >&2
  exit 1
fi

if ! grep -Fq 'platforms: linux/amd64,linux/arm64' "$root/.github/workflows/release.yaml"; then
  echo "container publishing must build for linux/amd64 and linux/arm64" >&2
  exit 1
fi

helm lint "$chart"
rendered=$(helm template alertmanager-route-tester "$chart")

grep -Fq 'listen: "0.0.0.0:8080"' <<<"$rendered"
grep -Fq 'containerPort: 8080' <<<"$rendered"
grep -Fq 'url: http://alertmanager:9093' <<<"$rendered"
if grep -Fq 'kind: IngressRoute' <<<"$rendered"; then
  echo "default render must not create an IngressRoute" >&2
  exit 1
fi
if grep -Fq 'kind: Certificate' <<<"$rendered"; then
  echo "default render must not create a Certificate" >&2
  exit 1
fi

port_override=$(helm template alertmanager-route-tester "$chart" --set service.port=9090)
grep -Fq 'listen: "0.0.0.0:9090"' <<<"$port_override"
grep -Fq 'containerPort: 9090' <<<"$port_override"
grep -Fq 'targetPort: http' <<<"$port_override"
grep -Fq '/etc/alertmanager-route-tester/config.yaml' <<<"$rendered"
grep -Fq 'ghcr.io/linode-obs/alertmanager-route-tester:0.1.1' <<<"$rendered"
with_image_tag=$(helm template alertmanager-route-tester "$chart" --set image.tag=custom)
grep -Fq 'ghcr.io/linode-obs/alertmanager-route-tester:custom' <<<"$with_image_tag"
if grep -Fq 'ghcr.io/linode-obs/alertmanager-route-tester:0.1.1' <<<"$with_image_tag"; then
  echo "image.tag must override Chart.appVersion" >&2
  exit 1
fi
grep -Fq 'kind: NetworkPolicy' <<<"$rendered"
grep -Fq 'policyTypes:' <<<"$rendered"
grep -Fq '    []' <<<"$rendered"
grep -Fq 'alertmanagers:' <<<"$rendered"
grep -Fq 'targetPort: http' <<<"$rendered"
if grep -Fq 'name: alertmanager-route-tester-alertmanager-route-tester' <<<"$rendered"; then
  echo "release name must not be duplicated in resource names" >&2
  exit 1
fi
grep -Fq 'automountServiceAccountToken: false' <<<"$rendered"
grep -Fq 'runAsNonRoot: true' <<<"$rendered"
grep -Fq 'readOnlyRootFilesystem: true' <<<"$rendered"
grep -Fq 'allowPrivilegeEscalation: false' <<<"$rendered"
grep -Fq 'type: RuntimeDefault' <<<"$rendered"
grep -Fq 'httpGet:' <<<"$rendered"
grep -Fq 'path: /healthz' <<<"$rendered"
if grep -Eq 'path: /($|[[:space:]])' <<<"$rendered"; then
  echo "probes must not use path /" >&2
  exit 1
fi
if grep -Fq 'path: /test' <<<"$rendered"; then
  echo "probes must not use path /test" >&2
  exit 1
fi
if grep -Fq 'tcpSocket:' <<<"$rendered"; then
  echo "probes must use httpGet /healthz, not tcpSocket" >&2
  exit 1
fi

if grep -Eq '^[[:space:]]+limits:' <<<"$rendered"; then
  echo "chart must not set resource limits" >&2
  exit 1
fi

checksum() {
  helm template alertmanager-route-tester "$chart" \
    --set-string "alertmanagers[0].name=default" \
    --set-string "alertmanagers[0].url=$1" |
    awk -F': ' '/checksum\/config:/ {gsub(/"/, "", $2); print $2; exit}'
}

first_checksum=$(checksum http://alertmanager-one:9093)
second_checksum=$(checksum http://alertmanager-two:9093)
if [[ -z "$first_checksum" || "$first_checksum" == "$second_checksum" ]]; then
  echo "changing the Alertmanager config must change the pod-template checksum" >&2
  exit 1
fi

without_network_policy=$(helm template alertmanager-route-tester "$chart" \
  --set networkPolicy.enabled=false)
if grep -Fq 'kind: NetworkPolicy' <<<"$without_network_policy"; then
  echo "disabled NetworkPolicy must not render" >&2
  exit 1
fi

with_pull_secret=$(helm template alertmanager-route-tester "$chart" \
  --set imagePullSecrets[0].name=ghcr-pull)
grep -Fq 'name: ghcr-pull' <<<"$with_pull_secret"

with_otel_env=$(helm template alertmanager-route-tester "$chart" \
  --set-json 'extraEnv=[{"name":"OTEL_EXPORTER_OTLP_ENDPOINT","value":"http://otel-collector.observability.svc.cluster.local:4317"},{"name":"OTEL_EXPORTER_OTLP_HEADERS","valueFrom":{"secretKeyRef":{"name":"otel-credentials","key":"headers"}}}]')
grep -Fq 'name: OTEL_EXPORTER_OTLP_ENDPOINT' <<<"$with_otel_env"
grep -Fq 'http://otel-collector.observability.svc.cluster.local:4317' <<<"$with_otel_env"
grep -Fq 'name: OTEL_EXPORTER_OTLP_HEADERS' <<<"$with_otel_env"
grep -Fq 'secretKeyRef:' <<<"$with_otel_env"
grep -Fq 'name: otel-credentials' <<<"$with_otel_env"

with_tls_secret=$(helm template alertmanager-route-tester "$chart" \
  --set extraVolumes[0].name=alertmanager-tls \
  --set extraVolumes[0].secret.secretName=alertmanager-tls \
  --set extraVolumeMounts[0].name=alertmanager-tls \
  --set extraVolumeMounts[0].mountPath=/etc/alertmanager/tls \
  --set extraVolumeMounts[0].readOnly=true)
grep -Fq 'secretName: alertmanager-tls' <<<"$with_tls_secret"
grep -Fq 'mountPath: /etc/alertmanager/tls' <<<"$with_tls_secret"

production_only=$(helm template alertmanager-route-tester "$chart" \
  --set-json 'alertmanagers=[{"name":"production","url":"https://alerts.example.com","matcher_mode":"utf8-strict","http":{"tls":{"ca_file":"/etc/alertmanager/tls/ca.crt","cert_file":"/etc/alertmanager/tls/tls.crt","key_file":"/etc/alertmanager/tls/tls.key"}},"retry":{"max_attempts":5},"pool":{"max_idle_conns":20}}]')
grep -Fq 'url: https://alerts.example.com' <<<"$production_only"
grep -Fq 'matcher_mode: utf8-strict' <<<"$production_only"
grep -Fq 'ca_file: /etc/alertmanager/tls/ca.crt' <<<"$production_only"
grep -Fq 'max_attempts: 5' <<<"$production_only"
grep -Fq 'max_idle_conns: 20' <<<"$production_only"
if grep -Fq 'url: http://alertmanager:9093' <<<"$production_only"; then
  echo "user alertmanagers list must replace the chart default" >&2
  exit 1
fi
if grep -Eq '^[[:space:]]+default:' <<<"$production_only"; then
  echo "default alertmanager must not remain when the user list omits it" >&2
  exit 1
fi

if helm template alertmanager-route-tester "$chart" --set-json 'alertmanagers=[]' >/dev/null 2>&1; then
  echo "empty alertmanagers list must fail" >&2
  exit 1
fi
if helm template alertmanager-route-tester "$chart" --set-json 'alertmanagers=[{"url":"http://alertmanager:9093"}]' >/dev/null 2>&1; then
  echo "alertmanagers entry without a name must fail" >&2
  exit 1
fi
if helm template alertmanager-route-tester "$chart" --set-json 'alertmanagers=[{"name":"default"}]' >/dev/null 2>&1; then
  echo "alertmanagers entry without a url must fail" >&2
  exit 1
fi

with_ingress=$(helm template alertmanager-route-tester "$chart" \
  --set ingress.enabled=true \
  --set ingress.hostname=route-tester.example.com \
  --set ingress.tls.issuer.name=letsencrypt-prod \
  --set ingress.networkPolicy.namespace=traefik)
grep -Fq 'kind: IngressRoute' <<<"$with_ingress"
grep -Fq 'kind: Certificate' <<<"$with_ingress"
grep -Fq 'kind: NetworkPolicy' <<<"$with_ingress"
grep -Fq 'route-tester.example.com' <<<"$with_ingress"
grep -Fq 'letsencrypt-prod' <<<"$with_ingress"
grep -Fq 'kind: ClusterIssuer' <<<"$with_ingress"
grep -Fq 'secretName: alertmanager-route-tester-tls' <<<"$with_ingress"
grep -Fq 'kubernetes.io/metadata.name: traefik' <<<"$with_ingress" || grep -Fq 'kubernetes.io/metadata.name: "traefik"' <<<"$with_ingress"

with_mtls=$(helm template alertmanager-route-tester "$chart" \
  --set ingress.enabled=true \
  --set ingress.hostname=route-tester.example.com \
  --set ingress.tls.issuer.name=letsencrypt-prod \
  --set ingress.networkPolicy.namespace=traefik \
  --set ingress.mtls.enabled=true \
  --set ingress.mtls.caSecretName=route-tester-client-ca)
grep -Fq 'kind: TLSOption' <<<"$with_mtls"
grep -Fq 'route-tester-client-ca' <<<"$with_mtls"
grep -Fq 'RequireAndVerifyClientCert' <<<"$with_mtls"

with_ingress_rules=$(helm template alertmanager-route-tester "$chart" \
  --set ingress.enabled=true \
  --set ingress.hostname=route-tester.example.com \
  --set ingress.tls.issuer.name=letsencrypt-prod \
  --set ingress.networkPolicy.namespace=traefik \
  --set-json 'networkPolicy.ingress=[{"from":[{"podSelector":{"matchLabels":{"app":"approved-proxy"}}}],"ports":[{"protocol":"TCP","port":8080}]}]')
grep -Fq 'approved-proxy' <<<"$with_ingress_rules"
grep -Fq 'kubernetes.io/metadata.name: traefik' <<<"$with_ingress_rules" || grep -Fq 'kubernetes.io/metadata.name: "traefik"' <<<"$with_ingress_rules"

if helm template alertmanager-route-tester "$chart" \
  --set ingress.enabled=true \
  --set ingress.tls.issuer.name=letsencrypt-prod \
  --set ingress.networkPolicy.namespace=traefik >/dev/null 2>&1; then
  echo "ingress without a hostname must fail helm template" >&2
  exit 1
fi

if helm template alertmanager-route-tester "$chart" \
  --set ingress.enabled=true \
  --set-string 'ingress.hostname=bad`host.example.com' \
  --set ingress.tls.issuer.name=letsencrypt-prod \
  --set ingress.networkPolicy.namespace=traefik >/dev/null 2>&1; then
  echo "ingress hostname with a backtick must fail helm template" >&2
  exit 1
fi

if grep -Fq 'source: extra-object-test' <<<"$rendered"; then
  echo "empty extraObjects must not render resources" >&2
  exit 1
fi

with_extra_object=$(helm template alertmanager-route-tester "$chart" \
  --set-json 'extraObjects=[{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"{{ .Release.Name }}-extra"},"data":{"source":"extra-object-test"}}]')
grep -Fq 'alertmanager-route-tester-extra' <<<"$with_extra_object"
grep -Fq 'source: extra-object-test' <<<"$with_extra_object"

with_reloader_annotation=$(helm template alertmanager-route-tester "$chart" \
  --set-json 'deploymentAnnotations={"secret.reloader.stakater.com/reload":"alertmanager-tls"}')
deployment=$(awk '
  /^# Source: alertmanager-route-tester\/templates\/deployment.yaml$/ { in_deployment=1; next }
  in_deployment && /^---$/ { exit }
  in_deployment { print }
' <<<"$with_reloader_annotation")
deployment_metadata=$(awk '/^spec:$/ { exit } { print }' <<<"$deployment")
grep -Eq '^  annotations:$' <<<"$deployment_metadata"
grep -Fq '    secret.reloader.stakater.com/reload: alertmanager-tls' <<<"$deployment_metadata"

if grep -Fq 'kind: ServiceMonitor' <<<"$rendered"; then
  echo "default chart render must not include a ServiceMonitor" >&2
  exit 1
fi

with_service_monitor=$(helm template alertmanager-route-tester "$chart" \
  --set serviceMonitor.enabled=true \
  --set serviceMonitor.labels.prometheus=o11y-apps)
grep -Fq 'kind: ServiceMonitor' <<<"$with_service_monitor"
grep -Fq '/metrics' <<<"$with_service_monitor"
grep -Fq 'prometheus: o11y-apps' <<<"$with_service_monitor"

echo "Helm chart tests passed"
