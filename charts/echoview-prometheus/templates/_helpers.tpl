{{- define "echoview-prometheus.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "echoview-prometheus.fullname" -}}
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

{{- define "echoview-prometheus.selectorLabels" -}}
app.kubernetes.io/name: {{ include "echoview-prometheus.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "echoview-prometheus.labels" -}}
{{- $base := dict "helm.sh/chart" (printf "%s-%s" .Chart.Name (.Chart.Version | replace "+" "_")) "app.kubernetes.io/name" (include "echoview-prometheus.name" .) "app.kubernetes.io/instance" .Release.Name "app.kubernetes.io/managed-by" .Release.Service -}}
{{- toYaml (mergeOverwrite (dict) (default (dict) .Values.commonLabels) $base) -}}
{{- end -}}

{{- define "echoview-prometheus.podLabels" -}}
{{- toYaml (mergeOverwrite (dict) (include "echoview-prometheus.labels" . | fromYaml) (default (dict) .Values.podLabels) (include "echoview-prometheus.selectorLabels" . | fromYaml)) -}}
{{- end -}}

{{- define "echoview-prometheus.serviceLabels" -}}
{{- toYaml (mergeOverwrite (dict) (include "echoview-prometheus.labels" . | fromYaml) (default (dict) .Values.service.labels) (include "echoview-prometheus.selectorLabels" . | fromYaml)) -}}
{{- end -}}

{{- define "echoview-prometheus.serviceMonitorLabels" -}}
{{- toYaml (mergeOverwrite (dict) (include "echoview-prometheus.labels" . | fromYaml) (default (dict) .Values.serviceMonitor.labels)) -}}
{{- end -}}

{{- define "echoview-prometheus.configMapName" -}}
{{- if .Values.instancesConfig.existingConfigMap -}}
{{- .Values.instancesConfig.existingConfigMap -}}
{{- else -}}
{{- default (printf "%s-instances" (include "echoview-prometheus.fullname" .)) .Values.instancesConfig.name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}
