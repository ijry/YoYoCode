package handlers

// get_folder_conversation_turns (the reverse-infinite-scroll history page) and
// the turn-window algorithm ported from commands/turn_window.rs: FNV-1a prefix
// fingerprint, round-aligned page bounds, and window metadata. The turn source
// (loadConversationTurns) yields the fully-parsed transcript; a DB conversation
// with no persisted agent transcript has an empty list — exactly as upstream
// returns for a conversation with no JSONL on disk. The page shape and the
// fingerprint are byte-compatible so the client’s consistency protocol holds
// once a richer transcript source is wired.

import (
	"database/sql"
	"encoding/binary"
	"fmt"
	"time"

	"yoyocode/src-go/db"
	"yoyocode/src-go/parsers"
	"yoyocode/src-go/web"
)

const maxWindowTurns = 500
const roundAlignCap = 200

const fnvOffset uint64 = 0xcbf29ce484222325
const fnvPrime uint64 = 0x00000100000001b3

// convTurn is the slim turn shape the window algorithm needs plus the raw
// MessageTurn JSON to serialize. role is "user"/"assistant"/"system".
type convTurn struct {
	role     string
	tsMillis int64
	hasTS    bool
	raw      map[string]any
}

func roleTag(role string) byte {
	switch role {
	case "assistant":
		return 1
	case "system":
		return 2
	default:
		return 0
	}
}

func fnv1aExtend(hash uint64, b []byte) uint64 {
	for _, c := range b {
		hash ^= uint64(c)
		hash *= fnvPrime
	}
	return hash
}

// prefixFingerprint is the chained (role_tag, timestamp_millis) FNV-1a over a
// turn prefix, as a fixed 16-hex string (mirrors turn_window.rs + the TS side).
func prefixFingerprint(turns []convTurn) string {
	hash := fnvOffset
	for _, t := range turns {
		hash = fnv1aExtend(hash, []byte{roleTag(t.role)})
		var buf [8]byte
		binary.LittleEndian.PutUint64(buf[:], uint64(t.tsMillis))
		hash = fnv1aExtend(hash, buf[:])
	}
	return fmt.Sprintf("%016x", hash)
}

// roundAlignBackward walks start back to a user-round boundary (or 0), bounded
// by roundAlignCap. Alignment only ever adds earlier turns.
func roundAlignBackward(turns []convTurn, start int) int {
	steps := 0
	for start > 0 && steps < roundAlignCap && turns[start].role != "user" {
		start--
		steps++
	}
	return start
}

func clampWindow(n int) int {
	if n < 1 {
		return 1
	}
	if n > maxWindowTurns {
		return maxWindowTurns
	}
	return n
}

// resolvePageBounds returns [start,end) for a history page ending just before
// beforeIndex, walking back limit turns then aligning to a user boundary.
func resolvePageBounds(turns []convTurn, beforeIndex, limit int) (int, int) {
	end := beforeIndex
	if end > len(turns) {
		end = len(turns)
	}
	if end < 0 {
		end = 0
	}
	limit = clampWindow(limit)
	start := end - limit
	if start < 0 {
		start = 0
	}
	start = roundAlignBackward(turns, start)
	if start > end {
		start = end
	}
	return start, end
}

type windowMeta struct {
	offset          int
	total           int
	assistantBefore int
	prefixHash      string
	uncoveredMaxTS  any
}

func computeWindowMeta(turns []convTurn, offset int) windowMeta {
	if offset > len(turns) {
		offset = len(turns)
	}
	prefix := turns[:offset]
	assistant := 0
	var maxTS int64
	haveTS := false
	for _, t := range prefix {
		if t.role == "assistant" {
			assistant++
		}
		if t.hasTS && (!haveTS || t.tsMillis > maxTS) {
			maxTS = t.tsMillis
			haveTS = true
		}
	}
	var uncovered any = nil
	if haveTS {
		uncovered = millisToRFC3339(maxTS)
	}
	return windowMeta{
		offset: offset, total: len(turns), assistantBefore: assistant,
		prefixHash: prefixFingerprint(prefix), uncoveredMaxTS: uncovered,
	}
}

func RegisterConversationTurns(reg *Registry) {
	reg.Register("get_folder_conversation_turns", func(c *web.Context) (any, error) {
		d := stateDB(c)
		convID := GetInt(c, "conversationId", "conversation_id")
		if convID <= 0 {
			return nil, web.ErrInvalidInput("conversationId required")
		}
		beforeIndex := int(GetInt(c, "beforeIndex", "before_index"))
		limit := int(GetInt(c, "limit"))
		turns := loadConversationTurns(d, convID)
		start, end := resolvePageBounds(turns, beforeIndex, limit)
		meta := computeWindowMeta(turns, start)
		seamIdx := beforeIndex
		if seamIdx > len(turns) {
			seamIdx = len(turns)
		}
		if seamIdx < 0 {
			seamIdx = 0
		}
		seam := computeWindowMeta(turns, seamIdx)
		rawTurns := make([]any, 0, end-start)
		for _, t := range turns[start:end] {
			rawTurns = append(rawTurns, t.raw)
		}
		return map[string]any{
			"turns":                         rawTurns,
			"turns_offset":                  meta.offset,
			"turns_total":                   meta.total,
			"assistant_turns_before_offset": meta.assistantBefore,
			"prefix_hash":                   meta.prefixHash,
			"prefix_hash_before_index":      seam.prefixHash,
			"uncovered_prefix_max_ts":       meta.uncoveredMaxTS,
		}, nil
	})
}

// loadConversationTurns returns the fully-parsed transcript for a conversation.
// A DB-backed conversation with no persisted agent transcript yields an empty
// list (matching upstream’s no-JSONL path); per-agent transcript parsing is a
// later enhancement that will populate this without changing the page contract.
// millisToRFC3339 renders epoch milliseconds as a UTC RFC3339 timestamp, the
// shape DateTime<Utc> serializes to.
func millisToRFC3339(ms int64) string {
	return time.UnixMilli(ms).UTC().Format(time.RFC3339)
}

func loadConversationTurns(d *db.Database, convID int64) []convTurn {
	if d == nil || d.Conn == nil {
		return []convTurn{}
	}
	var agent string
	var ext sql.NullString
	err := d.Conn.QueryRow(
		`SELECT agent_type, external_id FROM conversation WHERE id = ? AND deleted_at IS NULL`, convID).Scan(&agent, &ext)
	if err != nil || !ext.Valid || ext.String == "" {
		return []convTurn{}
	}
	// Per-agent transcript readers (mirrors upstream src-tauri/src/parsers).
	// Only claude_code is ported so far; other agents fall through to an empty
	// history (same honest no-JSONL path) until their reader lands.
	var raw []parsers.Turn
	switch agent {
	case "claude_code", "claude":
		raw = parsers.ReadClaudeTurns(ext.String)
	}
	out := make([]convTurn, 0, len(raw))
	for _, t := range raw {
		out = append(out, convTurn{role: t.Role, tsMillis: t.TSMillis, hasTS: t.HasTS, raw: t.Raw})
	}
	return out
}
