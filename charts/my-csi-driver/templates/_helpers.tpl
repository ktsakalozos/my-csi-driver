{{- define "my-csi-driver.fullname" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "my-csi-driver.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{ default (include "my-csi-driver.fullname" .) .Values.serviceAccount.name }}
{{- else -}}
{{ default "default" .Values.serviceAccount.name }}
{{- end -}}
{{- end -}}
