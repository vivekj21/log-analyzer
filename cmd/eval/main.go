package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"k8s-log-analyzer/internal/agent"
	"k8s-log-analyzer/internal/llm"
	"k8s-log-analyzer/internal/protocol"
	"k8s-log-analyzer/internal/toolbox"
)

type ExpectedCase struct {
	Category    string   `json:"category"`
	MustMention []string `json:"must_mention,omitempty"`
}

type CaseResult struct {
	CaseID               string   `json:"case_id"`
	ExpectedCategory     string   `json:"expected_category"`
	GotCategory          string   `json:"got_category"`
	Passed               bool     `json:"passed"`
	MustMention          []string `json:"must_mention,omitempty"`
	MustMentionSatisfied bool     `json:"must_mention_satisfied"`
	Tokens               int64    `json:"tokens"`
	DurationSec          float64  `json:"duration_sec"`
	Rounds               int      `json:"rounds"`
	RawReport            string   `json:"raw_report,omitempty"`
	Error                string   `json:"error,omitempty"`
}

type EvalSuiteResult struct {
	Timestamp        string        `json:"timestamp"`
	GitCommit        string        `json:"git_commit"`
	Engine           string        `json:"engine"`
	TotalCases       int           `json:"total_cases"`
	PassedCases      int           `json:"passed_cases"`
	Accuracy         float64       `json:"accuracy"`
	TotalTokens      int64         `json:"total_tokens"`
	TotalDurationSec float64       `json:"total_duration_sec"`
	Cases            []*CaseResult `json:"cases"`
}

var categoryRegex = regexp.MustCompile(`(?i)ROOT_CAUSE_CATEGORY:\s*([A-Z_]+)`)

