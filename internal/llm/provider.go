package llm

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strings"

	"k8s-log-analyzer/internal/toolbox"
)

// Provider names accepted by --provider.
const (
	ProviderGemini    = "gemini"
	ProviderOpenAI    = "openai"    // any OpenAI-compatible service: OpenAI, Groq, OpenRouter, Mistral, Ollama, vLLM ...
	ProviderAnthropic = "anthropic" // Claude through Anthropic's own API
)

// ProviderConfig says which model service to use.
type ProviderConfig struct {
	Provider string // gemini (default), openai, anthropic
	APIKey   string
	Model    string
	BaseURL  string // openai and anthropic only; empty means the service's own address
	// AllowHTTP permits plain http to a server that is not on this machine, for example a model
	// server inside your own cluster. Off by default: the key and your logs would travel unencrypted.
	AllowHTTP bool
}

// LiveModel is a model that talks to a real service.
type LiveModel interface {
	Model
	Forgetter
	Close()
}

// Validate checks the settings before anything is sent anywhere.
func (c ProviderConfig) Validate() error {
	switch c.Provider {
	case ProviderGemini:
		if c.BaseURL != "" {
			return fmt.Errorf("--base-url is not used with --provider=gemini")
		}
	case ProviderOpenAI, ProviderAnthropic:
		if strings.TrimSpace(c.Model) == "" {
			return fmt.Errorf("--provider=%s needs --model (model names change often, so there is no built-in default)", c.Provider)
		}
		if _, err := checkBaseURL(c.baseURL(), c.AllowHTTP); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown --provider %q: use gemini, openai or anthropic", c.Provider)
	}
	return nil
}

func (c ProviderConfig) baseURL() string {
	if c.BaseURL != "" {
		return strings.TrimRight(c.BaseURL, "/")
	}
	if c.Provider == ProviderAnthropic {
		return "https://api.anthropic.com/v1"
	}
	return "https://api.openai.com/v1"
}

// checkBaseURL refuses to send the key and the logs over plain http, except to this machine
// (Ollama and similar local servers).
func checkBaseURL(raw string, allowHTTP bool) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("--base-url %q is not a valid address", raw)
	}
	switch u.Scheme {
	case "https":
		return u, nil
	case "http":
		h := u.Hostname()
		if allowHTTP || h == "localhost" || net.ParseIP(h) != nil && net.ParseIP(h).IsLoopback() {
			return u, nil
		}
		return nil, fmt.Errorf("--base-url %q uses plain http to another machine; the key and your logs would travel unencrypted. Use https, or a server on this machine (localhost), or add --allow-http if you accept that (for example a model server inside your own cluster)", raw)
	}
	return nil, fmt.Errorf("--base-url %q must start with https:// (or http://localhost)", raw)
}

// NewLive builds the model for the chosen provider.
func NewLive(ctx context.Context, cfg ProviderConfig, registry *toolbox.ToolRegistry) (LiveModel, error) {
	if cfg.Provider == "" {
		cfg.Provider = ProviderGemini
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	switch cfg.Provider {
	case ProviderOpenAI:
		return newOpenAIClient(cfg, registry), nil
	case ProviderAnthropic:
		return newAnthropicClient(cfg, registry), nil
	default:
		return NewClient(ctx, cfg.APIKey, cfg.Model, registry)
	}
}
