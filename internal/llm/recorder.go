package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"

	"k8s-log-analyzer/internal/protocol"
)

// Mode determines how Recorder handles LLM requests.
type Mode string

const (
	ModeLive   Mode = "live"
	ModeRecord Mode = "record"
	ModeReplay Mode = "replay"
)

// Recorder implements Model to record LLM interactions to disk or replay them.
type Recorder struct {
	mode    Mode
	baseDir string
	wrapped Model
	mu      sync.Mutex
	rounds  map[string]int
}

// NewRecorder creates a new recorder wrapping the target model in the given mode.
func NewRecorder(mode Mode, baseDir string, wrapped Model) *Recorder {
	if baseDir == "" {
		baseDir = "recordings"
	}
	return &Recorder{
		mode:    mode,
		baseDir: baseDir,
		wrapped: wrapped,
		rounds:  make(map[string]int),
	}
}

// nextRound increments and returns the round number for an investigation.
func (r *Recorder) nextRound(invID string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rounds[invID]++
	return r.rounds[invID]
}

// Reset clears the round counter for a specific investigation.
func (r *Recorder) Reset(invID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.rounds, invID)
}

// Round returns the current round index for an investigation.
func (r *Recorder) Round(invID string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.rounds[invID]
}

// Analyze handles the initial investigation turn.
func (r *Recorder) Analyze(ctx context.Context, invID, prompt string) (*AnalysisResult, error) {
	round := r.nextRound(invID)
	return r.handleTurn(ctx, invID, round, func() (*AnalysisResult, error) {
		if r.wrapped == nil {
			return nil, fmt.Errorf("wrapped model is nil in record/live mode")
		}
		return r.wrapped.Analyze(ctx, invID, prompt)
	})
}

// SendToolResults handles follow-up turns with tool results.
func (r *Recorder) SendToolResults(ctx context.Context, invID string, toolResults []protocol.ToolCallResult) (*AnalysisResult, error) {
	round := r.nextRound(invID)
	return r.handleTurn(ctx, invID, round, func() (*AnalysisResult, error) {
		if r.wrapped == nil {
			return nil, fmt.Errorf("wrapped model is nil in record/live mode")
		}
		return r.wrapped.SendToolResults(ctx, invID, toolResults)
	})
}

func (r *Recorder) handleTurn(ctx context.Context, invID string, round int, callUnderlying func() (*AnalysisResult, error)) (*AnalysisResult, error) {
	filePath := filepath.Join(r.baseDir, invID, fmt.Sprintf("round-%d.json", round))

	switch r.mode {
	case ModeReplay:
		data, err := os.ReadFile(filePath)
		if err != nil {
			return nil, fmt.Errorf("REPLAY FAILED: recording file missing: %s (error: %w)", filePath, err)
		}

		var result AnalysisResult
		if err := json.Unmarshal(data, &result); err != nil {
			return nil, fmt.Errorf("REPLAY FAILED: invalid JSON in recording file %s: %w", filePath, err)
		}

		log.Printf("[Recorder] Replayed case=%s round=%d from %s", invID, round, filePath)
		return &result, nil

	case ModeRecord:
		result, err := callUnderlying()
		if err != nil {
			return nil, err
		}

		if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
			return nil, fmt.Errorf("RECORD FAILED: failed to create recording directory: %w", err)
		}

		data, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("RECORD FAILED: failed to marshal recording: %w", err)
		}

		if err := os.WriteFile(filePath, data, 0644); err != nil {
			return nil, fmt.Errorf("RECORD FAILED: failed to write recording file %s: %w", filePath, err)
		}

		log.Printf("[Recorder] Recorded case=%s round=%d to %s", invID, round, filePath)
		return result, nil

	case ModeLive:
		return callUnderlying()

	default:
		return nil, fmt.Errorf("unknown recorder mode: %q", r.mode)
	}
}
