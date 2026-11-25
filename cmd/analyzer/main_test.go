package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// These tests build the real analyzer program and run it, so they check what a user sees:
// what is printed, which files appear, the exit code, and what happens when git is missing.

var binPath string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "analyzer-e2e")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	binPath = filepath.Join(dir, "analyzer")
	if runtime.GOOS == "windows" {
		binPath += ".exe"
	}
	goBin, err := exec.LookPath("go")
	if err != nil {
		goBin = filepath.Join(runtime.GOROOT(), "bin", "go")
	}
	if out, err := exec.Command(goBin, "build", "-o", binPath, ".").CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "cannot build the analyzer: %v\n%s", err, out)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

type result struct {
	stdout, stderr string
	code           int
}

// run starts the analyzer with a clean environment. path is the whole PATH it gets.
func run(t *testing.T, work, home, path string, args ...string) result {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("these tests use a POSIX PATH")
	}
	cmd := exec.Command(binPath, args...)
	cmd.Dir = work
	cmd.Env = []string{"PATH=" + path, "HOME=" + home, "USERPROFILE=" + home}
	var so, se bytes.Buffer
	cmd.Stdout, cmd.Stderr = &so, &se
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("cannot run the analyzer: %v", err)
	}
	return result{so.String(), se.String(), code}
}

const oomLog = "2026-10-27T05:05:13Z kernel: Out of memory: Kill process 99 (java) score 850 or sacrifice child\n" +
	"2026-10-27T05:05:14Z terminated with exit code 137\n"

type env struct {
	work, home, noGit, logFile string
}

func newEnv(t *testing.T) env {
	t.Helper()
	e := env{work: t.TempDir(), home: t.TempDir(), noGit: t.TempDir(), logFile: filepath.Join(t.TempDir(), "app.log")}
	if err := os.WriteFile(e.logFile, []byte(oomLog), 0o600); err != nil {
		t.Fatal(err)
	}
	return e
}

func (e env) logRun(t *testing.T, path string, extra ...string) result {
	args := append([]string{"--log-file", e.logFile, "--llm", "heuristic"}, extra...)
	return run(t, e.work, e.home, path, args...)
}

func mustHaveReport(t *testing.T, r result) {
	t.Helper()
	for _, want := range []string{"## Autonomous Log Analysis", "OOM", "Remediation Guidance"} {
		if !strings.Contains(r.stdout, want) {
			t.Fatalf("stdout does not hold the full report (missing %q):\n%s\nstderr:\n%s", want, r.stdout, r.stderr)
		}
	}
}

func empty(t *testing.T, dir string) {
	t.Helper()
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("%s should be empty, found %v", dir, names)
	}
}

func needGit(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	return os.Getenv("PATH")
}

func TestLogFileDefaultPrintsReportAndNeedsNothing(t *testing.T) {
	e := newEnv(t)
	r := e.logRun(t, e.noGit) // no git on PATH at all
	if r.code != 0 {
		t.Fatalf("exit %d\n%s", r.code, r.stderr)
	}
	mustHaveReport(t, r)
	empty(t, e.work)
	empty(t, e.home) // nothing saved anywhere unless asked
}

func TestOutputFileHoldsTheSameReportAsTheScreen(t *testing.T) {
	e := newEnv(t)
	for _, flagName := range []string{"--output", "-o"} {
		target := filepath.Join(e.work, "out", flagName[len(flagName)-1:]+".md")
		r := e.logRun(t, e.noGit, flagName, target)
		if r.code != 0 {
			t.Fatalf("%s: exit %d\n%s", flagName, r.code, r.stderr)
		}
		mustHaveReport(t, r)
		got, err := os.ReadFile(target)
		if err != nil {
			t.Fatalf("%s: %v", flagName, err)
		}
		if strings.TrimRight(string(got), "\n") != strings.TrimRight(r.stdout, "\n") {
			t.Fatalf("%s: file and screen differ.\nfile:\n%s\nscreen:\n%s", flagName, got, r.stdout)
		}
	}
}

func TestOutputFolderGetsOneNamedFile(t *testing.T) {
	e := newEnv(t)
	dir := filepath.Join(e.work, "reports") + "/"
	r := e.logRun(t, e.noGit, "--output", dir)
	if r.code != 0 {
		t.Fatalf("exit %d\n%s", r.code, r.stderr)
	}
	mustHaveReport(t, r)
	files, _ := filepath.Glob(filepath.Join(e.work, "reports", "*.md"))
	if len(files) != 1 || !strings.Contains(files[0], "oom") {
		t.Fatalf("expected one .md file named after the issue, got %v", files)
	}
}

func TestUnwritableOutputOnlyWarns(t *testing.T) {
	e := newEnv(t)
	blocker := filepath.Join(e.work, "afile")
	os.WriteFile(blocker, []byte("x"), 0o600)
	r := e.logRun(t, e.noGit, "--output", filepath.Join(blocker, "r.md"))
	if r.code != 0 {
		t.Fatalf("a failed save must not change the exit code, got %d\n%s", r.code, r.stderr)
	}
	mustHaveReport(t, r)
	if !strings.Contains(r.stderr, "WARNING: the report was NOT saved") {
		t.Fatalf("no clear warning:\n%s", r.stderr)
	}
}

