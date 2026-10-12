package handlers

// Canvas (B11): boards + a node graph (folder/group/agent/conversation/custom/
// note/file/terminal nodes), mirroring commands/canvas.rs + canvas_service.rs.
// Every mutation runs under a process lock in one transaction that bumps the
// workspace-global canvas_revision, and returns a CanvasMutation{value,revision}
// (or a CanvasSnapshot for list_nodes). Node events broadcast on canvas://changed,
// board events on canvas-board://changed. Geometry/grid/colour are clamped and
// kind-binding invariants enforced exactly as upstream.

import (
	"database/sql"
	"encoding/json"
	"math"
	"sort"
	"strings"
	"sync"

	"yoyocode/src-go/db"
	"yoyocode/src-go/web"
)

const canvasRevisionKey = "canvas_revision"
const (
	canvasMaxCustomMembers = 200
	canvasMaxGridAxis      = 12
	canvasMinNodeSize      = 48.0
	canvasMaxNodeSize      = 20000.0
	canvasMaxCoord         = 1000000.0
	canvasMaxPathLen       = 4096
	canvasMaxBoardNameLen  = 200
	canvasMaxBoardDescLen  = 2000
	canvasMaxBoardPreview  = 200
	canvasCardWidth        = 224.0
	canvasCardHeight       = 132.0
)

// canvasMu serializes every canvas mutation + its broadcast, matching the
// upstream revision_lock + event_order_lock pair (one process clock).
var canvasMu sync.Mutex

var canvasThemeColors = map[string]bool{
	"neutral": true, "zinc": true, "slate": true, "stone": true, "gray": true,
	"red": true, "rose": true, "orange": true, "green": true, "blue": true,
	"yellow": true, "violet": true,
}

var canvasRegionKinds = map[string]bool{"folder": true, "group": true, "agent": true, "custom": true}

func canvasIsRegion(kind string) bool { return canvasRegionKinds[kind] }

// ── revision ──

type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
	QueryRow(query string, args ...any) *sql.Row
	Query(query string, args ...any) (*sql.Rows, error)
}

func canvasGetRevision(x execer) int64 {
	var v sql.NullString
	_ = x.QueryRow(`SELECT value FROM app_metadata WHERE key = ? AND deleted_at IS NULL`, canvasRevisionKey).Scan(&v)
	if !v.Valid {
		return 0
	}
	n, _ := parseInt64(v.String)
	return n
}

func canvasBumpRevision(x execer) int64 {
	next := canvasGetRevision(x) + 1
	now := nowRFC3339()
	_, _ = x.Exec(`INSERT INTO app_metadata (key, value, created_at, updated_at) VALUES (?,?,?,?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`,
		canvasRevisionKey, int64ToStr(next), now, now)
	return next
}

func parseInt64(s string) (int64, bool) {
	var n int64
	for _, c := range strings.TrimSpace(s) {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int64(c-'0')
	}
	return n, true
}

// ── normalize + clamp ──

func canvasClampCoord(v float64) (float64, error) {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, web.ErrInvalidInput("coordinate is not finite")
	}
	return math.Max(-canvasMaxCoord, math.Min(canvasMaxCoord, v)), nil
}

func canvasClampSize(v float64) (float64, error) {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, web.ErrInvalidInput("size is not finite")
	}
	return math.Max(canvasMinNodeSize, math.Min(canvasMaxNodeSize, v)), nil
}

func canvasClampGridAxis(v any) int64 {
	if v == nil {
		return 0
	}
	n := asInt64(v)
	if n < 0 {
		return 0
	}
	if n > canvasMaxGridAxis {
		return canvasMaxGridAxis
	}
	return n
}

func canvasNormalizeText(v any) any {
	s, ok := v.(string)
	if !ok {
		return nil
	}
	t := strings.TrimSpace(s)
	if t == "" {
		return nil
	}
	return t
}

func canvasNormalizeColor(v any) (any, error) {
	t := canvasNormalizeText(v)
	if t == nil {
		return nil, nil
	}
	if !canvasThemeColors[t.(string)] {
		return nil, web.ErrInvalidInput("unknown theme color '" + t.(string) + "'")
	}
	return t, nil
}

func canvasNormalizeBoardText(v any, max int, what string) (any, error) {
	t := canvasNormalizeText(v)
	if t == nil {
		return nil, nil
	}
	if len([]rune(t.(string))) > max {
		return nil, web.ErrInvalidInput("canvas board " + what + " is too long")
	}
	return t, nil
}

func canvasNormalizePath(v any) (string, error) {
	t := canvasNormalizeText(v)
	if t == nil {
		return "", web.ErrInvalidInput("this node kind needs a path")
	}
	if len([]rune(t.(string))) > canvasMaxPathLen {
		return "", web.ErrInvalidInput("path is too long")
	}
	return t.(string), nil
}

func canvasParseMembers(raw sql.NullString) []int64 {
	out := []int64{}
	if !raw.Valid || raw.String == "" {
		return out
	}
	_ = json.Unmarshal([]byte(raw.String), &out)
	if out == nil {
		return []int64{}
	}
	return out
}

