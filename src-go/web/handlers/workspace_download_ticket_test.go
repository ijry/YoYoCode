package handlers_test

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestWorkspaceDownloadTicket(t *testing.T) {
	srv := newTestServer(t)
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sub", "hello.txt"), []byte("ticketed\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// mint a file ticket
	code, out := call(t, srv, "workspace_download_ticket", map[string]any{
		"rootPath": root, "path": "sub/hello.txt", "kind": "file",
	})
	if code != 200 {
		t.Fatalf("ticket status = %d (%v)", code, out)
	}
	ticketURL, _ := out["url"].(string)
	if ticketURL == "" {
		t.Fatalf("no url in ticket: %v", out)
	}
	if out["filename"] != "hello.txt" {
		t.Fatalf("filename wrong: %v", out["filename"])
	}

	// consume it (GET, no auth needed for the raw route)
	resp, err := http.Get(srv.URL + ticketURL)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(body) != "ticketed\n" {
		t.Fatalf("consume wrong: %d %q", resp.StatusCode, body)
	}

	// single-use: second consume → 404
	resp2, _ := http.Get(srv.URL + ticketURL)
	io.Copy(io.Discard, resp2.Body)
	resp2.Body.Close()
	if resp2.StatusCode == 200 {
		t.Fatalf("ticket should be single-use")
	}

	// a dir ticket → zip
	code3, out3 := call(t, srv, "workspace_download_ticket", map[string]any{
		"rootPath": root, "path": "sub", "kind": "dir",
	})
	if code3 != 200 {
		t.Fatalf("dir ticket status = %d", code3)
	}
	resp3, _ := http.Get(srv.URL + out3["url"].(string))
	zbody, _ := io.ReadAll(resp3.Body)
	resp3.Body.Close()
	if resp3.StatusCode != 200 || resp3.Header.Get("Content-Type") != "application/zip" {
		t.Fatalf("dir ticket consume wrong: %d %s", resp3.StatusCode, resp3.Header.Get("Content-Type"))
	}
	if len(zbody) == 0 {
		t.Fatalf("empty zip")
	}

	// invalid kind
	if c, _ := call(t, srv, "workspace_download_ticket", map[string]any{"rootPath": root, "path": "x", "kind": "nope"}); c == 200 {
		t.Fatalf("invalid kind should fail")
	}

	// bogus ticket → 404
	rb, _ := http.Get(srv.URL + "/workspace_download/deadbeef")
	io.Copy(io.Discard, rb.Body)
	rb.Body.Close()
	if rb.StatusCode == 200 {
		t.Fatalf("bogus ticket should 404")
	}
}
