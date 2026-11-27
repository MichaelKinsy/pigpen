{{- define "pig.name" -}}{{ .Values.fullnameOverride | default "pig" }}{{- end -}}
{{- define "pig.labels" -}}
app.kubernetes.io/name: pig-kagent
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" }}
{{- end -}}
{{/* The Harness the Agent runs on: the one this release creates (default: the release name), or an existing one. */}}
{{- define "pig.harnessName" -}}
{{- if .Values.harness.create -}}{{ .Values.harness.name | default (include "pig.name" .) }}
{{- else -}}{{ required "harness.name is required when harness.create is false" .Values.harness.name }}{{- end -}}
{{- end -}}
{{/* The ModelConfig the AgentTemplate names: the created one, else the referenced one. */}}
{{- define "pig.modelConfigName" -}}
{{- if .Values.modelConfig.create -}}{{ .Values.modelConfig.name }}{{- else -}}{{ .Values.agentTemplate.modelConfig }}{{- end -}}
{{- end -}}