func canvasEncodeMembers(ids []int64) string {
	if ids == nil {
		ids = []int64{}
	}
	b, err := json.Marshal(ids)
	if err != nil {
		return "[]"
	}
	return string(b)
}

// ── scan + map ──

const canvasNodeCols = `SELECT id, board_id, kind, folder_id, folder_group_id, agent_type,
	conversation_id, member_ids, title, content, path, color, collapsed, grid_columns, grid_rows,
	x, y, width, height, created_at, updated_at FROM canvas_node`

func scanCanvasNode(row interface{ Scan(...any) error }) (map[string]any, int64, string, error) {
	var id, boardID, gridCols, gridRows int64
	var kind, createdAt, updatedAt string
	var folderID, folderGroupID, conversationID sql.NullInt64
	var agentType, memberIDs, title, content, path, color sql.NullString
	var collapsed int64
	var x, y, width, height float64
	if err := row.Scan(&id, &boardID, &kind, &folderID, &folderGroupID, &agentType,
		&conversationID, &memberIDs, &title, &content, &path, &color, &collapsed, &gridCols, &gridRows,
		&x, &y, &width, &height, &createdAt, &updatedAt); err != nil {
		return nil, 0, "", err
	}
	rec := map[string]any{
		"id": id, "board_id": boardID, "kind": kind,
		"folder_id": niAny(folderID), "folder_group_id": niAny(folderGroupID),
		"agent_type": nsAny(agentType), "conversation_id": niAny(conversationID),
		"member_ids": canvasMembersAsAny(canvasParseMembers(memberIDs)),
		"title":      nsAny(title), "content": nsAny(content), "path": nsAny(path),
		"color": nsAny(color), "collapsed": collapsed != 0,
		"grid_columns": gridCols, "grid_rows": gridRows,
		"x": x, "y": y, "width": width, "height": height,
		"created_at": createdAt, "updated_at": updatedAt,
	}
	return rec, boardID, kind, nil
}

func canvasMembersAsAny(ids []int64) []any {
	out := make([]any, 0, len(ids))
	for _, v := range ids {
		out = append(out, v)
	}
	return out
}

func canvasNodeByID(x execer, id int64) (map[string]any, error) {
	row := x.QueryRow(canvasNodeCols+` WHERE id = ?`, id)
	rec, _, _, err := scanCanvasNode(row)
	if err == sql.ErrNoRows {
		return nil, web.ErrNotFound("canvas node not found")
	}
	if err != nil {
		return nil, web.ErrDatabase(err.Error())
	}
	return rec, nil
}

func canvasBoardMap(id int64, name, description, color sql.NullString, createdAt, updatedAt string) map[string]any {
	return map[string]any{
		"id": id, "name": nsAny(name), "description": nsAny(description),
		"color": nsAny(color), "created_at": createdAt, "updated_at": updatedAt,
	}
}

func canvasBoardByID(x execer, id int64) (map[string]any, error) {
	var bid int64
	var name, description, color sql.NullString
	var createdAt, updatedAt string
	err := x.QueryRow(`SELECT id, name, description, color, created_at, updated_at FROM canvas_board WHERE id = ?`, id).
		Scan(&bid, &name, &description, &color, &createdAt, &updatedAt)
	if err == sql.ErrNoRows {
		return nil, web.ErrNotFound("canvas board not found")
	}
	if err != nil {
		return nil, web.ErrDatabase(err.Error())
	}
	return canvasBoardMap(bid, name, description, color, createdAt, updatedAt), nil
}

func canvasTouchBoards(x execer, boardIDs []int64, now string) {
	seen := map[int64]bool{}
	for _, b := range boardIDs {
		if seen[b] {
			continue
		}
		seen[b] = true
		_, _ = x.Exec(`UPDATE canvas_board SET updated_at = ? WHERE id = ?`, now, b)
	}
}

func canvasRequireBoard(x execer, boardID int64) error {
	var one int64
	err := x.QueryRow(`SELECT 1 FROM canvas_board WHERE id = ?`, boardID).Scan(&one)
	if err == sql.ErrNoRows {
		return web.ErrNotFound("canvas board not found")
	}
	if err != nil {
		return web.ErrDatabase(err.Error())
	}
	return nil
}

func canvasRequireLiveConversation(x execer, convID int64) error {
	var one int64
	err := x.QueryRow(`SELECT 1 FROM conversation WHERE id = ? AND deleted_at IS NULL`, convID).Scan(&one)
	if err == sql.ErrNoRows {
		return web.ErrNotFound("conversation not found")
	}
	if err != nil {
		return web.ErrDatabase(err.Error())
	}
	return nil
}

func canvasMutation(value any, revision int64) map[string]any {
	return map[string]any{"value": value, "revision": revision}
}

// ── commands ──

