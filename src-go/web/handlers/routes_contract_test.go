package handlers_test

// Contract tests for the file/folder routes mcode-app drives. These pin the
// WIRE SHAPE, not just the behaviour: an earlier implementation answered
// get_file_tree with a flat {entries:[...]} wrapper and read_file_base64 with
// {data,size}, both of which the client parses differently — so a green
// behaviour test would have missed the breakage these lock down.

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// treeNames returns the `name` of every node in order.
func treeNames(nodes []any) []string {
	out := make([]string, 0, len(nodes))
	for _, n := range nodes {
		m, _ := n.(map[string]any)
		name, _ := m["name"].(string)
		out = append(out, name)
	}
	return out
}

func node(t *testing.T, v any) map[string]any {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("not a node object: %#v", v)
	}
	return m
}

// TestGetFileTreeContract locks upstream's shape: {path, maxDepth} in, a nested
// FileTreeNode[] out — `kind`/`name`/`path`, a dir's `children` always present,
// a file's absent, ordering dirs-then-files case-insensitively.
func TestGetFileTreeContract(t *testing.T) {
	srv := newTestServer(t)
	root := t.TempDir()

	write := func(rel, body string) {
		t.Helper()
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("README.md", "hi\n")
	write("src/main.go", "package main\n")
	write("src/util/helper.go", "package util\n")
	write(".hidden", "dotfiles are kept\n") // upstream ignores only the listed names
	write(".git/config", "[core]\n")        // ignored dir
	write("__pycache__/x.pyc", "x")         // ignored dir
	write(".DS_Store", "junk")              // ignored file

	code, arr := callArr(t, srv, "get_file_tree", map[string]any{"path": root})
	if code != 200 {
		t.Fatalf("get_file_tree status %d", code)
	}
	got := treeNames(arr)
	want := []string{"src", ".hidden", "README.md"} // dir first, then files, case-insensitive
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("top-level = %v, want %v", got, want)
	}

	src := node(t, arr[0])
	if src["kind"] != "dir" || src["path"] != "src" {
		t.Fatalf("src node = %v", src)
	}
	children, ok := src["children"].([]any)
	if !ok || len(children) != 2 {
		t.Fatalf("src children = %v", src["children"])
	}
	if got := treeNames(children); got[0] != "util" || got[1] != "main.go" {
		t.Fatalf("src children order = %v", got)
	}
	util := node(t, children[0])
	if util["path"] != "src/util" {
		t.Fatalf("nested path must be root-relative: %v", util["path"])
	}
	leaf := node(t, util["children"].([]any)[0])
	if leaf["kind"] != "file" || leaf["path"] != "src/util/helper.go" {
		t.Fatalf("leaf = %v", leaf)
	}
	if _, present := leaf["children"]; present {
		t.Fatalf("a file node must not carry children: %v", leaf)
	}

	// maxDepth=1 → the top level only, and a dir still carries `children: []`.
	_, shallow := callArr(t, srv, "get_file_tree", map[string]any{"path": root, "maxDepth": 1})
	if len(shallow) != 3 {
		t.Fatalf("maxDepth=1 top-level = %v", treeNames(shallow))
	}
	if ch, ok := node(t, shallow[0])["children"].([]any); !ok || len(ch) != 0 {
		t.Fatalf("maxDepth=1 should leave src childless: %v", node(t, shallow[0])["children"])
	}

	// An unknown path is an empty list, not an error (upstream's walk yields
	// nothing for a missing root).
	code, empty := callArr(t, srv, "get_file_tree", map[string]any{"path": filepath.Join(root, "nope")})
	if code != 200 || len(empty) != 0 {
		t.Fatalf("missing root = %d %v", code, empty)
	}
	if code, _ := call(t, srv, "get_file_tree", map[string]any{}); code != 400 {
		t.Fatalf("no path status = %d, want 400", code)
	}
}

