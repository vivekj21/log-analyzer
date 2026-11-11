// Package redact strips secrets and credentials from tool output before it is
// stored as evidence, sent to the LLM, or persisted to Git.  The intent is
// defence‑in‑depth: even if a Kubernetes Secret, an environment dump, or a
// config file leaks into diagnostic output, sensitive values never reach the
// final report or repository.
package redact

import (
	"regexp"
	"strings"
)

// placeholder is the fixed replacement string for every redacted value.
const placeholder = "[REDACTED]"

// ──────────────────────────────────────────────────────────────────────────────
// Pre‑compiled patterns – order does not matter; all are applied.
// ──────────────────────────────────────────────────────────────────────────────

var (
	// 1. password / secret / token / api_key assignments.
	//    Matches forms like:
	//      password=abc123    password: abc123    PASSWORD = "abc123"
	//      DB_SECRET='s3cr3t' api_key=AKIAIOSFODNN7EXAMPLE
	//      token: "eyJhb..."
	assignmentRe = regexp.MustCompile(
		`(?im)((?:password|passwd|secret|token|api_key|api[-_]?secret|apikey|access[-_]?key|private[-_]?key)` +
			`\s*[:=]\s*)` + // key + separator
			`("(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*'|\S+)`) // quoted or unquoted value

	// 2. Authorization: Bearer ... (also Basic, Token, etc.)
	authHeaderRe = regexp.MustCompile(
		`(?im)(Authorization:\s*(?:Bearer|Basic|Token)\s+)\S+`)

	// 3. AWS‑style access key IDs (AKIA..., 20 chars) and secret keys (40 base64 chars).
	awsAccessKeyRe = regexp.MustCompile(`\b(AKIA[0-9A-Z]{16})\b`)
	awsSecretKeyRe = regexp.MustCompile(`(?i)(aws_secret_access_key\s*[:=]\s*)(\S+)`)

	// 4. PEM private key blocks.
	privateKeyBlockRe = regexp.MustCompile(
		`(?s)-----BEGIN (?:RSA |EC |DSA |OPENSSH |ED25519 )?PRIVATE KEY-----.*?-----END (?:RSA |EC |DSA |OPENSSH |ED25519 )?PRIVATE KEY-----`)

	// 5. Kubernetes Secret data values – base64 blobs under a "kind: Secret"
	//    YAML document.  We detect the document boundary and then mask every
	//    value in the `data:` or `stringData:` mapping.
	//
	//    This is handled by a dedicated function rather than a single regex.
)

// Redact returns a copy of s with sensitive material replaced by [REDACTED].
func Redact(s string) string {
	// Fast path: nothing to scan.
	if s == "" {
		return s
	}

	out := s

	// 1. Assignment patterns.
	out = assignmentRe.ReplaceAllString(out, "${1}"+placeholder)

	// 2. Authorization headers.
	out = authHeaderRe.ReplaceAllString(out, "${1}"+placeholder)

	// 3. AWS access key IDs.
	out = awsAccessKeyRe.ReplaceAllString(out, placeholder)

	// 4. AWS secret access keys (by labelled assignment).
	out = awsSecretKeyRe.ReplaceAllString(out, "${1}"+placeholder)

	// 5. PEM private key blocks.
	out = privateKeyBlockRe.ReplaceAllString(out, placeholder)

	// 6. Kubernetes Secret data values.
	out = redactK8sSecrets(out)

	return out
}

// ──────────────────────────────────────────────────────────────────────────────
// Kubernetes Secret document redaction
// ──────────────────────────────────────────────────────────────────────────────

// k8sSecretDocRe matches a YAML document that contains "kind: Secret".
// We split on YAML document boundaries ("---") and process only matching docs.
var k8sSecretDocRe = regexp.MustCompile(`(?m)^kind:\s*Secret\s*$`)

// dataValueRe matches "  key: <base64>" lines inside a data: or stringData: block.
var dataValueRe = regexp.MustCompile(`(?m)^(\s+\S+:\s+)\S+(.*)$`)

func redactK8sSecrets(s string) string {
	// Split on YAML document separators while keeping them.
	docs := splitYAMLDocs(s)
	var changed bool
	for i, doc := range docs {
		if !k8sSecretDocRe.MatchString(doc) {
			continue
		}
		docs[i] = redactSecretDataBlock(doc)
		changed = true
	}
	if !changed {
		return s
	}
	return strings.Join(docs, "")
}

// splitYAMLDocs splits on "---" document separators, preserving them as
// individual entries in the slice so the join is lossless.
func splitYAMLDocs(s string) []string {
	sep := "\n---"
	parts := strings.Split(s, sep)
	if len(parts) == 1 {
		return parts
	}
	result := make([]string, 0, len(parts)*2-1)
	for i, p := range parts {
		if i > 0 {
			result = append(result, sep)
		}
		result = append(result, p)
	}
	return result
}

// dataBlockRe matches a `data:` or `stringData:` header line in YAML.
var dataBlockRe = regexp.MustCompile(`(?m)^(data|stringData):\s*$`)

// redactSecretDataBlock masks values in `data:` and `stringData:` sections of
// a single YAML document that has kind: Secret.
func redactSecretDataBlock(doc string) string {
	lines := strings.Split(doc, "\n")
	inDataBlock := false
	for i, line := range lines {
		if dataBlockRe.MatchString(line) {
			inDataBlock = true
			continue
		}
		if inDataBlock {
			// Still inside the data block?  Data block entries are indented.
			// A non-empty, non-indented line ends the block.
			trimmed := strings.TrimRight(line, " \t\r")
			if trimmed == "" {
				continue
			}
			if line[0] != ' ' && line[0] != '\t' {
				inDataBlock = false
				continue
			}
			// Replace the value portion:  "  key: value" → "  key: [REDACTED]"
			lines[i] = dataValueRe.ReplaceAllString(line, "${1}"+placeholder)
		}
	}
	return strings.Join(lines, "\n")
}