func TestGitMissingOnlyWarnsAndCreatesNothing(t *testing.T) {
	e := newEnv(t)
	repo := filepath.Join(e.work, "repo")
	for _, args := range [][]string{{"--git"}, {"--git-repo", repo}} {
		r := e.logRun(t, e.noGit, append(args, "--output", filepath.Join(e.work, "kept.md"))...)
		if r.code != 0 {
			t.Fatalf("%v: exit %d\n%s", args, r.code, r.stderr)
		}
		mustHaveReport(t, r)
		if !strings.Contains(r.stderr, "git is not installed") {
			t.Fatalf("%v: no clear git warning:\n%s", args, r.stderr)
		}
		if _, err := os.Stat(repo); err == nil {
			t.Fatalf("%v: the repo folder was created although git is missing", args)
		}
		if _, err := os.Stat(filepath.Join(e.work, "kept.md")); err != nil {
			t.Fatalf("%v: the file output must still be written: %v", args, err)
		}
	}
	empty(t, e.home)
}

func TestGitRepoFlagTurnsGitOnAndStillPrints(t *testing.T) {
	path := needGit(t)
	e := newEnv(t)
	repo := filepath.Join(e.work, "repo")
	r := e.logRun(t, path, "--git-repo", repo)
	if r.code != 0 {
		t.Fatalf("exit %d\n%s", r.code, r.stderr)
	}
	mustHaveReport(t, r)
	out, err := exec.Command("git", "-C", repo, "log", "--oneline").CombinedOutput()
	if err != nil || len(strings.TrimSpace(string(out))) == 0 {
		t.Fatalf("expected one commit in %s: %v\n%s", repo, err, out)
	}
	files, _ := filepath.Glob(filepath.Join(repo, ".log-analyzer", "incidents", "*.md"))
	if len(files) != 1 {
		t.Fatalf("expected one incident file, got %v", files)
	}
}

func TestGitFlagUsesDefaultRepoInHome(t *testing.T) {
	path := needGit(t)
	e := newEnv(t)
	r := e.logRun(t, path, "--git")
	if r.code != 0 {
		t.Fatalf("exit %d\n%s", r.code, r.stderr)
	}
	mustHaveReport(t, r)
	if _, err := os.Stat(filepath.Join(e.home, "log-analyzer-incidents", ".git")); err != nil {
		t.Fatalf("expected the default repo under HOME: %v", err)
	}
}

func TestExplicitGitFalseWins(t *testing.T) {
	path := needGit(t)
	e := newEnv(t)
	repo := filepath.Join(e.work, "repo")
	r := e.logRun(t, path, "--git-repo", repo, "--git=false")
	if r.code != 0 {
		t.Fatalf("exit %d\n%s", r.code, r.stderr)
	}
	mustHaveReport(t, r)
	if _, err := os.Stat(repo); err == nil {
		t.Fatal("--git=false must win over --git-repo")
	}
}

func TestGitFailureOnlyWarns(t *testing.T) {
	path := needGit(t)
	e := newEnv(t)
	blocker := filepath.Join(e.work, "afile")
	os.WriteFile(blocker, []byte("x"), 0o600)
	r := e.logRun(t, path, "--git-repo", filepath.Join(blocker, "repo"))
	if r.code != 0 {
		t.Fatalf("a failed commit must not change the exit code, got %d\n%s", r.code, r.stderr)
	}
	mustHaveReport(t, r)
	if !strings.Contains(r.stderr, "WARNING: the report was NOT committed to Git") {
		t.Fatalf("no clear warning:\n%s", r.stderr)
	}
}

func TestClusterModeDefaultPrintsAndSavesNothing(t *testing.T) {
	e := newEnv(t)
	r := run(t, e.work, e.home, e.noGit, "--llm", "heuristic", "--namespace", "demo", "--dry-run", "--timeout", "60s")
	if r.code != 0 {
		t.Fatalf("exit %d\n%s", r.code, r.stderr)
	}
	if !strings.Contains(r.stdout, "ROOT CAUSE ANALYSIS REPORT") {
		t.Fatalf("report missing on screen:\n%s", r.stdout)
	}
	empty(t, e.work)
	empty(t, e.home)
}

func TestClusterModeOutputFile(t *testing.T) {
	e := newEnv(t)
	target := filepath.Join(e.work, "cluster.md")
	r := run(t, e.work, e.home, e.noGit, "--llm", "heuristic", "--namespace", "demo", "--dry-run", "--timeout", "60s", "--output", target)
	if r.code != 0 {
		t.Fatalf("exit %d\n%s", r.code, r.stderr)
	}
	got, err := os.ReadFile(target)
	if err != nil || !strings.Contains(string(got), "ROOT CAUSE ANALYSIS REPORT") {
		t.Fatalf("report file missing or wrong: %v\n%s", err, got)
	}
	if !strings.Contains(r.stdout, "ROOT CAUSE ANALYSIS REPORT") {
		t.Fatal("report must also be on screen")
	}
}

