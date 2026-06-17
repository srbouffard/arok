package vscode

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"
)

// DefaultUserDataDir returns the platform-appropriate VS Code user data directory.
func DefaultUserDataDir() string {
	if dir := strings.TrimSpace(os.Getenv("VSCODE_USER_DATA_DIR")); dir != "" {
		return dir
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	switch runtime.GOOS {
	case "linux":
		return filepath.Join(home, ".config", "Code", "User")
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "Code", "User")
	default:
		return ""
	}
}

// Session holds reconstructed session data from chatSessions JSONL.
type Session struct {
	SessionID       string
	WorkspaceFolder string
	CreationDate    time.Time
	Requests        []Request
}

// Request holds per-request data.
type Request struct {
	RequestID        string
	Timestamp        time.Time
	ModelID          string
	CompletionTokens int64
	PromptTokens     int64
	ElapsedMs        int64
}

// transaction represents one line of the JSONL patch log.
// K contains path segments that may be JSON strings or integers.
type transaction struct {
	Kind int               `json:"kind"`
	K    []json.RawMessage `json:"k"`
	V    json.RawMessage   `json:"v"`
	I    *int              `json:"i"` // splice index for kind 2
}

type workspaceConfig struct {
	Folder string `json:"folder"`
}

// ReadChatSession parses a chatSessions JSONL file and returns the reconstructed Session.
// The format is a patch-log: kind:0 is an initial full-document snapshot; kinds 1/2/3 are
// set, splice, and delete mutations that must be replayed in order.
func ReadChatSession(path string) (Session, error) {
	file, err := os.Open(path)
	if err != nil {
		return Session{}, err
	}
	defer file.Close()

	var state any
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}

		var txn transaction
		if err := json.Unmarshal(line, &txn); err != nil {
			return Session{}, fmt.Errorf("decode %s: %w", path, err)
		}

		switch txn.Kind {
		case 0:
			var doc any
			if err := decodeValue(txn.V, &doc); err != nil {
				return Session{}, fmt.Errorf("decode %s snapshot: %w", path, err)
			}
			state = doc
		case 1:
			if len(txn.K) > 0 {
				var value any
				if err := decodeValue(txn.V, &value); err == nil {
					state = applySet(state, txn.K, value)
				}
			}
		case 2:
			if len(txn.K) > 0 {
				var value any
				if err := decodeValue(txn.V, &value); err == nil {
					items, ok := value.([]any)
					if !ok {
						items = []any{value}
					}
					state = applySplice(state, txn.K, items, txn.I)
				}
			}
		case 3:
			if len(txn.K) > 0 {
				state = applyDelete(state, txn.K)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return Session{}, fmt.Errorf("scan %s: %w", path, err)
	}

	doc, _ := state.(map[string]any)
	session := Session{
		SessionID:       stringMapField(doc, "sessionId"),
		WorkspaceFolder: readWorkspaceFolder(path),
		CreationDate:    timeFromMillis(doc["creationDate"]),
	}

	requestsList, _ := doc["requests"].([]any)
	session.Requests = make([]Request, 0, len(requestsList))
	for _, r := range requestsList {
		item, ok := r.(map[string]any)
		if !ok {
			continue
		}
		ct, pt := extractTokens(item)
		session.Requests = append(session.Requests, Request{
			RequestID:        stringMapField(item, "requestId"),
			Timestamp:        timeFromMillis(item["timestamp"]),
			ModelID:          resolveModelID(item),
			CompletionTokens: ct,
			PromptTokens:     pt,
			ElapsedMs:        int64Value(item["elapsedMs"]),
		})
	}

	return session, nil
}

// ScanSessions finds all chatSession files under userDataDir/workspaceStorage.
func ScanSessions(userDataDir string) ([]Session, error) {
	root := filepath.Join(userDataDir, "workspaceStorage")
	if strings.TrimSpace(userDataDir) == "" {
		return nil, nil
	}
	if _, err := os.Stat(root); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	sessions := make([]Session, 0)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Ext(path) != ".jsonl" || filepath.Base(filepath.Dir(path)) != "chatSessions" {
			return nil
		}

		session, err := ReadChatSession(path)
		if err != nil {
			return nil
		}
		sessions = append(sessions, session)
		return nil
	})
	if err != nil {
		return nil, err
	}

	slices.SortFunc(sessions, func(a, b Session) int {
		if cmp := strings.Compare(a.SessionID, b.SessionID); cmp != 0 {
			return cmp
		}
		if tokensA, tokensB := totalCompletionTokens(a), totalCompletionTokens(b); tokensA != tokensB {
			if tokensA < tokensB {
				return -1
			}
			return 1
		}
		return strings.Compare(a.WorkspaceFolder, b.WorkspaceFolder)
	})
	return sessions, nil
}

func applySet(state any, path []json.RawMessage, value any) any {
	if len(path) == 0 {
		return value
	}
	seg := parsePathSegment(path[0])
	rest := path[1:]
	switch k := seg.(type) {
	case string:
		m, _ := state.(map[string]any)
		if m == nil {
			m = make(map[string]any)
		}
		m[k] = applySet(m[k], rest, value)
		return m
	case int:
		arr, _ := state.([]any)
		for len(arr) <= k {
			arr = append(arr, nil)
		}
		arr[k] = applySet(arr[k], rest, value)
		return arr
	}
	return state
}

