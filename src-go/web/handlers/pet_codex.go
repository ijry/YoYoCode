package handlers

// Pet Codex-import + marketplace (upstream pets/codex_import.rs +
// pets/marketplace.rs). Codex stores pets at ${CODEX_HOME:-~/.codex}/pets/<id>/
// in the same {pet.json, spritesheet.webp} layout; import copies the USER'S OWN
// Codex pet directories into upstream's store. The marketplace is a remote catalog
// of ready-made packs — not fetched here (its assets are third-party art), so
// list returns an empty page and install/asset report unavailable.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"yoyocode/src-go/web"
)

func codexPetsDir() string {
	if v := strings.TrimSpace(os.Getenv("CODEX_HOME")); v != "" {
		return filepath.Join(v, "pets")
	}
	return filepath.Join(skillHomeDir(), ".codex", "pets")
}

// listImportableCodexPets scans the Codex pets dir for eligible entries
// (manifest parses + spritesheet.webp present).
func listImportableCodexPets() []map[string]any {
	root := codexPetsDir()
	entries, err := os.ReadDir(root)
	if err != nil {
		return []map[string]any{}
	}
	ids := []string{}
	byID := map[string]map[string]any{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		id := e.Name()
		src := filepath.Join(root, id)
		mb, merr := os.ReadFile(filepath.Join(src, petManifestFile))
		if merr != nil {
			continue
		}
		var m petManifest
		if json.Unmarshal(mb, &m) != nil {
			continue
		}
		if fi, se := os.Stat(filepath.Join(src, petSpriteFile)); se != nil || fi.IsDir() {
			continue
		}
		displayName := firstNonEmptyStr(m.DisplayName, id)
		var desc any
		if m.Description != nil {
			desc = *m.Description
		}
		_, already := readPetManifest(id)
		ids = append(ids, id)
		byID[id] = map[string]any{
			"id":              id,
			"displayName":     displayName,
			"description":     desc,
			"sourcePath":      src,
			"alreadyImported": already,
		}
	}
	sort.Strings(ids)
	out := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		out = append(out, byID[id])
	}
	return out
}

func uniqueImportedID(baseID string) string {
	candidate := baseID + "-imported"
	n := 2
	for {
		if _, err := os.Stat(petDir(candidate)); err != nil {
			return candidate
		}
		candidate = baseID + "-imported-" + itoaInt(n)
		n++
	}
}

func itoaInt(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// RegisterPetCodex wires the Codex-import + marketplace endpoints.
func RegisterPetCodex(reg *Registry) {
	reg.Register("pet_codex_import_available", func(c *web.Context) (any, error) {
		_, err := os.Stat(codexPetsDir())
		return map[string]any{"available": err == nil}, nil
	})

	reg.Register("pet_list_importable_codex", func(c *web.Context) (any, error) {
		return listImportableCodexPets(), nil
	})

	reg.Register("pet_import_codex", func(c *web.Context) (any, error) {
		var ids []string
		if raw := GetRaw(c, "ids"); len(raw) > 0 {
			_ = json.Unmarshal(raw, &ids)
		}
		wanted := map[string]bool{}
		for _, id := range ids {
			wanted[id] = true
		}
		overwrite := GetBool(c, "overwriteWithSuffix", "overwrite_with_suffix")

		imported := []string{}
		skipped := []map[string]any{}
		if e := os.MkdirAll(petsRoot(), 0o755); e != nil {
			return nil, web.ErrIO(e.Error())
		}
		for _, pet := range listImportableCodexPets() {
			id := pet["id"].(string)
			if len(wanted) > 0 && !wanted[id] {
				continue
			}
			already, _ := pet["alreadyImported"].(bool)
			targetID := id
			if already {
				if !overwrite {
					skipped = append(skipped, map[string]any{"sourceId": id, "reason": "Pet with this id already exists in YoYoCode."})
					continue
				}
				targetID = uniqueImportedID(id)
			}
			srcDir := pet["sourcePath"].(string)
			dstDir := petDir(targetID)
			if e := copyPetDir(srcDir, dstDir, targetID); e != nil {
				skipped = append(skipped, map[string]any{"sourceId": id, "reason": e.Error()})
				continue
			}
			imported = append(imported, targetID)
		}
		return map[string]any{"importedIds": imported, "skipped": skipped}, nil
	})

	// Marketplace: remote catalog of ready-made packs (third-party art). Not
	// fetched/reproduced here — an empty page, and install/asset report that
	// the marketplace is unavailable in this build.
	reg.Register("pet_marketplace_list", func(c *web.Context) (any, error) {
		pageSize := GetInt(c, "pageSize", "page_size")
		if pageSize <= 0 {
			pageSize = 20
		}
		return map[string]any{"pets": []any{}, "page": 1, "pageSize": pageSize, "total": 0, "totalPages": 0}, nil
	})
	reg.Register("pet_marketplace_install", func(c *web.Context) (any, error) {
		return nil, web.ErrConfigInvalid("the pet marketplace is not available in this build")
	})
	reg.Register("pet_marketplace_asset", func(c *web.Context) (any, error) {
		return nil, web.ErrConfigInvalid("the pet marketplace is not available in this build")
	})
}

// copyPetDir copies pet.json + spritesheet.webp into the target dir, rewriting
// the manifest id to targetID.
func copyPetDir(src, dst, targetID string) error {
	mb, err := os.ReadFile(filepath.Join(src, petManifestFile))
	if err != nil {
		return web.ErrIO("source manifest missing: " + err.Error())
	}
	var m petManifest
	if json.Unmarshal(mb, &m) != nil {
		return web.ErrIO("source manifest unreadable")
	}
	sheet, serr := os.ReadFile(filepath.Join(src, petSpriteFile))
	if serr != nil {
		return web.ErrIO("source spritesheet missing: " + serr.Error())
	}
	if e := os.MkdirAll(dst, 0o755); e != nil {
		return web.ErrIO(e.Error())
	}
	m.ID = targetID
	m.SpritesheetPath = petSpriteFile
	if e := writePetManifest(targetID, &m); e != nil {
		return e
	}
	if e := os.WriteFile(filepath.Join(dst, petSpriteFile), sheet, 0o644); e != nil {
		return web.ErrIO(e.Error())
	}
	return nil
}
