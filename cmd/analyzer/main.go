package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"k8s-log-analyzer/internal/agent"
	"k8s-log-analyzer/internal/llm"
	"k8s-log-analyzer/internal/protocol"
	"k8s-log-analyzer/internal/toolbox"
)

func main() {
	var (
		logFile   = flag.String("log-file", "", "Path to raw log file to analyze and stick into Git (offline or LLM-augmented)")
		namespace = flag.String("namespace", "default", "Kubernetes namespace to investigate")
		service   = flag.String("service", "", "Target microservice/workload name (optional)")
		symptom   = flag.String("symptom", "CrashLoopBackOff and high 5xx error rate observed in logs", "Incident symptom description")
		since     = flag.String("since", "1h", "Time window for investigation logs and events (e.g., 30m, 1h, 24h)")
		modelName = flag.String("model", "gemini-2.5-pro", "Gemini model to use for long-context log reasoning")
		dryRun    = flag.Bool("dry-run", false, "Execute kubectl in dry-run mode (logs commands instead of invoking cluster)")
		timeout      = flag.Duration("timeout", 10*time.Minute, "Overall investigation timeout")
		enableGit    = flag.Bool("git", true, "Stick diagnosed issues and post-mortems into Git")
		gitRepo      = flag.String("git-repo", ".", "Path to local Git repository root")
		createBranch = flag.Bool("branch", false, "Create a separate git branch for the incident (default false: commit to current branch)")
	)
	flag.Parse()

	apiKey := os.Getenv("GEMINI_API_KEY")

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	ctx, timeoutCancel := context.WithTimeout(ctx, *timeout)
	defer timeoutCancel()

	log.Println("================================================================================")
	log.Println("      Autonomous K8s Root-Cause Deductor & Log Analysis System (Go)             ")
	log.Println("================================================================================")

	// Mode 1: Direct Raw Log File Analysis & Git Persistence
	if *logFile != "" {
		handleDirectLogFile(ctx, *logFile, *gitRepo, *enableGit, *createBranch, apiKey, *modelName)
		return
	}

	// Mode 2: Multi-Agent Kubernetes Cluster Investigation & Git Persistence
	if apiKey == "" {
		log.Fatalf("GEMINI_API_KEY environment variable is not set. Please export GEMINI_API_KEY to run the autonomous cluster analyzer, or use --log-file to analyze a log file directly.")
	}

	log.Printf("Target Namespace : %s", *namespace)
	if *service != "" {
		log.Printf("Target Service   : %s", *service)
	}
	log.Printf("Symptom          : %s", *symptom)
	log.Printf("Time Window      : %s", *since)
	log.Printf("Gemini Model     : %s", *modelName)
	log.Printf("Dry-Run Mode     : %v", *dryRun)
	log.Printf("Git Persistence  : %v (repo: %s)", *enableGit, *gitRepo)
	log.Println("================================================================================")

	// 1. Tool Schema & Default-Deny Allowlist
	toolRegistry := toolbox.NewToolRegistry()
	log.Printf("[Security] Initialized tool allowlist (%d read-only kubectl tools)", len(toolRegistry.AllTools()))

	// 2. Sandboxed Executor (No shell, 30s timeout, 1MB output truncate)
	executor := toolbox.NewSandboxedExecutor(toolRegistry, 30*time.Second, *dryRun)

	// 3. Agent Communication Bus
	bus := protocol.NewAgentBus(100)

	// 4. Gemini Long-Context LLM Client
	geminiClient, err := llm.NewClient(ctx, apiKey, *modelName, toolRegistry)
	if err != nil {
		log.Fatalf("Failed to initialize Gemini reasoning client: %v", err)
	}
	defer geminiClient.Close()

	// 5. Instantiate and Launch 4 Agents
	reportChan := make(chan string, 1)

	producerAgent := agent.NewProducerAgent(bus, toolRegistry, reportChan)
	producerAgent.SetGitPersistence(*enableGit, *gitRepo)

	fetcherAgent := agent.NewFetcherAgent(bus, executor)
	analyzerAgent := agent.NewAnalyzerAgent(bus, geminiClient)

	gitConfig := agent.DefaultGitAgentConfig()
	gitConfig.RepoDir = *gitRepo
	gitAgent := agent.NewGitAgent(bus, gitConfig)

	go producerAgent.Run(ctx)
	go fetcherAgent.Run(ctx)
	go analyzerAgent.Run(ctx)
	go gitAgent.Run(ctx)

	// 6. Dispatch Incident Investigation Request
	investigationID := protocol.GenerateID()
	log.Printf("[System] Launching investigation %s...", investigationID)

	bus.ProducerCh <- protocol.NewMessage(
		protocol.MsgInvestigate,
		investigationID,
		"cli",
		"producer",
		protocol.InvestigatePayload{
			Symptom:   *symptom,
			Namespace: *namespace,
			Service:   *service,
			Since:     *since,
		},
	)

	// 7. Await Result or Signal Cancellation
	select {
	case <-ctx.Done():
		if ctx.Err() == context.DeadlineExceeded {
			log.Println("\n[System] Investigation timed out.")
		} else {
			log.Println("\n[System] Interrupted by user. Exiting...")
		}
	case report := <-reportChan:
		fmt.Println(report)
	}
}

