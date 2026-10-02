{{- define "alertmanager-route-tester.name" -}}
{{- .Chart.Name | trunc 63 | trimSuffix "-" -}}
{{- end }}

{{- define "alertmanager-route-tester.fullname" -}}
{{- if contains (include "alertmanager-route-tester.name" .) .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name (include "alertmanager-route-tester.name" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end }}

{{- define "alertmanager-route-tester.labels" -}}
helm.sh/chart: {{ .Chart.Name }}-{{ .Chart.Version | replace "+" "_" }}
app.kubernetes.io/name: {{ include "alertmanager-route-tester.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{- define "alertmanager-route-tester.selectorLabels" -}}
app.kubernetes.io/name: {{ include "alertmanager-route-tester.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}