func RegisterCanvas(reg *Registry) {
	reg.Register("canvas_list_boards", func(c *web.Context) (any, error) {
		d := stateDB(c)
		return canvasListBoards(d), nil
	})

	reg.Register("canvas_create_board", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		in, _ := c.Body["input"].(map[string]any)
		if in == nil {
			in = map[string]any{}
		}
		name, e1 := canvasNormalizeBoardText(pick(in, "name"), canvasMaxBoardNameLen, "name")
		if e1 != nil {
			return nil, e1
		}
		desc, e2 := canvasNormalizeBoardText(pick(in, "description"), canvasMaxBoardDescLen, "description")
		if e2 != nil {
			return nil, e2
		}
		color, e3 := canvasNormalizeColor(pick(in, "color"))
		if e3 != nil {
			return nil, e3
		}
		canvasMu.Lock()
		defer canvasMu.Unlock()
		now := nowRFC3339()
		res, de := d.Conn.Exec(`INSERT INTO canvas_board (name, description, color, created_at, updated_at) VALUES (?,?,?,?,?)`, name, desc, color, now, now)
		if de != nil {
			return nil, web.ErrDatabase(de.Error())
		}
		id, _ := res.LastInsertId()
		board, _ := canvasBoardByID(d.Conn, id)
		Emit(c, "canvas-board://changed", map[string]any{"board_id": id})
		return board, nil
	})

	reg.Register("canvas_update_board", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		boardID := GetInt(c, "boardId", "board_id")
		patch, _ := c.Body["patch"].(map[string]any)
		if patch == nil {
			patch = map[string]any{}
		}
		canvasMu.Lock()
		defer canvasMu.Unlock()
		if _, e := canvasBoardByID(d.Conn, boardID); e != nil {
			return nil, e
		}
		now := nowRFC3339()
		if _, ok := patch["name"]; ok {
			v, e := canvasNormalizeBoardText(patch["name"], canvasMaxBoardNameLen, "name")
			if e != nil {
				return nil, e
			}
			_, _ = d.Conn.Exec(`UPDATE canvas_board SET name = ? WHERE id = ?`, v, boardID)
		}
		if _, ok := patch["description"]; ok {
			v, e := canvasNormalizeBoardText(patch["description"], canvasMaxBoardDescLen, "description")
			if e != nil {
				return nil, e
			}
			_, _ = d.Conn.Exec(`UPDATE canvas_board SET description = ? WHERE id = ?`, v, boardID)
		}
		if _, ok := patch["color"]; ok {
			v, e := canvasNormalizeColor(patch["color"])
			if e != nil {
				return nil, e
			}
			_, _ = d.Conn.Exec(`UPDATE canvas_board SET color = ? WHERE id = ?`, v, boardID)
		}
		_, _ = d.Conn.Exec(`UPDATE canvas_board SET updated_at = ? WHERE id = ?`, now, boardID)
		board, _ := canvasBoardByID(d.Conn, boardID)
		Emit(c, "canvas-board://changed", map[string]any{"board_id": boardID})
		return board, nil
	})

	reg.Register("canvas_delete_board", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		boardID := GetInt(c, "boardId", "board_id")
		canvasMu.Lock()
		defer canvasMu.Unlock()
		var one int64
		if d.Conn.QueryRow(`SELECT 1 FROM canvas_board WHERE id = ?`, boardID).Scan(&one) == sql.ErrNoRows {
			return canvasMutation([]any{}, canvasGetRevision(d.Conn)), nil
		}
		nodeIDs := canvasBoardNodeIDs(d.Conn, boardID)
		_, _ = d.Conn.Exec(`DELETE FROM canvas_node WHERE board_id = ?`, boardID)
		_, _ = d.Conn.Exec(`DELETE FROM canvas_board WHERE id = ?`, boardID)
		revision := canvasGetRevision(d.Conn)
		if len(nodeIDs) > 0 {
			revision = canvasBumpRevision(d.Conn)
		}
		Emit(c, "canvas-board://changed", map[string]any{"board_id": boardID})
		if len(nodeIDs) > 0 {
			Emit(c, "canvas://changed", map[string]any{"deleted_ids": nodeIDs, "revision": revision})
		}
		return canvasMutation(canvasIDsAsAny(nodeIDs), revision), nil
	})

	reg.Register("canvas_list_nodes", func(c *web.Context) (any, error) {
		d := stateDB(c)
		boardID := GetInt(c, "boardId", "board_id")
		nodes := []map[string]any{}
		if d != nil && d.Conn != nil {
			rows, e := d.Conn.Query(canvasNodeCols+` WHERE board_id = ? ORDER BY id ASC`, boardID)
			if e == nil {
				for rows.Next() {
					if rec, _, _, se := scanCanvasNode(rows); se == nil {
						nodes = append(nodes, rec)
					}
				}
				rows.Close()
			}
		}
		rev := int64(0)
		if d != nil && d.Conn != nil {
			rev = canvasGetRevision(d.Conn)
		}
		return map[string]any{"board_id": boardID, "nodes": nodes, "revision": rev}, nil
	})

	reg.Register("canvas_create_node", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		in, _ := c.Body["input"].(map[string]any)
		if in == nil {
			return nil, web.ErrInvalidInput("input required")
		}
		return canvasCreateNode(c, d, in)
	})

	reg.Register("canvas_update_node", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		nodeID := GetInt(c, "nodeId", "node_id")
		patch, _ := c.Body["patch"].(map[string]any)
		if patch == nil {
			patch = map[string]any{}
		}
		return canvasUpdateNode(c, d, nodeID, patch)
	})

	reg.Register("canvas_move_nodes", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		moves, _ := c.Body["moves"].([]any)
		return canvasMoveNodes(c, d, moves)
	})

	reg.Register("canvas_delete_node", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		nodeID := GetInt(c, "nodeId", "node_id")
		canvasMu.Lock()
		defer canvasMu.Unlock()
		var boardID int64
		if d.Conn.QueryRow(`SELECT board_id FROM canvas_node WHERE id = ?`, nodeID).Scan(&boardID) == sql.ErrNoRows {
			return canvasMutation(nil, canvasGetRevision(d.Conn)), nil
		}
		_, _ = d.Conn.Exec(`DELETE FROM canvas_node WHERE id = ?`, nodeID)
		canvasTouchBoards(d.Conn, []int64{boardID}, nowRFC3339())
		revision := canvasBumpRevision(d.Conn)
		Emit(c, "canvas://changed", map[string]any{"deleted_ids": []any{nodeID}, "revision": revision})
		return canvasMutation(nil, revision), nil
	})

	reg.Register("canvas_delete_nodes", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		ids := GetIntSlice(c, "nodeIds", "node_ids")
		canvasMu.Lock()
		defer canvasMu.Unlock()
		existing, boards := canvasExistingNodes(d.Conn, ids)
		if len(existing) == 0 {
			return canvasMutation([]any{}, canvasGetRevision(d.Conn)), nil
		}
		for _, id := range existing {
			_, _ = d.Conn.Exec(`DELETE FROM canvas_node WHERE id = ?`, id)
		}
		canvasTouchBoards(d.Conn, boards, nowRFC3339())
		revision := canvasBumpRevision(d.Conn)
		Emit(c, "canvas://changed", map[string]any{"deleted_ids": canvasIDsAsAny(existing), "revision": revision})
		return canvasMutation(canvasIDsAsAny(existing), revision), nil
	})

	reg.Register("canvas_group_into_region", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		in, _ := c.Body["input"].(map[string]any)
		if in == nil {
			return nil, web.ErrInvalidInput("input required")
		}
		return canvasGroupIntoRegion(c, d, in)
	})

	reg.Register("canvas_detach_member", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		return canvasDetachMember(c, d, GetInt(c, "regionId", "region_id"), GetInt(c, "conversationId", "conversation_id"), getFloat(c, "x"), getFloat(c, "y"))
	})
}

