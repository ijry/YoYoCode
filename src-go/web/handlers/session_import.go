package handlers

// scan_importable_sessions (upstream commands/conversations.rs
// scan_importable_sessions_core + build_scan_result). Read-only: enumerate the
// agents' local sessions, group them by the cwd they ran in, and reconcile each
// against the upstream DB (by external_id + agent_type) so the import picker can
// mark every session New / Imported / Deleted. This build enumerates Claude
// Code sessions; other agents' parsers are not yet ported, so they contribute
// nothing (an honest empty, not a failure). The two import (write) endpoints
// build on this and follow.

import (
	"database/sql"
	"encoding/json"
	"runtime"
	"sort"
	"strings"

	"yoyocode/src-go/db"
	"yoyocode/src-go/parsers"
	"yoyocode/src-go/web"
)

// RegisterSessionImport wires scan_importable_sessions.
func RegisterSessionImport(reg *Registry) {
	reg.Register("scan_importable_sessions", func(c *web.Context) (any, error) {
		d := stateDB(c)
		summaries := collectLocalSummaries()

		// imported_index: (agentType, externalId) -> anyLive.
		type key struct{ agent, ext string }
		imported := map[key]bool{}
		if d != nil && d.Conn != nil {
			rows, err := d.Conn.Query(`SELECT agent_type, external_id, deleted_at FROM conversation WHERE external_id IS NOT NULL`)
			if err == nil {
				for rows.Next() {
					var at, ext string
					var deletedAt sql.NullString
					if rows.Scan(&at, &ext, &deletedAt) == nil {
						k := key{at, ext}
						imported[k] = imported[k] || !deletedAt.Valid
					}
				}
				rows.Close()
			}
		}

		// folder rows indexed by normalized path.
		type frow struct {
			id      int64
			path    string
			name    string
			deleted bool
		}
		folderIndex := map[string]frow{}
		if d != nil && d.Conn != nil {
			rows, err := d.Conn.Query(`SELECT id, path, name, deleted_at FROM folder`)
			if err == nil {
				for rows.Next() {
					var id int64
					var path, name string
					var deletedAt sql.NullString
					if rows.Scan(&id, &path, &name, &deletedAt) == nil {
						folderIndex[normalizePathForMatching(path)] = frow{id, path, name, deletedAt.Valid}
					}
				}
				rows.Close()
			}
		}

		type groupAcc struct {
			path       string
			name       string
			exists     bool
			folderID   any
			agentTypes []string
			sessions   []map[string]any
		}
		groups := map[string]*groupAcc{}
		noFolder := 0
		total := 0
		importable := 0

		for _, s := range summaries {
			total++
			raw := strings.TrimSpace(s.FolderPath)
			if raw == "" {
				noFolder++
				continue
			}
			importable++
			nk := normalizePathForMatching(raw)
			g := groups[nk]
			if g == nil {
				g = &groupAcc{path: raw, name: s.FolderName, agentTypes: []string{}, folderID: nil, exists: false}
				if row, ok := folderIndex[nk]; ok {
					g.path = row.path
					if row.name != "" {
						g.name = row.name
					}
					g.exists = !row.deleted
					g.folderID = row.id
				}
				if g.name == "" {
					g.name = folderNameFromPathH(raw)
				}
				groups[nk] = g
			}
			if !containsStr(g.agentTypes, s.AgentType) {
				g.agentTypes = append(g.agentTypes, s.AgentType)
			}
			status := "new"
			if live, ok := imported[key{s.AgentType, s.ID}]; ok {
				if live {
					status = "imported"
				} else {
					status = "deleted"
				}
			}
			g.sessions = append(g.sessions, map[string]any{
				"external_id":   s.ID,
				"agent_type":    s.AgentType,
				"title":         nilIfEmpty(s.Title),
				"started_at":    s.StartedAt,
				"ended_at":      nilIfEmpty(s.EndedAt),
				"message_count": s.MessageCount,
				"model":         nilIfEmpty(s.Model),
				"git_branch":    nilIfEmpty(s.GitBranch),
				"status":        status,
			})
		}

		folders := make([]map[string]any, 0, len(groups))
		for _, g := range groups {
			sort.SliceStable(g.sessions, func(i, j int) bool {
				return asString(g.sessions[i]["started_at"]) > asString(g.sessions[j]["started_at"])
			})
			folders = append(folders, map[string]any{
				"path":            g.path,
				"name":            g.name,
				"exists_in_codeg": g.exists,
				"folder_id":       g.folderID,
				"agent_types":     toAnySlice(g.agentTypes),
				"sessions":        g.sessions,
			})
		}
		sort.SliceStable(folders, func(i, j int) bool {
			return asString(folders[i]["name"]) < asString(folders[j]["name"])
		})

		return map[string]any{
			"folders":          folders,
			"no_folder_count":  noFolder,
			"total_sessions":   total,
			"importable_count": importable,
		}, nil
	})
}

