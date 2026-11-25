package reportout

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

var at = time.Date(2026, 11, 25, 10, 30, 0, 0, time.UTC)

func TestSaveIntoExistingFolderNamesTheFile(t *testing.T) {
	dir := t.TempDir()
	p, err := Save(dir, "OOM Kill: payment!", "hello", at)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(p) != "2026-11-25-103000-oom-kill-payment.md" {
		t.Fatalf("unexpected name %s", p)
	}
	if b, _ := os.ReadFile(p); string(b) != "hello" {
		t.Fatalf("content = %q", b)
	}
}

func TestSaveNeverOverwritesInAFolder(t *testing.T) {
	dir := t.TempDir()
	p1, _ := Save(dir, "x", "first", at)
	p2, err := Save(dir, "x", "second", at)
	if err != nil {
		t.Fatal(err)
	}
	if p1 == p2 || !strings.HasSuffix(p2, "-2.md") {
		t.Fatalf("expected a -2 name, got %s and %s", p1, p2)
	}
	if b, _ := os.ReadFile(p1); string(b) != "first" {
		t.Fatalf("first file was changed: %q", b)
	}
}

func TestSaveTrailingSlashCreatesFolder(t *testing.T) {
	target := filepath.Join(t.TempDir(), "new", "reports") + string(os.PathSeparator)
	p, err := Save(target, "x", "c", at)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(p) != filepath.Clean(target) {
		t.Fatalf("file %s not inside %s", p, target)
	}
}

func TestSaveExactFileIsWrittenAndReplaced(t *testing.T) {
	f := filepath.Join(t.TempDir(), "sub", "report.md")
	if _, err := Save(f, "x", "one", at); err != nil {
		t.Fatal(err)
	}
	if _, err := Save(f, "x", "two", at); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(f); string(b) != "two" {
		t.Fatalf("content = %q", b)
	}
	if fi, _ := os.Stat(f); runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 { // Windows has no Unix permission bits
		t.Fatalf("file mode = %v, want 0600", fi.Mode().Perm())
	}
}

func TestSaveFailsClearlyWhenParentIsAFile(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "afile")
	os.WriteFile(blocker, []byte("x"), 0o600)
	if _, err := Save(filepath.Join(blocker, "r.md"), "x", "c", at); err == nil {
		t.Fatal("expected an error")
	}
	if _, err := Save(filepath.Join(blocker, "sub")+"/", "x", "c", at); err == nil {
		t.Fatal("expected an error for a folder under a file")
	}
}

func TestSaveRejectsEmptyTarget(t *testing.T) {
	if _, err := Save("  ", "x", "c", at); err == nil {
		t.Fatal("expected an error")
	}
}

func TestSlugIsSafe(t *testing.T) {
	for in, want := range map[string]string{"": "report", "../../etc/passwd": "etc-passwd", "A  B": "a-b"} {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
	if len(Slug(strings.Repeat("a", 200))) > 40 {
		t.Error("slug too long")
	}
}
