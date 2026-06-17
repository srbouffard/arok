package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/srbouffard/arok/internal/config"
	sessionpkg "github.com/srbouffard/arok/internal/session"
)

type Store struct {
	db *sql.DB
}

var ErrSessionNotFound = errors.New("session not found")

func Open(stateDir string) (*Store, error) {
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return nil, fmt.Errorf("create state directory: %w", err)
	}

	db, err := sql.Open("sqlite", config.DBPath(stateDir))
	if err != nil {
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}

	store := &Store{db: db}
	if err := store.init(); err != nil {
		db.Close()
		return nil, err
	}

	return store, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) UpsertSession(summary sessionpkg.SessionSummary) error {
	modelsJSON, err := json.Marshal(summary.Models)
	if err != nil {
		return fmt.Errorf("marshal models: %w", err)
	}
	subagentsJSON, err := json.Marshal(summary.Subagents)
	if err != nil {
		return fmt.Errorf("marshal subagents: %w", err)
	}
	tagsJSON, err := json.Marshal(summary.Tags)
	if err != nil {
		return fmt.Errorf("marshal tags: %w", err)
	}
	rawSummaryJSON, err := json.Marshal(summary)
	if err != nil {
		return fmt.Errorf("marshal raw summary: %w", err)
	}

	query := `
		INSERT INTO sessions (
			session_id, harness, parent_session_id, started_at, ended_at, capture_state, usage_source,
			stop_reason, host_name, cwd, repo_root, worktree_root, git_common_dir, repo_remote,
			repo_branch, repo_head, total_input_tokens, total_output_tokens, total_cache_read_tokens,
			total_cache_write_tokens, total_reasoning_tokens, models_json, subagents_json, task_id,
			title, summary, tags_json, raw_summary_json, updated_at, event_name, transcript_path,
			event_log_path, state_dir, assistant_message_count, assistant_output_tokens,
			tool_request_count, successful_tool_execution_count, interaction_count,
			shutdown_event_present, shutdown_metrics_present, collected_at, source,
			subagent_breakdown_source
		) VALUES (
			?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?
		)
		ON CONFLICT(session_id) DO UPDATE SET
			harness = excluded.harness,
			parent_session_id = excluded.parent_session_id,
			started_at = excluded.started_at,
			ended_at = excluded.ended_at,
			capture_state = excluded.capture_state,
			usage_source = excluded.usage_source,
			stop_reason = excluded.stop_reason,
			host_name = excluded.host_name,
			cwd = excluded.cwd,
			repo_root = excluded.repo_root,
			worktree_root = excluded.worktree_root,
			git_common_dir = excluded.git_common_dir,
			repo_remote = excluded.repo_remote,
			repo_branch = excluded.repo_branch,
			repo_head = excluded.repo_head,
			total_input_tokens = excluded.total_input_tokens,
			total_output_tokens = excluded.total_output_tokens,
			total_cache_read_tokens = excluded.total_cache_read_tokens,
			total_cache_write_tokens = excluded.total_cache_write_tokens,
			total_reasoning_tokens = excluded.total_reasoning_tokens,
			models_json = excluded.models_json,
			subagents_json = excluded.subagents_json,
			task_id = excluded.task_id,
			title = excluded.title,
			summary = excluded.summary,
			tags_json = excluded.tags_json,
			raw_summary_json = excluded.raw_summary_json,
			updated_at = excluded.updated_at,
			event_name = excluded.event_name,
			transcript_path = excluded.transcript_path,
			event_log_path = excluded.event_log_path,
			state_dir = excluded.state_dir,
			assistant_message_count = excluded.assistant_message_count,
			assistant_output_tokens = excluded.assistant_output_tokens,
			tool_request_count = excluded.tool_request_count,
			successful_tool_execution_count = excluded.successful_tool_execution_count,
			interaction_count = excluded.interaction_count,
			shutdown_event_present = excluded.shutdown_event_present,
			shutdown_metrics_present = excluded.shutdown_metrics_present,
			collected_at = excluded.collected_at,
			source = excluded.source,
			subagent_breakdown_source = excluded.subagent_breakdown_source
	`

	_, err = s.db.Exec(
		query,
		summary.SessionID,
		summary.Harness,
		nullableString(summary.ParentSessionID),
		nullableString(summary.StartedAt),
		nullableString(summary.EndedAt),
		summary.CaptureState,
		summary.UsageSource,
		nullableString(summary.StopReason),
		summary.HostName,
		nullableString(summary.CWD),
		nullableString(summary.RepoRoot),
		nullableString(summary.WorktreeRoot),
		nullableString(summary.GitCommonDir),
		nullableString(summary.RepoRemote),
		nullableString(summary.RepoBranch),
		nullableString(summary.RepoHead),
		nullableInt64(summary.TotalInputTokens),
		nullableInt64(summary.TotalOutputTokens),
		nullableInt64(summary.TotalCacheReadTokens),
		nullableInt64(summary.TotalCacheWriteTokens),
		nullableInt64(summary.TotalReasoningTokens),
		string(modelsJSON),
		string(subagentsJSON),
		nullableString(summary.TaskID),
		nullableString(summary.Title),
		nullableString(summary.Summary),
		string(tagsJSON),
		string(rawSummaryJSON),
		time.Now().UTC().Format(time.RFC3339Nano),
		summary.EventName,
		nullableString(summary.TranscriptPath),
		nullableString(summary.EventLogPath),
		summary.StateDir,
		summary.AssistantMessageCount,
		summary.AssistantOutputTokens,
		summary.ToolRequestCount,
		summary.SuccessfulToolExecutionCount,
		summary.InteractionCount,
		boolToInt(summary.ShutdownEventPresent),
		boolToInt(summary.ShutdownMetricsPresent),
		summary.CollectedAt,
		summary.Source,
		nullableString(summary.SubagentBreakdownSource),
	)
	if err != nil {
		return fmt.Errorf("upsert session %s: %w", summary.SessionID, err)
	}

	return nil
}