func getFloat(c *web.Context, key string) float64 {
	if v, ok := c.Body[key]; ok {
		if f, ok := v.(float64); ok {
			return f
		}
	}
	return 0
}

func canvasIDsAsAny(ids []int64) []any {
	out := make([]any, 0, len(ids))
	for _, v := range ids {
		out = append(out, v)
	}
	return out
}

func canvasBoardNodeIDs(x execer, boardID int64) []int64 {
	out := []int64{}
	rows, err := x.Query(`SELECT id FROM canvas_node WHERE board_id = ? ORDER BY id ASC`, boardID)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			out = append(out, id)
		}
	}
	return out
}

func canvasExistingNodes(x execer, ids []int64) ([]int64, []int64) {
	existing := []int64{}
	boards := []int64{}
	for _, id := range ids {
		var boardID int64
		if x.QueryRow(`SELECT board_id FROM canvas_node WHERE id = ?`, id).Scan(&boardID) == nil {
			existing = append(existing, id)
			boards = append(boards, boardID)
		}
	}
	return existing, boards
}

// ── list_boards (with preview rects) ──

func canvasListBoards(d *db.Database) []map[string]any {
	out := []map[string]any{}
	if d == nil || d.Conn == nil {
		return out
	}
	type rect struct {
		kind                string
		x, y, width, height float64
		color               any
	}
	byBoard := map[int64][]rect{}
	nodeRows, err := d.Conn.Query(`SELECT board_id, kind, x, y, width, height, color FROM canvas_node`)
	if err == nil {
		for nodeRows.Next() {
			var bid int64
			var kind string
			var x, y, wd, h float64
			var color sql.NullString
			if nodeRows.Scan(&bid, &kind, &x, &y, &wd, &h, &color) == nil {
				byBoard[bid] = append(byBoard[bid], rect{kind, x, y, wd, h, nsAny(color)})
			}
		}
		nodeRows.Close()
	}
	rows, berr := d.Conn.Query(`SELECT id, name, description, color, created_at, updated_at FROM canvas_board ORDER BY updated_at DESC, id DESC`)
	if berr != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var name, description, color sql.NullString
		var createdAt, updatedAt string
		if rows.Scan(&id, &name, &description, &color, &createdAt, &updatedAt) != nil {
			continue
		}
		rects := byBoard[id]
		nodeCount := int64(len(rects))
		terminalCount := int64(0)
		for _, r := range rects {
			if r.kind == "terminal" {
				terminalCount++
			}
		}
		sort.SliceStable(rects, func(i, j int) bool {
			return rects[i].width*rects[i].height > rects[j].width*rects[j].height
		})
		if len(rects) > canvasMaxBoardPreview {
			rects = rects[:canvasMaxBoardPreview]
		}
		preview := make([]map[string]any, 0, len(rects))
		for _, r := range rects {
			preview = append(preview, map[string]any{"kind": r.kind, "x": r.x, "y": r.y, "width": r.width, "height": r.height, "color": r.color})
		}
		out = append(out, map[string]any{
			"board":          canvasBoardMap(id, name, description, color, createdAt, updatedAt),
			"node_count":     nodeCount,
			"terminal_count": terminalCount,
			"preview":        preview,
		})
	}
	return out
}

