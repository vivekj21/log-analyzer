package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"k8s-log-analyzer/internal/agent"
	"k8s-log-analyzer/internal/creds"
	"k8s-log-analyzer/internal/llm"
	"k8s-log-analyzer/internal/protocol"
	"k8s-log-analyzer/internal/redact"
	"k8s-log-analyzer/internal/toolbox"
)

func main() {
	var (
		logFile      = flag.String("log-file", "", "Path to raw log file to analyze and stick into Git (offline or LLM-augmented)")
		namespace    = flag.String("namespace", "default", "Kubernetes namespace to investigate")
		service      = flag.String("service", "", "Target microservice/workload name (optional)")
		symptom      = flag.String("symptom", "CrashLoopBackOff and high 5xx error rate observed in logs", "Incident symptom description")
		since        = flag.String("since", "1h", "Time window for investigation logs and events (e.g., 30m, 1h, 24h)")
		provider     = flag.String("provider", envOr("LLM_PROVIDER", llm.ProviderGemini), "Model service: gemini, openai (any OpenAI-compatible server: OpenAI, Groq, OpenRouter, Mistral, Ollama ...) or anthropic (env LLM_PROVIDER)")
		baseURL      = flag.String("base-url", os.Getenv("LLM_BASE_URL"), "Server address for --provider=openai or anthropic, e.g. https://api.groq.com/openai/v1 (env LLM_BASE_URL). https only, except localhost")
		allowHTTP    = flag.Bool("allow-http", os.Getenv("LLM_ALLOW_HTTP") == "true", "Allow plain http to a model server on another machine (for example inside your cluster). The key and logs are then NOT encrypted in transit (env LLM_ALLOW_HTTP=true)")
		apiKeyFile   = flag.String("api-key-file", "", "Read the model API key from this file (env LLM_API_KEY_FILE does the same). Wins over the environment variables")
		keyFile      = flag.String("gemini-key-file", "", "Same as --api-key-file, kept for existing setups; only for --provider=gemini (env GEMINI_API_KEY_FILE)")
		modelName    = flag.String("model", "", "Model name (env LLM_MODEL). Gemini has a built-in default (env GEMINI_MODEL); openai and anthropic need one")
		llmMode      = flag.String("llm", "live", "LLM execution mode: live, record, replay, heuristic")
		recordDir    = flag.String("recordings-dir", "recordings", "Directory to store/read LLM recordings")
		caseID       = flag.String("case-id", "", "Case ID for recordings/replay naming (defaults to generated ID)")
		dryRun       = flag.Bool("dry-run", false, "Execute kubectl in dry-run mode (logs commands instead of invoking cluster)")
		timeout      = flag.Duration("timeout", 10*time.Minute, "Overall investigation timeout")
		enableGit    = flag.Bool("git", true, "Stick diagnosed issues and post-mortems into Git")
		gitRepo      = flag.String("git-repo", "", "Git repo for incident files (default: ~/log-analyzer-incidents)")
		createBranch = flag.Bool("branch", false, "Create a separate git branch for the incident (default false: commit to current branch)")
	)
	flag.Parse()

	if *modelName == "" {
		*modelName = os.Getenv("LLM_MODEL")
	}
	if *modelName == "" && *provider == llm.ProviderGemini {
		*modelName = llm.ModelFromEnv()
	}
	if *keyFile != "" && *provider != llm.ProviderGemini {
		log.Fatalf("--gemini-key-file only works with --provider=gemini. Use --api-key-file instead.")
	}

	// Key sources, first one that has a key wins. The Gemini-named sources only apply to the Gemini
	// provider, so a Gemini key can never be sent to another service by mistake.
	// A key file that is set but unusable stops the program; it never falls back to another source.
	warn := func(m string) { log.Printf("[Creds] WARNING: %s", m) }
	sources := []creds.Provider{creds.FileProvider("--api-key-file", *apiKeyFile, warn)}
	if *provider == llm.ProviderGemini {
		sources = append(sources, creds.FileProvider("--gemini-key-file", *keyFile, warn))
	}
	sources = append(sources, creds.FileProvider("LLM_API_KEY_FILE", os.Getenv("LLM_API_KEY_FILE"), warn))
	if *provider == llm.ProviderGemini {
		sources = append(sources, creds.FileProvider("GEMINI_API_KEY_FILE", os.Getenv("GEMINI_API_KEY_FILE"), warn))
	}
	sources = append(sources, creds.EnvProvider("LLM_API_KEY", os.Getenv))
	switch *provider {
	case llm.ProviderGemini:
		sources = append(sources, creds.EnvProvider("GEMINI_API_KEY", os.Getenv))
	case llm.ProviderOpenAI:
		sources = append(sources, creds.EnvProvider("OPENAI_API_KEY", os.Getenv))
	case llm.ProviderAnthropic:
		sources = append(sources, creds.EnvProvider("ANTHROPIC_API_KEY", os.Getenv))
	}
	apiKey, keySource, keyErr := creds.Resolve(sources...)
	if keyErr != nil {
		log.Fatalf("Cannot load the API key: %v", keyErr)
	}
	if apiKey != "" {
		log.Printf("[Creds] API key for provider %s loaded from %s", *provider, keySource)
	}
	cfg := llm.ProviderConfig{Provider: *provider, APIKey: apiKey, Model: *modelName, BaseURL: *baseURL, AllowHTTP: *allowHTTP}
	if *allowHTTP && strings.HasPrefix(*baseURL, "http://") {
		log.Printf("[LLM] WARNING: plain http to %s. The key and your logs are not encrypted on the way.", *baseURL)
	}
	if err := cfg.Validate(); err != nil {
		log.Fatalf("Invalid model settings: %v", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	ctx, timeoutCancel := context.WithTimeout(ctx, *timeout)
	defer timeoutCancel()

	log.Println("================================================================================")
	log.Println("      Autonomous K8s Root-Cause Deductor & Log Analysis System (Go)             ")
	log.Println("================================================================================")

	// Mode 1: Direct Raw Log File Analysis & Git Persistence
	if *logFile != "" {
		logCfg := cfg
		if *llmMode == "heuristic" || *llmMode == "replay" {
			logCfg.APIKey = "" // these modes must never send anything to a model service, key or no key
		}
		if modelFailed := handleDirectLogFile(ctx, *logFile, *gitRepo, *enableGit, *createBranch, logCfg); modelFailed {
			os.Exit(1) // the report was written, but the model analysis you asked for is missing
		}
		return
	}

	// Mode 2: Multi-Agent Kubernetes Cluster Investigation & Git Persistence
	needsAPIKey := *llmMode == "live" || *llmMode == "record"
	if needsAPIKey && apiKey == "" {
		log.Fatalf("No API key found for provider %s. Required for --llm=%s. Use --api-key-file, or set LLM_API_KEY_FILE or LLM_API_KEY (or the provider's own variable: GEMINI_API_KEY, OPENAI_API_KEY, ANTHROPIC_API_KEY), or use --llm=heuristic or --llm=replay.", *provider, *llmMode)
	}

	log.Printf("Target Namespace : %s", *namespace)
	if *service != "" {
		log.Printf("Target Service   : %s", *service)
	}
	log.Printf("Symptom          : %s", *symptom)
	log.Printf("Time Window      : %s", *since)
	log.Printf("LLM Engine / Mode: %s", *llmMode)
	if *llmMode != "heuristic" {
		log.Printf("Model            : %s (%s)", *modelName, *provider)
		log.Printf("Recordings Dir   : %s", *recordDir)
	}
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

	// 4. Model Selection (live, record, replay, heuristic)
	var model llm.Model
	switch *llmMode {
	case "heuristic":
		model = llm.NewHeuristicModel()
		log.Println("[Model] Initialized rule-based Heuristic analyzer")
	case "replay":
		model = llm.NewRecorder(llm.ModeReplay, *recordDir, nil)
		log.Printf("[Model] Initialized Recorder in REPLAY mode (dir: %s)", *recordDir)
	case "record":
		liveClient, err := llm.NewLive(ctx, cfg, toolRegistry)
		if err != nil {
			log.Fatalf("Failed to initialize the reasoning client: %v", err)
		}
		defer liveClient.Close()
		model = llm.NewRecorder(llm.ModeRecord, *recordDir, llm.NewCategoryGuard(liveClient))
		log.Printf("[Model] Initialized Recorder in RECORD mode (dir: %s)", *recordDir)
	case "live":
		liveClient, err := llm.NewLive(ctx, cfg, toolRegistry)
		if err != nil {
			log.Fatalf("Failed to initialize the reasoning client: %v", err)
		}
		defer liveClient.Close()
		model = llm.NewCategoryGuard(liveClient)
		log.Printf("[Model] Initialized live reasoning client (%s, %s)", *provider, *modelName)
	default:
		log.Fatalf("Invalid --llm mode: %q. Must be one of: live, record, replay, heuristic", *llmMode)
	}

	// 5. Instantiate and Launch 4 Agents
	reportChan := make(chan string, 1)

	policy := toolbox.NewPolicy(toolRegistry, toolbox.PolicyConfig{})
	fetcherAgent := agent.NewFetcherAgent(bus, executor, policy)
	producerAgent := agent.NewProducerAgent(bus, toolRegistry, reportChan, fetcherAgent)
	producerAgent.SetCreateBranch(*createBranch)
	producerAgent.SetGitPersistence(*enableGit, *gitRepo)

	analyzerAgent := agent.NewAnalyzerAgent(bus, model)
	producerAgent.SetCleanup(func(id string) {
		fetcherAgent.Forget(id)
		analyzerAgent.Forget(id)
		policy.ResetInvestigation(id)
	})

	gitConfig := agent.DefaultGitAgentConfig()
	gitConfig.RepoDir = *gitRepo
	gitAgent := agent.NewGitAgent(bus, gitConfig)

	go producerAgent.Run(ctx)
	go fetcherAgent.Run(ctx)
	go analyzerAgent.Run(ctx)
	go gitAgent.Run(ctx)

	// 6. Dispatch Incident Investigation Request
	investigationID := *caseID
	if investigationID == "" {
		investigationID = protocol.GenerateID()
	}
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

	// 7. Await Result or Signal Cancellation.
	// The exit code matters: a Kubernetes Job or a script must be able to tell a failed
	// investigation from a finished one.
	select {
	case <-ctx.Done():
		if ctx.Err() == context.DeadlineExceeded {
			log.Println("\n[System] Investigation timed out.")
			os.Exit(1)
		}
		log.Println("\n[System] Interrupted by user. Exiting...")
		os.Exit(130)
	case report := <-reportChan:
		fmt.Println(report)
		if strings.HasPrefix(report, "Investigation Failed") {
			os.Exit(1)
		}
	}
}

// envOr returns the environment variable's value, or def when it is unset or empty.
func envOr(name, def string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return def
}

// handleDirectLogFile analyzes one log file. It returns true if a model service was asked to help and failed:
// the report is still written (with the rule-based part and a note), but the caller must exit non-zero.
func handleDirectLogFile(ctx context.Context, filePath, repoDir string, enableGit, createBranch bool, cfg llm.ProviderConfig) (modelFailed bool) {
	log.Printf("[LogReader] Reading log file: %s", filePath)
	content, err := os.ReadFile(filePath)
	if err != nil {
		log.Fatalf("Failed to read log file %s: %v", filePath, err)
	}

	// Mask secrets and email addresses first: the text below goes to the model service (if a key is set) and into the Git report.
	rawLogs := redact.Redact(string(content))
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

	// 2. If a model service is available, enhance with LLM reasoning
	if cfg.APIKey != "" {
		log.Printf("[LLM] An API key is set: sending this log file (secrets and email addresses masked, first 50,000 characters) to %s model %s. It leaves this machine unless the server is local.", cfg.Provider, cfg.Model)
		registry := toolbox.NewToolRegistry()
		client, err := llm.NewLive(ctx, cfg, registry)
		if err != nil {
			modelFailed = true
			log.Printf("[LLM] Could not start the model client: %v", err)
			reportMarkdown += modelFailureNote(err)
		} else {
			defer client.Close()
			prompt := fmt.Sprintf("Analyze these application logs and provide deep root cause analysis and fix recommendations. No tools are available here: answer from the log text only.\n\n```\n%s\n```", rawLogs)
			if len(prompt) > 50000 {
				prompt = prompt[:50000] + "\n[truncated]\n```"
			}
			res, err := client.Analyze(ctx, "direct-log", prompt)
			// There is no cluster to look at in log-file mode. If the model asks for a tool anyway, tell it
			// so (a few times at most) and let it answer from the text.
			for round := 0; err == nil && res.HasToolCalls && round < 3; round++ {
				var results []protocol.ToolCallResult
				for _, fc := range res.FunctionCalls {
					results = append(results, protocol.ToolCallResult{ToolName: fc.Name, Error: "tools are not available when analysing a log file; answer from the log text you were given"})
				}
				res, err = client.SendToolResults(ctx, "direct-log", results)
			}
			switch {
			case err != nil:
				modelFailed = true
				log.Printf("[LLM] The model service failed: %v", err)
				reportMarkdown += modelFailureNote(err)
			case res.TextResponse == "":
				modelFailed = true
				log.Printf("[LLM] The model service returned no answer")
				reportMarkdown += modelFailureNote(fmt.Errorf("it returned no answer"))
			default:
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
	return modelFailed
}

// modelFailureNote is added to the report when the model service was asked to help and failed,
// so nobody mistakes a rule-based-only report for a model-checked one.
func modelFailureNote(err error) string {
	msg := err.Error()
	if len(msg) > 200 {
		msg = msg[:200] + "..."
	}
	return "\n\n### LLM Deep Diagnostics\nNot available: the model service failed (" + msg + "). The diagnosis above comes from the built-in rules only.\n"
}
