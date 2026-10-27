package agent

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"k8s-log-analyzer/internal/protocol"
)

// LogIssue represents an issue or anomaly figured out from logs.
type LogIssue struct {
	Title           string
	Severity        string // P0, P1, P2, P3
	ErrorSignatures []string
	Summary         string
	RootCause       string
	Recommendations []string
	SampleLines     []string
}

// GitAgentConfig controls the repository behavior of the GitAgent.
type GitAgentConfig struct {
	RepoDir       string
	IncidentsDir  string
	CreateBranch  bool
	BranchPrefix  string
	CommitAuthor  string
	CommitEmail   string
	DefaultBranch string
}

func DefaultGitAgentConfig() GitAgentConfig {
	return GitAgentConfig{
		RepoDir:       ".",
		IncidentsDir:  "incidents",
		CreateBranch:  true,
		BranchPrefix:  "incident/",
		CommitAuthor:  "Autonomous Log Analyzer Agent",
		CommitEmail:   "log-analyzer-agent@internal.local",
		DefaultBranch: "main",
	}
}

// GitAgent listens for log analysis results or raw logs and commits structured issues into Git.
type GitAgent struct {
	bus    *protocol.AgentBus
	config GitAgentConfig
}

func NewGitAgent(bus *protocol.AgentBus, config GitAgentConfig) *GitAgent {
	if config.RepoDir == "" {
		config.RepoDir = "."
	}
	if config.IncidentsDir == "" {
		config.IncidentsDir = "incidents"
	}
	if config.CommitAuthor == "" {
		config.CommitAuthor = "Autonomous Log Analyzer Agent"
	}
	if config.CommitEmail == "" {
		config.CommitEmail = "log-analyzer-agent@internal.local"
	}
	return &GitAgent{
		bus:    bus,
		config: config,
	}
}

func (g *GitAgent) Run(ctx context.Context) {
	log.Println("[GitAgent] ▶ Agent started — ready to record & stick issues into Git")
	for {
		select {
		case <-ctx.Done():
			log.Println("[GitAgent] ■ Agent shutting down")
			return
		case msg := <-g.bus.GitCh:
			g.handleMessage(ctx, msg)
		}
	}
}

func (g *GitAgent) handleMessage(ctx context.Context, msg protocol.Message) {
	if msg.Type != protocol.MsgGitPersistRequest {
		return
	}

	payload, ok := msg.Payload.(protocol.GitPersistRequestPayload)
	if !ok {
		log.Printf("[GitAgent] Invalid payload type received for investigation %s", msg.InvestigationID)
		return
	}

	res, err := g.PersistIssue(ctx, payload)
	responsePayload := protocol.GitPersistResponsePayload{
		InvestigationID: msg.InvestigationID,
	}

	if err != nil {
		responsePayload.Success = false
		responsePayload.Error = err.Error()
		log.Printf("[GitAgent] ✖ Failed to stick issue in Git: %v", err)
	} else {
		responsePayload.Success = true
		responsePayload.FilePath = res.FilePath
		responsePayload.CommitHash = res.CommitHash
		responsePayload.Branch = res.Branch
		log.Printf("[GitAgent] ✔ Stuck issue in Git: file=%s, commit=%s, branch=%s", res.FilePath, res.CommitHash, res.Branch)
	}

	g.bus.ProducerCh <- protocol.NewMessage(
		protocol.MsgGitPersistResponse,
		msg.InvestigationID,
		"git",
		"producer",
		responsePayload,
	)
}

// PersistResult contains the metadata after sticking an issue in Git.
type PersistResult struct {
	FilePath   string
	CommitHash string
	Branch     string
}