// ── create_node ──

func canvasCreateNode(c *web.Context, d *db.Database, in map[string]any) (any, error) {
	kind := asString(pick(in, "kind"))
	boardID := asInt64(pick(in, "board_id", "boardId"))
	canvasMu.Lock()
	defer canvasMu.Unlock()
	if err := canvasRequireBoard(d.Conn, boardID); err != nil {
		return nil, err
	}
	var folderID, folderGroupID, conversationID any
	var agentType, path any
	switch kind {
	case "folder":
		fid := asInt64(pick(in, "folder_id", "folderId"))
		if fid == 0 {
			return nil, web.ErrInvalidInput("folder region needs folder_id")
		}
		var one int64
		if d.Conn.QueryRow(`SELECT 1 FROM folder WHERE id = ? AND deleted_at IS NULL`, fid).Scan(&one) == sql.ErrNoRows {
			return nil, web.ErrNotFound("folder not found")
		}
		folderID = fid
	case "group":
		gid := asInt64(pick(in, "folder_group_id", "folderGroupId"))
		if gid == 0 {
			return nil, web.ErrInvalidInput("group region needs folder_group_id")
		}
		folderGroupID = gid
	case "agent":
		at := strings.TrimSpace(asString(pick(in, "agent_type", "agentType")))
		if at == "" {
			return nil, web.ErrInvalidInput("agent region needs agent_type")
		}
		agentType = at
	case "conversation":
		cid := asInt64(pick(in, "conversation_id", "conversationId"))
		if cid == 0 {
			return nil, web.ErrInvalidInput("conversation node needs conversation_id")
		}
		if e := canvasRequireLiveConversation(d.Conn, cid); e != nil {
			return nil, e
		}
		conversationID = cid
	case "custom", "note":
	case "file", "terminal":
		pp, e := canvasNormalizePath(pick(in, "path"))
		if e != nil {
			return nil, e
		}
		path = pp
	default:
		return nil, web.ErrInvalidInput("unknown canvas node kind: " + kind)
	}
	if path == nil {
		if s := canvasNormalizeText(pick(in, "path")); s != nil {
			return nil, web.ErrInvalidInput("path only applies to file and terminal nodes")
		}
	}
	var memberIDs any
	if kind == "custom" {
		memberIDs = "[]"
	}
	var content any
	if kind == "note" {
		if s, ok := pick(in, "content").(string); ok && s != "" {
			content = s
		}
	} else if s, ok := pick(in, "content").(string); ok && s != "" {
		return nil, web.ErrInvalidInput("content only applies to notes")
	}
	color, e := canvasNormalizeColor(pick(in, "color"))
	if e != nil {
		return nil, e
	}
	gridCols, gridRows := int64(0), int64(0)
	if canvasIsRegion(kind) {
		gridCols = canvasClampGridAxis(pick(in, "grid_columns", "gridColumns"))
		gridRows = canvasClampGridAxis(pick(in, "grid_rows", "gridRows"))
	}
	x, e1 := canvasClampCoord(getFloatAny(pick(in, "x")))
	if e1 != nil {
		return nil, e1
	}
	y, e2 := canvasClampCoord(getFloatAny(pick(in, "y")))
	if e2 != nil {
		return nil, e2
	}
	width, e3 := canvasClampSize(getFloatAny(pick(in, "width")))
	if e3 != nil {
		return nil, e3
	}
	height, e4 := canvasClampSize(getFloatAny(pick(in, "height")))
	if e4 != nil {
		return nil, e4
	}
	now := nowRFC3339()
	res, de := d.Conn.Exec(`INSERT INTO canvas_node (board_id, kind, folder_id, folder_group_id, agent_type,
		conversation_id, member_ids, title, content, path, color, collapsed, grid_columns, grid_rows,
		x, y, width, height, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,0,?,?,?,?,?,?,?,?)`,
		boardID, kind, folderID, folderGroupID, agentType, conversationID, memberIDs,
		canvasNormalizeText(pick(in, "title")), content, path, color, gridCols, gridRows,
		x, y, width, height, now, now)
	if de != nil {
		return nil, web.ErrDatabase(de.Error())
	}
	id, _ := res.LastInsertId()
	canvasTouchBoards(d.Conn, []int64{boardID}, now)
	revision := canvasBumpRevision(d.Conn)
	node, _ := canvasNodeByID(d.Conn, id)
	Emit(c, "canvas://changed", map[string]any{"node": node, "revision": revision})
	return canvasMutation(node, revision), nil
}

