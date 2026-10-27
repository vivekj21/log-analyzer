// Package protocol defines the inter-agent communication primitives and
// investigation state machine for the 3-agent log analysis system.
package protocol

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

// MessageType identifies the kind of inter-agent message.
type MessageType int

const (
	MsgInvestigate MessageType = iota
	MsgFetchRequest
	MsgFetchResponse
	MsgAnalyzeRequest
	MsgAnalyzeResponse
	MsgGitPersistRequest
	MsgGitPersistResponse
	MsgError
	MsgShutdown
)

func (m MessageType) String() string {
	names := [...]string{
		"INVESTIGATE", "FETCH_REQUEST", "FETCH_RESPONSE",
		"ANALYZE_REQUEST", "ANALYZE_RESPONSE", "GIT_PERSIST_REQUEST",
		"GIT_PERSIST_RESPONSE", "ERROR", "SHUTDOWN",
	}
	if int(m) < len(names) {
		return names[m]
	}
	return fmt.Sprintf("UNKNOWN(%d)", m)
}

// InvestigationPhase tracks the lifecycle of an investigation.
type InvestigationPhase int

const (
	PhaseInit InvestigationPhase = iota
	PhaseContextGathering
	PhaseAnalysis
	PhaseFollowUp
	PhaseComplete
	PhaseFailed
)

func (p InvestigationPhase) String() string {
	names := [...]string{
		"INIT", "CONTEXT_GATHERING", "ANALYSIS",
		"FOLLOW_UP", "COMPLETE", "FAILED",
	}
	if int(p) < len(names) {
		return names[p]
	}
	return fmt.Sprintf("UNKNOWN(%d)", p)
}

// Message is the typed envelope for all inter-agent communication.
type Message struct {
	ID              string      `json:"id"`
	Type            MessageType `json:"type"`
	InvestigationID string      `json:"investigation_id"`
	From            string      `json:"from"`
	To              string      `json:"to"`
	Timestamp       time.Time   `json:"timestamp"`
	Payload         any         `json:"payload"`
}

// NewMessage creates a Message with a generated ID and current timestamp.
func NewMessage(msgType MessageType, investigationID, from, to string, payload any) Message {
	return Message{
		ID:              GenerateID(),
		Type:            msgType,
		InvestigationID: investigationID,
		From:            from,
		To:              to,
		Timestamp:       time.Now(),
		Payload:         payload,
	}
}

// InvestigatePayload starts a new investigation.
type InvestigatePayload struct {
	Symptom   string `json:"symptom"`
	Namespace string `json:"namespace"`
	Service   string `json:"service"`
	Since     string `json:"since"` // e.g. "1h", "30m"
}

// FetchRequestPayload asks the Fetcher to execute a specific allowlisted tool.
type FetchRequestPayload struct {
	ToolName   string            `json:"tool_name"`
	Parameters map[string]string `json:"parameters"`
}

// FetchResponsePayload returns the result of a sandboxed tool execution.
type FetchResponsePayload struct {
	ToolName string        `json:"tool_name"`
	Output   string        `json:"output"`
	Error    string        `json:"error,omitempty"`
	ExitCode int           `json:"exit_code"`
	Duration time.Duration `json:"duration"`
}

// AnalyzeRequestPayload sends data to the Analyzer for reasoning.
type AnalyzeRequestPayload struct {
	Symptom     string                 `json:"symptom"`
	Context     []FetchResponsePayload `json:"context"`
	IsFollowUp  bool                   `json:"is_follow_up"`
	ToolResults []ToolCallResult       `json:"tool_results,omitempty"`
}

// ToolCallResult pairs a tool name with its execution output for Gemini.
type ToolCallResult struct {
	ToolName string `json:"tool_name"`
	Result   string `json:"result"`
	Error    string `json:"error,omitempty"`
}

// AnalyzeResponsePayload carries analysis results or requests for more data.
type AnalyzeResponsePayload struct {
	ToolCalls     []FetchRequestPayload `json:"tool_calls,omitempty"`
	NeedsMoreData bool                  `json:"needs_more_data"`
	RootCause     string                `json:"root_cause,omitempty"`
	Confidence    string                `json:"confidence,omitempty"`
	Evidence      []string              `json:"evidence,omitempty"`
	Suggestions   []string              `json:"suggestions,omitempty"`
	RawAnalysis   string                `json:"raw_analysis,omitempty"`
}

