{{- define "isoboot.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- $name := default .Chart.Name .Values.nameOverride -}}
{{- if contains $name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{- define "isoboot.selectorLabels" -}}
app.kubernetes.io/name: isoboot
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "isoboot.labels" -}}
{{ include "isoboot.selectorLabels" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{- define "isoboot.requireNodeName" -}}
{{- if not .Values.nodeName -}}
{{- fail "nodeName is required" -}}
{{- end -}}
{{- end -}}

{{/*
isoboot.subnet: dnsmasq.subnet, the PXE subnet, checked to be an IPv4 CIDR. It
is written into the dnsmasq, nginx and squid configuration and limits who may
use nginx, squid and nfsd, so anything else fails the render.
*/}}
{{- define "isoboot.subnet" -}}
{{- $subnet := required "dnsmasq.subnet is required (the PXE subnet, e.g. 192.168.101.0/24)" .Values.dnsmasq.subnet -}}
{{- if not (regexMatch "^([0-9]{1,3}\\.){3}[0-9]{1,3}/[0-9]{1,2}$" $subnet) -}}
{{- fail (printf "dnsmasq.subnet must be an IPv4 CIDR such as 192.168.101.0/24, got %q" $subnet) -}}
{{- end -}}
{{- $subnet -}}
{{- end -}}

{{- define "isoboot.affinity" -}}
affinity:
  nodeAffinity:
    requiredDuringSchedulingIgnoredDuringExecution:
      nodeSelectorTerms:
      - matchFields:
        - key: metadata.name
          operator: In
          values:
          - "{{ .context.Values.nodeName }}"
  podAntiAffinity:
    requiredDuringSchedulingIgnoredDuringExecution:
    - labelSelector:
        matchLabels:
          {{- include "isoboot.selectorLabels" .context | nindent 10 }}
          app.kubernetes.io/component: "{{ .component }}"
      topologyKey: kubernetes.io/hostname
{{- end -}}

{{- define "isoboot.restrictedSecurityContext" -}}
securityContext:
  readOnlyRootFilesystem: true
  allowPrivilegeEscalation: false
  capabilities:
    drop:
    - "ALL"
{{- end -}}
