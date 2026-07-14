{{/*
Expand the name of the chart.
*/}}
{{- define "octoconv.name" -}}
{{- .Chart.Name | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "octoconv.fullname" -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels. Applied to every object in this chart, including the
stateful backbone (postgres/redis/minio). Deliberately does NOT include
octoconv.io/tier — that label is added per-template ONLY on the 5 app
Deployments in plan 02 (see templates/postgres.yaml, redis.yaml, minio.yaml
for the tier-label boundary note). The plan-02 metrics NetworkPolicy
podSelector matches exactly on octoconv.io/tier: app, so keeping it off
these labels is what prevents the default-deny policy from black-holing
5432/6379/9000.
*/}}
{{- define "octoconv.labels" -}}
app.kubernetes.io/name: {{ include "octoconv.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/part-of: octoconv
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels, keyed by a "component" arg (e.g. "postgres", "api",
"worker"). Callers pass a dict with `component` and the root context, e.g.:
  {{- include "octoconv.selectorLabels" (dict "component" "postgres" "root" $) }}
*/}}
{{- define "octoconv.selectorLabels" -}}
app.kubernetes.io/name: {{ include "octoconv.name" .root }}
app.kubernetes.io/instance: {{ .root.Release.Name }}
app.kubernetes.io/component: {{ .component }}
{{- end }}

{{/*
Shared env contract: every Deployment in plan 02 pulls the identical
non-secret + secret env surface from these two objects via envFrom, so the
ConfigMap/Secret in this plan is the single choke point for the env
contract (DEBT-05 — every binary carries the full retry/timeout surface).
*/}}
{{- define "octoconv.commonEnv" -}}
envFrom:
  - configMapRef:
      name: octoconv-config
  - secretRef:
      name: octoconv-secret
{{- end }}