// collectLocalSummaries enumerates every ported agent parser (claude only so
// far), skipping delegation children. Mirrors import_service::collect_local_summaries.
func collectLocalSummaries() []parsers.ConversationSummary {
	out := []parsers.ConversationSummary{}
	for _, s := range parsers.ListClaudeConversations() {
		if s.ParentID != "" {
			continue
		}
		out = append(out, s)
	}
	return out
}

// normalizePathForMatching mirrors parsers::normalize_path_for_matching: trim,
// backslash to slash, drop a trailing slash, and lowercase on Windows.
func normalizePathForMatching(p string) string {
	n := strings.ReplaceAll(strings.TrimSpace(p), "\\", "/")
	if runtime.GOOS == "windows" {
		n = strings.ToLower(n)
	}
	if len(n) > 1 {
		n = strings.TrimRight(n, "/")
	}
	return n
}

func folderNameFromPathH(p string) string {
	i := strings.LastIndexAny(p, "/\\")
	if i < 0 {
		return p
	}
	return p[i+1:]
}

// ── import (write) endpoints ──────────────────────────────────────────────

// RegisterSessionImportWrite wires import_local_conversations + import_selected_sessions.
func RegisterSessionImportWrite(reg *Registry) {
	reg.Register("import_local_conversations", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		folderID := GetInt(c, "folderId", "folder_id")
		if folderID <= 0 {
			return nil, web.ErrInvalidInput("folderId required")
		}
		fpath := folderPath(d, int64(folderID))
		if fpath == "" {
			return nil, web.ErrNotFound("Folder not found")
		}
		target := normalizePathForMatching(fpath)
		var imported, updated, skipped, restored int
		for _, s := range collectLocalSummaries() {
			if normalizePathForMatching(s.FolderPath) != target {
				continue
			}
			switch importOneSession(d, int64(folderID), s, false) {
			case "imported":
				imported++
			case "updated":
				updated++
			case "restored":
				restored++
			default:
				skipped++
			}
		}
		EmitConversationChanged(c, 0)
		return map[string]any{"imported": imported, "updated": updated, "skipped": skipped, "restored": restored}, nil
	})

	reg.Register("import_selected_sessions", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		var selections []map[string]any
		if raw := GetRaw(c, "selections"); len(raw) > 0 {
			_ = json.Unmarshal(raw, &selections)
		}
		// index discoverable summaries by (agentType, externalId)
		index := map[string]parsers.ConversationSummary{}
		for _, s := range collectLocalSummaries() {
			index[s.AgentType+"\x00"+s.ID] = s
		}
		type folderTally struct {
			path                                 string
			folderID                             int64
			created                              bool
			imported, updated, skipped, restored int
		}
		tallies := map[string]*folderTally{}
		order := []string{}
		var imported, updated, skipped, restored, notFound, failed, createdFolders int
		errorsList := []string{}

		for _, sel := range selections {
			agent := strVal(sel["agentType"], sel["agent_type"])
			ext := strVal(sel["externalId"], sel["external_id"])
			s, ok := index[agent+"\x00"+ext]
			if !ok || strings.TrimSpace(s.FolderPath) == "" {
				notFound++
				continue
			}
			fid, fpathStored, created, ferr := resolveImportFolder(d, s.FolderPath)
			if ferr != nil {
				failed++
				if len(errorsList) < 10 {
					errorsList = append(errorsList, ferr.Error())
				}
				continue
			}
			key := normalizePathForMatching(fpathStored)
			ft := tallies[key]
			if ft == nil {
				ft = &folderTally{path: fpathStored, folderID: fid, created: created}
				tallies[key] = ft
				order = append(order, key)
				if created {
					createdFolders++
				}
			}
			switch importOneSession(d, fid, s, true) {
			case "imported":
				imported++
				ft.imported++
			case "updated":
				updated++
				ft.updated++
			case "restored":
				restored++
				ft.restored++
			default:
				skipped++
				ft.skipped++
			}
		}

		folders := make([]map[string]any, 0, len(order))
		for _, key := range order {
			ft := tallies[key]
			folders = append(folders, map[string]any{
				"path": ft.path, "folder_id": ft.folderID, "created": ft.created,
				"imported": ft.imported, "updated": ft.updated, "skipped": ft.skipped, "restored": ft.restored,
			})
		}
		EmitConversationChanged(c, 0)
		return map[string]any{
			"imported": imported, "updated": updated, "skipped": skipped, "restored": restored,
			"not_found": notFound, "failed": failed, "created_folders": createdFolders,
			"folders": folders, "errors": errorsList,
		}, nil
	})
}

