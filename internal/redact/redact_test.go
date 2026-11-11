package redact

import (
	"strings"
	"testing"
)

// canary is a plaintext secret value that must never survive redaction.
const canary = "SUPER_SECRET_CANARY_VALUE_12345"

func TestAssignmentPatterns(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{"password=value", `password=` + canary},
		{"password: value", `password: ` + canary},
		{"PASSWORD = quoted", `PASSWORD = "` + canary + `"`},
		{"DB_SECRET='val'", `DB_SECRET='` + canary + `'`},
		{"api_key=val", `api_key=` + canary},
		{"token: val", `token: ` + canary},
		{"secret=val", `secret=` + canary},
		{"apikey=val", `apikey=` + canary},
		{"api-secret=val", `api-secret=` + canary},
		{"access_key=val", `access_key=` + canary},
		{"private_key=val", `private_key=` + canary},
		{"PASSWD=val", `PASSWD=` + canary},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := Redact(tc.input)
			if strings.Contains(out, canary) {
				t.Errorf("canary survived redaction:\ninput:  %s\noutput: %s", tc.input, out)
			}
			if !strings.Contains(out, "[REDACTED]") {
				t.Errorf("expected [REDACTED] in output:\ninput:  %s\noutput: %s", tc.input, out)
			}
		})
	}
}

func TestAuthorizationBearer(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{"Bearer token", `Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.` + canary},
		{"Basic token", `Authorization: Basic dXNlcjpwYXNzd29yZA==`},
		{"Token scheme", `Authorization: Token ghp_abcdef1234567890`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := Redact(tc.input)
			if strings.Contains(out, canary) {
				t.Errorf("canary survived: %s", out)
			}
			if !strings.Contains(out, "[REDACTED]") {
				t.Errorf("expected [REDACTED]: %s", out)
			}
			if !strings.Contains(out, "Authorization:") {
				t.Errorf("label should be preserved: %s", out)
			}
		})
	}
}

func TestAWSKeys(t *testing.T) {
	t.Run("access key ID", func(t *testing.T) {
		input := "found key AKIAIOSFODNN7EXAMPLE in config"
		out := Redact(input)
		if strings.Contains(out, "AKIAIOSFODNN7EXAMPLE") {
			t.Errorf("AWS access key survived: %s", out)
		}
	})

	t.Run("secret access key", func(t *testing.T) {
		input := "aws_secret_access_key = wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
		out := Redact(input)
		if strings.Contains(out, "wJalrXUtnFEMI") {
			t.Errorf("AWS secret key survived: %s", out)
		}
	})
}

func TestPrivateKeyBlock(t *testing.T) {
	input := `some preamble
-----BEGIN RSA PRIVATE KEY-----
MIIEowIBAAKCAQEA2a2rwplBQLxEQ0oB` + canary + `
dGVzdCBrZXkgbWF0ZXJpYWwK
-----END RSA PRIVATE KEY-----
some postamble`

	out := Redact(input)
	if strings.Contains(out, canary) {
		t.Errorf("canary in PEM block survived: %s", out)
	}
	if strings.Contains(out, "BEGIN RSA PRIVATE KEY") {
		t.Errorf("PEM block header survived: %s", out)
	}
	if !strings.Contains(out, "[REDACTED]") {
		t.Errorf("expected [REDACTED]: %s", out)
	}
	if !strings.Contains(out, "some preamble") {
		t.Errorf("preamble should survive: %s", out)
	}
	if !strings.Contains(out, "some postamble") {
		t.Errorf("postamble should survive: %s", out)
	}
}

func TestPrivateKeyVariants(t *testing.T) {
	variants := []string{
		"-----BEGIN PRIVATE KEY-----\ndata\n-----END PRIVATE KEY-----",
		"-----BEGIN EC PRIVATE KEY-----\ndata\n-----END EC PRIVATE KEY-----",
		"-----BEGIN OPENSSH PRIVATE KEY-----\ndata\n-----END OPENSSH PRIVATE KEY-----",
	}
	for _, v := range variants {
		out := Redact(v)
		if strings.Contains(out, "PRIVATE KEY-----") {
			t.Errorf("PEM block survived: %s", out)
		}
	}
}

