package serve

import (
	"strings"
)

// Alertmanager sends one POST with a group of alerts. Only the fields used here are read.
type alertPayload struct {
	Alerts []alert `json:"alerts"`
}

type alert struct {
	Status      string            `json:"status"`
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
}

const maxAlertsPerPost = 20

// alertRequest turns one alert into an investigation request. ok is false, with a reason, when the
// alert cannot be investigated.
func alertRequest(a alert, allowed []string) (req createRequest, name, reason string, ok bool) {
	name = cleanText(a.Labels["alertname"], 80)
	if a.Status != "" && a.Status != "firing" {
		return req, name, "alert is " + a.Status, false
	}
	ns := a.Labels["namespace"]
	if ns == "" {
		return req, name, `the alert has no "namespace" label`, false
	}
	svc := ""
	for _, k := range []string{"service", "app", "deployment"} {
		if v := a.Labels[k]; v != "" && serviceRe.MatchString(v) && len(v) <= 63 {
			svc = v
			break
		}
	}
	text := a.Annotations["summary"]
	if text == "" {
		text = a.Annotations["description"]
	}
	if text == "" {
		text = a.Annotations["message"]
	}
	sym := strings.TrimSpace(name + ": " + cleanText(text, 300))
	sym = strings.TrimSuffix(sym, ":")
	return createRequest{Namespace: ns, Service: svc, Symptom: sym}, name, "", true
}