// resolveImportFolder finds the folder whose normalized path matches cwd,
// reopening a soft-deleted row, or creates one. Returns (id, storedPath, created, err).
func resolveImportFolder(d *db.Database, cwd string) (int64, string, bool, error) {
	norm := normalizePathForMatching(cwd)
	rows, err := d.Conn.Query(`SELECT id, path, deleted_at FROM folder`)
	if err != nil {
		return 0, "", false, web.ErrDatabase(err.Error())
	}
	var foundID int64
	var foundPath string
	var wasDeleted bool
	for rows.Next() {
		var id int64
		var path string
		var del sql.NullString
		if rows.Scan(&id, &path, &del) == nil && normalizePathForMatching(path) == norm {
			foundID = id
			foundPath = path
			wasDeleted = del.Valid
			break
		}
	}
	rows.Close()
	if foundID > 0 {
		if wasDeleted {
			now := nowRFC3339()
			_, _ = d.Conn.Exec(`UPDATE folder SET deleted_at = NULL, is_open = 1, updated_at = ? WHERE id = ?`, now, foundID)
			return foundID, foundPath, true, nil
		}
		return foundID, foundPath, false, nil
	}
	rec, aerr := addFolder(d, cwd)
	if aerr != nil {
		return 0, "", false, aerr
	}
	id, _ := rec["id"].(int64)
	path, _ := rec["path"].(string)
	return id, path, true, nil
}

// importOneSession reconciles one summary into folderID (mirrors import_one).
// restore=true un-deletes a soft-deleted row (the picker); false leaves it.
// Returns "imported" | "updated" | "restored" | "skipped".
func importOneSession(d *db.Database, folderID int64, s parsers.ConversationSummary, restore bool) string {
	var id int64
	var titleLocked int
	var deletedAt sql.NullString
	var parentID sql.NullInt64
	var curTitle sql.NullString
	var curUpdated sql.NullString
	row := d.Conn.QueryRow(`SELECT id, title_locked, deleted_at, parent_id, title, updated_at FROM conversation WHERE external_id = ? AND agent_type = ?`, s.ID, s.AgentType)
	if row.Scan(&id, &titleLocked, &deletedAt, &parentID, &curTitle, &curUpdated) != nil {
		// insert new
		now := nowRFC3339()
		created := firstNonEmptyStr(s.StartedAt, now)
		upd := firstNonEmptyStr(s.EndedAt, created)
		var lastMsg any
		if s.EndedAt != "" {
			lastMsg = s.EndedAt
		}
		_, ierr := d.Conn.Exec(
			`INSERT INTO conversation (folder_id, title, agent_type, status, model, git_branch, external_id, message_count, kind, title_locked, created_at, updated_at, last_message_at)
			 VALUES (?,?,?,?,?,?,?,?,?,0,?,?,?)`,
			folderID, nilIfEmpty(s.Title), s.AgentType, "pending_review", nilIfEmpty(s.Model), nilIfEmpty(s.GitBranch), s.ID, s.MessageCount, "regular", created, upd, lastMsg)
		if ierr != nil {
			return "skipped"
		}
		return "imported"
	}
	// existing
	if parentID.Valid {
		return "skipped"
	}
	restoring := deletedAt.Valid
	if restoring && !restore {
		return "skipped"
	}
	wonRestore := false
	if restoring {
		now := nowRFC3339()
		_, _ = d.Conn.Exec(`UPDATE conversation SET deleted_at = NULL, folder_id = ?, status = 'pending_review', updated_at = ? WHERE id = ?`, folderID, now, id)
		wonRestore = true
		deletedAt = sql.NullString{}
	}
	wrote := false
	// title refresh (never bumps updated_at), only when not locked and changed.
	if titleLocked == 0 && strings.TrimSpace(s.Title) != "" && strings.TrimSpace(s.Title) != strings.TrimSpace(curTitle.String) {
		if _, e := d.Conn.Exec(`UPDATE conversation SET title = ? WHERE id = ? AND deleted_at IS NULL`, s.Title, id); e == nil {
			wrote = true
		}
	}
	// activity refresh when the parsed end time is strictly newer.
	if s.EndedAt != "" && curUpdated.Valid && s.EndedAt > curUpdated.String {
		if _, e := d.Conn.Exec(`UPDATE conversation SET updated_at = ?, last_message_at = ?, message_count = ? WHERE id = ? AND deleted_at IS NULL`, s.EndedAt, s.EndedAt, s.MessageCount, id); e == nil {
			wrote = true
		}
	}
	if wonRestore {
		return "restored"
	}
	if wrote {
		return "updated"
	}
	return "skipped"
}
