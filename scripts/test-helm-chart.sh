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
grep -Fq 'url: http://alertmanager:9093' <<<"$rendered"
grep -Fq '/etc/alertmanager-route-tester/config.yaml' <<<"$rendered"
grep -Fq 'ghcr.io/wbollock/alertmanager-route-tester:latest' <<<"$rendered"
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
grep -Fq 'tcpSocket:' <<<"$rendered"
if grep -Fq 'httpGet:' <<<"$rendered"; then
  echo "probes must not depend on Alertmanager HTTP responses" >&2
  exit 1
fi

if grep -Eq '^[[:space:]]+limits:' <<<"$rendered"; then
  echo "chart must not set resource limits" >&2
  exit 1
fi

checksum() {
  helm template alertmanager-route-tester "$chart" \
    --set-string "alertmanagers.default.url=$1" |
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

echo "Helm chart tests passed"
