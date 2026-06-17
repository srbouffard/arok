package session

const (
	CaptureStateFinal       = "final"
	CaptureStateProvisional = "provisional"
	CaptureStateBestEffort  = "best_effort" // reconcile exhausted; best data available
	UsageSourceShutdown     = "session.shutdown.modelMetrics"
	HarnessCopilotCLI       = "copilot-cli"
	HarnessVSCodeCopilot    = "copilot-vscode"
)

type SessionSummary struct {
	SchemaVersion                int               `json:"schema_version"`
	Source                       string            `json:"source"`
	Harness                      string            `json:"harness"`
	CollectedAt                  string            `json:"collected_at"`
	SessionID                    string            `json:"session_id"`
	ParentSessionID              string            `json:"parent_session_id,omitempty"`
	EventName                    string            `json:"event_name"`
	CaptureState                 string            `json:"capture_state"`
	UsageSource                  string            `json:"usage_source"`
	StopReason                   string            `json:"stop_reason,omitempty"`
	TranscriptPath               string            `json:"transcript_path,omitempty"`
	EventLogPath                 string            `json:"event_log_path,omitempty"`
	StateDir                     string            `json:"state_dir"`
	CWD                          string            `json:"cwd,omitempty"`
	RepoRoot                     string            `json:"repo_root,omitempty"`
	WorktreeRoot                 string            `json:"worktree_root,omitempty"`
	GitCommonDir                 string            `json:"git_common_dir,omitempty"`
	RepoRemote                   string            `json:"repo_remote,omitempty"`
	RepoBranch                   string            `json:"repo_branch,omitempty"`
	RepoHead                     string            `json:"repo_head,omitempty"`
	HostName                     string            `json:"host_name"`
	StartedAt                    string            `json:"started_at,omitempty"`
	EndedAt                      string            `json:"ended_at,omitempty"`
	InteractionCount             int64             `json:"interaction_count"`
	AssistantMessageCount        int64             `json:"assistant_message_count"`
	AssistantOutputTokens        int64             `json:"assistant_output_tokens"`
	TotalInputTokens             *int64            `json:"total_input_tokens,omitempty"`
	TotalOutputTokens            *int64            `json:"total_output_tokens,omitempty"`
	TotalCacheReadTokens         *int64            `json:"total_cache_read_tokens,omitempty"`
	TotalCacheWriteTokens        *int64            `json:"total_cache_write_tokens,omitempty"`
	TotalReasoningTokens         *int64            `json:"total_reasoning_tokens,omitempty"`
	ToolRequestCount             int64             `json:"tool_request_count"`
	SuccessfulToolExecutionCount int64             `json:"successful_tool_execution_count"`
	ShutdownEventPresent         bool              `json:"shutdown_event_present"`
	ShutdownMetricsPresent       bool              `json:"shutdown_metrics_present"`
	SubagentBreakdownSource      string            `json:"subagent_breakdown_source,omitempty"`
	Models                       []ModelUsage      `json:"models"`
	Subagents                    []SubagentSummary `json:"subagent_summaries"`
	TaskID                       string            `json:"task_id,omitempty"`
	Title                        string            `json:"title,omitempty"`
	Summary                      string            `json:"summary,omitempty"`
	Tags                         []string          `json:"tags,omitempty"`
	Notes                        []string          `json:"notes,omitempty"`
}

type ModelUsage struct {
	Model                 string `json:"model"`
	AssistantMessageCount int64  `json:"assistant_message_count,omitempty"`
	AssistantOutputTokens int64  `json:"assistant_output_tokens,omitempty"`
	InputTokens           *int64 `json:"input_tokens,omitempty"`
	OutputTokens          *int64 `json:"output_tokens,omitempty"`
	CacheReadTokens       *int64 `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens      *int64 `json:"cache_write_tokens,omitempty"`
	ReasoningTokens       *int64 `json:"reasoning_tokens,omitempty"`
	RequestCount          *int64 `json:"request_count,omitempty"`
}

type SubagentSummary struct {
	AgentID          string `json:"agent_id,omitempty"`
	ToolCallID       string `json:"tool_call_id,omitempty"`
	AgentName        string `json:"agent_name,omitempty"`
	AgentDisplayName string `json:"agent_display_name,omitempty"`
	Model            string `json:"model,omitempty"`
	TotalToolCalls   int64  `json:"total_tool_calls"`
	TotalTokens      int64  `json:"total_tokens"`
	DurationMS       int64  `json:"duration_ms"`
	CompletedAt      string `json:"completed_at,omitempty"`
}

type SessionListItem struct {
	SessionID         string
	Harness           string
	CaptureState      string
	UsageSource       string
	HostName          string
	RepoBranch        string
	WorktreeRoot      string
	TotalInputTokens  int64
	TotalOutputTokens int64
	EndedAt           string
}

type GroupTotal struct {
	Key                   string
	Sessions              int64
	TotalInputTokens      int64
	TotalOutputTokens     int64
	TotalCacheReadTokens  int64
	TotalCacheWriteTokens int64
	TotalReasoningTokens  int64
}

type ModelTotal struct {
	Model                 string
	Sessions              int64
	AssistantMessages     int64
	RequestCount          int64
	TotalInputTokens      int64
	TotalOutputTokens     int64
	TotalCacheReadTokens  int64
	TotalCacheWriteTokens int64
	TotalReasoningTokens  int64
}

type Overview struct {
	TotalSessions         int64
	FinalSessions         int64
	ProvisionalSessions   int64
	BestEffortSessions    int64
	TotalInputTokens      int64
	TotalOutputTokens     int64
	TotalCacheReadTokens  int64
	TotalCacheWriteTokens int64
	TotalReasoningTokens  int64
	TopRepos              []GroupTotal
	TopBranches           []GroupTotal
	TopHarnesses          []GroupTotal
	TopModels             []ModelTotal
	MissingFinals         []SessionListItem
}

func PtrInt64(v int64) *int64 {
	return &v
}
