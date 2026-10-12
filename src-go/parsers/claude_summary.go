package parsers

// Claude session enumeration for the import picker (upstream
// parsers/claude.rs list_conversations + parse_jsonl_summary). Walks
// <claude>/projects/*/<uuid>.jsonl and extracts one ConversationSummary per
// transcript: session id, cwd (import target folder), title, timestamps,
// message count, model, git branch. Delegation children (those carrying a
// parentUuid-style link) are skipped by the caller via ParentID.

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ConversationSummary mirrors models::conversation::ConversationSummary (the
// fields the import picker needs). Timestamps are RFC3339 strings.
type ConversationSummary struct {
	ID           string
	AgentType    string
	FolderPath   string
	FolderName   string
	Title        string
	StartedAt    string
	EndedAt      string
	MessageCount int
	Model        string
	GitBranch    string
	ParentID     string
}

// ListClaudeConversations enumerates Claude Code transcripts, newest first.
func ListClaudeConversations() []ConversationSummary {
	base := filepath.Join(claudeConfigDir(), "projects")
	out := []ConversationSummary{}
	ents, err := os.ReadDir(base)
	if err != nil {
		return out
	}
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		projectDir := filepath.Join(base, e.Name())
		files, ferr := os.ReadDir(projectDir)
		if ferr != nil {
			continue
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".jsonl") {
				continue
			}
			path := filepath.Join(projectDir, f.Name())
			if s, ok := parseClaudeSummary(path); ok {
				if s.FolderPath == "" {
					s.FolderPath = decodeClaudeFolderPath(e.Name())
					s.FolderName = folderNameFromPath(s.FolderPath)
				}
				out = append(out, s)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].StartedAt > out[j].StartedAt })
	return out
}

func decodeClaudeFolderPath(encoded string) string { return strings.ReplaceAll(encoded, "-", "/") }

func folderNameFromPath(p string) string {
	i := strings.LastIndexAny(p, "/\\")
	if i < 0 {
		return p
	}
	return p[i+1:]
}

// parseClaudeSummary mirrors parse_jsonl_summary (the picker-relevant subset).
func parseClaudeSummary(path string) (ConversationSummary, bool) {
	file, err := os.Open(path)
	if err != nil {
		return ConversationSummary{}, false
	}
	defer file.Close()

	var id, cwd, gitBranch, model, firstUserTitle, aiTitle, customTitle string
	var parentID string
	var firstTS, lastTS string
	msgCount := 0

	sc := bufio.NewScanner(file)
	sc.Buffer(make([]byte, 0, 1024*1024), 16*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var v map[string]any
		if json.Unmarshal([]byte(line), &v) != nil {
			continue
		}
		msgType, _ := v["type"].(string)
		if msgType == "file-history-snapshot" || msgType == "progress" {
			continue
		}
		if claudeIsMeta(v) {
			continue
		}
		// title records: a /rename custom title and Claude's own generated title.
		if msgType == "summary" {
			if s, ok := v["summary"].(string); ok && s != "" {
				aiTitle = s
			}
		}
		if ct, ok := v["customTitle"].(string); ok && ct != "" {
			customTitle = ct
		}
		if id == "" {
			if s, ok := v["sessionId"].(string); ok {
				id = s
			}
		}
		if cwd == "" {
			if s, ok := v["cwd"].(string); ok {
				cwd = s
			}
		}
		if gitBranch == "" {
			if s, ok := v["gitBranch"].(string); ok {
				gitBranch = s
			}
		}
		if parentID == "" {
			if s, ok := v["parentUuid"].(string); ok && s != "" {
				// Only a dedicated delegation link marks a child; parentUuid is
				// the normal transcript chain, so it does NOT mark a child here.
				_ = s
			}
		}
		if ts, ok := v["timestamp"].(string); ok && ts != "" {
			if firstTS == "" {
				firstTS = ts
			}
			lastTS = ts
		}
		if msgType == "user" || msgType == "assistant" {
			msgCount++
			if msgType == "assistant" && model == "" {
				if m, ok := v["message"].(map[string]any); ok {
					if md, ok := m["model"].(string); ok {
						model = md
					}
				}
			}
			if msgType == "user" && firstUserTitle == "" {
				if txt := claudeExtractUserText(v); txt != "" {
					firstUserTitle = claudeTitleFromText(txt)
				}
			}
		}
	}

	if firstTS == "" {
		return ConversationSummary{}, false
	}
	if id == "" {
		id = strings.TrimSuffix(filepath.Base(path), ".jsonl")
	}
	title := customTitle
	if title == "" {
		title = aiTitle
	}
	if title == "" {
		title = firstUserTitle
	}
	s := ConversationSummary{
		ID:           id,
		AgentType:    "claude_code",
		FolderPath:   cwd,
		Title:        title,
		StartedAt:    firstTS,
		EndedAt:      lastTS,
		MessageCount: msgCount,
		Model:        model,
		GitBranch:    gitBranch,
		ParentID:     parentID,
	}
	if cwd != "" {
		s.FolderName = folderNameFromPath(cwd)
	}
	return s, true
}

func claudeIsMeta(v map[string]any) bool {
	if b, ok := v["isMeta"].(bool); ok && b {
		return true
	}
	return false
}

func claudeExtractUserText(v map[string]any) string {
	msg, ok := v["message"].(map[string]any)
	if !ok {
		return ""
	}
	switch c := msg["content"].(type) {
	case string:
		return strings.TrimSpace(c)
	case []any:
		for _, blk := range c {
			if m, ok := blk.(map[string]any); ok {
				if m["type"] == "text" {
					if t, ok := m["text"].(string); ok && strings.TrimSpace(t) != "" {
						return strings.TrimSpace(t)
					}
				}
			}
		}
	}
	return ""
}

// claudeTitleFromText truncates to 100 runes (+ "..." when cut), mirroring
// title_from_user_text's cap (link-folding is omitted — a cosmetic refinement).
func claudeTitleFromText(text string) string {
	t := strings.TrimSpace(text)
	r := []rune(t)
	if len(r) <= 100 {
		return t
	}
	return string(r[:100]) + "..."
}

// claudeParseTime is a small helper for callers needing a time.Time.
func claudeParseTime(s string) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}
