package copilot

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/srbouffard/arok/internal/gitmeta"
	sessionpkg "github.com/srbouffard/arok/internal/session"
)

type Payload map[string]any

type Meta struct {
	TaskID  string
	Title   string
	Summary string
	Tags    []string
}

type SummarizeOptions struct {
	EventName   string
	StateDir    string
	Payload     Payload
	SessionFile string
	Meta        Meta
}

type eventRecord struct {
	Type      string         `json:"type"`
	Timestamp string         `json:"timestamp"`
	AgentID   string         `json:"agentId"`
	Data      map[string]any `json:"data"`
}

func ParsePayload(raw []byte) (Payload, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()

	var payload map[string]any
	if err := decoder.Decode(&payload); err != nil {
		return nil, fmt.Errorf("parse Copilot hook payload: %w", err)
	}

	return Payload(payload), nil
}

func ResolveSessionFile(payload Payload) string {
	if transcriptPath := payload.stringValue("transcriptPath", "transcript_path"); transcriptPath != "" {
		return transcriptPath
	}

	home := os.Getenv("COPILOT_HOME")
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err == nil {
			home = filepath.Join(userHome, ".copilot")
		}
	}

	return filepath.Join(home, "session-state", payload.SessionID(), "events.jsonl")
}

func Summarize(opts SummarizeOptions) (sessionpkg.SessionSummary, error) {
	events, err := readJSONLines(opts.SessionFile)
	if err != nil {
		return sessionpkg.SessionSummary{}, fmt.Errorf("read Copilot session log: %w", err)
	}

	cwd := opts.Payload.stringValue("cwd")
	git := gitmeta.Inspect(cwd)
	hostName, _ := os.Hostname()

	var (
		startedAt                string
		endedAt                  string
		assistantMessageCount    int64
		assistantOutputTokens    int64
		toolRequestCount         int64
		successfulToolExecutions int64
		shutdownTimestamp        string
		shutdownEventPresent     bool
		shutdownMetrics          map[string]any
		interactionIDs           = map[string]struct{}{}
		modelStats               = map[string]*sessionpkg.ModelUsage{}
		subagentSummaries        []sessionpkg.SubagentSummary
	)

	for _, event := range events {
		startedAt = minTimestamp(startedAt, event.Timestamp)
		endedAt = maxTimestamp(endedAt, event.Timestamp)

		switch event.Type {
		case "assistant.message":
			model := stringValue(event.Data["model"])
			if model == "" {
				model = "unknown"
			}
			outputTokens := int64Value(event.Data["outputTokens"])
			assistantMessageCount++
			assistantOutputTokens += outputTokens
			toolRequestCount += int64(len(sliceValue(event.Data["toolRequests"])))
			if interactionID := stringValue(event.Data["interactionId"]); interactionID != "" {
				interactionIDs[interactionID] = struct{}{}
			}

			current := modelStats[model]
			if current == nil {
				current = &sessionpkg.ModelUsage{Model: model}
				modelStats[model] = current
			}
			current.AssistantMessageCount++
			current.AssistantOutputTokens += outputTokens
		case "session.shutdown":
			shutdownEventPresent = true
			if metrics := mapValue(event.Data["modelMetrics"]); metrics != nil {
				shutdownMetrics = metrics
				shutdownTimestamp = event.Timestamp
			}
		case "subagent.completed":
			subagentSummaries = append(subagentSummaries, sessionpkg.SubagentSummary{
				AgentID:          event.AgentID,
				ToolCallID:       stringValue(event.Data["toolCallId"]),
				AgentName:        stringValue(event.Data["agentName"]),
				AgentDisplayName: stringValue(event.Data["agentDisplayName"]),
				Model:            stringValue(event.Data["model"]),
				TotalToolCalls:   int64Value(event.Data["totalToolCalls"]),
				TotalTokens:      int64Value(event.Data["totalTokens"]),
				DurationMS:       int64Value(event.Data["durationMs"]),
				CompletedAt:      event.Timestamp,
			})
		case "tool.execution_complete":
			if boolValue(event.Data["success"]) {
				successfulToolExecutions++
			}
		}
	}

	var (
		totalInputTokens      *int64
		totalOutputTokens     *int64
		totalCacheReadTokens  *int64
		totalCacheWriteTokens *int64
		totalReasoningTokens  *int64
		usageSource           = "assistant.message.outputTokens"
		captureState          = sessionpkg.CaptureStateProvisional
	)

	if len(shutdownMetrics) > 0 {
		var input, output, cacheRead, cacheWrite, reasoning int64
		for model, metricAny := range shutdownMetrics {
			metric := mapValue(metricAny)
			usage := mapValue(metric["usage"])
			requests := mapValue(metric["requests"])
			current := modelStats[model]
			if current == nil {
				current = &sessionpkg.ModelUsage{Model: model}
				modelStats[model] = current
			}

			currentInput := int64Value(usage["inputTokens"])
			currentOutput := int64Value(usage["outputTokens"])
			currentCacheRead := int64Value(usage["cacheReadTokens"])
			currentCacheWrite := int64Value(usage["cacheWriteTokens"])
			currentReasoning := int64Value(usage["reasoningTokens"])
			currentRequests := int64Value(requests["count"])

			current.InputTokens = sessionpkg.PtrInt64(currentInput)
			current.OutputTokens = sessionpkg.PtrInt64(currentOutput)
			current.CacheReadTokens = sessionpkg.PtrInt64(currentCacheRead)
			current.CacheWriteTokens = sessionpkg.PtrInt64(currentCacheWrite)
			current.ReasoningTokens = sessionpkg.PtrInt64(currentReasoning)
			current.RequestCount = sessionpkg.PtrInt64(currentRequests)

			input += currentInput
			output += currentOutput
			cacheRead += currentCacheRead
			cacheWrite += currentCacheWrite
			reasoning += currentReasoning
		}

		totalInputTokens = sessionpkg.PtrInt64(input)
		totalOutputTokens = sessionpkg.PtrInt64(output)
		totalCacheReadTokens = sessionpkg.PtrInt64(cacheRead)
		totalCacheWriteTokens = sessionpkg.PtrInt64(cacheWrite)
		totalReasoningTokens = sessionpkg.PtrInt64(reasoning)
		usageSource = sessionpkg.UsageSourceShutdown
		captureState = sessionpkg.CaptureStateFinal
	} else {
		totalOutputTokens = sessionpkg.PtrInt64(assistantOutputTokens)
	}

	models := make([]sessionpkg.ModelUsage, 0, len(modelStats))
	for _, stat := range modelStats {
		models = append(models, *stat)
	}
	slices.SortFunc(models, func(a, b sessionpkg.ModelUsage) int {
		return strings.Compare(a.Model, b.Model)
	})

	if shutdownTimestamp != "" {
		endedAt = shutdownTimestamp
	}

	taskID := opts.Payload.stringValue("taskId", "task_id")
	if taskID == "" {
		taskID = opts.Meta.TaskID
	}

	title := opts.Payload.stringValue("title")
	if title == "" {
		title = opts.Meta.Title
	}

	summaryText := opts.Payload.stringValue("summary")
	if summaryText == "" {
		summaryText = opts.Meta.Summary
	}

	tags := opts.Payload.tags()
	if len(tags) == 0 {
		tags = append([]string(nil), opts.Meta.Tags...)
	}

	return sessionpkg.SessionSummary{
		SchemaVersion:                1,
		Source:                       sessionpkg.HarnessCopilotCLI,
		Harness:                      sessionpkg.HarnessCopilotCLI,
		CollectedAt:                  time.Now().UTC().Format(time.RFC3339Nano),
		SessionID:                    opts.Payload.SessionID(),
		ParentSessionID:              opts.Payload.stringValue("parentSessionId", "parent_session_id"),
		EventName:                    opts.EventName,
		CaptureState:                 captureState,
		UsageSource:                  usageSource,
		StopReason:                   opts.Payload.stringValue("stopReason", "stop_reason", "reason"),
		TranscriptPath:               opts.Payload.stringValue("transcriptPath", "transcript_path"),
		EventLogPath:                 opts.SessionFile,
		StateDir:                     opts.StateDir,
		CWD:                          cwd,
		RepoRoot:                     git.RepoRoot,
		WorktreeRoot:                 git.WorktreeRoot,
		GitCommonDir:                 git.GitCommonDir,
		RepoRemote:                   git.RepoRemote,
		RepoBranch:                   git.RepoBranch,
		RepoHead:                     git.RepoHead,
		HostName:                     hostName,
		StartedAt:                    startedAt,
		EndedAt:                      endedAt,
		InteractionCount:             int64(len(interactionIDs)),
		AssistantMessageCount:        assistantMessageCount,
		AssistantOutputTokens:        assistantOutputTokens,
		TotalInputTokens:             totalInputTokens,
		TotalOutputTokens:            totalOutputTokens,
		TotalCacheReadTokens:         totalCacheReadTokens,
		TotalCacheWriteTokens:        totalCacheWriteTokens,
		TotalReasoningTokens:         totalReasoningTokens,
		ToolRequestCount:             toolRequestCount,
		SuccessfulToolExecutionCount: successfulToolExecutions,
		ShutdownEventPresent:         shutdownEventPresent,
		ShutdownMetricsPresent:       usageSource == sessionpkg.UsageSourceShutdown,
		SubagentBreakdownSource:      subagentBreakdownSource(subagentSummaries),
		Models:                       models,
		Subagents:                    subagentSummaries,
		TaskID:                       taskID,
		Title:                        title,
		Summary:                      summaryText,
		Tags:                         tags,
		Notes: []string{
			"Copilot usage is summarized from the local events.jsonl session log.",
			"session.shutdown.modelMetrics is treated as the authoritative overall-session usage record.",
			"Repo and worktree metadata are enriched from cwd via local git inspection.",
			"Repeated session-end captures for the same session ID are intended to refresh one logical session row.",
		},
	}, nil
}

