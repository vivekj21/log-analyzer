package creds

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// canary stands for a real key. It must never show up in any error or warning text.
const canary = "AIzaSyCANARY_KEY_VALUE_0123456789"

func write(t *testing.T, content string, mode os.FileMode) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(p, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}
	return p
}

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestFileKeyIsTrimmed(t *testing.T) {
	p := write(t, canary+"\n", 0o400)
	k, src, err := Resolve(FileProvider("--gemini-key-file", p, nil))
	if err != nil || k != canary || !strings.Contains(src, "--gemini-key-file") {
		t.Fatalf("got key=%q src=%q err=%v", k, src, err)
	}
}

func TestOrderFileBeatsEnvAndEnvIsFallback(t *testing.T) {
	p := write(t, "file-key", 0o400)
	e := EnvProvider("GEMINI_API_KEY", env(map[string]string{"GEMINI_API_KEY": "env-key"}))
	if k, _, _ := Resolve(FileProvider("flag", p, nil), e); k != "file-key" {
		t.Errorf("file should win, got %q", k)
	}
	if k, src, _ := Resolve(FileProvider("flag", "", nil), e); k != "env-key" || !strings.Contains(src, "GEMINI_API_KEY") {
		t.Errorf("env should be the fallback, got %q from %q", k, src)
	}
}

func TestNoKeyAnywhereIsNotAnError(t *testing.T) {
	k, src, err := Resolve(FileProvider("flag", "", nil), EnvProvider("GEMINI_API_KEY", env(nil)))
	if k != "" || src != "" || err != nil {
		t.Fatalf("want empty and no error, got %q %q %v", k, src, err)
	}
}

// A configured but broken file must fail loudly, never quietly use the env var instead.
func TestBrokenFileDoesNotFallThroughToEnv(t *testing.T) {
	e := EnvProvider("GEMINI_API_KEY", env(map[string]string{"GEMINI_API_KEY": "env-key"}))
	dir := t.TempDir()
	cases := map[string]string{
		"missing":   filepath.Join(dir, "nope"),
		"empty":     write(t, " \n", 0o400),
		"multiline": write(t, "line one\n"+canary+"\n", 0o400),
		"spaces":    write(t, "key with spaces "+canary, 0o400),
		"too big":   write(t, strings.Repeat("x", maxKeyFileBytes+1), 0o400),
		"folder":    dir,
	}
	for name, path := range cases {
		k, _, err := Resolve(FileProvider("flag", path, nil), e)
		if err == nil || k != "" {
			t.Errorf("%s: want error and no key, got key=%q err=%v", name, k, err)
			continue
		}
		if strings.Contains(err.Error(), canary) {
			t.Errorf("%s: error text leaks the key: %v", name, err)
		}
	}
}

func TestWorldReadableFileWarnsButWorks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no Unix permission bits")
	}
	var warned []string
	warn := func(s string) { warned = append(warned, s) }

	bad := write(t, canary, 0o644)
	if k, _, err := Resolve(FileProvider("flag", bad, warn)); err != nil || k != canary {
		t.Fatalf("0644 file should still work: %q %v", k, err)
	}
	if len(warned) != 1 {
		t.Fatalf("want one warning for 0644, got %v", warned)
	}
	for _, w := range warned {
		if strings.Contains(w, canary) {
			t.Errorf("warning leaks the key: %s", w)
		}
	}

	warned = nil
	good := write(t, canary, 0o440)
	Resolve(FileProvider("flag", good, warn))
	if len(warned) != 0 {
		t.Errorf("0440 should not warn, got %v", warned)
	}
}

func TestKeyFileTooOpen(t *testing.T) {
	cases := []struct {
		goos string
		perm fs.FileMode
		want bool
	}{
		{"linux", 0o644, true}, {"linux", 0o400, false}, {"linux", 0o440, false},
		{"darwin", 0o604, true}, {"darwin", 0o600, false},
		{"windows", 0o666, false}, {"windows", 0o444, false}, // never a warning on Windows
	}
	for _, c := range cases {
		if got := keyFileTooOpen(c.goos, c.perm); got != c.want {
			t.Errorf("keyFileTooOpen(%s, %04o) = %v, want %v", c.goos, c.perm, got, c.want)
		}
	}
}
