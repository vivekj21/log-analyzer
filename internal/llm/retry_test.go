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
		{"model not found", "Code: 404 NOT_FOUND", false, 0},
	}
	for _, c := range cases {
		d, ok := retryDelay(errors.New(c.err), 1)
		if ok != c.wantOK || (ok && d < c.minWait) {
			t.Errorf("%s: got (%v, %v)", c.name, d, ok)
		}
	}
}
