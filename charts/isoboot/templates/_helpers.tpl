{{/*
Chart name, truncated to 63 characters.
*/}}
{{- define "isoboot.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Fully qualified app name, truncated to 63 characters.
If release name contains chart name it will be used as a full name.
*/}}
{{- define "isoboot.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Common labels.
*/}}
{{- define "isoboot.labels" -}}
app.kubernetes.io/name: {{ include "isoboot.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels (immutable subset for matchLabels).
*/}}
{{- define "isoboot.selectorLabels" -}}
app.kubernetes.io/name: {{ include "isoboot.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Controller manager ServiceAccount name.
*/}}
{{- define "isoboot.serviceAccountName" -}}
{{- include "isoboot.fullname" . }}-controller-manager
{{- end }}

{{/*
Validate required values. Include this template to trigger errors
when required values are missing. Assigns to $_ so no output is
produced.
*/}}
{{- define "isoboot.validate" -}}
{{- $_ := required "nodeName is required" .Values.nodeName }}
{{- $_ := required "networkInterface is required" .Values.networkInterface }}
{{- if lt (int .Values.httpPort) 1024 }}
{{- fail "httpPort must be >= 1024 (container runs as non-root and drops all capabilities)" }}
{{- end }}
{{- end }}
