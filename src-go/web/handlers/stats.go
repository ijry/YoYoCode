package handlers

import (
	"sort"

	"yoyocode/src-go/db"
	"yoyocode/src-go/web"
)

// createConversation inserts a conversation for an agent session.
// createConversation mirrors upstream's `create_conversation`, which returns the
// new conversation id as a bare Json<i32> (not an object). So we return the
// int64 id directly — the mcode-app client reads it the same way it reads
// upstream's number.
func createConversation(d *db.Database, body map[string]any) (int64, error) {
	if d == nil || d.Conn == nil {
		return 0, web.ErrDatabase("db unavailable")
	}
	folderID := int64Num(body["folderId"])
	if folderID == 0 {
		folderID = int64Num(body["folder_id"])
	}
	agentType := strVal(body["agentType"], body["agent_type"])
	if agentType == "" {
		agentType = "claude_code"
	}
	title := strVal(body["title"])
	status := strVal(body["status"])
	if status == "" {
		status = "in_progress"
	}
	model := strVal(body["model"])
	now := nowRFC3339()
	res, err := d.Conn.Exec(
		`INSERT INTO conversation (folder_id, title, agent_type, status, model, kind, created_at, updated_at)
		 VALUES (?,?,?,?,?,'regular',?,?)`, folderID, title, agentType, status, model, now, now)
	if err != nil {
		return 0, web.ErrDatabase(err.Error())
	}
	id, _ := res.LastInsertId()
	return id, nil
}

// computeFolders aggregates non-deleted conversations into FolderInfo records,
// mirroring upstream compute_folders: {path, name, agent_types, conversation_count}.
// Upstream groups transcript-derived conversations by folder path; this port's
// source of truth is the DB conversation table, so we group the same way over
// it. Missing/deleted folders collapse to the sentinel "unknown".
func computeFolders(d *db.Database) []map[string]any {
	out := []map[string]any{}
	if d == nil || d.Conn == nil {
		return out
	}
	rows, err := d.Conn.Query(`
		SELECT COALESCE(f.path, ''), COALESCE(f.name, ''), c.agent_type
		FROM conversation c
		LEFT JOIN folder f ON c.folder_id = f.id AND f.deleted_at IS NULL
		WHERE c.deleted_at IS NULL
		ORDER BY COALESCE(c.last_message_at, c.updated_at) DESC`)
	if err != nil {
		return out
	}
	defer rows.Close()
	type folderAgg struct {
		path, name string
		agentTypes []string
		count      int64
		seen       map[string]bool
	}
	order := []string{}
	m := map[string]*folderAgg{}
	for rows.Next() {
		var path, name, agentType string
		if rows.Scan(&path, &name, &agentType) != nil {
			continue
		}
		if path == "" {
			path = "unknown"
		}
		if name == "" {
			name = "unknown"
		}
		agg := m[path]
		if agg == nil {
			agg = &folderAgg{path: path, name: name, seen: map[string]bool{}}
			m[path] = agg
			order = append(order, path)
		}
		agg.count++
		if !agg.seen[agentType] {
			agg.seen[agentType] = true
			agg.agentTypes = append(agg.agentTypes, agentType)
		}
	}
	for _, p := range order {
		agg := m[p]
		out = append(out, map[string]any{
			"path":               agg.path,
			"name":               agg.name,
			"agent_types":        agg.agentTypes,
			"conversation_count": agg.count,
		})
	}
	// Upstream sorts by conversation_count descending (stable).
	sort.SliceStable(out, func(i, j int) bool {
		return out[i]["conversation_count"].(int64) > out[j]["conversation_count"].(int64)
	})
	return out
}

// computeStats mirrors upstream compute_stats / AgentStats:
// {total_conversations, total_messages, by_agent:[{agent_type, conversation_count}]}
// with by_agent sorted by conversation_count descending.
func computeStats(d *db.Database) map[string]any {
	stats := map[string]any{
		"total_conversations": int64(0),
		"total_messages":      int64(0),
		"by_agent":            []map[string]any{},
	}
	if d == nil || d.Conn == nil {
		return stats
	}
	rows, err := d.Conn.Query(`
		SELECT agent_type, COUNT(*), COALESCE(SUM(message_count), 0)
		FROM conversation WHERE deleted_at IS NULL GROUP BY agent_type`)
	if err != nil {
		return stats
	}
	defer rows.Close()
	var total, totalMsgs int64
	byAgent := []map[string]any{}
	for rows.Next() {
		var at string
		var count, msgs int64
		if rows.Scan(&at, &count, &msgs) != nil {
			continue
		}
		total += count
		totalMsgs += msgs
		byAgent = append(byAgent, map[string]any{
			"agent_type":         at,
			"conversation_count": count,
		})
	}
	sort.SliceStable(byAgent, func(i, j int) bool {
		return byAgent[i]["conversation_count"].(int64) > byAgent[j]["conversation_count"].(int64)
	})
	stats["total_conversations"] = total
	stats["total_messages"] = totalMsgs
	stats["by_agent"] = byAgent
	return stats
}

// getSidebarData mirrors upstream get_sidebar_data: {folders, stats}.
func getSidebarData(d *db.Database) map[string]any {
	return map[string]any{
		"folders": computeFolders(d),
		"stats":   computeStats(d),
	}
}
