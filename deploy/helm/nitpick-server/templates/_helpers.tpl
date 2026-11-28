{{- define "notary.name" -}}{{ .Chart.Name }}{{- end -}}

{{- define "notary.fullname" -}}
{{- if contains .Chart.Name .Release.Name -}}{{ .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else -}}{{ printf "%s-%s" .Release.Name .Chart.Name | trunc 63 | trimSuffix "-" }}{{- end -}}
{{- end -}}

{{- define "notary.labels" -}}
app.kubernetes.io/name: {{ include "notary.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" }}
{{- end -}}

{{- define "notary.selector" -}}
app.kubernetes.io/name: {{ include "notary.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{/* The Secret the pod reads tokens from: the operator's, or the one this chart makes. */}}
{{- define "notary.secretName" -}}
{{- if .Values.existingSecret -}}{{ .Values.existingSecret }}{{- else -}}{{ include "notary.fullname" . }}{{- end -}}
{{- end -}}

{{/* The Secret the pod reads insights tokens from: the operator's, or the one this chart makes. */}}
{{- define "notary.insightsSecretName" -}}
{{- if .Values.insightsExistingSecret -}}{{ .Values.insightsExistingSecret }}{{- else -}}{{ include "notary.fullname" . }}-insights{{- end -}}
{{- end -}}

{{/* Pods of the bundled database. Their name differs from the server's so the server's Service never selects them. */}}
{{- define "notary.dbSelector" -}}
app.kubernetes.io/name: {{ include "notary.name" . }}-db
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{/* The bundled database's Service, and so its host. */}}
{{- define "notary.dbHost" -}}{{ include "notary.fullname" . }}-db{{- end -}}

{{/* The Secret holding the bundled database's password: the operator's, or the one this chart makes. */}}
{{- define "notary.dbSecretName" -}}
{{- if .Values.database.existingSecret -}}{{ .Values.database.existingSecret }}{{- else -}}{{ include "notary.fullname" . }}-db{{- end -}}
{{- end -}}

{{- define "notary.dbPort" -}}{{ if eq .Values.database.bundled "mysql" }}3306{{ else }}5432{{ end }}{{- end -}}

{{/* The Secret holding the external database's URL: the operator's, or the one this chart makes. */}}
{{- define "notary.dbURLSecretName" -}}
{{- if .Values.externalDatabase.existingSecret -}}{{ .Values.externalDatabase.existingSecret }}{{- else -}}{{ include "notary.fullname" . }}-database-url{{- end -}}
{{- end -}}

{{/* Fails the render when the database settings cannot make a working server. */}}
{{- define "notary.validateDatabase" -}}
{{- $b := .Values.database.bundled -}}
{{- if not (has $b (list "postgres" "mysql" "none")) }}{{ fail (printf "database.bundled must be postgres, mysql or none, got %q" $b) }}{{ end }}
{{- $ext := or .Values.externalDatabase.url .Values.externalDatabase.existingSecret -}}
{{- if and (eq $b "none") (not $ext) }}{{ fail "database.bundled is none, so set externalDatabase.url or externalDatabase.existingSecret: the server cannot start without a database" }}{{ end }}
{{- if and (ne $b "none") $ext }}{{ fail "externalDatabase is set while database.bundled is not none: the server would use the bundled database and ignore it. Set database.bundled to none, or clear externalDatabase" }}{{ end }}
{{- if and .Values.externalDatabase.url .Values.externalDatabase.existingSecret }}{{ fail "set externalDatabase.url or externalDatabase.existingSecret, not both" }}{{ end }}
{{- if lt (int .Values.database.maxConnections) (mul 20 (int .Values.replicaCount)) }}{{ fail (printf "database.maxConnections (%d) is below 20 connections for each of the %d replicas" (int .Values.database.maxConnections) (int .Values.replicaCount)) }}{{ end }}
{{- end -}}

{{/* True when nothing gives the server a way in, so the chart makes a notary token. */}}
{{- define "notary.generateToken" -}}
{{- if not (or .Values.tokens .Values.existingSecret .Values.oidc.audience .Values.config.existingSecret .Values.insightsTokens .Values.insightsExistingSecret) }}true{{ end -}}
{{- end -}}