func main() {
	var (
		casesDir      = flag.String("cases-dir", "evals/cases", "Path to evaluation cases directory")
		resultsDir    = flag.String("results-dir", "evals/results", "Path to evaluation results output directory")
		recordingsDir = flag.String("recordings-dir", "recordings", "Path to recordings directory for replay/record")
		llmMode       = flag.String("llm", "compare", "Evaluation engine: heuristic, replay, live, record, or compare (baseline + Gemini)")
		modelName     = flag.String("model", "gemini-2.5-pro", "Gemini model name if running live/record")
		timeout       = flag.Duration("case-timeout", 20*time.Second, "Timeout per case evaluation")
		verbose       = flag.Bool("v", false, "Enable verbose agent logging")
	)
	flag.Parse()

	if !*verbose {
		log.SetOutput(io.Discard)
	}

	caseDirs, err := discoverCases(*casesDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error discovering cases in %s: %v\n", *casesDir, err)
		os.Exit(1)
	}
	if len(caseDirs) == 0 {
		fmt.Fprintf(os.Stderr, "No evaluation cases found in %s\n", *casesDir)
		os.Exit(1)
	}

	gitCommit := getGitCommit()

	if *llmMode == "compare" {
		// 8) Run the baseline first (heuristic), then Gemini. The gap between them is your headline number.
		fmt.Println("====================================================================================================")
		fmt.Println("                    K8S LOG ANALYZER ROOT CAUSE EVALUATION SUITE                                   ")
		fmt.Println("====================================================================================================")
		fmt.Printf("Evaluation Cases: %d in %s | Git Commit: %s\n\n", len(caseDirs), *casesDir, gitCommit)

		// 1. Baseline: Heuristic
		heuristicModel := llm.NewHeuristicModel()
		heuristicSuite := runSuite(context.Background(), "heuristic (baseline)", heuristicModel, caseDirs, *timeout, nil)
		printSuiteTable(heuristicSuite)
		saveSuiteResult(*resultsDir, gitCommit, heuristicSuite)

		// 2. Gemini: check if recordings exist for replay, or live API key
		apiKey := os.Getenv("GEMINI_API_KEY")
		var geminiModel llm.Model
		var geminiEngineName string
		var geminiRecorder *llm.Recorder

		if apiKey != "" {
			registry := toolbox.NewToolRegistry()
			geminiClient, err := llm.NewClient(context.Background(), apiKey, *modelName, registry)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Failed to initialize live Gemini client: %v\n", err)
				os.Exit(1)
			}
			defer geminiClient.Close()
			geminiRecorder = llm.NewRecorder(llm.ModeRecord, *recordingsDir, geminiClient)
			geminiModel = geminiRecorder
			geminiEngineName = fmt.Sprintf("Gemini (%s - record/live)", *modelName)
		} else {
			geminiRecorder = llm.NewRecorder(llm.ModeReplay, *recordingsDir, nil)
			geminiModel = geminiRecorder
			geminiEngineName = "Gemini (replay recordings)"
		}

		fmt.Printf("\n")
		geminiSuite := runSuite(context.Background(), geminiEngineName, geminiModel, caseDirs, *timeout, geminiRecorder)
		printSuiteTable(geminiSuite)
		saveSuiteResult(*resultsDir, gitCommit, geminiSuite)

		// Headline comparison
		printComparison(heuristicSuite, geminiSuite)
		return
	}

	// Single engine evaluation
	var model llm.Model
	var recorder *llm.Recorder
	switch *llmMode {
	case "heuristic":
		model = llm.NewHeuristicModel()
	case "replay":
		recorder = llm.NewRecorder(llm.ModeReplay, *recordingsDir, nil)
		model = recorder
	case "record":
		apiKey := os.Getenv("GEMINI_API_KEY")
		if apiKey == "" {
			fmt.Fprintln(os.Stderr, "GEMINI_API_KEY is required for --llm=record")
			os.Exit(1)
		}
		registry := toolbox.NewToolRegistry()
		client, err := llm.NewClient(context.Background(), apiKey, *modelName, registry)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to init Gemini client: %v\n", err)
			os.Exit(1)
		}
		defer client.Close()
		recorder = llm.NewRecorder(llm.ModeRecord, *recordingsDir, client)
		model = recorder
	case "live":
		apiKey := os.Getenv("GEMINI_API_KEY")
		if apiKey == "" {
			fmt.Fprintln(os.Stderr, "GEMINI_API_KEY is required for --llm=live")
			os.Exit(1)
		}
		registry := toolbox.NewToolRegistry()
		client, err := llm.NewClient(context.Background(), apiKey, *modelName, registry)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to init Gemini client: %v\n", err)
			os.Exit(1)
		}
		defer client.Close()
		model = client
	default:
		fmt.Fprintf(os.Stderr, "Invalid --llm mode: %s. Use heuristic, replay, live, record, or compare\n", *llmMode)
		os.Exit(1)
	}

	fmt.Println("====================================================================================================")
	fmt.Println("                    K8S LOG ANALYZER ROOT CAUSE EVALUATION SUITE                                   ")
	fmt.Println("====================================================================================================")
	suite := runSuite(context.Background(), *llmMode, model, caseDirs, *timeout, recorder)
	printSuiteTable(suite)
	savedPath, err := saveSuiteResult(*resultsDir, gitCommit, suite)
	if err == nil {
		fmt.Printf("Results saved to: %s\n", savedPath)
	}
	fmt.Println("====================================================================================================")
}

func discoverCases(casesDir string) ([]string, error) {
	entries, err := os.ReadDir(casesDir)
	if err != nil {
		return nil, err
	}
	var dirs []string
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, filepath.Join(casesDir, e.Name()))
		}
	}
	sort.Strings(dirs)
	return dirs, nil
}

func runSuite(ctx context.Context, engineName string, model llm.Model, caseDirs []string, timeout time.Duration, recorder *llm.Recorder) *EvalSuiteResult {
	suite := &EvalSuiteResult{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		GitCommit: getGitCommit(),
		Engine:    engineName,
		Cases:     make([]*CaseResult, 0, len(caseDirs)),
	}

	suiteStart := time.Now()
	for _, dir := range caseDirs {
		caseID := filepath.Base(dir)
		if recorder != nil {
			recorder.Reset(caseID)
		}

		res := evaluateCase(ctx, dir, caseID, model, timeout, recorder)
		if strings.Contains(strings.ToLower(engineName), "heuristic") {
			res.Tokens = 0
		}
		suite.Cases = append(suite.Cases, res)
		suite.TotalCases++
		if res.Passed {
			suite.PassedCases++
		}
		suite.TotalTokens += res.Tokens
	}
	suite.TotalDurationSec = time.Since(suiteStart).Seconds()
	if suite.TotalCases > 0 {
		suite.Accuracy = float64(suite.PassedCases) / float64(suite.TotalCases)
	}
	return suite
}