func handleDirectLogFile(ctx context.Context, filePath, repoDir string, enableGit, createBranch bool, apiKey, modelName string) {
	log.Printf("[LogReader] Reading log file: %s", filePath)
	content, err := os.ReadFile(filePath)
	if err != nil {
		log.Fatalf("Failed to read log file %s: %v", filePath, err)
	}

	rawLogs := string(content)
	log.Printf("[LogReader] Ingested %d bytes of log data. Extracting issues...", len(rawLogs))

	// 1. Figure out issues using heuristic error signature clustering
	issue := agent.AnalyzeRawLogs(rawLogs)
	log.Printf("[Deductor] Figure out issue: [%s] %s", issue.Severity, issue.Title)
	log.Printf("[Deductor] Deduced Root Cause: %s", issue.RootCause)

	reportMarkdown := fmt.Sprintf(`## Autonomous Log Analysis
- **Diagnosed Issue:** %s
- **Severity:** %s
- **Root Cause Deduction:** %s

### Remediation Guidance
`, issue.Title, issue.Severity, issue.RootCause)

	for _, rec := range issue.Recommendations {
		reportMarkdown += fmt.Sprintf("1. %s\n", rec)
	}

	// 2. If Gemini is available, enhance with LLM reasoning
	if apiKey != "" {
		log.Printf("[Gemini] Enhancing analysis with Gemini model %s...", modelName)
		registry := toolbox.NewToolRegistry()
		client, err := llm.NewClient(ctx, apiKey, modelName, registry)
		if err == nil {
			defer client.Close()
			prompt := fmt.Sprintf("Analyze these application logs and provide deep root cause analysis and fix recommendations:\n\n```\n%s\n```", rawLogs)
			if len(prompt) > 50000 {
				prompt = prompt[:50000] + "\n[truncated]\n```"
			}
			res, err := client.Analyze(ctx, "direct-log", prompt)
			if err == nil && res.TextResponse != "" {
				reportMarkdown += "\n\n### LLM Deep Diagnostics\n" + res.TextResponse
			}
		}
	}

	// 3. Stick in Git
	if enableGit {
		log.Printf("[GitAgent] Sticking diagnosed issue into Git repository at %s...", repoDir)
		gitConfig := agent.DefaultGitAgentConfig()
		gitConfig.RepoDir = repoDir
		gitConfig.CreateBranch = createBranch
		gitAgent := agent.NewGitAgent(nil, gitConfig)

		req := protocol.GitPersistRequestPayload{
			InvestigationID: protocol.GenerateID(),
			Title:           issue.Title,
			Severity:        issue.Severity,
			Symptom:         fmt.Sprintf("Discovered in log file %s", filePath),
			RootCause:       issue.RootCause,
			ErrorSignatures: issue.ErrorSignatures,
			ReportMarkdown:  reportMarkdown,
			RawLogs:         rawLogs,
			RepoDir:         repoDir,
			CreateBranch:    createBranch,
		}

		res, err := gitAgent.PersistIssue(ctx, req)
		if err != nil {
			log.Fatalf("Failed to stick issue in Git: %v", err)
		}

		fmt.Println("\n╔══════════════════════════════════════════════════════════════════╗")
		fmt.Println("║       LOG ANALYSIS COMPLETE — ISSUE COMMITTED TO GIT             ║")
		fmt.Println("╚══════════════════════════════════════════════════════════════════╝")
		fmt.Printf("Incident File  : %s\n", res.FilePath)
		fmt.Printf("Git Commit SHA : %s\n", res.CommitHash)
		fmt.Printf("Git Branch     : %s\n", res.Branch)
		fmt.Printf("Severity       : %s\n", issue.Severity)
		fmt.Printf("Root Cause     : %s\n", issue.RootCause)
		fmt.Println("════════════════════════════════════════════════════════════════════")
	} else {
		fmt.Println("\n" + reportMarkdown)
	}
}
