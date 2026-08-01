{{- define "aegisops.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "aegisops.fullname" -}}
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

{{- define "aegisops.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" }}
app.kubernetes.io/part-of: aegisops
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{- define "aegisops.selectorLabels" -}}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "aegisops.image" -}}
{{- $registry := .Values.global.imageRegistry -}}
{{- $tag := .Values.global.imageTag -}}
{{- if .digest -}}
{{ printf "%s/%s@%s" $registry .repository .digest }}
{{- else -}}
{{ printf "%s/%s:%s" $registry .repository (default $tag "dev") }}
{{- end -}}
{{- end -}}

{{- define "aegisops.serviceAccountName" -}}
{{- printf "aegisops-%s" .component -}}
{{- end -}}