func TestGitRequested(t *testing.T) {
	cases := []struct {
		name string
		set  map[string]bool
		git  bool
		repo string
		want bool
	}{
		{"nothing given", map[string]bool{}, false, "", false},
		{"--git", map[string]bool{"git": true}, true, "", true},
		{"--git=false", map[string]bool{"git": true}, false, "", false},
		{"--git-repo", map[string]bool{"git-repo": true}, false, "/r", true},
		{"--git-repo with empty value", map[string]bool{"git-repo": true}, false, "", false},
		{"--git=false beats --git-repo", map[string]bool{"git": true, "git-repo": true}, false, "/r", false},
	}
	for _, c := range cases {
		if got := gitRequested(c.set, c.git, c.repo); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestNoteGoesToStderrAndStdoutStaysClean(t *testing.T) {
	e := newEnv(t)
	r := e.logRun(t, e.noGit)
	if strings.Contains(r.stdout, "Where things went") {
		t.Fatal("the note must not be on stdout: `analyzer > report.md` must hold only the report")
	}
	for _, want := range []string{"Where things went", "--output report.md", "--git-repo", "No log text was sent anywhere", "--help"} {
		if !strings.Contains(r.stderr, want) {
			t.Fatalf("note is missing %q:\n%s", want, r.stderr)
		}
	}
}

func TestNoteSaysWhereTheFileWent(t *testing.T) {
	e := newEnv(t)
	target := filepath.Join(e.work, "kept.md")
	r := e.logRun(t, e.noGit, "-o", target)
	if !strings.Contains(r.stderr, "saved to "+target) || strings.Contains(r.stderr, "To keep a copy") {
		t.Fatalf("note should name the file and not suggest --output again:\n%s", r.stderr)
	}
}

func TestNoteSaysGitWasNotCommittedWhenGitIsMissing(t *testing.T) {
	e := newEnv(t)
	r := e.logRun(t, e.noGit, "--git")
	if !strings.Contains(r.stderr, "NOT committed (see the warning above)") {
		t.Fatalf("note must say the commit did not happen:\n%s", r.stderr)
	}
}

func TestNoteSaysWhichCommitWasMade(t *testing.T) {
	path := needGit(t)
	e := newEnv(t)
	r := e.logRun(t, path, "--git-repo", filepath.Join(e.work, "repo"))
	if !regexp.MustCompile(`committed .*\.md \(commit [0-9a-f]{7}\)`).MatchString(r.stderr) {
		t.Fatalf("note should name the file and a short commit:\n%s", r.stderr)
	}
}

func TestNoteAdmitsTheModelServiceWasContacted(t *testing.T) {
	e := newEnv(t)
	keyFile := filepath.Join(e.work, "key")
	os.WriteFile(keyFile, []byte("fake-key\n"), 0o600)
	// A local address with nothing listening: the model call fails fast.
	r := e.logRun(t, e.noGit, "--llm", "live", "--provider", "openai", "--model", "m", "--base-url", "http://127.0.0.1:1", "--api-key-file", keyFile)
	if r.code != 1 {
		t.Fatalf("a failed model call must exit 1, got %d\n%s", r.code, r.stderr)
	}
	mustHaveReport(t, r)
	if !strings.Contains(r.stderr, "tried to send the log text to openai (m)") {
		t.Fatalf("the privacy line must admit the attempt:\n%s", r.stderr)
	}
	if strings.Contains(r.stderr, "No log text was sent anywhere") {
		t.Fatal("must not claim nothing was sent")
	}
}

func TestNoArgumentsExplainsInsteadOfFailingOddly(t *testing.T) {
	e := newEnv(t)
	r := run(t, e.work, e.home, e.noGit)
	if r.code != 2 {
		t.Fatalf("exit %d, want 2", r.code)
	}
	for _, want := range []string{"Nothing to analyze yet", "--log-file app.log", "--help"} {
		if !strings.Contains(r.stderr, want) {
			t.Fatalf("missing %q:\n%s", want, r.stderr)
		}
	}
	if strings.Contains(r.stderr, "API key") {
		t.Fatal("a newcomer should not be told about API keys first")
	}
}

// Every flag defined in main.go must appear in --help, so help can never fall behind.
func TestHelpListsEveryFlag(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`flag\.(?:String|Bool|Duration|Int)\("([a-z-]+)"`)
	names := re.FindAllStringSubmatch(string(src), -1)
	if len(names) < 15 {
		t.Fatalf("expected to find the flags in main.go, found %d", len(names))
	}
	e := newEnv(t)
	r := run(t, e.work, e.home, e.noGit, "--help")
	if r.code != 0 {
		t.Fatalf("--help exit %d", r.code)
	}
	help := r.stdout + r.stderr
	for _, m := range names {
		if !strings.Contains(help, "--"+m[1]) {
			t.Errorf("flag --%s is not described in --help", m[1])
		}
	}
	if !strings.Contains(help, "-o FILE") {
		t.Error("short flag -o is not in --help")
	}
}