// AnalyzeRawLogs inspects a log string, extracts issues, error signatures, and deduces root causes.
func AnalyzeRawLogs(rawLogs string) LogIssue {
	scanner := bufio.NewScanner(strings.NewReader(rawLogs))

	var (
		signatures  []string
		sampleLines []string
		oomFound    bool
		panicFound  bool
		crashFound  bool
		timeoutCnt  int
		http5xxCnt  int
		deadlockCnt int
	)

	// Regex patterns for common critical issues
	panicRegex := regexp.MustCompile(`(?i)(panic:|fatal error:|stack trace:|NullPointerException|SIGSEGV|traceback \(most recent call last\))`)
	oomRegex := regexp.MustCompile(`(?i)(OutOfMemoryError|oom-killer|OOMKilled|exit code 137|memory limit exceeded|cgroup out of memory)`)
	crashRegex := regexp.MustCompile(`(?i)(CrashLoopBackOff|container died|terminated with exit code [^0]|SIGKILL|SIGTERM)`)
	timeoutRegex := regexp.MustCompile(`(?i)(timeout|timed out|context deadline exceeded|i/o timeout|connection reset|broken pipe)`)
	http5xxRegex := regexp.MustCompile(`(?i)(HTTP/?[12]?(\.[0-9])?["\s]+50[0-4]|502 Bad Gateway|504 Gateway Timeout|500 Internal Server Error)`)
	deadlockRegex := regexp.MustCompile(`(?i)(deadlock detected|lock wait timeout|database locked)`)

	for scanner.Scan() {
		line := scanner.Text()

		if panicRegex.MatchString(line) {
			panicFound = true
			if len(sampleLines) < 10 {
				sampleLines = append(sampleLines, line)
			}
			signatures = appendSignature(signatures, "Runtime Panic / Fatal Exception")
		}
		if oomRegex.MatchString(line) {
			oomFound = true
			if len(sampleLines) < 10 {
				sampleLines = append(sampleLines, line)
			}
			signatures = appendSignature(signatures, "Out Of Memory (OOMKilled / Heap Exhaustion)")
		}
		if crashRegex.MatchString(line) {
			crashFound = true
			if len(sampleLines) < 10 {
				sampleLines = append(sampleLines, line)
			}
			signatures = appendSignature(signatures, "Container Crash / CrashLoopBackOff")
		}
		if timeoutRegex.MatchString(line) {
			timeoutCnt++
			if len(sampleLines) < 10 {
				sampleLines = append(sampleLines, line)
			}
		}
		if http5xxRegex.MatchString(line) {
			http5xxCnt++
			if len(sampleLines) < 10 {
				sampleLines = append(sampleLines, line)
			}
		}
		if deadlockRegex.MatchString(line) {
			deadlockCnt++
			if len(sampleLines) < 10 {
				sampleLines = append(sampleLines, line)
			}
			signatures = appendSignature(signatures, "Database Lock / Deadlock")
		}
	}

	if timeoutCnt > 0 {
		signatures = appendSignature(signatures, fmt.Sprintf("I/O & Network Timeouts (%d detected)", timeoutCnt))
	}
	if http5xxCnt > 0 {
		signatures = appendSignature(signatures, fmt.Sprintf("HTTP 5xx Server Errors (%d detected)", http5xxCnt))
	}

	// Classify severity & deduce root cause
	issue := LogIssue{
		ErrorSignatures: signatures,
		SampleLines:     sampleLines,
	}

	switch {
	case panicFound:
		issue.Severity = "P0"
		issue.Title = "Critical Application Panic & Crash"
		issue.Summary = "Unhandled exception or kernel signal caused immediate service termination."
		issue.RootCause = "Code-level fatal exception or panic trace in the application runtime."
		issue.Recommendations = []string{
			"Inspect application stack traces for null references or illegal state transitions.",
			"Add recovery middleware / panic handlers to prevent cascading container crashes.",
			"Review recent application code deployment commits.",
		}
	case oomFound:
		issue.Severity = "P0"
		issue.Title = "Container Termination via OOM (Out Of Memory)"
		issue.Summary = "Process was terminated by Linux kernel OOM-killer or container runtime with exit code 137."
		issue.RootCause = "Application memory usage exceeded Kubernetes pod resource limits or JVM/Runtime max heap."
		issue.Recommendations = []string{
			"Increase Kubernetes memory request/limit in pod deployment specification.",
			"Check for memory leaks or unbounded in-memory caching in the target service.",
			"Configure runtime heap flags (e.g. -XX:MaxRAMPercentage for Java).",
		}
	case crashFound:
		issue.Severity = "P1"
		issue.Title = "Kubernetes Pod CrashLoopBackOff"
		issue.Summary = "Target pods are repeatedly crashing upon startup or health check failure."
		issue.RootCause = "Failing startup probe, missing environment variables, or rapid unhandled exit."
		issue.Recommendations = []string{
			"Examine pod logs from previous crashed instance using 'kubectl logs --previous'.",
			"Verify ConfigMaps, Secrets, and environmental dependencies required at boot.",
			"Check liveness and readiness probe timeouts.",
		}
	case deadlockCnt > 0:
		issue.Severity = "P1"
		issue.Title = "Database Lock Contention & Deadlocks"
		issue.Summary = "Transactions are timing out waiting for database lock acquisition."
		issue.RootCause = "Unindexed heavy queries or concurrent incompatible lock orders in persistence layer."
		issue.Recommendations = []string{
			"Analyze active transactions and locks on database server.",
			"Ensure all database transactions acquire resources in a strict consistent order.",
		}
	case http5xxCnt > 0 || timeoutCnt > 5:
		issue.Severity = "P2"
		issue.Title = "Elevated Upstream Service & Gateway Timeouts"
		issue.Summary = "Inter-service RPCs or gateway reverse proxy connections are failing or timing out."
		issue.RootCause = "Network latency, downstream service congestion, or worker thread pool saturation."
		issue.Recommendations = []string{
			"Verify downstream microservice health and database connection pools.",
			"Check network policies and DNS resolution latencies.",
			"Adjust upstream client timeouts and implement circuit breakers.",
		}
	default:
		issue.Severity = "P3"
		issue.Title = "Log Anomaly / Warning Stream Detected"
		issue.Summary = "Unusual error or warning frequency identified in application log stream."
		issue.RootCause = "Intermittent or non-fatal errors occurring during workload execution."
		issue.Recommendations = []string{
			"Monitor application metrics for progressive degradation.",
			"Review service health checks.",
		}
	}

	return issue
}

