package agent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"k8s-log-analyzer/internal/protocol"
)

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func newTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitIn(t, dir, "init", "-b", "main")
	gitIn(t, dir, "config", "user.name", "Test")
	gitIn(t, dir, "config", "user.email", "test@test.local")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "add", "README.md")
	gitIn(t, dir, "commit", "-m", "initial")
	return dir
}

func persist(t *testing.T, dir string, mutate func(*protocol.GitPersistRequestPayload)) (*PersistResult, error) {
	t.Helper()
	cfg := DefaultGitAgentConfig()
	cfg.RepoDir = dir
	cfg.CreateBranch = false
	req := protocol.GitPersistRequestPayload{InvestigationID: "inv-1", Title: "Same title", Namespace: "demo", Severity: "P1", RootCause: "cause", RepoDir: dir}
	if mutate != nil {
		mutate(&req)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return NewGitAgent(protocol.NewAgentBus(1), cfg).PersistIssue(ctx, req)
}

// Work the user already staged in the same repository must not be swept into the incident commit.
func TestPersistCommitsOnlyTheIncidentFile(t *testing.T) {
	dir := newTestRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "mine.txt"), []byte("my work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, dir, "add", "mine.txt")
	res, err := persist(t, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	files := gitIn(t, dir, "show", "--name-only", "--format=", "HEAD")
	if strings.Contains(files, "mine.txt") || !strings.Contains(files, filepath.Base(res.FilePath)) {
		t.Fatalf("the commit must hold only the incident file, got:\n%s", files)
	}
	if st := gitIn(t, dir, "status", "--porcelain"); !strings.Contains(st, "A  mine.txt") {
		t.Fatalf("the user's staged file must stay staged, status:\n%s", st)
	}
}

// A second incident with the same date, severity and title must not replace the first one.
func TestPersistNeverOverwritesAnIncident(t *testing.T) {
	dir := newTestRepo(t)
	a, err := persist(t, dir, func(r *protocol.GitPersistRequestPayload) { r.InvestigationID = "first" })
	if err != nil {
		t.Fatal(err)
	}
	b, err := persist(t, dir, func(r *protocol.GitPersistRequestPayload) { r.InvestigationID = "second" })
	if err != nil {
		t.Fatalf("a second incident with the same title must work: %v", err)
	}
	if a.FilePath == b.FilePath || !strings.HasSuffix(b.FilePath, "-2.md") {
		t.Fatalf("expected a new name for the second incident, got %q and %q", a.FilePath, b.FilePath)
	}
	first, _ := os.ReadFile(filepath.Join(dir, a.FilePath))
	if !strings.Contains(string(first), "first") {
		t.Fatalf("the first incident file was changed: %s", first)
	}
}

// If the branch cannot be used, nothing is written and nothing is committed on another branch.
func TestPersistFailsWhenTheBranchCannotBeUsed(t *testing.T) {
	dir := newTestRepo(t)
	before := gitIn(t, dir, "rev-parse", "HEAD")
	_, err := persist(t, dir, func(r *protocol.GitPersistRequestPayload) { r.BranchName = "bad..name" })
	if err == nil {
		t.Fatal("an unusable branch name must be an error")
	}
	if gitIn(t, dir, "rev-parse", "HEAD") != before {
		t.Fatal("no commit may be made when the branch fails")
	}
	if _, statErr := os.Stat(filepath.Join(dir, ".log-analyzer")); statErr == nil {
		t.Fatal("no incident file may be written when the branch fails")
	}
}

func TestPersistUsesAnExistingBranch(t *testing.T) {
	dir := newTestRepo(t)
	gitIn(t, dir, "branch", "incident/shared")
	if _, err := persist(t, dir, func(r *protocol.GitPersistRequestPayload) { r.BranchName = "incident/shared" }); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(gitIn(t, dir, "rev-parse", "--abbrev-ref", "HEAD")); got != "incident/shared" {
		t.Fatalf("expected to be on incident/shared, on %q", got)
	}
}

// The severity goes into a file name: it must not be able to point outside the incidents folder.
func TestPersistSeverityCannotEscapeTheFolder(t *testing.T) {
	dir := newTestRepo(t)
	res, err := persist(t, dir, func(r *protocol.GitPersistRequestPayload) { r.Severity = "../../evil" })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(res.FilePath, ".log-analyzer/incidents/") || strings.Contains(res.FilePath, "..") || !strings.Contains(res.FilePath, "-P2-") {
		t.Fatalf("unsafe severity must become P2 inside the folder, got %q", res.FilePath)
	}
}
