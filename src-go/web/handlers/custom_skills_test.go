package handlers_test

import (
	"os"
	"path/filepath"
	"testing"
)

// setHome points os.UserHomeDir at a temp dir on every platform.
func setHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}

func TestCustomSkillsStoreCRUD(t *testing.T) {
	setHome(t)
	srv := newTestServer(t)

	// empty
	_, empty := callArr(t, srv, "custom_list", map[string]any{})
	if len(empty) != 0 {
		t.Fatalf("expected no custom skills, got %d", len(empty))
	}

	// create
	_, created := call(t, srv, "custom_create_skill", map[string]any{
		"id":      "my-skill",
		"content": "---\nname: My Skill\ndescription: does things\n---\nbody\n",
	})
	if created["id"] != "my-skill" || created["name"] != "My Skill" {
		t.Fatalf("create wrong: %v", created)
	}
	if created["description"] != "does things" {
		t.Fatalf("description not parsed: %v", created)
	}

	// creating again → already exists
	if code, _ := call(t, srv, "custom_create_skill", map[string]any{"id": "my-skill", "content": "x"}); code == 200 {
		t.Fatalf("duplicate create should fail")
	}

	// read
	code, read := rawPostJSON(t, srv, "custom_read_skill", map[string]any{"id": "my-skill"})
	if code != 200 {
		t.Fatalf("read failed: %d", code)
	}
	if read == "" || read == "null" {
		t.Fatalf("read returned empty: %q", read)
	}

	// save (edit)
	call(t, srv, "custom_save_skill", map[string]any{"id": "my-skill", "content": "---\nname: Renamed\n---\nx\n"})
	_, list := callArr(t, srv, "custom_list", map[string]any{})
	if len(list) != 1 || list[0].(map[string]any)["name"] != "Renamed" {
		t.Fatalf("save did not update name: %v", list)
	}

	// duplicate
	_, dup := call(t, srv, "custom_duplicate_skill", map[string]any{"sourceId": "my-skill", "newId": "copy-skill"})
	if dup["id"] != "copy-skill" {
		t.Fatalf("duplicate wrong: %v", dup)
	}
	_, list2 := callArr(t, srv, "custom_list", map[string]any{})
	if len(list2) != 2 {
		t.Fatalf("expected 2 skills after duplicate, got %d", len(list2))
	}

	// delete both (batch); report ok
	_, del := callArr(t, srv, "custom_delete_skills", map[string]any{"ids": []any{"my-skill", "copy-skill"}})
	if len(del) != 2 {
		t.Fatalf("expected 2 delete results, got %v", del)
	}
	for _, r := range del {
		if r.(map[string]any)["ok"] != true {
			t.Fatalf("delete failed: %v", r)
		}
	}
	_, after := callArr(t, srv, "custom_list", map[string]any{})
	if len(after) != 0 {
		t.Fatalf("expected empty after delete, got %d", len(after))
	}
}

func TestCustomSkillImport(t *testing.T) {
	setHome(t)
	srv := newTestServer(t)

	// import a bare .md file → wrapped as <id>/SKILL.md
	srcDir := t.TempDir()
	mdFile := filepath.Join(srcDir, "imported.md")
	if err := os.WriteFile(mdFile, []byte("---\nname: Imported\n---\nz\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, imp := call(t, srv, "custom_import_skill", map[string]any{"sourcePath": mdFile})
	if imp["id"] != "imported" {
		t.Fatalf("import id wrong: %v", imp)
	}
	// import a directory with SKILL.md
	dirSkill := filepath.Join(srcDir, "dir-skill")
	_ = os.MkdirAll(dirSkill, 0o755)
	if err := os.WriteFile(filepath.Join(dirSkill, "SKILL.md"), []byte("---\nname: Dir\n---\nq\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, imp2 := call(t, srv, "custom_import_skill", map[string]any{"sourcePath": dirSkill, "id": "dir-skill"})
	if imp2["id"] != "dir-skill" {
		t.Fatalf("import dir id wrong: %v", imp2)
	}
	_, list := callArr(t, srv, "custom_list", map[string]any{})
	if len(list) != 2 {
		t.Fatalf("expected 2 imported skills, got %d", len(list))
	}
}