func appendSignature(list []string, sig string) []string {
	for _, item := range list {
		if item == sig {
			return list
		}
	}
	return append(list, sig)
}

// PersistIssue creates a markdown incident post-mortem and commits it into the Git repository.
func (g *GitAgent) PersistIssue(ctx context.Context, req protocol.GitPersistRequestPayload) (*PersistResult, error) {
	repoDir := req.RepoDir
	if repoDir == "" {
		repoDir = g.config.RepoDir
	}

	absRepoDir, err := filepath.Abs(repoDir)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve repo dir %s: %w", repoDir, err)
	}

	// 1. Ensure directory is a Git repository; initialize if not
	if err := ensureGitRepo(absRepoDir); err != nil {
		return nil, fmt.Errorf("git repository check/init failed: %w", err)
	}

	// 2. Derive issue details if not supplied
	title := req.Title
	severity := req.Severity
	rootCause := req.RootCause
	signatures := req.ErrorSignatures

	if req.RawLogs != "" && (title == "" || severity == "" || rootCause == "") {
		issue := AnalyzeRawLogs(req.RawLogs)
		if title == "" {
			title = issue.Title
		}
		if severity == "" {
			severity = issue.Severity
		}
		if rootCause == "" {
			rootCause = issue.RootCause
		}
		if len(signatures) == 0 {
			signatures = issue.ErrorSignatures
		}
	}

	if title == "" {
		title = fmt.Sprintf("Incident in %s", req.Namespace)
	}
	if severity == "" {
		severity = "P1"
	}

	// 3. Format markdown incident content
	now := time.Now().UTC()
	incidentID := req.InvestigationID
	if incidentID == "" {
		incidentID = protocol.GenerateID()
	}

	dateStr := now.Format("2006-01-02")
	slug := sanitizeSlug(title)
	fileName := fmt.Sprintf("%s-%s-%s.md", dateStr, severity, slug)
	if len(fileName) > 80 {
		fileName = fileName[:80] + ".md"
	}

	incidentsDir := filepath.Join(absRepoDir, g.config.IncidentsDir)
	if err := os.MkdirAll(incidentsDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create incidents directory %s: %w", incidentsDir, err)
	}

	filePath := filepath.Join(incidentsDir, fileName)
	content := buildMarkdownReport(incidentID, title, severity, now, req, rootCause, signatures)

	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		return nil, fmt.Errorf("failed to write incident file %s: %w", filePath, err)
	}

	// 4. Optionally switch to or create an incident branch
	branchName := req.BranchName
	if branchName == "" && (g.config.CreateBranch || req.CreateBranch) {
		branchName = fmt.Sprintf("%s%s-%s", g.config.BranchPrefix, dateStr, incidentID)
	}

	currentBranch, _ := getGitCurrentBranch(absRepoDir)
	if branchName != "" {
		_, _ = execGit(absRepoDir, "checkout", "-b", branchName)
	}

	// 5. Stage the incident file
	relPath, err := filepath.Rel(absRepoDir, filePath)
	if err != nil {
		relPath = filePath
	}

	if _, err := execGit(absRepoDir, "add", relPath); err != nil {
		return nil, fmt.Errorf("git add failed: %w", err)
	}

	// 6. Commit the incident with structured message
	commitMsg := fmt.Sprintf("incident(%s): %s\n\nInvestigation ID: %s\nSeverity: %s\nRoot Cause: %s\nAuto-committed by Autonomous Log Analyzer Agent",
		severity, title, incidentID, severity, rootCause)

	commitArgs := []string{
		"-c", fmt.Sprintf("user.name=%s", g.config.CommitAuthor),
		"-c", fmt.Sprintf("user.email=%s", g.config.CommitEmail),
		"commit", "-m", commitMsg,
	}

	if _, err := execGit(absRepoDir, commitArgs...); err != nil {
		return nil, fmt.Errorf("git commit failed: %w", err)
	}

	// 7. Extract commit hash
	commitHash, err := execGit(absRepoDir, "rev-parse", "HEAD")
	if err != nil {
		commitHash = "unknown"
	}

	activeBranch, err := getGitCurrentBranch(absRepoDir)
	if err != nil {
		activeBranch = currentBranch
	}

	return &PersistResult{
		FilePath:   relPath,
		CommitHash: strings.TrimSpace(commitHash),
		Branch:     activeBranch,
	}, nil
}