func evaluateCase(ctx context.Context, caseDir, caseID string, model llm.Model, timeout time.Duration, recorder *llm.Recorder) *CaseResult {
	start := time.Now()
	res := &CaseResult{CaseID: caseID}

	// 1. Read symptom.txt
	symptomBytes, err := os.ReadFile(filepath.Join(caseDir, "symptom.txt"))
	if err != nil {
		res.Error = fmt.Sprintf("failed to read symptom.txt: %v", err)
		res.DurationSec = time.Since(start).Seconds()
		return res
	}
	symptom := strings.TrimSpace(string(symptomBytes))

	// 2. Read expected.json
	expectedBytes, err := os.ReadFile(filepath.Join(caseDir, "expected.json"))
	if err != nil {
		res.Error = fmt.Sprintf("failed to read expected.json: %v", err)
		res.DurationSec = time.Since(start).Seconds()
		return res
	}
	var expected ExpectedCase
	if err := json.Unmarshal(expectedBytes, &expected); err != nil {
		res.Error = fmt.Sprintf("invalid expected.json: %v", err)
		res.DurationSec = time.Since(start).Seconds()
		return res
	}
	res.ExpectedCategory = expected.Category
	res.MustMention = expected.MustMention

	// 3. Read tool_outputs.json (fake cluster tools)
	outputsBytes, err := os.ReadFile(filepath.Join(caseDir, "tool_outputs.json"))
	if err != nil {
		res.Error = fmt.Sprintf("failed to read tool_outputs.json: %v", err)
		res.DurationSec = time.Since(start).Seconds()
		return res
	}
	var toolOutputs map[string]string
	if err := json.Unmarshal(outputsBytes, &toolOutputs); err != nil {
		res.Error = fmt.Sprintf("invalid tool_outputs.json: %v", err)
		res.DurationSec = time.Since(start).Seconds()
		return res
	}

	// 4. Run the multi-agent investigation pipeline with faked tools
	caseCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	bus := protocol.NewAgentBus(50)
	cannedExecutor := toolbox.NewCannedExecutor(toolOutputs)
	registry := toolbox.NewToolRegistry()
	reportCh := make(chan string, 1)

	producerAgent := agent.NewProducerAgent(bus, registry, reportCh)
	producerAgent.SetGitPersistence(false, "") // Evals never pollute git

	fetcherAgent := agent.NewFetcherAgent(bus, cannedExecutor)
	analyzerAgent := agent.NewAnalyzerAgent(bus, model)

	go producerAgent.Run(caseCtx)
	go fetcherAgent.Run(caseCtx)
	go analyzerAgent.Run(caseCtx)

	bus.ProducerCh <- protocol.NewMessage(
		protocol.MsgInvestigate,
		caseID,
		"eval-runner",
		"producer",
		protocol.InvestigatePayload{
			Symptom:   symptom,
			Namespace: "default",
			Since:     "1h",
		},
	)

	var report string
	select {
	case <-caseCtx.Done():
		res.Error = "case investigation timed out"
	case report = <-reportCh:
	}

	res.DurationSec = time.Since(start).Seconds()
	res.RawReport = report

	// 5. Grade: plain string compare on ROOT_CAUSE_CATEGORY: <CATEGORY>
	gotCategory := extractCategory(report)
	res.GotCategory = gotCategory
	res.Passed = strings.EqualFold(gotCategory, expected.Category)

	// Check must_mention requirements
	reportLower := strings.ToLower(report)
	mustMentionSatisfied := true
	for _, phrase := range expected.MustMention {
		if !strings.Contains(reportLower, strings.ToLower(phrase)) {
			mustMentionSatisfied = false
			break
		}
	}
	res.MustMentionSatisfied = mustMentionSatisfied

	// Compute rounds & tokens
	if recorder != nil {
		res.Rounds = recorder.Round(caseID)
	} else if res.Rounds == 0 {
		res.Rounds = 1
	}

	res.Tokens = estimateTokens(report, res.Rounds)
	return res
}

func extractCategory(report string) string {
	matches := categoryRegex.FindStringSubmatch(report)
	if len(matches) > 1 {
		return strings.ToUpper(strings.TrimSpace(matches[1]))
	}

	// Fallback to checking category names directly in report
	categories := []string{
		llm.CategoryOOMKilled,
		llm.CategoryCrashLoopBadConfig,
		llm.CategoryImagePullFail,
		llm.CategoryReadinessProbeFail,
		llm.CategoryDBLock,
		llm.CategoryNoFault,
	}
	for _, cat := range categories {
		if strings.Contains(report, "ROOT_CAUSE_CATEGORY: "+cat) || strings.Contains(report, cat) {
			return cat
		}
	}
	return "UNKNOWN"
}

