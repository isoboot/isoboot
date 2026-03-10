{{- define "isoboot.fullname" -}}
{{- printf "%s-%s" .Release.Name .Chart.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "isoboot.selectorLabels" -}}
control-plane: controller-manager
app.kubernetes.io/name: isoboot
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "isoboot.labels" -}}
{{ include "isoboot.selectorLabels" . }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}
