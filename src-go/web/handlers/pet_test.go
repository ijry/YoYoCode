package handlers_test

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestPetLocalCRUD(t *testing.T) {
	setHome(t)
	srv := newTestServer(t)

	// empty list
	_, empty := callArr(t, srv, "pet_list", map[string]any{})
	if len(empty) != 0 {
		t.Fatalf("expected no pets, got %d", len(empty))
	}

	// a synthetic "spritesheet": a WEBP magic header padded past the min size.
	blob := append([]byte{'R', 'I', 'F', 'F', 0, 0, 0, 0, 'W', 'E', 'B', 'P'}, make([]byte, 2000)...)
	b64 := base64.StdEncoding.EncodeToString(blob)

	// too-small sprite rejected
	if code, _ := call(t, srv, "pet_add", map[string]any{"id": "x", "displayName": "X", "spritesheetBase64": base64.StdEncoding.EncodeToString([]byte("tiny"))}); code == 200 {
		t.Fatalf("tiny sprite should be rejected")
	}

	// add
	_, added := call(t, srv, "pet_add", map[string]any{
		"id": "buddy", "displayName": "Buddy", "description": "my helper", "spritesheetBase64": b64,
	})
	if added["id"] != "buddy" || added["displayName"] != "Buddy" {
		t.Fatalf("add wrong: %v", added)
	}

	// duplicate rejected
	if code, _ := call(t, srv, "pet_add", map[string]any{"id": "buddy", "displayName": "B", "spritesheetBase64": b64}); code == 200 {
		t.Fatalf("duplicate pet should be rejected")
	}

	// list has 1
	_, list := callArr(t, srv, "pet_list", map[string]any{})
	if len(list) != 1 {
		t.Fatalf("expected 1 pet, got %d", len(list))
	}

	// read spritesheet → webp mime + base64 round-trips
	_, asset := call(t, srv, "pet_read_spritesheet", map[string]any{"id": "buddy"})
	if asset["mime"] != "image/webp" {
		t.Fatalf("mime = %v", asset["mime"])
	}
	if db, _ := asset["dataBase64"].(string); !strings.HasPrefix(db, "UklGR") {
		t.Fatalf("dataBase64 should encode RIFF, got %q", db)
	}

	// update meta
	_, upd := call(t, srv, "pet_update_meta", map[string]any{"id": "buddy", "patch": map[string]any{"displayName": "Buddy 2"}})
	if upd["displayName"] != "Buddy 2" {
		t.Fatalf("update wrong: %v", upd)
	}

	// settings default + set active + save window state
	_, cfg := call(t, srv, "pet_get_settings", map[string]any{})
	if cfg["scale"].(float64) != 0.75 || cfg["alwaysOnTop"] != true {
		t.Fatalf("default config wrong: %v", cfg)
	}
	_, act := call(t, srv, "pet_set_active", map[string]any{"petId": "buddy"})
	if act["activePetId"] != "buddy" {
		t.Fatalf("set active wrong: %v", act)
	}
	_, win := call(t, srv, "pet_save_window_state", map[string]any{"x": 10.0, "y": 20.0, "scale": 1.5, "enabled": true})
	if win["x"].(float64) != 10 || win["enabled"] != true {
		t.Fatalf("window state wrong: %v", win)
	}

	// current state + celebrate
	code, st := rawPostJSON(t, srv, "pet_get_current_state", map[string]any{})
	if code != 200 || trimWS(st) != "\"idle\"" {
		t.Fatalf("current state = %q", st)
	}
	if c2, _ := call(t, srv, "pet_celebrate", map[string]any{"kind": "jumping"}); c2 != 200 {
		t.Fatalf("celebrate failed")
	}
	if c3, _ := call(t, srv, "pet_celebrate", map[string]any{"kind": "bogus"}); c3 == 200 {
		t.Fatalf("bogus celebration kind should be rejected")
	}

	// delete
	if code, _ := call(t, srv, "pet_delete", map[string]any{"id": "buddy"}); code != 200 {
		t.Fatalf("delete failed")
	}
	_, after := callArr(t, srv, "pet_list", map[string]any{})
	if len(after) != 0 {
		t.Fatalf("expected empty after delete, got %d", len(after))
	}
}