// TestGetFileTreeSymlinkDir checks a symlinked directory is reported as an empty
// dir flagged `symlink` (the walk never descends through links).
func TestGetFileTreeSymlinkDir(t *testing.T) {
	srv := newTestServer(t)
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "real", "inner"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "real"), filepath.Join(root, "link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	_, arr := callArr(t, srv, "get_file_tree", map[string]any{"path": root})
	var link map[string]any
	for _, n := range arr {
		if m := node(t, n); m["name"] == "link" {
			link = m
		}
	}
	if link == nil {
		t.Fatalf("link missing from %v", treeNames(arr))
	}
	if link["kind"] != "dir" || link["symlink"] != true {
		t.Fatalf("link node = %v", link)
	}
	if ch, ok := link["children"].([]any); !ok || len(ch) != 0 {
		t.Fatalf("a symlinked dir must arrive empty: %v", link["children"])
	}
}

// TestReadFilePreviewContract locks {rootPath, path} → {path, content}: the path
// is workspace-relative, the whole file comes back, and the old wrapper keys are
// gone.
func TestReadFilePreviewContract(t *testing.T) {
	srv := newTestServer(t)
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sub", "a.txt"), []byte("line1\nline2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "bin.dat"), []byte{1, 0, 2}, 0o644); err != nil {
		t.Fatal(err)
	}

	code, out := call(t, srv, "read_file_preview", map[string]any{"rootPath": root, "path": "sub/a.txt"})
	if code != 200 {
		t.Fatalf("status %d: %v", code, out)
	}
	if out["path"] != "sub/a.txt" || out["content"] != "line1\nline2\n" {
		t.Fatalf("preview = %v", out)
	}
	if _, present := out["truncated"]; present {
		t.Fatalf("the old `truncated` key leaked: %v", out)
	}
	if _, present := out["size"]; present {
		t.Fatalf("the old `size` key leaked: %v", out)
	}

	cases := []struct {
		name string
		body map[string]any
		want int
	}{
		{"missing rootPath", map[string]any{"path": "sub/a.txt"}, 400},
		{"directory", map[string]any{"rootPath": root, "path": "sub"}, 400},
		{"traversal", map[string]any{"rootPath": root, "path": "../escape"}, 400},
		{"missing file", map[string]any{"rootPath": root, "path": "nope.txt"}, 404},
		{"binary", map[string]any{"rootPath": root, "path": "bin.dat"}, 400},
		{"root is not a dir", map[string]any{"rootPath": filepath.Join(root, "nope"), "path": "a"}, 404},
	}
	for _, tc := range cases {
		if code, _ := call(t, srv, "read_file_preview", tc.body); code != tc.want {
			t.Errorf("%s: status = %d, want %d", tc.name, code, tc.want)
		}
	}
}

// TestReadFileBase64Contract locks an ABSOLUTE path in and a BARE base64 string
// out — not the {data,size} wrapper the first implementation returned.
func TestReadFileBase64Contract(t *testing.T) {
	srv := newTestServer(t)
	dir := t.TempDir()
	small := filepath.Join(dir, "a.bin")
	payload := []byte("hello base64")
	if err := os.WriteFile(small, payload, 0o644); err != nil {
		t.Fatal(err)
	}
	big := filepath.Join(dir, "big.bin")
	if err := os.WriteFile(big, make([]byte, 5000), 0o644); err != nil {
		t.Fatal(err)
	}

	body, _ := jsonBody(map[string]any{"path": small})
	code, raw := rawPost(t, srv, "read_file_base64", body)
	if code != 200 {
		t.Fatalf("status %d: %s", code, raw)
	}
	var got string
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("response must be a bare JSON string, got %s", raw)
	}
	if got != base64.StdEncoding.EncodeToString(payload) {
		t.Fatalf("base64 = %q", got)
	}

	// maxBytes clamps UP to the 4 KiB floor, so a 5000-byte file is refused with
	// the limit in the error detail.
	code, out := call(t, srv, "read_file_base64", map[string]any{"path": big, "maxBytes": 4096})
	if code != 400 {
		t.Fatalf("oversized status = %d, want 400: %v", code, out)
	}
	if detail, _ := out["detail"].(string); !strings.Contains(detail, "max_bytes=4096") {
		t.Fatalf("detail should name the limit: %v", out)
	}
	// Raising maxBytes lets it through.
	if code, _ := call(t, srv, "read_file_base64", map[string]any{"path": big, "maxBytes": 8192}); code != 200 {
		t.Fatalf("larger maxBytes status = %d", code)
	}

	if code, _ := call(t, srv, "read_file_base64", map[string]any{"path": "  "}); code != 400 {
		t.Fatalf("empty path status = %d, want 400", code)
	}
	if code, _ := call(t, srv, "read_file_base64", map[string]any{"path": filepath.Join(dir, "nope")}); code != 404 {
		t.Fatalf("missing status = %d, want 404", code)
	}
	if code, _ := call(t, srv, "read_file_base64", map[string]any{"path": dir}); code != 400 {
		t.Fatalf("directory status = %d, want 400", code)
	}
}