func (s *Store) GetSession(sessionID string) (sessionpkg.SessionSummary, error) {
	row := s.db.QueryRow(`SELECT raw_summary_json FROM sessions WHERE session_id = ?`, sessionID)
	var raw string
	if err := row.Scan(&raw); err != nil {
		if err == sql.ErrNoRows {
			return sessionpkg.SessionSummary{}, fmt.Errorf("%w: %s", ErrSessionNotFound, sessionID)
		}
		return sessionpkg.SessionSummary{}, err
	}

	var summary sessionpkg.SessionSummary
	if err := json.Unmarshal([]byte(raw), &summary); err != nil {
		return sessionpkg.SessionSummary{}, fmt.Errorf("decode raw summary: %w", err)
	}
	return summary, nil
}

func (s *Store) ListSessions(limit int) ([]sessionpkg.SessionListItem, error) {
	rows, err := s.db.Query(`
		SELECT session_id, harness, capture_state, usage_source, host_name, COALESCE(repo_branch, ''),
		       COALESCE(worktree_root, repo_root, ''),
		       COALESCE(total_input_tokens, 0),
		       COALESCE(total_output_tokens, assistant_output_tokens, 0),
		       COALESCE(ended_at, collected_at, '')
		FROM sessions
		ORDER BY COALESCE(ended_at, collected_at) DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []sessionpkg.SessionListItem
	for rows.Next() {
		var item sessionpkg.SessionListItem
		if err := rows.Scan(
			&item.SessionID,
			&item.Harness,
			&item.CaptureState,
			&item.UsageSource,
			&item.HostName,
			&item.RepoBranch,
			&item.WorktreeRoot,
			&item.TotalInputTokens,
			&item.TotalOutputTokens,
			&item.EndedAt,
		); err != nil {
			return nil, err
		}
		items = append(items, item)
	}

	return items, rows.Err()
}

func (s *Store) GroupTotals(groupBy string, since *time.Time, limit int) ([]sessionpkg.GroupTotal, error) {
	column, ok := map[string]string{
		"repo":     "repo_remote",
		"branch":   "repo_branch",
		"worktree": "worktree_root",
		"harness":  "harness",
		"task":     "task_id",
		"host":     "host_name",
	}[groupBy]
	if !ok {
		return nil, fmt.Errorf("unsupported grouping: %s", groupBy)
	}

	where, args := timeWindowWhere(since)
	args = append(args, limit)
	query := fmt.Sprintf(`
		SELECT COALESCE(NULLIF(%s, ''), '(none)'),
		       COUNT(*),
		       COALESCE(SUM(total_input_tokens), 0),
		       COALESCE(SUM(COALESCE(total_output_tokens, assistant_output_tokens, 0)), 0),
		       COALESCE(SUM(total_cache_read_tokens), 0),
		       COALESCE(SUM(total_cache_write_tokens), 0),
		       COALESCE(SUM(total_reasoning_tokens), 0)
		FROM sessions
		%s
		GROUP BY 1
		ORDER BY 4 DESC, 2 DESC
		LIMIT ?
	`, column, where)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var totals []sessionpkg.GroupTotal
	for rows.Next() {
		var item sessionpkg.GroupTotal
		if err := rows.Scan(
			&item.Key,
			&item.Sessions,
			&item.TotalInputTokens,
			&item.TotalOutputTokens,
			&item.TotalCacheReadTokens,
			&item.TotalCacheWriteTokens,
			&item.TotalReasoningTokens,
		); err != nil {
			return nil, err
		}
		totals = append(totals, item)
	}

	return totals, rows.Err()
}

func (s *Store) AggregateModels(since *time.Time, limit int) ([]sessionpkg.ModelTotal, error) {
	where, args := timeWindowWhere(since)
	query := `SELECT models_json FROM sessions ` + where
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	aggregate := map[string]*sessionpkg.ModelTotal{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}

		var models []sessionpkg.ModelUsage
		if err := json.Unmarshal([]byte(raw), &models); err != nil {
			return nil, err
		}
		seenInSession := map[string]struct{}{}
		for _, model := range models {
			current := aggregate[model.Model]
			if current == nil {
				current = &sessionpkg.ModelTotal{Model: model.Model}
				aggregate[model.Model] = current
			}

			if _, ok := seenInSession[model.Model]; !ok {
				current.Sessions++
				seenInSession[model.Model] = struct{}{}
			}
			current.AssistantMessages += model.AssistantMessageCount
			current.TotalInputTokens += derefInt64(model.InputTokens)
			if model.OutputTokens != nil {
				current.TotalOutputTokens += *model.OutputTokens
			} else {
				current.TotalOutputTokens += model.AssistantOutputTokens
			}
			current.TotalCacheReadTokens += derefInt64(model.CacheReadTokens)
			current.TotalCacheWriteTokens += derefInt64(model.CacheWriteTokens)
			current.TotalReasoningTokens += derefInt64(model.ReasoningTokens)
			current.RequestCount += derefInt64(model.RequestCount)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	items := make([]sessionpkg.ModelTotal, 0, len(aggregate))
	for _, item := range aggregate {
		items = append(items, *item)
	}
	sortModelTotals(items)
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

func (s *Store) Overview(since *time.Time, limit int) (sessionpkg.Overview, error) {
	where, args := timeWindowWhere(since)
	query := `
		SELECT COUNT(*),
		       COALESCE(SUM(CASE WHEN capture_state = 'final' THEN 1 ELSE 0 END), 0),
		       COALESCE(SUM(CASE WHEN capture_state = 'provisional' THEN 1 ELSE 0 END), 0),
		       COALESCE(SUM(CASE WHEN capture_state = 'best_effort' THEN 1 ELSE 0 END), 0),
		       COALESCE(SUM(total_input_tokens), 0),
		       COALESCE(SUM(COALESCE(total_output_tokens, assistant_output_tokens, 0)), 0),
		       COALESCE(SUM(total_cache_read_tokens), 0),
		       COALESCE(SUM(total_cache_write_tokens), 0),
		       COALESCE(SUM(total_reasoning_tokens), 0)
		FROM sessions
	` + where

	var overview sessionpkg.Overview
	if err := s.db.QueryRow(query, args...).Scan(
		&overview.TotalSessions,
		&overview.FinalSessions,
		&overview.ProvisionalSessions,
		&overview.BestEffortSessions,
		&overview.TotalInputTokens,
		&overview.TotalOutputTokens,
		&overview.TotalCacheReadTokens,
		&overview.TotalCacheWriteTokens,
		&overview.TotalReasoningTokens,
	); err != nil {
		return sessionpkg.Overview{}, err
	}

	var err error
	if overview.TopHosts, err = s.GroupTotals("host", since, limit); err != nil {
		return sessionpkg.Overview{}, err
	}
	if overview.TopRepos, err = s.GroupTotals("repo", since, limit); err != nil {
		return sessionpkg.Overview{}, err
	}
	if overview.TopBranches, err = s.GroupTotals("branch", since, limit); err != nil {
		return sessionpkg.Overview{}, err
	}
	if overview.TopHarnesses, err = s.GroupTotals("harness", since, limit); err != nil {
		return sessionpkg.Overview{}, err
	}
	if overview.TopModels, err = s.AggregateModels(since, limit); err != nil {
		return sessionpkg.Overview{}, err
	}
	if overview.MissingFinals, err = s.listNonFinalSessions(since, limit); err != nil {
		return sessionpkg.Overview{}, err
	}

	return overview, nil
}

func (s *Store) CountSessions() (int64, error) {
	var count int64
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func (s *Store) CountSessionsByHarness(harness string) (int64, error) {
	var count int64
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE harness = ?`, harness).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func (s *Store) CountNonFinalSessions() (int64, error) {
	var count int64
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE capture_state = ?`, sessionpkg.CaptureStateProvisional).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func (s *Store) CountBestEffortSessions() (int64, error) {
	var count int64
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE capture_state = ?`, sessionpkg.CaptureStateBestEffort).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func (s *Store) ListNonFinalSessions(limit int) ([]sessionpkg.SessionListItem, error) {
	return s.listNonFinalSessions(nil, limit)
}

func (s *Store) listNonFinalSessions(since *time.Time, limit int) ([]sessionpkg.SessionListItem, error) {
	where, args := timeWindowWhere(since)
	args = append([]any{sessionpkg.CaptureStateProvisional}, args...)
	args = append(args, limit)

	query := `
		SELECT session_id, harness, capture_state, usage_source, host_name, COALESCE(repo_branch, ''),
		       COALESCE(worktree_root, repo_root, ''),
		       COALESCE(total_input_tokens, 0),
		       COALESCE(total_output_tokens, assistant_output_tokens, 0),
		       COALESCE(ended_at, collected_at, '')
		FROM sessions
		WHERE capture_state = ?`
	if where != "" {
		query += " AND " + strings.TrimPrefix(where, "WHERE ")
	}
	query += `
		ORDER BY COALESCE(ended_at, collected_at) DESC
		LIMIT ?`

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []sessionpkg.SessionListItem
	for rows.Next() {
		var item sessionpkg.SessionListItem
		if err := rows.Scan(
			&item.SessionID,
			&item.Harness,
			&item.CaptureState,
			&item.UsageSource,
			&item.HostName,
			&item.RepoBranch,
			&item.WorktreeRoot,
			&item.TotalInputTokens,
			&item.TotalOutputTokens,
			&item.EndedAt,
		); err != nil {
			return nil, err
		}
		items = append(items, item)
	}

	return items, rows.Err()
}

// FilteredSessions returns sessions matching the filter plus an aggregate GroupTotal.
func (s *Store) FilteredSessions(f sessionpkg.SessionFilter, limit int) ([]sessionpkg.SessionListItem, sessionpkg.GroupTotal, error) {
	var conditions []string
	var args []any

	if f.Since != nil {
		conditions = append(conditions, "COALESCE(ended_at, collected_at) >= ?")
		args = append(args, f.Since.UTC().Format(time.RFC3339Nano))
	}
	if f.Host != "" {
		conditions = append(conditions, "host_name = ?")
		args = append(args, f.Host)
	}
	if f.Repo != "" {
		conditions = append(conditions, "repo_remote = ?")
		args = append(args, f.Repo)
	}
	if f.Branch != "" {
		conditions = append(conditions, "repo_branch = ?")
		args = append(args, f.Branch)
	}

	where := ""
	if len(conditions) > 0 {
		where = "WHERE " + strings.Join(conditions, " AND ")
	}

	// Aggregate totals.
	aggQuery := fmt.Sprintf(`
		SELECT COUNT(*),
		       COALESCE(SUM(total_input_tokens), 0),
		       COALESCE(SUM(COALESCE(total_output_tokens, assistant_output_tokens, 0)), 0),
		       COALESCE(SUM(total_cache_read_tokens), 0),
		       COALESCE(SUM(total_cache_write_tokens), 0),
		       COALESCE(SUM(total_reasoning_tokens), 0)
		FROM sessions %s
	`, where)
	var totals sessionpkg.GroupTotal
	if err := s.db.QueryRow(aggQuery, args...).Scan(
		&totals.Sessions,
		&totals.TotalInputTokens,
		&totals.TotalOutputTokens,
		&totals.TotalCacheReadTokens,
		&totals.TotalCacheWriteTokens,
		&totals.TotalReasoningTokens,
	); err != nil {
		return nil, sessionpkg.GroupTotal{}, err
	}

	// Session rows.
	listArgs := append(args, limit)
	listQuery := fmt.Sprintf(`
		SELECT session_id, harness, capture_state, usage_source, host_name, COALESCE(repo_branch, ''),
		       COALESCE(worktree_root, repo_root, ''),
		       COALESCE(total_input_tokens, 0),
		       COALESCE(total_output_tokens, assistant_output_tokens, 0),
		       COALESCE(ended_at, collected_at, '')
		FROM sessions
		%s
		ORDER BY COALESCE(ended_at, collected_at) DESC
		LIMIT ?
	`, where)

	rows, err := s.db.Query(listQuery, listArgs...)
	if err != nil {
		return nil, sessionpkg.GroupTotal{}, err
	}
	defer rows.Close()

	var items []sessionpkg.SessionListItem
	for rows.Next() {
		var item sessionpkg.SessionListItem
		if err := rows.Scan(
			&item.SessionID,
			&item.Harness,
			&item.CaptureState,
			&item.UsageSource,
			&item.HostName,
			&item.RepoBranch,
			&item.WorktreeRoot,
			&item.TotalInputTokens,
			&item.TotalOutputTokens,
			&item.EndedAt,
		); err != nil {
			return nil, sessionpkg.GroupTotal{}, err
		}
		items = append(items, item)
	}
	return items, totals, rows.Err()
}

func (s *Store) init() error {
	statements := []string{
		`PRAGMA journal_mode = WAL;`,
		`PRAGMA busy_timeout = 5000;`,
		`CREATE TABLE IF NOT EXISTS sessions (
			session_id TEXT PRIMARY KEY,
			harness TEXT NOT NULL,
			parent_session_id TEXT,
			started_at TEXT,
			ended_at TEXT,
			capture_state TEXT NOT NULL,
			usage_source TEXT NOT NULL,
			stop_reason TEXT,
			host_name TEXT NOT NULL,
			cwd TEXT,
			repo_root TEXT,
			worktree_root TEXT,
			git_common_dir TEXT,
			repo_remote TEXT,
			repo_branch TEXT,
			repo_head TEXT,
			total_input_tokens INTEGER,
			total_output_tokens INTEGER,
			total_cache_read_tokens INTEGER,
			total_cache_write_tokens INTEGER,
			total_reasoning_tokens INTEGER,
			models_json TEXT NOT NULL DEFAULT '[]',
			subagents_json TEXT NOT NULL DEFAULT '[]',
			task_id TEXT,
			title TEXT,
			summary TEXT,
			tags_json TEXT NOT NULL DEFAULT '[]',
			raw_summary_json TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			event_name TEXT,
			transcript_path TEXT,
			event_log_path TEXT,
			state_dir TEXT,
			assistant_message_count INTEGER NOT NULL DEFAULT 0,
			assistant_output_tokens INTEGER NOT NULL DEFAULT 0,
			tool_request_count INTEGER NOT NULL DEFAULT 0,
			successful_tool_execution_count INTEGER NOT NULL DEFAULT 0,
			interaction_count INTEGER NOT NULL DEFAULT 0,
			shutdown_event_present INTEGER NOT NULL DEFAULT 0,
			shutdown_metrics_present INTEGER NOT NULL DEFAULT 0,
			collected_at TEXT NOT NULL,
			source TEXT NOT NULL,
			subagent_breakdown_source TEXT
		);`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_host_name ON sessions (host_name);`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_ended_at ON sessions (ended_at DESC);`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_repo_remote ON sessions (repo_remote);`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_repo_branch ON sessions (repo_branch);`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_worktree_root ON sessions (worktree_root);`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_task_id ON sessions (task_id);`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_harness ON sessions (harness);`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_capture_state ON sessions (capture_state);`,
	}

	for _, statement := range statements {
		if _, err := s.db.Exec(statement); err != nil {
			return fmt.Errorf("initialize sqlite schema: %w", err)
		}
	}

	return nil
}

func timeWindowWhere(since *time.Time) (string, []any) {
	if since == nil {
		return "", nil
	}
	return "WHERE COALESCE(ended_at, collected_at) >= ?", []any{since.UTC().Format(time.RFC3339Nano)}
}

func nullableString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func nullableInt64(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func derefInt64(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}

func sortModelTotals(items []sessionpkg.ModelTotal) {
	for i := 0; i < len(items); i++ {
		for j := i + 1; j < len(items); j++ {
			if items[j].TotalOutputTokens > items[i].TotalOutputTokens {
				items[i], items[j] = items[j], items[i]
			}
		}
	}
}