func getFloatAny(v any) float64 {
	if f, ok := v.(float64); ok {
		return f
	}
	return 0
}

// ── update_node ──

func canvasUpdateNode(c *web.Context, d *db.Database, nodeID int64, patch map[string]any) (any, error) {
	canvasMu.Lock()
	defer canvasMu.Unlock()
	var kind string
	var boardID int64
	var memberRaw sql.NullString
	err := d.Conn.QueryRow(`SELECT kind, board_id, member_ids FROM canvas_node WHERE id = ?`, nodeID).Scan(&kind, &boardID, &memberRaw)
	if err == sql.ErrNoRows {
		return nil, web.ErrNotFound("canvas node not found")
	}
	if err != nil {
		return nil, web.ErrDatabase(err.Error())
	}
	members := canvasParseMembers(memberRaw)
	now := nowRFC3339()
	_, hasAdd := patch["memberAdd"]
	_, hasAdd2 := patch["member_add"]
	_, hasRemove := patch["memberRemove"]
	_, hasRemove2 := patch["member_remove"]
	if hasAdd || hasAdd2 || hasRemove || hasRemove2 {
		if kind != "custom" {
			return nil, web.ErrInvalidInput("member operations only apply to custom regions")
		}
		if add := asInt64(pick(patch, "memberAdd", "member_add")); add != 0 {
			if e := canvasRequireLiveConversation(d.Conn, add); e != nil {
				return nil, e
			}
			if !containsInt64(members, add) {
				if len(members) >= canvasMaxCustomMembers {
					return nil, web.ErrInvalidInput("a region holds at most 200 conversations")
				}
				members = append(members, add)
			}
		}
		if rem := asInt64(pick(patch, "memberRemove", "member_remove")); rem != 0 {
			members = removeInt64(members, rem)
		}
		_, _ = d.Conn.Exec(`UPDATE canvas_node SET member_ids = ? WHERE id = ?`, canvasEncodeMembers(members), nodeID)
	}
	if _, ok := patch["title"]; ok {
		_, _ = d.Conn.Exec(`UPDATE canvas_node SET title = ? WHERE id = ?`, canvasNormalizeText(patch["title"]), nodeID)
	}
	if _, ok := patch["content"]; ok {
		if kind != "note" {
			return nil, web.ErrInvalidInput("content only applies to notes")
		}
		var content any
		if s, ok := patch["content"].(string); ok && s != "" {
			content = s
		}
		_, _ = d.Conn.Exec(`UPDATE canvas_node SET content = ? WHERE id = ?`, content, nodeID)
	}
	if _, ok := patch["color"]; ok {
		color, e := canvasNormalizeColor(patch["color"])
		if e != nil {
			return nil, e
		}
		_, _ = d.Conn.Exec(`UPDATE canvas_node SET color = ? WHERE id = ?`, color, nodeID)
	}
	if v, ok := patch["collapsed"].(bool); ok {
		_, _ = d.Conn.Exec(`UPDATE canvas_node SET collapsed = ? WHERE id = ?`, boolToInt(v), nodeID)
	}
	_, hasGC := patch["gridColumns"]
	_, hasGC2 := patch["grid_columns"]
	_, hasGR := patch["gridRows"]
	_, hasGR2 := patch["grid_rows"]
	if hasGC || hasGC2 || hasGR || hasGR2 {
		if !canvasIsRegion(kind) {
			return nil, web.ErrInvalidInput("grid shape only applies to regions")
		}
		if hasGC || hasGC2 {
			_, _ = d.Conn.Exec(`UPDATE canvas_node SET grid_columns = ? WHERE id = ?`, canvasClampGridAxis(pick(patch, "gridColumns", "grid_columns")), nodeID)
		}
		if hasGR || hasGR2 {
			_, _ = d.Conn.Exec(`UPDATE canvas_node SET grid_rows = ? WHERE id = ?`, canvasClampGridAxis(pick(patch, "gridRows", "grid_rows")), nodeID)
		}
	}
	if _, ok := patch["x"]; ok {
		v, e := canvasClampCoord(getFloatAny(patch["x"]))
		if e != nil {
			return nil, e
		}
		_, _ = d.Conn.Exec(`UPDATE canvas_node SET x = ? WHERE id = ?`, v, nodeID)
	}
	if _, ok := patch["y"]; ok {
		v, e := canvasClampCoord(getFloatAny(patch["y"]))
		if e != nil {
			return nil, e
		}
		_, _ = d.Conn.Exec(`UPDATE canvas_node SET y = ? WHERE id = ?`, v, nodeID)
	}
	if _, ok := patch["width"]; ok {
		v, e := canvasClampSize(getFloatAny(patch["width"]))
		if e != nil {
			return nil, e
		}
		_, _ = d.Conn.Exec(`UPDATE canvas_node SET width = ? WHERE id = ?`, v, nodeID)
	}
	if _, ok := patch["height"]; ok {
		v, e := canvasClampSize(getFloatAny(patch["height"]))
		if e != nil {
			return nil, e
		}
		_, _ = d.Conn.Exec(`UPDATE canvas_node SET height = ? WHERE id = ?`, v, nodeID)
	}
	_, _ = d.Conn.Exec(`UPDATE canvas_node SET updated_at = ? WHERE id = ?`, now, nodeID)
	canvasTouchBoards(d.Conn, []int64{boardID}, now)
	revision := canvasBumpRevision(d.Conn)
	node, _ := canvasNodeByID(d.Conn, nodeID)
	Emit(c, "canvas://changed", map[string]any{"node": node, "revision": revision})
	return canvasMutation(node, revision), nil
}

