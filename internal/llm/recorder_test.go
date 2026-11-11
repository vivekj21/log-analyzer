package llm

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"k8s-log-analyzer/internal/protocol"
)

type mockModel struct {
	analyzeCalls         int
	sendToolResultsCalls int
	lastPrompt           string
}

func (m *mockModel) Analyze(ctx context.Context, invID, prompt string) (*AnalysisResult, error) {
	m.analyzeCalls++
	m.lastPrompt = prompt
	return &AnalysisResult{
		TextResponse: "Round 1 Mock Analysis",
		TotalTokens:  150,
	}, nil
}

func (m *mockModel) SendToolResults(ctx context.Context, invID string, r []protocol.ToolCallResult) (*AnalysisResult, error) {
	m.sendToolResultsCalls++
	return &AnalysisResult{
		TextResponse: "Round 2 Mock Analysis",
		TotalTokens:  250,
	}, nil
}

func TestRecorder_RecordAndReplay(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "recorder-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	mock := &mockModel{}
	caseID := "case-abc"

	// 1. Record Mode
	recorder := NewRecorder(ModeRecord, tmpDir, mock)
	ctx := context.Background()

	res1, err := recorder.Analyze(ctx, caseID, "symptom prompt")
	if err != nil {
		t.Fatalf("Analyze failed in record mode: %v", err)
	}
	if res1.TextResponse != "Round 1 Mock Analysis" {
		t.Errorf("unexpected text response: %s", res1.TextResponse)
	}

	res2, err := recorder.SendToolResults(ctx, caseID, []protocol.ToolCallResult{{ToolName: "list_pods", Result: "pod-1"}})
	if err != nil {
		t.Fatalf("SendToolResults failed in record mode: %v", err)
	}
	if res2.TextResponse != "Round 2 Mock Analysis" {
		t.Errorf("unexpected text response: %s", res2.TextResponse)
	}

	// Verify files written
	round1Path := filepath.Join(tmpDir, caseID, "round-1.json")
	round2Path := filepath.Join(tmpDir, caseID, "round-2.json")
	if _, err := os.Stat(round1Path); os.IsNotExist(err) {
		t.Fatalf("expected round 1 file to exist at %s", round1Path)
	}
	if _, err := os.Stat(round2Path); os.IsNotExist(err) {
		t.Fatalf("expected round 2 file to exist at %s", round2Path)
	}

	// 2. Replay Mode (wrapped is nil to verify real model is not called)
	replayer := NewRecorder(ModeReplay, tmpDir, nil)

	rep1, err := replayer.Analyze(ctx, caseID, "different prompt wording should not break replay")
	if err != nil {
		t.Fatalf("Analyze failed in replay mode: %v", err)
	}
	if rep1.TextResponse != "Round 1 Mock Analysis" {
		t.Errorf("replay round 1 mismatch: got %q, want %q", rep1.TextResponse, "Round 1 Mock Analysis")
	}
	if rep1.TotalTokens != 150 {
		t.Errorf("expected 150 tokens, got %d", rep1.TotalTokens)
	}

	rep2, err := replayer.SendToolResults(ctx, caseID, nil)
	if err != nil {
		t.Fatalf("SendToolResults failed in replay mode: %v", err)
	}
	if rep2.TextResponse != "Round 2 Mock Analysis" {
		t.Errorf("replay round 2 mismatch: got %q, want %q", rep2.TextResponse, "Round 2 Mock Analysis")
	}
	if rep2.TotalTokens != 250 {
		t.Errorf("expected 250 tokens, got %d", rep2.TotalTokens)
	}

	// 3. Replay missing file fails loudly
	replayerMissing := NewRecorder(ModeReplay, tmpDir, nil)
	_, err = replayerMissing.Analyze(ctx, "non-existent-case", "prompt")
	if err == nil {
		t.Fatalf("expected error when replaying non-existent case, got nil")
	}
	if !strings.Contains(err.Error(), "REPLAY FAILED") {
		t.Errorf("expected error message to contain 'REPLAY FAILED', got: %v", err)
	}
}
