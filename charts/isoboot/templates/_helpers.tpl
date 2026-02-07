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
{{- $_ := required "subnet is required (CIDR format, e.g. 192.168.100.0/24)" .Values.subnet }}
{{- if not (regexMatch "^(25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)\\.(25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)\\.(25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)\\.(25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)/([0-9]|[12][0-9]|3[0-2])$" .Values.subnet) }}
{{- fail "subnet must be in CIDR format (e.g. 192.168.100.0/24)" }}
{{- end }}
{{- $httpPortStr := printf "%v" .Values.httpPort }}
{{- if not (regexMatch "^[0-9]+$" $httpPortStr) }}
{{- fail "httpPort must be a numeric value" }}
{{- end }}
{{- $httpPort := int $httpPortStr }}
{{- if or (lt $httpPort 1024) (gt $httpPort 65535) }}
{{- fail "httpPort must be an integer between 1024 and 65535" }}
{{- end }}
{{- $healthPortStr := printf "%v" .Values.healthPort }}
{{- if not (regexMatch "^[0-9]+$" $healthPortStr) }}
{{- fail "healthPort must be a numeric value" }}
{{- end }}
{{- $healthPort := int $healthPortStr }}
{{- if or (lt $healthPort 1024) (gt $healthPort 65535) }}
{{- fail "healthPort must be an integer between 1024 and 65535" }}
{{- end }}
{{- if and (ge $healthPort 10248) (le $healthPort 10260) }}
{{- fail "healthPort must not be in the Kubernetes reserved range 10248-10260" }}
{{- end }}
{{/* httpPort and healthPort may be equal — they bind to different IPs
     (httpPort on the subnet address, healthPort on 127.0.0.1). */}}
{{- end }}