func containsInt64(s []int64, v int64) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func removeInt64(s []int64, v int64) []int64 {
	out := s[:0]
	for _, x := range s {
		if x != v {
			out = append(out, x)
		}
	}
	return out
}

// ── move_nodes ──

func canvasMoveNodes(c *web.Context, d *db.Database, moves []any) (any, error) {
	canvasMu.Lock()
	defer canvasMu.Unlock()
	now := nowRFC3339()
	applied := []map[string]any{}
	boards := []int64{}
	for _, m := range moves {
		mm, _ := m.(map[string]any)
		if mm == nil {
			continue
		}
		id := asInt64(pick(mm, "id"))
		var boardID int64
		if d.Conn.QueryRow(`SELECT board_id FROM canvas_node WHERE id = ?`, id).Scan(&boardID) != nil {
			continue
		}
		x, e1 := canvasClampCoord(getFloatAny(pick(mm, "x")))
		if e1 != nil {
			return nil, e1
		}
		y, e2 := canvasClampCoord(getFloatAny(pick(mm, "y")))
		if e2 != nil {
			return nil, e2
		}
		_, _ = d.Conn.Exec(`UPDATE canvas_node SET x = ?, y = ?, updated_at = ? WHERE id = ?`, x, y, now, id)
		boards = append(boards, boardID)
		applied = append(applied, map[string]any{"id": id, "x": x, "y": y})
	}
	if len(applied) == 0 {
		return canvasMutation([]any{}, canvasGetRevision(d.Conn)), nil
	}
	canvasTouchBoards(d.Conn, boards, now)
	revision := canvasBumpRevision(d.Conn)
	Emit(c, "canvas://changed", map[string]any{"moves": applied, "revision": revision})
	return canvasMutation(applied, revision), nil
}

// ── group_into_region ──

func canvasGroupIntoRegion(c *web.Context, d *db.Database, in map[string]any) (any, error) {
	boardID := asInt64(pick(in, "board_id", "boardId"))
	targetRegionID := asInt64(pick(in, "target_region_id", "targetRegionId"))
	memberIn := GetInt64SliceFrom(pick(in, "member_ids", "memberIds"))
	consumeIn := GetInt64SliceFrom(pick(in, "consume_node_ids", "consumeNodeIds"))
	// Geometry: all four or none.
	hasGeom := hasKey(in, "x") && hasKey(in, "y") && hasKey(in, "width") && hasKey(in, "height")
	anyGeom := hasKey(in, "x") || hasKey(in, "y") || hasKey(in, "width") || hasKey(in, "height")
	if anyGeom && !hasGeom {
		return nil, web.ErrInvalidInput("a region frame needs x, y, width and height together")
	}
	canvasMu.Lock()
	defer canvasMu.Unlock()
	if err := canvasRequireBoard(d.Conn, boardID); err != nil {
		return nil, err
	}
	// Dedupe members preserving order + liveness check.
	members := []int64{}
	for _, id := range memberIn {
		if containsInt64(members, id) {
			continue
		}
		if len(members) >= canvasMaxCustomMembers {
			return nil, web.ErrInvalidInput("a region holds at most 200 conversations")
		}
		if e := canvasRequireLiveConversation(d.Conn, id); e != nil {
			return nil, e
		}
		members = append(members, id)
	}
	now := nowRFC3339()
	var nodeID int64
	var finalMembers []int64
	if targetRegionID != 0 {
		var kind string
		var rboard int64
		var memRaw sql.NullString
		if d.Conn.QueryRow(`SELECT kind, board_id, member_ids FROM canvas_node WHERE id = ?`, targetRegionID).Scan(&kind, &rboard, &memRaw) == sql.ErrNoRows {
			return nil, web.ErrNotFound("canvas node not found")
		}
		if kind != "custom" {
			return nil, web.ErrInvalidInput("only custom regions can absorb conversations")
		}
		if rboard != boardID {
			return nil, web.ErrInvalidInput("the region is on a different canvas")
		}
		finalMembers = canvasParseMembers(memRaw)
		for _, id := range members {
			if containsInt64(finalMembers, id) {
				continue
			}
			if len(finalMembers) >= canvasMaxCustomMembers {
				return nil, web.ErrInvalidInput("a region holds at most 200 conversations")
			}
			finalMembers = append(finalMembers, id)
		}
		nodeID = targetRegionID
	} else {
		finalMembers = members
	}
	// Consume loose pinned conversation cards (adoption-checked).
	deletedIDs := []int64{}
	for _, id := range consumeIn {
		var k string
		var cb int64
		var cid sql.NullInt64
		if d.Conn.QueryRow(`SELECT kind, board_id, conversation_id FROM canvas_node WHERE id = ?`, id).Scan(&k, &cb, &cid) != nil {
			continue
		}
		if k != "conversation" || cb != boardID {
			continue
		}
		if !cid.Valid || !containsInt64(finalMembers, cid.Int64) {
			return nil, web.ErrInvalidInput("a consumed card's conversation must be a member of the region")
		}
		deletedIDs = append(deletedIDs, id)
	}
	for _, id := range deletedIDs {
		_, _ = d.Conn.Exec(`DELETE FROM canvas_node WHERE id = ?`, id)
	}
	if targetRegionID != 0 {
		_, _ = d.Conn.Exec(`UPDATE canvas_node SET member_ids = ?, updated_at = ? WHERE id = ?`, canvasEncodeMembers(finalMembers), now, nodeID)
	} else {
		if !hasGeom {
			return nil, web.ErrInvalidInput("a new region needs its geometry")
		}
		color, e := canvasNormalizeColor(pick(in, "color"))
		if e != nil {
			return nil, e
		}
		x, _ := canvasClampCoord(getFloatAny(pick(in, "x")))
		y, _ := canvasClampCoord(getFloatAny(pick(in, "y")))
		width, _ := canvasClampSize(getFloatAny(pick(in, "width")))
		height, _ := canvasClampSize(getFloatAny(pick(in, "height")))
		res, de := d.Conn.Exec(`INSERT INTO canvas_node (board_id, kind, member_ids, title, color, collapsed, grid_columns, grid_rows, x, y, width, height, created_at, updated_at)
			VALUES (?,'custom',?,?,?,0,?,?,?,?,?,?,?,?)`,
			boardID, canvasEncodeMembers(finalMembers), canvasNormalizeText(pick(in, "title")), color,
			canvasClampGridAxis(pick(in, "grid_columns", "gridColumns")), canvasClampGridAxis(pick(in, "grid_rows", "gridRows")),
			x, y, width, height, now, now)
		if de != nil {
			return nil, web.ErrDatabase(de.Error())
		}
		nodeID, _ = res.LastInsertId()
	}
	canvasTouchBoards(d.Conn, []int64{boardID}, now)
	revision := canvasBumpRevision(d.Conn)
	node, _ := canvasNodeByID(d.Conn, nodeID)
	result := map[string]any{"node": node, "deleted_ids": canvasIDsAsAny(deletedIDs)}
	Emit(c, "canvas://changed", map[string]any{"grouped": result, "revision": revision})
	return canvasMutation(result, revision), nil
}