// TestSaveFileCopyContract locks the sibling-copy behaviour: the original is
// left alone, the copy is named `<stem>.local.<ext>` then `.local.2.<ext>`, and
// the response is a FileSaveResult with a root-relative path.
func TestSaveFileCopyContract(t *testing.T) {
	srv := newTestServer(t)
	root := t.TempDir()
	orig := filepath.Join(root, "report.md")
	if err := os.WriteFile(orig, []byte("original\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "Makefile"), []byte("all:\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	code, out := call(t, srv, "save_file_copy", map[string]any{
		"rootPath": root, "path": "report.md", "content": "copy one\n",
	})
	if code != 200 {
		t.Fatalf("status %d: %v", code, out)
	}
	if out["path"] != "report.local.md" {
		t.Fatalf("first copy path = %v", out["path"])
	}
	for _, k := range []string{"etag", "mtime_ms", "readonly", "line_ending"} {
		if _, ok := out[k]; !ok {
			t.Errorf("FileSaveResult is missing %q: %v", k, out)
		}
	}
	if b, _ := os.ReadFile(orig); string(b) != "original\n" {
		t.Fatalf("the original must be untouched, got %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "report.local.md")); string(b) != "copy one\n" {
		t.Fatalf("copy content = %q", b)
	}

	// A second copy gets the numbered suffix.
	code, out = call(t, srv, "save_file_copy", map[string]any{
		"rootPath": root, "path": "report.md", "content": "copy two\n",
	})
	if code != 200 || out["path"] != "report.local.2.md" {
		t.Fatalf("second copy = %v (status %d)", out, code)
	}

	// A file with no extension keeps the suffix at the end.
	code, out = call(t, srv, "save_file_copy", map[string]any{
		"rootPath": root, "path": "Makefile", "content": "all:\n\techo hi\n",
	})
	if code != 200 || out["path"] != "Makefile.local" {
		t.Fatalf("extensionless copy = %v (status %d)", out, code)
	}

	// Errors: a missing source, a directory source, and a missing rootPath.
	if code, _ := call(t, srv, "save_file_copy", map[string]any{"rootPath": root, "path": "nope.md", "content": "x"}); code != 404 {
		t.Errorf("missing source status = %d, want 404", code)
	}
	if code, _ := call(t, srv, "save_file_copy", map[string]any{"rootPath": root, "path": ".", "content": "x"}); code != 400 {
		t.Errorf("directory source status = %d, want 400", code)
	}
	if code, _ := call(t, srv, "save_file_copy", map[string]any{"path": "report.md", "content": "x"}); code != 400 {
		t.Errorf("missing rootPath status = %d, want 400", code)
	}
}

// TestListDirectoryEntriesContract locks upstream's DirectoryEntry: only
// SUBDIRECTORIES are listed (files never appear), hidden dirs are skipped, and
// each row is {name, path, hasChildren} — not the {name, type} the first
// implementation returned.
func TestListDirectoryEntriesContract(t *testing.T) {
	srv := newTestServer(t)
	root := t.TempDir()
	mk := func(rel string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(rel)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mk("alpha")
	mk("alpha/child")
	mk("beta")
	mk(".hidden")
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	code, arr := callArr(t, srv, "list_directory_entries", map[string]any{"path": root})
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	if got := treeNames(arr); len(got) != 2 || got[0] != "alpha" || got[1] != "beta" {
		t.Fatalf("entries = %v (dirs only, hidden skipped, name-sorted)", got)
	}
	alpha := node(t, arr[0])
	if alpha["path"] != filepath.Join(root, "alpha") || alpha["hasChildren"] != true {
		t.Fatalf("alpha entry = %v", alpha)
	}
	if _, present := alpha["type"]; present {
		t.Fatalf("the old `type` key leaked: %v", alpha)
	}
	if beta := node(t, arr[1]); beta["hasChildren"] != false {
		t.Fatalf("beta entry = %v", beta)
	}

	// A non-directory is an io_error (HTTP 500), with the path in the detail.
	code, out := call(t, srv, "list_directory_entries", map[string]any{"path": filepath.Join(root, "file.txt")})
	if code != 500 {
		t.Fatalf("file path status = %d, want 500", code)
	}
	if detail, _ := out["detail"].(string); detail == "" {
		t.Fatalf("io_error should carry the path as detail: %v", out)
	}
}

// TestFolderHistoryContract locks the two folder-history routes: add answers
// with a FolderHistoryEntry, and both mutations answer with a literal `null`
// (upstream's Json<()>), not the {} the response helper used to emit.
func TestFolderHistoryContract(t *testing.T) {
	srv := newTestServer(t)
	dir := t.TempDir()

	body, _ := jsonBody(map[string]any{"path": dir})
	code, raw := rawPost(t, srv, "add_folder_to_history", body)
	if code != 200 {
		t.Fatalf("add folder status %d: %s", code, raw)
	}
	var folder map[string]any
	if err := json.Unmarshal([]byte(raw), &folder); err != nil {
		t.Fatalf("add_folder_to_history body: %s", raw)
	}
	for _, k := range []string{"id", "path", "name", "last_opened_at"} {
		if _, ok := folder[k]; !ok {
			t.Errorf("FolderHistoryEntry is missing %q: %v", k, folder)
		}
	}

	// A conversation mutation upstream declares Json<()> must be `null`.
	_, cid := callRaw(t, srv, "create_conversation", map[string]any{
		"folderId": folder["id"], "agentType": "claude_code",
	})
	upd, _ := jsonBody(map[string]any{"conversationId": cid, "title": "renamed"})
	if code, raw := rawPost(t, srv, "update_conversation_title", upd); code != 200 || strings.TrimSpace(raw) != "null" {
		t.Fatalf("update_conversation_title = %d %q, want 200 null", code, raw)
	}

	// remove_folder_from_history takes {path} and answers `null`.
	rm, _ := jsonBody(map[string]any{"path": folder["path"]})
	code, raw = rawPost(t, srv, "remove_folder_from_history", rm)
	if code != 200 {
		t.Fatalf("remove status %d: %s", code, raw)
	}
	if strings.TrimSpace(raw) != "null" {
		t.Fatalf("remove_folder_from_history body = %q, want null", raw)
	}
	if code, _ := call(t, srv, "remove_folder_from_history", map[string]any{}); code != 400 {
		t.Errorf("remove without a path status = %d, want 400", code)
	}
}

// TestGetFolderConversationContract locks the route's real semantics: it takes
// a CONVERSATION id (not a folder) and answers with the conversation detail —
// summary + turns — optionally windowed by tailTurns/fromIndex.
func TestGetFolderConversationContract(t *testing.T) {
	srv := newTestServer(t)
	_, folder := call(t, srv, "add_folder_to_history", map[string]any{"path": t.TempDir()})
	_, convID := callRaw(t, srv, "create_conversation", map[string]any{
		"folderId": folder["id"], "agentType": "claude_code", "title": "detail probe",
	})

	code, out := call(t, srv, "get_folder_conversation", map[string]any{"conversationId": convID})
	if code != 200 {
		t.Fatalf("status %d: %v", code, out)
	}
	summary, ok := out["summary"].(map[string]any)
	if !ok {
		t.Fatalf("summary missing: %v", out)
	}
	if summary["id"] != convID || summary["title"] != "detail probe" {
		t.Fatalf("summary = %v", summary)
	}
	if _, ok := out["turns"].([]any); !ok {
		t.Fatalf("turns must be an array: %v", out["turns"])
	}
	// No window requested → no window metadata (its absence is how the client
	// knows this is a full response).
	for _, k := range []string{"turns_offset", "turns_total", "assistant_turns_before_offset"} {
		if _, present := out[k]; present {
			t.Errorf("unwindowed response should omit %q: %v", k, out)
		}
	}

	// tailTurns turns the metadata on.
	_, win := call(t, srv, "get_folder_conversation", map[string]any{"conversationId": convID, "tailTurns": 5})
	for _, k := range []string{"turns_offset", "turns_total", "assistant_turns_before_offset"} {
		if _, present := win[k]; !present {
			t.Errorf("windowed response is missing %q: %v", k, win)
		}
	}

	// Both selectors at once is invalid, as upstream has it.
	if code, _ := call(t, srv, "get_folder_conversation", map[string]any{
		"conversationId": convID, "tailTurns": 5, "fromIndex": 1,
	}); code != 400 {
		t.Errorf("both selectors status = %d, want 400", code)
	}
	if code, _ := call(t, srv, "get_folder_conversation", map[string]any{}); code != 400 {
		t.Errorf("no conversationId status = %d, want 400", code)
	}
	if code, _ := call(t, srv, "get_folder_conversation", map[string]any{"conversationId": 999999}); code != 404 {
		t.Errorf("unknown conversation status = %d, want 404", code)
	}
}

// TestUpdateSystemLanguageSettingsContract locks upstream's envelope: the body
// is {settings: {mode, language}} and the same object comes back. The first
// implementation read a flat top-level `language`, so the client's write was
// silently dropped and the language never changed.
func TestUpdateSystemLanguageSettingsContract(t *testing.T) {
	srv := newTestServer(t)

	code, out := call(t, srv, "update_system_language_settings", map[string]any{
		"settings": map[string]any{"mode": "manual", "language": "en"},
	})
	if code != 200 {
		t.Fatalf("status %d: %v", code, out)
	}
	if out["mode"] != "manual" || out["language"] != "en" {
		t.Fatalf("update should echo the settings, got %v", out)
	}

	_, got := call(t, srv, "get_system_language_settings", map[string]any{})
	if got["mode"] != "manual" || got["language"] != "en" {
		t.Fatalf("read-back = %v, want manual/en", got)
	}

	// mode defaults to `system` and an unknown mode normalizes to it.
	_, def := call(t, srv, "update_system_language_settings", map[string]any{
		"settings": map[string]any{"language": "ja"},
	})
	if def["mode"] != "system" {
		t.Fatalf("mode should default to system, got %v", def["mode"])
	}
	_, odd := call(t, srv, "update_system_language_settings", map[string]any{
		"settings": map[string]any{"mode": "nonsense", "language": "ja"},
	})
	if odd["mode"] != "system" {
		t.Fatalf("unknown mode should normalize to system, got %v", odd["mode"])
	}
}

// TestListAllConversationsFilters locks upstream's filter semantics for the
// route the sidebar list is built from. Each filter is asserted against a small
// fixture so a dropped one shows up as an over-broad result.
func TestListAllConversationsFilters(t *testing.T) {
	srv := newTestServer(t)
	_, folder := call(t, srv, "add_folder_to_history", map[string]any{"path": t.TempDir()})
	folderID := folder["id"]

	mk := func(title, agentType, status string) any {
		t.Helper()
		_, cid := callRaw(t, srv, "create_conversation", map[string]any{
			"folderId": folderID, "agentType": agentType, "title": title, "status": status,
		})
		return cid
	}
	mk("fix the parser", "claude_code", "in_progress")
	mk("add a parser test", "codex", "completed")
	mk("unrelated chore", "claude_code", "completed")

	callList := func(body map[string]any) []any {
		t.Helper()
		code, arr := callArr(t, srv, "list_all_conversations", body)
		if code != 200 {
			t.Fatalf("list_all_conversations %d: %v", code, arr)
		}
		return arr
	}

	if got := len(callList(map[string]any{})); got != 3 {
		t.Fatalf("unfiltered = %d rows, want 3", got)
	}

	// search is a title substring.
	if got := callList(map[string]any{"search": "parser"}); len(got) != 2 {
		t.Errorf("search=parser = %d rows, want 2", len(got))
	}
	if got := callList(map[string]any{"search": "nothing matches"}); len(got) != 0 {
		t.Errorf("search with no hit = %d rows, want 0", len(got))
	}

	// agentType.
	if got := callList(map[string]any{"agentType": "codex"}); len(got) != 1 {
		t.Errorf("agentType=codex = %d rows, want 1", len(got))
	}

	// status, and the silent-ignore of an unparseable value.
	if got := callList(map[string]any{"status": "completed"}); len(got) != 2 {
		t.Errorf("status=completed = %d rows, want 2", len(got))
	}
	if got := callList(map[string]any{"status": "not-a-status"}); len(got) != 3 {
		t.Errorf("an unparseable status must be ignored, got %d rows", len(got))
	}

	// The default order is updated_at DESC and "oldest" flips it. Timestamps can
	// tie within the same second, so assert against the returned values rather
	// than assuming insertion order.
	descending := func(arr []any) bool {
		for i := 1; i < len(arr); i++ {
			if fmt.Sprintf("%v", node(t, arr[i-1])["updated_at"]) <
				fmt.Sprintf("%v", node(t, arr[i])["updated_at"]) {
				return false
			}
		}
		return true
	}
	ascending := func(arr []any) bool {
		for i := 1; i < len(arr); i++ {
			if fmt.Sprintf("%v", node(t, arr[i-1])["updated_at"]) >
				fmt.Sprintf("%v", node(t, arr[i])["updated_at"]) {
				return false
			}
		}
		return true
	}
	if got := callList(map[string]any{}); !descending(got) {
		t.Errorf("default order should be updated_at DESC: %v", got)
	}
	if got := callList(map[string]any{"sortBy": "oldest"}); !ascending(got) {
		t.Errorf("sortBy=oldest should be updated_at ASC: %v", got)
	}

	// folderIds wins over the "every live folder" default.
	if got := callList(map[string]any{"folderIds": []any{folderID}}); len(got) != 3 {
		t.Errorf("explicit folderIds = %d rows, want 3", len(got))
	}
	if got := callList(map[string]any{"folderIds": []any{999999}}); len(got) != 0 {
		t.Errorf("unknown folderIds = %d rows, want 0", len(got))
	}

	// Closing the folder hides its conversations from the unfiltered list —
	// an absent folderIds means "every folder that still exists".
	if _, err := call(t, srv, "remove_folder_from_history", map[string]any{"path": folder["path"]}); err == nil {
		t.Log("folder closed")
	}
	if got := callList(map[string]any{}); len(got) != 0 {
		t.Errorf("closed folder should hide its conversations, got %d rows", len(got))
	}
}

// TestConversationKindValues locks the ConversationKind values the client
// groups by: a normal conversation is `regular`, a chat is `chat` — not the
// placeholder 'agent' an earlier build left in the column default.
func TestConversationKindValues(t *testing.T) {
	srv := newTestServer(t)
	_, folder := call(t, srv, "add_folder_to_history", map[string]any{"path": t.TempDir()})
	_, cid := callRaw(t, srv, "create_conversation", map[string]any{
		"folderId": folder["id"], "agentType": "claude_code", "title": "kind probe",
	})
	_, detail := call(t, srv, "get_folder_conversation", map[string]any{"conversationId": cid})
	summary, _ := detail["summary"].(map[string]any)
	if summary == nil {
		t.Fatalf("no summary: %v", detail)
	}
	if got := summary["kind"]; got != "regular" {
		t.Errorf("create_conversation kind = %v, want regular", got)
	}
}

// TestCreateConversationReturnsBareID locks the upstream wire shape: the command
// answers a bare Json<i32> (the new conversation id), NOT an object. Our earlier
// port returned the full DbConversationSummary, which the mobile client tolerates
// but is not what upstream sends.
func TestCreateConversationReturnsBareID(t *testing.T) {
	srv := newTestServer(t)
	_, folder := call(t, srv, "add_folder_to_history", map[string]any{"path": t.TempDir()})
	fid := folder["id"]

	code, id := callRaw(t, srv, "create_conversation", map[string]any{
		"folderId": fid, "agentType": "claude_code", "title": "bare id probe",
	})
	if code != 200 {
		t.Fatalf("create_conversation status %d: %v", code, id)
	}
	n, ok := id.(float64)
	if !ok {
		t.Fatalf("create_conversation returned %T %v, want a bare number", id, id)
	}
	if n <= 0 {
		t.Fatalf("create_conversation id = %v, want > 0", n)
	}
}
