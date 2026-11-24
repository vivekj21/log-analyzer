package llm

import (
	"errors"
	"testing"
	"time"
)

func TestRetryDelay(t *testing.T) {
	cases := []struct {
		name    string
		err     string
		wantOK  bool
		minWait time.Duration
	}{
		{"per-minute limit", "Code: 429 ... Please retry in 13.3s.", true, 13 * time.Second},
		{"daily limit", "Code: 429 ... Please retry in 4h21m33.5s.", false, 0},
		{"429 without hint", "Code: 429 RESOURCE_EXHAUSTED", false, 0},
		{"overloaded", "Code: 503 UNAVAILABLE", true, 5 * time.Second},
		{"gateway timeout", "Error 504, Message: Deadline expired before operation could complete., Status: DEADLINE_EXCEEDED", true, 5 * time.Second},
		{"internal error", "Error 500, Status: INTERNAL", true, 5 * time.Second},
		{"model not found", "Code: 404 NOT_FOUND", false, 0},
	}
	for _, c := range cases {
		d, ok := retryDelay(errors.New(c.err), 1)
		if ok != c.wantOK || (ok && d < c.minWait) {
			t.Errorf("%s: got (%v, %v)", c.name, d, ok)
		}
	}
}

// A service error is retried, but not for ever: the third failure ends the tries.
func TestRetryDelay_ServiceErrorIsBounded(t *testing.T) {
	err := errors.New("Error 504, Status: DEADLINE_EXCEEDED")
	if _, ok := retryDelay(err, 2); !ok {
		t.Error("attempt 2 should still be retried")
	}
	if _, ok := retryDelay(err, 3); ok {
		t.Error("attempt 3 must not be retried")
	}
	// a plain Go context timeout is the caller's own limit, not a service error
	if _, ok := retryDelay(errors.New("context deadline exceeded"), 1); ok {
		t.Error("a context deadline must not be retried")
	}
}
