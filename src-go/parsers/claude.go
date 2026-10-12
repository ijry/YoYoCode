// Package parsers reads on-disk agent session transcripts into the neutral
// MessageTurn shape the conversation-turns endpoint serves. It mirrors the
// upstream src-tauri/src/parsers tree, one file per agent. This is a minimal,
// correct reader for the common record shapes; the long tail (sidechains,
// /clear rollover chains, compaction summaries, subagent stats, usage) is a
// staged follow-on — unparsed records are skipped rather than guessed.
package parsers

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Turn is the neutral transcript turn the handler layer converts into its wire
// MessageTurn. Role is "user" | "assistant"; Raw is the MessageTurn JSON object.
type Turn struct {
	Role     string
	TSMillis int64
	HasTS    bool
	Raw      map[string]any
}

// claudeConfigDir resolves CLAUDE_CONFIG_DIR, else ~/.claude, mirroring
// resolve_claude_config_dir.
func claudeConfigDir() string {
	if v := os.Getenv("CLAUDE_CONFIG_DIR"); v != "" {
		return v
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude")
}

// isSafeSessionID rejects path-traversal shapes in a session id that becomes a
// filename (separators, "..", drive colon, NUL).
func isSafeSessionID(id string) bool {
	if id == "" || id == "." || id == ".." {
		return false
	}
	if strings.ContainsAny(id, "/\\:\x00") || strings.Contains(id, "..") {
		return false
	}
	return true
}

// findClaudeSessionFile scans <claude>/projects/*/<sessionID>.jsonl.
func findClaudeSessionFile(sessionID string) string {
	if !isSafeSessionID(sessionID) {
		return ""
	}
	base := filepath.Join(claudeConfigDir(), "projects")
	ents, err := os.ReadDir(base)
	if err != nil {
		return ""
	}
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		cand := filepath.Join(base, e.Name(), sessionID+".jsonl")
		if st, err := os.Stat(cand); err == nil && !st.IsDir() {
			return cand
		}
	}
	return ""
}

// ReadClaudeTurns reads the Claude Code transcript for sessionID (the
// conversation external_id) and returns its user/assistant turns in order.
// Returns nil when no transcript exists on disk — the honest empty-history
// case, identical to upstream's no-JSONL path.
func ReadClaudeTurns(sessionID string) []Turn {
	path := findClaudeSessionFile(sessionID)
	if path == "" {
		return nil
	}
	return parseClaudeFile(path)
}

func parseClaudeFile(path string) []Turn {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	turns := []Turn{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024) // long JSONL lines
	idx := 0
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var rec map[string]any
		if json.Unmarshal([]byte(line), &rec) != nil {
			continue
		}
		role, _ := rec["type"].(string)
		if role != "user" && role != "assistant" {
			continue // summary / meta / synthetic — skipped in the minimal reader
		}
		msg, _ := rec["message"].(map[string]any)
		if msg == nil {
			continue
		}
		blocks := contentBlocks(msg["content"])
		if len(blocks) == 0 {
			continue // nothing renderable (e.g. a pure tool_result user echo)
		}
		id := strOf(rec["uuid"])
		if id == "" {
			id = "turn-" + itoa(idx)
		}
		ts := strOf(rec["timestamp"])
		raw := map[string]any{"id": id, "role": role, "blocks": blocks}
		if ts != "" {
			raw["timestamp"] = ts
		}
		t := Turn{Role: role, Raw: raw}
		if ts != "" {
			if parsed, err := time.Parse(time.RFC3339, ts); err == nil {
				t.TSMillis = parsed.UnixMilli()
				t.HasTS = true
			}
		}
		turns = append(turns, t)
		idx++
	}
	return turns
}

// contentBlocks normalizes a Claude message.content (string or array of typed
// blocks) into the wire ContentBlock list. Only renderable shapes are kept:
// text, thinking (as text), and tool_use (name + input preview).
func contentBlocks(content any) []map[string]any {
	out := []map[string]any{}
	switch v := content.(type) {
	case string:
		if strings.TrimSpace(v) != "" {
			out = append(out, map[string]any{"type": "text", "text": v})
		}
	case []any:
		for _, raw := range v {
			b, _ := raw.(map[string]any)
			if b == nil {
				continue
			}
			switch strOf(b["type"]) {
			case "text":
				if txt := strOf(b["text"]); txt != "" {
					out = append(out, map[string]any{"type": "text", "text": txt})
				}
			case "thinking":
				if txt := strOf(b["thinking"]); txt != "" {
					out = append(out, map[string]any{"type": "text", "text": txt})
				}
			case "tool_use":
				blk := map[string]any{"type": "tool_use", "tool_name": strOf(b["name"])}
				if id := strOf(b["id"]); id != "" {
					blk["tool_use_id"] = id
				}
				if prev := inputPreview(b["input"]); prev != "" {
					blk["input_preview"] = prev
				}
				out = append(out, blk)
			}
		}
	}
	return out
}

// inputPreview renders a tool_use input object as a compact one-line JSON
// preview, truncated for display.
func inputPreview(input any) string {
	if input == nil {
		return ""
	}
	b, err := json.Marshal(input)
	if err != nil {
		return ""
	}
	s := string(b)
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}

func strOf(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