func TestKubernetesSecretData(t *testing.T) {
	input := `apiVersion: v1
kind: Secret
metadata:
  name: db-credentials
type: Opaque
data:
  username: YWRtaW4=
  password: ` + canary + `
stringData:
  api-key: plaintext-secret-value
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: app-config
data:
  LOG_LEVEL: debug
`
	out := Redact(input)

	// Secret values must be redacted.
	if strings.Contains(out, canary) {
		t.Errorf("canary in Secret data survived:\n%s", out)
	}
	if strings.Contains(out, "YWRtaW4=") {
		t.Errorf("base64 username in Secret survived:\n%s", out)
	}
	if strings.Contains(out, "plaintext-secret-value") {
		t.Errorf("stringData value in Secret survived:\n%s", out)
	}

	// ConfigMap values must NOT be redacted.
	if !strings.Contains(out, "debug") {
		t.Errorf("ConfigMap data value should survive:\n%s", out)
	}

	// Structure must be preserved.
	if !strings.Contains(out, "kind: Secret") {
		t.Errorf("kind: Secret label should be preserved:\n%s", out)
	}
	if !strings.Contains(out, "kind: ConfigMap") {
		t.Errorf("kind: ConfigMap label should be preserved:\n%s", out)
	}
}

func TestCanaryInMixedOutput(t *testing.T) {
	// Simulates realistic tool output that contains several kinds of secrets.
	input := `
kubectl get secret -o yaml
apiVersion: v1
kind: Secret
metadata:
  name: prod-creds
data:
  db-password: ` + canary + `

--- Tool: describe_pod ---
Name: api-server-7b4f5c9d8f-x2j9k
Containers:
  env:
    - PASSWORD=` + canary + `
    - API_KEY="` + canary + `"
    - AWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE
    - Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.` + canary + `

-----BEGIN RSA PRIVATE KEY-----
MIIEowIBAAKCAQEA` + canary + `
base64datahere
-----END RSA PRIVATE KEY-----
`
	out := Redact(input)
	if strings.Contains(out, canary) {
		t.Errorf("canary survived in mixed output:\n%s", out)
	}
	count := strings.Count(out, "[REDACTED]")
	if count < 5 {
		t.Errorf("expected at least 5 redactions, got %d:\n%s", count, out)
	}
}

func TestNonSecretContentPreserved(t *testing.T) {
	input := `
NAME                          READY   STATUS    RESTARTS   AGE
payment-svc-7b4f5c9d8f-x2j9k   1/1     Running   0          2h
payment-svc-7b4f5c9d8f-abc12   1/1     Running   3          2h

Events:
  Type    Reason   Age   Message
  Normal  Pulled   30m   Container image "payment:v1.2.3" already present on machine
  Warning BackOff  5m    Back-off restarting failed container
`
	out := Redact(input)
	if out != input {
		t.Errorf("non-secret content should be unchanged:\nwant: %s\ngot:  %s", input, out)
	}
}

func TestEmptyString(t *testing.T) {
	if Redact("") != "" {
		t.Error("empty string should return empty")
	}
}

func TestMultipleSecretAssignmentsOnSameLine(t *testing.T) {
	input := `config: password=abc123 token=xyz789`
	out := Redact(input)
	if strings.Contains(out, "abc123") || strings.Contains(out, "xyz789") {
		t.Errorf("secrets survived: %s", out)
	}
}

func TestCaseInsensitivity(t *testing.T) {
	cases := []string{
		"PASSWORD=" + canary,
		"Password=" + canary,
		"pAssWoRd=" + canary,
		"SECRET=" + canary,
		"Token=" + canary,
		"API_KEY=" + canary,
	}
	for _, input := range cases {
		out := Redact(input)
		if strings.Contains(out, canary) {
			t.Errorf("case-insensitive match failed for: %s → %s", input, out)
		}
	}
}
