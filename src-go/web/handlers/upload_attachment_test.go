package handlers_test

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"os"
	"path/filepath"
	"testing"
)

func TestUploadAttachment(t *testing.T) {
	setHome(t)
	home := t.TempDir()
	t.Setenv("YOYOCODE_HOME", home) // uploads land under <home>/uploads
	srv := newTestServer(t)

	post := func(fields map[string]string, fileName string, body []byte) (int, map[string]any) {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		for k, v := range fields {
			_ = mw.WriteField(k, v)
		}
		if fileName != "" {
			fw, _ := mw.CreateFormFile("file", fileName)
			_, _ = fw.Write(body)
		}
		mw.Close()
		resp, raw := postRaw(t, srv.URL+"/api/upload_attachment", mw.FormDataContentType(), buf.Bytes())
		var out map[string]any
		_ = json.Unmarshal(raw, &out)
		return resp.StatusCode, out
	}

	// basic upload
	code, out := post(map[string]string{"session_id": "sess-1"}, "notes.txt", []byte("hello"))
	if code != 200 {
		t.Fatalf("upload status = %d (%v)", code, out)
	}
	if out["name"] != "notes.txt" || out["size"].(float64) != 5 {
		t.Fatalf("result wrong: %v", out)
	}
	stored := filepath.Join(home, "uploads", "sess-1", "notes.txt")
	if b, err := os.ReadFile(stored); err != nil || string(b) != "hello" {
		t.Fatalf("stored file wrong: %q err=%v", b, err)
	}

	// collision → name (1).txt
	code2, out2 := post(map[string]string{"session_id": "sess-1"}, "notes.txt", []byte("again"))
	if code2 != 200 || out2["name"] != "notes (1).txt" {
		t.Fatalf("collision name wrong: %d %v", code2, out2)
	}

	// filename traversal sanitized
	_, out3 := post(map[string]string{"session_id": "sess-1"}, "../../etc/passwd", []byte("x"))
	if out3["name"] != "passwd" {
		t.Fatalf("traversal filename should sanitize to passwd, got %v", out3["name"])
	}

	// empty file rejected
	if c, _ := post(map[string]string{"session_id": "sess-1"}, "empty.txt", []byte("")); c == 200 {
		t.Fatalf("empty file should be rejected")
	}

	// missing file field rejected
	if c, _ := post(map[string]string{"session_id": "sess-1"}, "", nil); c == 200 {
		t.Fatalf("missing file field should be rejected")
	}

	// no session → anon bucket
	code6, out6 := post(map[string]string{}, "x.txt", []byte("y"))
	if code6 != 200 {
		t.Fatalf("anon upload status = %d", code6)
	}
	if _, err := os.Stat(filepath.Join(home, "uploads", "anon", "x.txt")); err != nil {
		t.Fatalf("anon bucket file missing: %v", err)
	}
	_ = out6
}