func hasKey(m map[string]any, k string) bool {
	_, ok := m[k]
	return ok
}

func GetInt64SliceFrom(v any) []int64 {
	out := []int64{}
	arr, ok := v.([]any)
	if !ok {
		return out
	}
	for _, e := range arr {
		out = append(out, asInt64(e))
	}
	return out
}

// ── detach_member ──

func canvasDetachMember(c *web.Context, d *db.Database, regionID, conversationID int64, x, y float64) (any, error) {
	canvasMu.Lock()
	defer canvasMu.Unlock()
	var kind string
	var boardID int64
	var memRaw sql.NullString
	if d.Conn.QueryRow(`SELECT kind, board_id, member_ids FROM canvas_node WHERE id = ?`, regionID).Scan(&kind, &boardID, &memRaw) == sql.ErrNoRows {
		return nil, web.ErrNotFound("canvas node not found")
	}
	now := nowRFC3339()
	switch kind {
	case "custom":
		members := canvasParseMembers(memRaw)
		if !containsInt64(members, conversationID) {
			return nil, web.ErrNotFound("conversation is not a member of the region")
		}
		members = removeInt64(members, conversationID)
		_, _ = d.Conn.Exec(`UPDATE canvas_node SET member_ids = ?, updated_at = ? WHERE id = ?`, canvasEncodeMembers(members), now, regionID)
	case "folder", "group", "agent":
		// Binding regions copy; nothing to remove.
	default:
		return nil, web.ErrInvalidInput("canvas node is not a region")
	}
	if e := canvasRequireLiveConversation(d.Conn, conversationID); e != nil {
		return nil, e
	}
	cx, _ := canvasClampCoord(x)
	cy, _ := canvasClampCoord(y)
	res, de := d.Conn.Exec(`INSERT INTO canvas_node (board_id, kind, conversation_id, collapsed, grid_columns, grid_rows, x, y, width, height, created_at, updated_at)
		VALUES (?,'conversation',?,0,0,0,?,?,?,?,?,?)`,
		boardID, conversationID, cx, cy, canvasCardWidth, canvasCardHeight, now, now)
	if de != nil {
		return nil, web.ErrDatabase(de.Error())
	}
	id, _ := res.LastInsertId()
	canvasTouchBoards(d.Conn, []int64{boardID}, now)
	revision := canvasBumpRevision(d.Conn)
	node, _ := canvasNodeByID(d.Conn, id)
	Emit(c, "canvas://changed", map[string]any{"node": node, "revision": revision})
	return canvasMutation(node, revision), nil
}
