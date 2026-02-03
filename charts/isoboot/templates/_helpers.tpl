{{/*
Expand the name of the chart.
*/}}
{{- define "isoboot.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
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
Create chart name and version as used by the chart label.
*/}}
{{- define "isoboot.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "isoboot.labels" -}}
helm.sh/chart: {{ include "isoboot.chart" . }}
{{ include "isoboot.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "isoboot.selectorLabels" -}}
app.kubernetes.io/name: {{ include "isoboot.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "isoboot.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "isoboot.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- required "serviceAccount.name is required when serviceAccount.create is false" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Create the image reference
*/}}
{{- define "isoboot.image" -}}
{{- if not .Values.image.repository }}
{{- fail "image.repository is required" }}
{{- end }}
{{- $tag := default .Chart.AppVersion .Values.image.tag }}
{{- printf "%s:%s" .Values.image.repository $tag }}
{{- end }}

{{/*
Validate image pull policy
*/}}
{{- define "isoboot.validatePullPolicy" -}}
{{- $validPolicies := list "Always" "IfNotPresent" "Never" }}
{{- if not (has .Values.image.pullPolicy $validPolicies) }}
{{- fail "image.pullPolicy must be one of: Always, IfNotPresent, Never" }}
{{- end }}
{{- end }}

{{/*
Validate replica count with leader election
Running multiple replicas without leader election causes concurrent reconciliation conflicts.
*/}}
{{- define "isoboot.validateReplicaCount" -}}
{{- if and (gt (int .Values.replicaCount) 1) (not .Values.controller.leaderElection.enabled) }}
{{- fail "replicaCount > 1 requires controller.leaderElection.enabled=true to avoid concurrent reconciliation conflicts" }}
{{- end }}
{{- end }}