func estimateTokens(report string, rounds int) int64 {
	// If zero or unrecorded, rough estimation (4 chars per token)
	if report == "" {
		return 0
	}
	return int64(len(report)/4) + int64(rounds*200)
}

func printSuiteTable(s *EvalSuiteResult) {
	fmt.Printf("Engine / Model : %s\n", s.Engine)
	fmt.Printf("Cases Evaluated: %d\n", s.TotalCases)
	fmt.Println("----------------------------------------------------------------------------------------------------")
	fmt.Printf("%-22s %-22s %-22s %-8s %-8s %-8s %-6s\n",
		"CASE", "EXPECTED", "GOT", "STATUS", "TOKENS", "TIME", "ROUNDS")
	fmt.Println("----------------------------------------------------------------------------------------------------")

	for _, c := range s.Cases {
		status := "FAIL"
		if c.Passed {
			status = "PASS"
		}
		got := c.GotCategory
		if got == "" {
			got = "ERROR"
		}
		timeStr := fmt.Sprintf("%.2fs", c.DurationSec)
		fmt.Printf("%-22s %-22s %-22s %-8s %-8d %-8s %-6d\n",
			truncate(c.CaseID, 22),
			truncate(c.ExpectedCategory, 22),
			truncate(got, 22),
			status,
			c.Tokens,
			timeStr,
			c.Rounds,
		)
	}

	fmt.Println("----------------------------------------------------------------------------------------------------")
	fmt.Printf("ACCURACY: %d / %d (%.1f%%) | TOTAL TIME: %.2fs | TOTAL TOKENS: %d\n",
		s.PassedCases, s.TotalCases, s.Accuracy*100.0, s.TotalDurationSec, s.TotalTokens)
	fmt.Println("----------------------------------------------------------------------------------------------------")
}

func printComparison(baseline, ai *EvalSuiteResult) {
	gap := (ai.Accuracy - baseline.Accuracy) * 100.0
	gapSign := "+"
	if gap < 0 {
		gapSign = ""
	}

	fmt.Println("\n====================================================================================================")
	fmt.Println("                               HEADLINE EVALUATION RESULTS                                          ")
	fmt.Println("====================================================================================================")
	fmt.Printf(" Baseline (Heuristic Rules) : %.1f%% accuracy (%d/%d cases)\n", baseline.Accuracy*100, baseline.PassedCases, baseline.TotalCases)
	fmt.Printf(" AI Reasoner (%s) : %.1f%% accuracy (%d/%d cases)\n", ai.Engine, ai.Accuracy*100, ai.PassedCases, ai.TotalCases)
	fmt.Printf(" Headline Gap              : %s%.1f%% accuracy over baseline\n", gapSign, gap)
	fmt.Printf(" Heuristic Cost            : $0.00 (0 tokens, %.2fs)\n", baseline.TotalDurationSec)
	fmt.Printf(" AI Cost                   : %d tokens (%.2fs)\n", ai.TotalTokens, ai.TotalDurationSec)
	fmt.Println("====================================================================================================")
}

func saveSuiteResult(resultsDir, gitCommit string, suite *EvalSuiteResult) (string, error) {
	if err := os.MkdirAll(resultsDir, 0755); err != nil {
		return "", err
	}
	dateStr := time.Now().Format("2006-01-02-150405")
	cleanEngine := strings.ReplaceAll(strings.ToLower(suite.Engine), " ", "-")
	cleanEngine = strings.ReplaceAll(cleanEngine, "/", "-")
	cleanEngine = strings.ReplaceAll(cleanEngine, "(", "")
	cleanEngine = strings.ReplaceAll(cleanEngine, ")", "")
	filename := fmt.Sprintf("%s-%s-%s.json", dateStr, gitCommit, cleanEngine)
	fullPath := filepath.Join(resultsDir, filename)

	data, err := json.MarshalIndent(suite, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(fullPath, data, 0644); err != nil {
		return "", err
	}
	return fullPath, nil
}

func getGitCommit() string {
	cmd := exec.Command("git", "rev-parse", "--short", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}

func truncate(s string, max int) string {
	if len(s) > max {
		return s[:max-3] + "..."
	}
	return s
}
