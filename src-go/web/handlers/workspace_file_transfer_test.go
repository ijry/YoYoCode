package handlers_test

import (
	"archive/zip"
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func postRaw(t *testing.T, url, contentType string, body []byte) (*http.Response, []byte) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	req.Header.Set("content-type", contentType)
	req.Header.Set("authorization", "Bearer tok")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post %s: %v", url, err)
	}
	out, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, out
}

func TestWorkspaceFileTransfer(t *testing.T) {
	srv := newTestServer(t)
	root := t.TempDir()

	// ── upload a file ──
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("root_path", root)
	_ = mw.WriteField("target_path", "sub")
	fw, _ := mw.CreateFormFile("file", "hello.txt")
	_, _ = fw.Write([]byte("hi there\n"))
	mw.Close()

	resp, _ := postRaw(t, srv.URL+"/api/upload_workspace_file", mw.FormDataContentType(), buf.Bytes())
	if resp.StatusCode != 200 {
		t.Fatalf("upload status = %d", resp.StatusCode)
	}
	onDisk := filepath.Join(root, "sub", "hello.txt")
	if b, err := os.ReadFile(onDisk); err != nil || string(b) != "hi there\n" {
		t.Fatalf("uploaded file wrong: %q err=%v", b, err)
	}

	// re-upload same path → already exists
	var buf2 bytes.Buffer
	mw2 := multipart.NewWriter(&buf2)
	_ = mw2.WriteField("root_path", root)
	_ = mw2.WriteField("target_path", "sub")
	fw2, _ := mw2.CreateFormFile("file", "hello.txt")
	_, _ = fw2.Write([]byte("again\n"))
	mw2.Close()
	if resp2, _ := postRaw(t, srv.URL+"/api/upload_workspace_file", mw2.FormDataContentType(), buf2.Bytes()); resp2.StatusCode == 200 {
		t.Fatalf("re-upload to existing path should fail")
	}

	// ── download the file ──
	resp3, body3 := postRaw(t, srv.URL+"/api/download_workspace_file", "application/json", []byte(`{"rootPath":"`+jsonEscape(root)+`","path":"sub/hello.txt"}`))
	if resp3.StatusCode != 200 {
		t.Fatalf("download status = %d (%s)", resp3.StatusCode, body3)
	}
	if string(body3) != "hi there\n" {
		t.Fatalf("downloaded bytes wrong: %q", body3)
	}
	if cd := resp3.Header.Get("Content-Disposition"); cd == "" {
		t.Fatalf("missing Content-Disposition")
	}

	// path traversal rejected
	if r, _ := postRaw(t, srv.URL+"/api/download_workspace_file", "application/json", []byte(`{"rootPath":"`+jsonEscape(root)+`","path":"../etc/passwd"}`)); r.StatusCode == 200 {
		t.Fatalf("traversal download should be refused")
	}

	// ── download the dir as zip ──
	resp4, body4 := postRaw(t, srv.URL+"/api/download_workspace_dir", "application/json", []byte(`{"rootPath":"`+jsonEscape(root)+`","path":"sub"}`))
	if resp4.StatusCode != 200 {
		t.Fatalf("zip status = %d", resp4.StatusCode)
	}
	zr, zerr := zip.NewReader(bytes.NewReader(body4), int64(len(body4)))
	if zerr != nil {
		t.Fatalf("zip parse: %v", zerr)
	}
	found := false
	for _, f := range zr.File {
		if f.Name == "sub/hello.txt" {
			found = true
		}
	}
	if !found {
		t.Fatalf("zip missing sub/hello.txt, has %d entries", len(zr.File))
	}
}
