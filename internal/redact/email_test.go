package redact

import (
	"strings"
	"testing"
)

func TestEmailAddressesAreMasked(t *testing.T) {
	for _, in := range []string{
		"login failed for jane.doe+test@example.com from 10.0.0.5",
		"user=Bob_Smith@mail.corp.example.co.uk rejected",
		"contact: ops@acme.io",
	} {
		out := Redact(in)
		if strings.Contains(out, "@") || !strings.Contains(out, "[REDACTED]") {
			t.Errorf("email survived or nothing masked:\n in:  %s\n out: %s", in, out)
		}
	}
}

func TestEmailRuleLeavesNormalLogTextAlone(t *testing.T) {
	for _, in := range []string{
		"INFO [QuorumPeer[myid=1]:FastLeaderElection@774] - Notification time out: 3200",
		"dial postgres://app:pw@db:5432/orders failed",
		"WARN Listener@493 connection from /10.10.34.11:45307",
		"pod web-7d9f8c6b5-abcde OOMKilled exit code 137",
	} {
		// password-style masking may change the URL case; only the email rule is under test,
		// so compare against a run of Redact on text without any email-like part.
		if strings.Contains(emailRe.ReplaceAllString(in, "[X]"), "[X]") {
			t.Errorf("email rule matched normal text: %s", in)
		}
	}
}
