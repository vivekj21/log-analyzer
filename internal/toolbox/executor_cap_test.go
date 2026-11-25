package toolbox

import (
	"strings"
	"testing"
)

func TestCapWriterKeepsOnlyTheCapAndNeverBlocks(t *testing.T) {
	w := &capWriter{max: 10}
	for i := 0; i < 1000; i++ {
		if n, err := w.Write([]byte("0123456789")); n != 10 || err != nil {
			t.Fatalf("a write must always report all bytes as written, got %d %v", n, err)
		}
	}
	if w.buf.Len() != 10 {
		t.Fatalf("memory must stay at the cap, holds %d bytes", w.buf.Len())
	}
	if out := w.String(); !strings.HasPrefix(out, "0123456789") || !strings.Contains(out, "truncated") {
		t.Fatalf("output must say it was cut: %q", out)
	}
}

func TestCapWriterLeavesShortOutputAlone(t *testing.T) {
	w := &capWriter{max: 100}
	w.Write([]byte("short"))
	if w.String() != "short" {
		t.Fatalf("got %q", w.String())
	}
}
