{{- /* Settings shared by the one-off Job and the always-on web service, so the two cannot drift apart. */}}
{{- define "log-analyzer.modelEnv" -}}
{{- if eq .Values.llm.mode "live" }}
- name: LLM_API_KEY_FILE
  value: /var/run/secrets/gemini/key
- name: LLM_PROVIDER
  value: {{ .Values.llm.provider | quote }}
{{- if .Values.llm.baseUrl }}
- name: LLM_BASE_URL
  value: {{ .Values.llm.baseUrl | quote }}
{{- end }}
{{- if .Values.llm.allowInsecureHttp }}
- name: LLM_ALLOW_HTTP
  value: "true"
{{- end }}
{{- if .Values.llm.model }}
- name: LLM_MODEL
  value: {{ .Values.llm.model | quote }}
{{- end }}
{{- end }}
{{- if .Values.gemini.model }}
- name: GEMINI_MODEL
  value: {{ .Values.gemini.model | quote }}
{{- end }}
- name: LOG_ANALYZER_MAX_CALLS
  value: {{ .Values.limits.maxCalls | quote }}
- name: LOG_ANALYZER_MAX_TOKENS
  value: {{ .Values.limits.maxTokensPerInvestigation | quote }}
- name: LOG_ANALYZER_MAX_TOTAL_TOKENS
  value: {{ .Values.limits.maxTotalTokens | quote }}
{{- end }}

{{- define "log-analyzer.keyMount" -}}
{{- if eq .Values.llm.mode "live" }}
- name: gemini-key
  mountPath: /var/run/secrets/gemini
  readOnly: true
{{- end }}
{{- end }}

{{- define "log-analyzer.keyVolume" -}}
{{- if eq .Values.llm.mode "live" }}
- name: gemini-key
  secret:
    secretName: {{ required "llm.apiKeySecret (or gemini.existingSecret) is required" (default .Values.gemini.existingSecret .Values.llm.apiKeySecret) }}
    defaultMode: 0440
    items:
      - key: {{ default .Values.gemini.secretKey .Values.llm.apiKeySecretKey }}
        path: key
{{- end }}
{{- end }}

{{- /* The read-only permissions. Same list for the Job and the web service. No secrets, no write verbs. */}}
{{- define "log-analyzer.rules" -}}
- apiGroups: [""]
  resources: ["pods", "pods/log", "events", "services", "endpoints"{{ if .Values.rbac.clusterWide }}, "nodes"{{ end }}]
  verbs: ["get", "list"]
- apiGroups: ["apps"]
  resources: ["deployments", "replicasets"]
  verbs: ["get", "list"]
- apiGroups: ["autoscaling"]
  resources: ["horizontalpodautoscalers"]
  verbs: ["get", "list"]
- apiGroups: ["discovery.k8s.io"]
  resources: ["endpointslices"]
  verbs: ["get", "list"]
- apiGroups: ["metrics.k8s.io"]
  resources: ["pods"{{ if .Values.rbac.clusterWide }}, "nodes"{{ end }}]
  verbs: ["get", "list"]
{{- if .Values.rbac.allowConfigMaps }}
- apiGroups: [""]
  resources: ["configmaps"]
  verbs: ["get"]
{{- end }}
{{- end }}
