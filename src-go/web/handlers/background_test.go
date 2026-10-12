package handlers

import (
	"encoding/base64"
	"encoding/binary"
	"os"
	"testing"
)

func testPNG(w, h uint32) []byte {
	b := make([]byte, 64)
	copy(b, []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a})
	b[8], b[9], b[10], b[11] = 0, 0, 0, 13
	copy(b[12:16], []byte("IHDR"))
	binary.BigEndian.PutUint32(b[16:20], w)
	binary.BigEndian.PutUint32(b[20:24], h)
	return b
}

func TestBackgroundSetReadClearRoundTrip(t *testing.T) {
	root := t.TempDir()
	// No background yet → nil (JSON null).
	if got, err := readBackgroundAt(root); err != nil || got != nil {
		t.Fatalf("empty read = (%v, %v), want (nil, nil)", got, err)
	}
	png := testPNG(100, 100)
	b64 := base64.StdEncoding.EncodeToString(png)
	if err := setBackgroundAt(root, b64); err != nil {
		t.Fatalf("set: %v", err)
	}
	got, err := readBackgroundAt(root)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("read result type %T, want map", got)
	}
	if m["mime"] != "image/png" {
		t.Fatalf("mime = %v, want image/png", m["mime"])
	}
	if m["dataBase64"] != b64 {
		t.Fatalf("dataBase64 round-trip mismatch")
	}
	if err := clearBackgroundAt(root); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if got, err := readBackgroundAt(root); err != nil || got != nil {
		t.Fatalf("post-clear read = (%v, %v), want (nil, nil)", got, err)
	}
	// clear is idempotent.
	if err := clearBackgroundAt(root); err != nil {
		t.Fatalf("second clear: %v", err)
	}
}

func TestValidateBackground(t *testing.T) {
	if err := validateBackground([]byte{0, 1, 2}); err == nil {
		t.Fatal("expected too-small rejection")
	}
	nonImage := make([]byte, 4096)
	for i := range nonImage {
		nonImage[i] = 0x42
	}
	if err := validateBackground(nonImage); err == nil {
		t.Fatal("expected non-image rejection")
	}
	if err := validateBackground(testPNG(64, 64)); err != nil {
		t.Fatalf("valid PNG rejected: %v", err)
	}
	// 40 Mpx pixel cap: a header declaring 10000x10000 (100 Mpx) is refused.
	if err := validateBackground(testPNG(10000, 10000)); err == nil {
		t.Fatal("expected pixel-cap rejection")
	}
}

func TestSniffMime(t *testing.T) {
	cases := []struct {
		name string
		b    []byte
		want string
	}{
		{"png", testPNG(8, 8), "image/png"},
		{"jpeg", append([]byte{0xFF, 0xD8, 0xFF, 0xE0}, make([]byte, 8)...), "image/jpeg"},
		{"webp", append(append([]byte("RIFF"), 0, 0, 0, 0), []byte("WEBPvp8 ")...), "image/webp"},
		{"gif89", append([]byte("GIF89a"), make([]byte, 8)...), "image/gif"},
		{"gif87", append([]byte("GIF87a"), make([]byte, 8)...), "image/gif"},
	}
	for _, c := range cases {
		if got := sniffMime(c.b); got != c.want {
			t.Errorf("%s: sniffMime = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestWallhavenHostValidation(t *testing.T) {
	ok := []string{
		"https://wallhaven.cc/w/abc123",
		"https://w.wallhaven.cc/full/ab/wallhaven-abc123.jpg",
		"https://th.wallhaven.cc/small/ab/abc123.jpg",
	}
	for _, u := range ok {
		if _, err := parseWallhavenHTTPSURL(u); err != nil {
			t.Errorf("expected %q allowed, got %v", u, err)
		}
	}
	bad := []string{
		"http://wallhaven.cc/w/x",          // not https
		"https://evil.example/full/x.jpg",  // off host
		"https://wallhaven.cc.evil.com/x",  // suffix trick
		"https://user:pw@wallhaven.cc/w/x", // userinfo
		"ftp://wallhaven.cc/x",             // scheme
	}
	for _, u := range bad {
		if _, err := parseWallhavenHTTPSURL(u); err == nil {
			t.Errorf("expected %q rejected", u)
		}
	}
}

func TestWallhavenCategories(t *testing.T) {
	cases := map[string]string{"": "111", "all": "111", "general": "100", "anime": "010", "people": "001"}
	for in, want := range cases {
		got, err := wallhavenCategories(in)
		if err != nil || got != want {
			t.Errorf("categories(%q) = (%q, %v), want %q", in, got, err, want)
		}
	}
	if _, err := wallhavenCategories("nsfw"); err == nil {
		t.Fatal("expected unknown category rejected")
	}
}

func TestMarketDownloadRejectsBadSource(t *testing.T) {
	root := t.TempDir()
	// Valid full URL, but source is not a /w/<id> page → error before any fetch.
	err := marketDownload(root, "https://w.wallhaven.cc/full/ab/wallhaven-abc.jpg", "https://wallhaven.cc/search?q=x")
	if err == nil {
		t.Fatal("expected bad sourceUrl rejection")
	}
	// Off-host full URL → rejected at parse, no network.
	if err := marketDownload(root, "https://evil.example/x.jpg", "https://wallhaven.cc/w/abc"); err == nil {
		t.Fatal("expected off-host full URL rejection")
	}
	if _, err := os.Stat(root + "/background.img"); !os.IsNotExist(err) {
		t.Fatal("no background should have been written")
	}
}

func TestParseSearchPayloadFiltersAndDerives(t *testing.T) {
	fixture := `{
      "data": [
        {
          "id": "abc123",
          "url": "https://wallhaven.cc/w/abc123",
          "path": "https://w.wallhaven.cc/full/ab/wallhaven-abc123.jpg",
          "thumbs": { "small": "https://th.wallhaven.cc/small/ab/abc123.jpg" },
          "dimension_x": 1920,
          "dimension_y": 1080,
          "file_size": 4455320,
          "category": "general"
        },
        {
          "id": "bad9",
          "url": "https://wallhaven.cc/w/bad9",
          "path": "https://evil.example/full/wallhaven-bad9.jpg",
          "thumbs": { "small": "https://th.wallhaven.cc/small/ba/bad9.jpg" },
          "dimension_x": 800,
          "dimension_y": 600,
          "file_size": 91234,
          "category": "anime"
        }
      ],
      "meta": { "current_page": 2, "last_page": 10, "per_page": 24, "total": 240 }
    }`
	got, err := parseSearchPayload([]byte(fixture))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	page := got.(map[string]any)
	items := page["items"].([]map[string]any)
	if len(items) != 1 {
		t.Fatalf("expected 1 item (off-host dropped), got %d", len(items))
	}
	it := items[0]
	if it["id"] != "abc123" {
		t.Errorf("id = %v", it["id"])
	}
	if it["sourceUrl"] != "https://wallhaven.cc/w/abc123" {
		t.Errorf("sourceUrl = %v (should be derived from id)", it["sourceUrl"])
	}
	if page["page"].(uint32) != 2 || page["lastPage"].(uint32) != 10 {
		t.Errorf("page/lastPage = %v/%v, want 2/10", page["page"], page["lastPage"])
	}
}
