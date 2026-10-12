package handlers_test

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPetCodexImport(t *testing.T) {
	setHome(t)
	// Isolate upstream's pets root so the test neither reads nor pollutes the
	// developer's real YOYOCODE_DATA_DIR (petsRoot checks YOYOCODE_HOME first).
	t.Setenv("YOYOCODE_HOME", t.TempDir())
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	srv := newTestServer(t)

	// no codex pets dir yet → unavailable + empty importable
	_, avail := call(t, srv, "pet_codex_import_available", map[string]any{})
	if avail["available"] != false {
		t.Fatalf("codex import should be unavailable with no dir, got %v", avail)
	}

	// seed a codex pet (user's own data) under CODEX_HOME/pets/foo
	foo := filepath.Join(codexHome, "pets", "foo")
	if err := os.MkdirAll(foo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(foo, "pet.json"), []byte(`{"id":"foo","displayName":"Foo","spritesheetPath":"spritesheet.webp"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	sheet := append([]byte{'R', 'I', 'F', 'F', 0, 0, 0, 0, 'W', 'E', 'B', 'P'}, make([]byte, 2000)...)
	if err := os.WriteFile(filepath.Join(foo, "spritesheet.webp"), sheet, 0o644); err != nil {
		t.Fatal(err)
	}

	_, avail2 := call(t, srv, "pet_codex_import_available", map[string]any{})
	if avail2["available"] != true {
		t.Fatalf("codex import should now be available")
	}

	_, importable := callArr(t, srv, "pet_list_importable_codex", map[string]any{})
	if len(importable) != 1 || importable[0].(map[string]any)["id"] != "foo" {
		t.Fatalf("expected 1 importable codex pet, got %v", importable)
	}
	if importable[0].(map[string]any)["alreadyImported"] != false {
		t.Fatalf("foo should not be already imported")
	}

	// import all
	_, res := call(t, srv, "pet_import_codex", map[string]any{})
	ids, _ := res["importedIds"].([]any)
	if len(ids) != 1 || ids[0] != "foo" {
		t.Fatalf("import result wrong: %v", res)
	}
	// it now exists in upstream's store (check via the API, not a hardcoded path,
	// since the pets root follows YOYOCODE_DATA_DIR/YOYOCODE_HOME in tests).
	if _, got := call(t, srv, "pet_get", map[string]any{"id": "foo"}); got["id"] != "foo" {
		t.Fatalf("imported pet not visible via pet_get: %v", got)
	}

	// second import without overwrite → skipped
	_, res2 := call(t, srv, "pet_import_codex", map[string]any{})
	sk, _ := res2["skipped"].([]any)
	if len(sk) != 1 {
		t.Fatalf("re-import should skip the existing pet, got %v", res2)
	}
	// with overwriteWithSuffix → imported as foo-imported
	_, res3 := call(t, srv, "pet_import_codex", map[string]any{"overwriteWithSuffix": true})
	ids3, _ := res3["importedIds"].([]any)
	if len(ids3) != 1 || ids3[0] != "foo-imported" {
		t.Fatalf("overwrite-with-suffix wrong: %v", res3)
	}
}

func TestPetMarketplaceGraceful(t *testing.T) {
	srv := newTestServer(t)
	_, list := call(t, srv, "pet_marketplace_list", map[string]any{})
	pets, _ := list["pets"].([]any)
	if len(pets) != 0 || list["total"].(float64) != 0 {
		t.Fatalf("marketplace list should be empty, got %v", list)
	}
	if code, _ := call(t, srv, "pet_marketplace_install", map[string]any{"id": "x", "downloadUrl": "http://e/x"}); code == 200 {
		t.Fatalf("marketplace install should report unavailable")
	}
}
