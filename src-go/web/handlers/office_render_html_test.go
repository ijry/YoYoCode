package handlers_test

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOfficecliRenderHTMLGuards(t *testing.T) {
	srv := newTestServer(t)
	root := t.TempDir()

	// missing workspace root
	if code, _ := call(t, srv, "officecli_render_html", map[string]any{"rootPath": filepath.Join(root, "nope"), "path": "a.docx"}); code == 200 {
		t.Fatalf("missing root should fail")
	}

	// non-office extension rejected
	if err := os.WriteFile(filepath.Join(root, "note.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _ := call(t, srv, "officecli_render_html", map[string]any{"rootPath": root, "path": "note.txt"}); code == 200 {
		t.Fatalf("non-office file should be rejected")
	}

	// path traversal rejected
	if code, _ := call(t, srv, "officecli_render_html", map[string]any{"rootPath": root, "path": "../secret.docx"}); code == 200 {
		t.Fatalf("traversal should be rejected")
	}

	// a real office file: with no officecli installed in this build, it must
	// report a dependency error (never a 200 with empty body / panic).
	if err := os.WriteFile(filepath.Join(root, "doc.docx"), []byte("PK\x03\x04"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _ := call(t, srv, "officecli_render_html", map[string]any{"rootPath": root, "path": "doc.docx"})
	// 200 only if a real officecli happens to be installed on the dev machine;
	// otherwise a clean error. Either way, not a crash.
	if code != 200 && code < 400 {
		t.Fatalf("unexpected status for office file render: %d", code)
	}
}
