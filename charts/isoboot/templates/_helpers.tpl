{{- define "isoboot.fullname" -}}
{{- printf "%s-%s" .Release.Name .Chart.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "isoboot.selectorLabels" -}}
control-plane: controller-manager
app.kubernetes.io/name: isoboot
{{- end -}}

{{- define "isoboot.labels" -}}
{{ include "isoboot.selectorLabels" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}