func applySplice(state any, path []json.RawMessage, items []any, spliceIdx *int) any {
	if len(path) == 0 {
		arr, _ := state.([]any)
		if spliceIdx != nil {
			i := *spliceIdx
			if i < 0 {
				i = 0
			}
			if i > len(arr) {
				i = len(arr)
			}
			out := make([]any, 0, len(arr)+len(items))
			out = append(out, arr[:i]...)
			out = append(out, items...)
			out = append(out, arr[i:]...)
			return out
		}
		return append(arr, items...)
	}
	seg := parsePathSegment(path[0])
	rest := path[1:]
	switch k := seg.(type) {
	case string:
		m, _ := state.(map[string]any)
		if m == nil {
			m = make(map[string]any)
		}
		m[k] = applySplice(m[k], rest, items, spliceIdx)
		return m
	case int:
		arr, _ := state.([]any)
		for len(arr) <= k {
			arr = append(arr, nil)
		}
		arr[k] = applySplice(arr[k], rest, items, spliceIdx)
		return arr
	}
	return state
}

func applyDelete(state any, path []json.RawMessage) any {
	if len(path) == 0 {
		return nil
	}
	if len(path) == 1 {
		seg := parsePathSegment(path[0])
		switch k := seg.(type) {
		case string:
			m, _ := state.(map[string]any)
			if m != nil {
				delete(m, k)
			}
			return m
		case int:
			arr, _ := state.([]any)
			if arr != nil && k >= 0 && k < len(arr) {
				arr = append(arr[:k], arr[k+1:]...)
			}
			return arr
		}
		return state
	}
	seg := parsePathSegment(path[0])
	rest := path[1:]
	switch k := seg.(type) {
	case string:
		m, _ := state.(map[string]any)
		if m == nil {
			return state
		}
		m[k] = applyDelete(m[k], rest)
		return m
	case int:
		arr, _ := state.([]any)
		if arr == nil || k < 0 || k >= len(arr) {
			return state
		}
		arr[k] = applyDelete(arr[k], rest)
		return arr
	}
	return state
}

// parsePathSegment decodes a JSON path segment as either a string or an integer.
func parsePathSegment(raw json.RawMessage) any {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var i int
	if json.Unmarshal(raw, &i) == nil {
		return i
	}
	return nil
}

// decodeValue decodes JSON bytes into a Go value, using json.Number to avoid float64 precision loss.
func decodeValue(raw json.RawMessage, out *any) error {
	if raw == nil {
		return nil
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	return d.Decode(out)
}

// extractTokens returns (completionTokens, promptTokens) from a request map.
// It tries result.metadata first (primary source), then usage{}, then direct fields.
func extractTokens(req map[string]any) (completionTokens, promptTokens int64) {
	if result, ok := req["result"].(map[string]any); ok {
		if meta, ok := result["metadata"].(map[string]any); ok {
			completionTokens = int64Value(meta["outputTokens"])
			promptTokens = int64Value(meta["promptTokens"])
		}
	}
	if usage, ok := req["usage"].(map[string]any); ok {
		if completionTokens == 0 {
			completionTokens = int64Value(usage["completionTokens"])
			if completionTokens == 0 {
				completionTokens = int64Value(usage["outputTokens"])
			}
		}
		if promptTokens == 0 {
			promptTokens = int64Value(usage["promptTokens"])
		}
	}
	if completionTokens == 0 {
		completionTokens = int64Value(req["completionTokens"])
	}
	if promptTokens == 0 {
		promptTokens = int64Value(req["promptTokens"])
	}
	return
}

// resolveModelID returns the best model identifier for a request.
// It prefers result.metadata.resolvedModel (actual model) over modelId (requested model).
func resolveModelID(req map[string]any) string {
	if result, ok := req["result"].(map[string]any); ok {
		if meta, ok := result["metadata"].(map[string]any); ok {
			if m := stringMapField(meta, "resolvedModel"); m != "" {
				return m
			}
		}
	}
	return stringMapField(req, "modelId")
}

func readWorkspaceFolder(chatSessionPath string) string {
	workspaceConfigPath := filepath.Join(filepath.Dir(filepath.Dir(chatSessionPath)), "workspace.json")
	raw, err := os.ReadFile(workspaceConfigPath)
	if err != nil {
		return ""
	}

	var cfg workspaceConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return ""
	}

	return decodeWorkspaceFolder(cfg.Folder)
}

func decodeWorkspaceFolder(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.HasPrefix(raw, "vscode-remote://") {
		return ""
	}

	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "file" {
		return ""
	}

	path, err := url.PathUnescape(parsed.Path)
	if err != nil {
		return ""
	}
	if parsed.Host != "" && parsed.Host != "localhost" {
		path = string(filepath.Separator) + filepath.Join(parsed.Host, path)
	}
	return path
}

func stringMapField(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	v, ok := m[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	return s
}

func int64Value(value any) int64 {
	switch current := value.(type) {
	case int64:
		return current
	case int:
		return int64(current)
	case float64:
		return int64(current)
	case json.Number:
		parsed, err := current.Int64()
		if err == nil {
			return parsed
		}
		fallback, err := current.Float64()
		if err == nil {
			return int64(fallback)
		}
	}
	return 0
}

func timeFromMillis(value any) time.Time {
	millis := int64Value(value)
	if millis <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(millis).UTC()
}

func totalCompletionTokens(session Session) int64 {
	var total int64
	for _, req := range session.Requests {
		total += req.CompletionTokens
	}
	return total
}