func (p Payload) SessionID() string {
	return p.stringValue("sessionId", "session_id")
}

func (p Payload) stringValue(keys ...string) string {
	for _, key := range keys {
		if value := stringValue(p[key]); value != "" {
			return value
		}
	}
	return ""
}

func (p Payload) tags() []string {
	for _, key := range []string{"tags", "LLM_USAGE_TAGS"} {
		switch value := p[key].(type) {
		case string:
			return splitTags(value)
		case []any:
			out := make([]string, 0, len(value))
			for _, item := range value {
				if tag := stringValue(item); tag != "" {
					out = append(out, tag)
				}
			}
			return out
		}
	}
	return nil
}

func readJSONLines(path string) ([]eventRecord, error) {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer file.Close()

	var events []eventRecord
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var event eventRecord
		decoder := json.NewDecoder(strings.NewReader(line))
		decoder.UseNumber()
		if err := decoder.Decode(&event); err != nil {
			continue
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return events, nil
}

func minTimestamp(current, candidate string) string {
	switch {
	case current == "":
		return candidate
	case candidate == "":
		return current
	case candidate < current:
		return candidate
	default:
		return current
	}
}

func maxTimestamp(current, candidate string) string {
	switch {
	case current == "":
		return candidate
	case candidate == "":
		return current
	case candidate > current:
		return candidate
	default:
		return current
	}
}

func stringValue(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case json.Number:
		return typed.String()
	default:
		return ""
	}
}

func int64Value(value any) int64 {
	switch typed := value.(type) {
	case nil:
		return 0
	case int:
		return int64(typed)
	case int64:
		return typed
	case float64:
		return int64(typed)
	case json.Number:
		number, err := typed.Int64()
		if err == nil {
			return number
		}
		floatValue, _ := typed.Float64()
		return int64(floatValue)
	default:
		return 0
	}
}

func boolValue(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	default:
		return false
	}
}

func mapValue(value any) map[string]any {
	typed, _ := value.(map[string]any)
	return typed
}

func sliceValue(value any) []any {
	typed, _ := value.([]any)
	return typed
}

func splitTags(value string) []string {
	if value == "" {
		return nil
	}

	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if tag := strings.TrimSpace(part); tag != "" {
			out = append(out, tag)
		}
	}
	return out
}

func subagentBreakdownSource(subagents []sessionpkg.SubagentSummary) string {
	if len(subagents) == 0 {
		return ""
	}
	return "subagent.completed"
}