func buildMarkdownReport(
	id, title, severity string,
	timestamp time.Time,
	req protocol.GitPersistRequestPayload,
	rootCause string,
	signatures []string,
) string {
	var sb strings.Builder

	// YAML Frontmatter for GitOps / Static Site / Issue tracking parsers
	sb.WriteString("---\n")
	sb.WriteString(fmt.Sprintf("id: %q\n", id))
	sb.WriteString(fmt.Sprintf("title: %q\n", title))
	sb.WriteString(fmt.Sprintf("severity: %q\n", severity))
	sb.WriteString("status: \"OPEN\"\n")
	sb.WriteString(fmt.Sprintf("timestamp: %q\n", timestamp.Format(time.RFC3339)))
	sb.WriteString(fmt.Sprintf("namespace: %q\n", req.Namespace))
	if req.Service != "" {
		sb.WriteString(fmt.Sprintf("service: %q\n", req.Service))
	}
	sb.WriteString("signatures:\n")
	for _, sig := range signatures {
		sb.WriteString(fmt.Sprintf("  - %q\n", sig))
	}
	sb.WriteString("---\n\n")

	sb.WriteString(fmt.Sprintf("# Incident Postmortem: %s\n\n", title))
	sb.WriteString(fmt.Sprintf("- **Incident ID:** `%s`\n", id))
	sb.WriteString(fmt.Sprintf("- **Severity:** `%s`\n", severity))
	sb.WriteString(fmt.Sprintf("- **Reported At:** %s\n", timestamp.Format("2006-01-02 15:04:05 UTC")))
	if req.Namespace != "" {
		sb.WriteString(fmt.Sprintf("- **Kubernetes Namespace:** `%s`\n", req.Namespace))
	}
	if req.Service != "" {
		sb.WriteString(fmt.Sprintf("- **Impacted Service:** `%s`\n", req.Service))
	}
	sb.WriteString("\n---\n\n")

	sb.WriteString("## 1. Executive Summary & Root Cause\n\n")
	sb.WriteString(fmt.Sprintf("> **Root Cause:** %s\n\n", rootCause))
	if req.Symptom != "" {
		sb.WriteString(fmt.Sprintf("**Initial Symptom:** %s\n\n", req.Symptom))
	}

	if len(signatures) > 0 {
		sb.WriteString("### Detected Failure Signatures\n\n")
		for _, sig := range signatures {
			sb.WriteString(fmt.Sprintf("- ⚠️ %s\n", sig))
		}
		sb.WriteString("\n")
	}

	if req.ReportMarkdown != "" {
		sb.WriteString("## 2. Full Multi-Agent Diagnostic Report\n\n")
		sb.WriteString(req.ReportMarkdown)
		sb.WriteString("\n\n")
	}

	if req.RawLogs != "" {
		sb.WriteString("## 3. Log Excerpt Evidence\n\n")
		sb.WriteString("```text\n")
		// Truncate logs if too lengthy for markdown postmortem
		logs := req.RawLogs
		if len(logs) > 10000 {
			logs = logs[:10000] + "\n... [truncated for postmortem brevity] ..."
		}
		sb.WriteString(logs)
		if !strings.HasSuffix(logs, "\n") {
			sb.WriteString("\n")
		}
		sb.WriteString("```\n")
	}

	return sb.String()
}

func sanitizeSlug(s string) string {
	reg := regexp.MustCompile(`[^a-zA-Z0-9]+`)
	slug := strings.ToLower(reg.ReplaceAllString(s, "-"))
	slug = strings.Trim(slug, "-")
	if len(slug) > 30 {
		slug = slug[:30]
	}
	if slug == "" {
		slug = "issue"
	}
	return slug
}

func ensureGitRepo(dir string) error {
	gitDir := filepath.Join(dir, ".git")
	if _, err := os.Stat(gitDir); err == nil {
		return nil
	}

	// Initialize empty git repository
	cmd := exec.Command("git", "init")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to git init in %s: %s (%w)", dir, string(out), err)
	}
	return nil
}

func getGitCurrentBranch(dir string) (string, error) {
	out, err := execGit(dir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "main", err
	}
	return strings.TrimSpace(out), nil
}

func execGit(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("git %s failed: %s (%w)", strings.Join(args, " "), string(out), err)
	}
	return string(out), nil
}
