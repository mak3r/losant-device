{{/*
Expand the name of the chart.
*/}}
{{- define "losant-device.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "losant-device.fullname" -}}
{{- default .Chart.Name .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "losant-device.labels" -}}
helm.sh/chart: {{ include "losant-device.name" . }}-{{ .Chart.Version }}
{{ include "losant-device.selectorLabels" . }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "losant-device.selectorLabels" -}}
app.kubernetes.io/name: {{ include "losant-device.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
ServiceAccount name for the controller
*/}}
{{- define "losant-device.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- include "losant-device.fullname" . }}-controller
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
GEA credentials secret name
*/}}
{{- define "losant-device.geaSecretName" -}}
{{- if .Values.gea.credentials.existingSecret }}
{{- .Values.gea.credentials.existingSecret }}
{{- else }}
{{- include "losant-device.fullname" . }}-gea-credentials
{{- end }}
{{- end }}
