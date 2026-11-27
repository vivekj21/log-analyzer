package main

import (
	"context"
	"flag"
	"log"
	"os"
	"strings"

	"k8s-log-analyzer/internal/creds"
	"k8s-log-analyzer/internal/llm"
	"k8s-log-analyzer/internal/toolbox"
)

// modelFlags are the settings for the model service. The command line and `serve` share them, so
// the key rules and the defaults can never drift apart.
type modelFlags struct {
	provider, baseURL, apiKeyFile, keyFile, modelName, llmMode, recordDir *string
	allowHTTP, dryRun                                                     *bool
}

func registerModelFlags(fs *flag.FlagSet) *modelFlags {
	return &modelFlags{
		provider:   fs.String("provider", envOr("LLM_PROVIDER", llm.ProviderGemini), "Model service: gemini, openai (any OpenAI-compatible server: OpenAI, Groq, OpenRouter, Mistral, Ollama ...) or anthropic (env LLM_PROVIDER)"),
		baseURL:    fs.String("base-url", os.Getenv("LLM_BASE_URL"), "Server address for --provider=openai or anthropic, e.g. https://api.groq.com/openai/v1 (env LLM_BASE_URL). https only, except localhost"),
		allowHTTP:  fs.Bool("allow-http", os.Getenv("LLM_ALLOW_HTTP") == "true", "Allow plain http to a model server on another machine (for example inside your cluster). The key and logs are then NOT encrypted in transit (env LLM_ALLOW_HTTP=true)"),
		apiKeyFile: fs.String("api-key-file", "", "Read the model API key from this file (env LLM_API_KEY_FILE does the same). Wins over the environment variables"),
		keyFile:    fs.String("gemini-key-file", "", "Same as --api-key-file, kept for existing setups; only for --provider=gemini (env GEMINI_API_KEY_FILE)"),
		modelName:  fs.String("model", "", "Model name (env LLM_MODEL). Gemini has a built-in default (env GEMINI_MODEL); openai and anthropic need one"),
		llmMode:    fs.String("llm", "live", "LLM execution mode: live, record, replay, heuristic"),
		recordDir:  fs.String("recordings-dir", "recordings", "Directory to store/read LLM recordings"),
		dryRun:     fs.Bool("dry-run", false, "Execute kubectl in dry-run mode (logs commands instead of invoking cluster)"),
	}
}

// resolved is the model setting after the model name and the key have been worked out.
type resolved struct {
	APIKey, KeySource string
	Cfg               llm.ProviderConfig
}

// resolve fills in the model name, finds the API key and checks the settings. It stops the program
// with a clear message when something is unusable.
func (m *modelFlags) resolve() resolved {
	if *m.modelName == "" {
		*m.modelName = os.Getenv("LLM_MODEL")
	}
	if *m.modelName == "" && *m.provider == llm.ProviderGemini {
		*m.modelName = llm.ModelFromEnv()
	}
	if *m.keyFile != "" && *m.provider != llm.ProviderGemini {
		log.Fatalf("--gemini-key-file only works with --provider=gemini. Use --api-key-file instead.")
	}

	// Key sources, first one that has a key wins. The Gemini-named sources only apply to the Gemini
	// provider, so a Gemini key can never be sent to another service by mistake.
	// A key file that is set but unusable stops the program; it never falls back to another source.
	warn := func(msg string) { log.Printf("[Creds] WARNING: %s", msg) }
	sources := []creds.Provider{creds.FileProvider("--api-key-file", *m.apiKeyFile, warn)}
	if *m.provider == llm.ProviderGemini {
		sources = append(sources, creds.FileProvider("--gemini-key-file", *m.keyFile, warn))
	}
	sources = append(sources, creds.FileProvider("LLM_API_KEY_FILE", os.Getenv("LLM_API_KEY_FILE"), warn))
	if *m.provider == llm.ProviderGemini {
		sources = append(sources, creds.FileProvider("GEMINI_API_KEY_FILE", os.Getenv("GEMINI_API_KEY_FILE"), warn))
	}
	sources = append(sources, creds.EnvProvider("LLM_API_KEY", os.Getenv))
	switch *m.provider {
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
		log.Printf("[Creds] API key for provider %s loaded from %s", *m.provider, keySource)
	}
	cfg := llm.ProviderConfig{Provider: *m.provider, APIKey: apiKey, Model: *m.modelName, BaseURL: *m.baseURL, AllowHTTP: *m.allowHTTP}
	if *m.allowHTTP && strings.HasPrefix(*m.baseURL, "http://") {
		log.Printf("[LLM] WARNING: plain http to %s. The key and your logs are not encrypted on the way.", *m.baseURL)
	}
	if err := cfg.Validate(); err != nil {
		log.Fatalf("Invalid model settings: %v", err)
	}
	return resolved{APIKey: apiKey, KeySource: keySource, Cfg: cfg}
}

// buildModel creates the model for the chosen mode. The returned function releases it.
func buildModel(ctx context.Context, mode string, cfg llm.ProviderConfig, registry *toolbox.ToolRegistry, recordDir string) (llm.Model, func()) {
	switch mode {
	case "heuristic":
		log.Println("[Model] Initialized rule-based Heuristic analyzer")
		return llm.NewHeuristicModel(), func() {}
	case "replay":
		log.Printf("[Model] Initialized Recorder in REPLAY mode (dir: %s)", recordDir)
		return llm.NewRecorder(llm.ModeReplay, recordDir, nil), func() {}
	case "record":
		liveClient, err := llm.NewLive(ctx, cfg, registry)
		if err != nil {
			log.Fatalf("Failed to initialize the reasoning client: %v", err)
		}
		log.Printf("[Model] Initialized Recorder in RECORD mode (dir: %s)", recordDir)
		return llm.NewRecorder(llm.ModeRecord, recordDir, llm.NewCategoryGuard(liveClient)), liveClient.Close
	case "live":
		liveClient, err := llm.NewLive(ctx, cfg, registry)
		if err != nil {
			log.Fatalf("Failed to initialize the reasoning client: %v", err)
		}
		log.Printf("[Model] Initialized live reasoning client (%s, %s)", cfg.Provider, cfg.Model)
		return llm.NewCategoryGuard(liveClient), liveClient.Close
	}
	log.Fatalf("Invalid --llm mode: %q. Must be one of: live, record, replay, heuristic", mode)
	return nil, nil
}
