{{- define "pulseboard.name" -}}
{{- .Chart.Name }}
{{- end }}

{{/*
Fully qualified name: <release>-pulseboard, or just the release name when it
already contains "pulseboard".
*/}}
{{- define "pulseboard.fullname" -}}
{{- if contains .Chart.Name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name .Chart.Name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}

{{- define "pulseboard.selectorLabels" -}}
app.kubernetes.io/name: {{ include "pulseboard.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "pulseboard.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{ include "pulseboard.selectorLabels" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
True when the chart creates the token Secret itself: a token value is given and
no existing Secret is named (an existing Secret always wins).
*/}}
{{- define "pulseboard.createSecret" -}}
{{- if and .Values.ingestToken (not .Values.existingSecret) }}true{{ end }}
{{- end }}

{{/* Name of the Secret holding the ingest token; fails the render if there is none. */}}
{{- define "pulseboard.tokenSecretName" -}}
{{- if .Values.existingSecret }}
{{- .Values.existingSecret }}
{{- else if .Values.ingestToken }}
{{- include "pulseboard.fullname" . }}
{{- else }}
{{- fail "pulseboard: set ingestToken or existingSecret (the ingest token is required)" }}
{{- end }}
{{- end }}

{{/* Key of the ingest token inside that Secret. */}}
{{- define "pulseboard.tokenSecretKey" -}}
{{- if .Values.existingSecret }}
{{- .Values.existingSecretKey }}
{{- else }}
{{- "ingest-token" }}
{{- end }}
{{- end }}