// GitPersistRequestPayload asks the Git Agent to record an issue/incident into Git.
type GitPersistRequestPayload struct {
	InvestigationID string   `json:"investigation_id"`
	Title           string   `json:"title"`
	Severity        string   `json:"severity"` // e.g. "P0", "P1", "P2", "P3"
	Namespace       string   `json:"namespace,omitempty"`
	Service         string   `json:"service,omitempty"`
	Symptom         string   `json:"symptom"`
	RootCause       string   `json:"root_cause,omitempty"`
	ErrorSignatures []string `json:"error_signatures,omitempty"`
	ReportMarkdown  string   `json:"report_markdown"`
	RawLogs         string   `json:"raw_logs,omitempty"`
	RepoDir         string   `json:"repo_dir,omitempty"`
	CreateBranch    bool     `json:"create_branch"`
	BranchName      string   `json:"branch_name,omitempty"`
}

// GitPersistResponsePayload returns the result of the Git commit/issue operation.
type GitPersistResponsePayload struct {
	InvestigationID string `json:"investigation_id"`
	FilePath        string `json:"file_path"`
	CommitHash      string `json:"commit_hash"`
	Branch          string `json:"branch"`
	Success         bool   `json:"success"`
	Error           string `json:"error,omitempty"`
}

// ErrorPayload carries error details from any agent.
type ErrorPayload struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details string `json:"details,omitempty"`
}

// Investigation holds the full mutable state of an ongoing investigation.
type Investigation struct {
	mu sync.RWMutex

	ID             string                   `json:"id"`
	Phase          InvestigationPhase       `json:"phase"`
	Symptom        string                   `json:"symptom"`
	Namespace      string                   `json:"namespace"`
	Service        string                   `json:"service"`
	Since          string                   `json:"since"`
	StartedAt      time.Time                `json:"started_at"`
	CompletedAt    time.Time                `json:"completed_at,omitempty"`
	Evidence       []FetchResponsePayload   `json:"evidence"`
	Analyses       []AnalyzeResponsePayload `json:"analyses"`
	FinalReport    string                   `json:"final_report,omitempty"`
	GitCommit      string                   `json:"git_commit,omitempty"`
	GitFilePath    string                   `json:"git_file_path,omitempty"`
	pendingFetches int
	pendingResults []FetchResponsePayload
}

func NewInvestigation(id, symptom, namespace, service, since string) *Investigation {
	return &Investigation{
		ID:        id,
		Phase:     PhaseInit,
		Symptom:   symptom,
		Namespace: namespace,
		Service:   service,
		Since:     since,
		StartedAt: time.Now(),
		Evidence:  make([]FetchResponsePayload, 0),
		Analyses:  make([]AnalyzeResponsePayload, 0),
	}
}

func (inv *Investigation) SetPhase(p InvestigationPhase) {
	inv.mu.Lock()
	defer inv.mu.Unlock()
	inv.Phase = p
}

func (inv *Investigation) GetPhase() InvestigationPhase {
	inv.mu.RLock()
	defer inv.mu.RUnlock()
	return inv.Phase
}

func (inv *Investigation) AddEvidence(e FetchResponsePayload) {
	inv.mu.Lock()
	defer inv.mu.Unlock()
	inv.Evidence = append(inv.Evidence, e)
}

func (inv *Investigation) AddAnalysis(a AnalyzeResponsePayload) {
	inv.mu.Lock()
	defer inv.mu.Unlock()
	inv.Analyses = append(inv.Analyses, a)
}

func (inv *Investigation) Complete() {
	inv.mu.Lock()
	defer inv.mu.Unlock()
	inv.Phase = PhaseComplete
	inv.CompletedAt = time.Now()
}

func (inv *Investigation) Fail() {
	inv.mu.Lock()
	defer inv.mu.Unlock()
	inv.Phase = PhaseFailed
	inv.CompletedAt = time.Now()
}

func (inv *Investigation) SetPendingFetches(n int) {
	inv.mu.Lock()
	defer inv.mu.Unlock()
	inv.pendingFetches = n
	inv.pendingResults = make([]FetchResponsePayload, 0, n)
}

func (inv *Investigation) AddPendingResult(r FetchResponsePayload) (allDone bool) {
	inv.mu.Lock()
	defer inv.mu.Unlock()
	inv.pendingResults = append(inv.pendingResults, r)
	inv.pendingFetches--
	return inv.pendingFetches <= 0
}

func (inv *Investigation) DrainPendingResults() []FetchResponsePayload {
	inv.mu.Lock()
	defer inv.mu.Unlock()
	results := inv.pendingResults
	inv.pendingResults = nil
	return results
}

// AgentBus holds the buffered channels connecting the autonomous agents.
type AgentBus struct {
	ProducerCh chan Message
	FetcherCh  chan Message
	AnalyzerCh chan Message
	GitCh      chan Message
}

func NewAgentBus(bufferSize int) *AgentBus {
	return &AgentBus{
		ProducerCh: make(chan Message, bufferSize),
		FetcherCh:  make(chan Message, bufferSize),
		AnalyzerCh: make(chan Message, bufferSize),
		GitCh:      make(chan Message, bufferSize),
	}
}

func GenerateID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
