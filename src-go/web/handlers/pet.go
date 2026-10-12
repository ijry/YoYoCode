package handlers

// Pet feature (upstream commands/pet.rs + pets/*). The desktop pet is a small
// sprite window; this implements the data layer: a local store of USER-PROVIDED
// pet packs under <pets-root>/<id>/{pet.json, spritesheet.webp}, plus the
// persisted window config. No pet sprite art is bundled, generated or shipped
// here — every spritesheet is bytes the user supplies via pet_add /
// pet_replace_sprite. The marketplace (a remote catalog of ready-made packs)
// and the Codex-import scanner are a separate pass.
//
// Window open/close are pure desktop ops with no server equivalent and are not
// exposed, matching upstream's web handler.

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"yoyocode/src-go/web"
)

const (
	petManifestFile = "pet.json"
	petSpriteFile   = "spritesheet.webp"
	petConfigKey    = "pet.config"
	petMinSprite    = 1024
	petMaxSprite    = 16 * 1024 * 1024
)

func petsRoot() string {
	if v := strings.TrimSpace(os.Getenv("YOYOCODE_HOME")); v != "" {
		return filepath.Join(v, "pets")
	}
	if v := strings.TrimSpace(os.Getenv("YOYOCODE_DATA_DIR")); v != "" {
		return filepath.Join(v, "pets")
	}
	return filepath.Join(skillHomeDir(), ".yoyocode", "pets")
}

func petDir(id string) string { return filepath.Join(petsRoot(), id) }

type petManifest struct {
	ID              string  `json:"id"`
	DisplayName     string  `json:"displayName"`
	Description     *string `json:"description,omitempty"`
	SpritesheetPath string  `json:"spritesheetPath"`
}

func readPetManifest(id string) (*petManifest, bool) {
	b, err := os.ReadFile(filepath.Join(petDir(id), petManifestFile))
	if err != nil {
		return nil, false
	}
	var m petManifest
	if json.Unmarshal(b, &m) != nil {
		return nil, false
	}
	if m.ID == "" {
		m.ID = id
	}
	return &m, true
}

func petSummary(id string, m *petManifest) map[string]any {
	var desc any
	if m.Description != nil {
		desc = *m.Description
	}
	return map[string]any{
		"id":              m.ID,
		"displayName":     firstNonEmptyStr(m.DisplayName, id),
		"description":     desc,
		"spritesheetPath": filepath.Join(petDir(id), petSpriteFile),
	}
}

func petSpriteMime(b []byte) string {
	if len(b) >= 12 && string(b[0:4]) == "RIFF" && string(b[8:12]) == "WEBP" {
		return "image/webp"
	}
	if len(b) >= 8 && b[0] == 0x89 && b[1] == 'P' && b[2] == 'N' && b[3] == 'G' {
		return "image/png"
	}
	return "image/webp"
}

// petConfig reads the persisted PetWindowConfig, applying defaults.
func petConfig(c *web.Context) map[string]any {
	cfg := map[string]any{
		"enabled":     false,
		"activePetId": nil,
		"x":           nil,
		"y":           nil,
		"scale":       0.75,
		"alwaysOnTop": true,
	}
	d, err := requireDB(c)
	if err != nil {
		return cfg
	}
	raw := metadataValue(d, petConfigKey)
	if raw == "" {
		return cfg
	}
	var stored map[string]any
	if json.Unmarshal([]byte(raw), &stored) != nil {
		return cfg
	}
	for _, k := range []string{"enabled", "activePetId", "x", "y", "scale", "alwaysOnTop"} {
		if v, ok := stored[k]; ok {
			cfg[k] = v
		}
	}
	return cfg
}

func savePetConfig(c *web.Context, cfg map[string]any) error {
	d, err := requireDB(c)
	if err != nil {
		return err
	}
	b, _ := json.Marshal(cfg)
	return upsertMetadataValue(d, petConfigKey, string(b))
}

// RegisterPet wires the pet data-layer + settings endpoints.
func RegisterPet(reg *Registry) {
	reg.Register("pet_list", func(c *web.Context) (any, error) {
		root := petsRoot()
		entries, err := os.ReadDir(root)
		if err != nil {
			return []map[string]any{}, nil
		}
		ids := []string{}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			if _, ok := readPetManifest(e.Name()); ok {
				ids = append(ids, e.Name())
			}
		}
		sort.Strings(ids)
		out := make([]map[string]any, 0, len(ids))
		for _, id := range ids {
			m, _ := readPetManifest(id)
			out = append(out, petSummary(id, m))
		}
		return out, nil
	})

	reg.Register("pet_get", func(c *web.Context) (any, error) {
		id, err := validateSkillID(GetString(c, "id"))
		if err != nil {
			return nil, err
		}
		m, ok := readPetManifest(id)
		if !ok {
			return nil, web.ErrNotFound("pet not found: " + id)
		}
		return petSummary(id, m), nil
	})

	reg.Register("pet_read_spritesheet", func(c *web.Context) (any, error) {
		id, err := validateSkillID(GetString(c, "id"))
		if err != nil {
			return nil, err
		}
		b, rerr := os.ReadFile(filepath.Join(petDir(id), petSpriteFile))
		if rerr != nil {
			return nil, web.ErrNotFound("pet spritesheet not found: " + id)
		}
		return map[string]any{"mime": petSpriteMime(b), "dataBase64": base64.StdEncoding.EncodeToString(b)}, nil
	})

	reg.Register("pet_add", func(c *web.Context) (any, error) {
		id, err := validateSkillID(GetString(c, "id"))
		if err != nil {
			return nil, err
		}
		if _, e := os.Stat(petDir(id)); e == nil {
			return nil, web.ErrAlreadyExists("a pet with id '" + id + "' already exists")
		}
		sprite, derr := decodePetSprite(GetString(c, "spritesheetBase64", "spritesheet_base64"))
		if derr != nil {
			return nil, derr
		}
		dir := petDir(id)
		if e := os.MkdirAll(dir, 0o755); e != nil {
			return nil, web.ErrIO(e.Error())
		}
		m := &petManifest{ID: id, DisplayName: firstNonEmptyStr(GetString(c, "displayName", "display_name"), id), SpritesheetPath: petSpriteFile}
		if desc := GetString(c, "description"); desc != "" {
			m.Description = &desc
		}
		if e := writePetManifest(id, m); e != nil {
			return nil, e
		}
		if e := os.WriteFile(filepath.Join(dir, petSpriteFile), sprite, 0o644); e != nil {
			return nil, web.ErrIO(e.Error())
		}
		return petSummary(id, m), nil
	})

	reg.Register("pet_update_meta", func(c *web.Context) (any, error) {
		id, err := validateSkillID(GetString(c, "id"))
		if err != nil {
			return nil, err
		}
		m, ok := readPetManifest(id)
		if !ok {
			return nil, web.ErrNotFound("pet not found: " + id)
		}
		patch, _ := c.Body["patch"].(map[string]any)
		if patch == nil {
			patch = c.Body
		}
		if dn, ok := patch["displayName"].(string); ok && dn != "" {
			m.DisplayName = dn
		}
		if v, present := patch["description"]; present {
			if sv, ok := v.(string); ok {
				m.Description = &sv
			} else {
				m.Description = nil
			}
		}
		if e := writePetManifest(id, m); e != nil {
			return nil, e
		}
		return petSummary(id, m), nil
	})

	reg.Register("pet_replace_sprite", func(c *web.Context) (any, error) {
		id, err := validateSkillID(GetString(c, "id"))
		if err != nil {
			return nil, err
		}
		if _, ok := readPetManifest(id); !ok {
			return nil, web.ErrNotFound("pet not found: " + id)
		}
		sprite, derr := decodePetSprite(GetString(c, "spritesheetBase64", "spritesheet_base64"))
		if derr != nil {
			return nil, derr
		}
		if e := os.WriteFile(filepath.Join(petDir(id), petSpriteFile), sprite, 0o644); e != nil {
			return nil, web.ErrIO(e.Error())
		}
		return nil, nil
	})

	reg.Register("pet_delete", func(c *web.Context) (any, error) {
		id, err := validateSkillID(GetString(c, "id"))
		if err != nil {
			return nil, err
		}
		dir := petDir(id)
		root := petsRoot()
		rel, rerr := filepath.Rel(root, dir)
		if rerr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, web.ErrIO("refusing to delete outside the pets store")
		}
		if e := os.RemoveAll(dir); e != nil {
			return nil, web.ErrIO(e.Error())
		}
		return nil, nil
	})

	reg.Register("pet_get_settings", func(c *web.Context) (any, error) {
		return petConfig(c), nil
	})

	reg.Register("pet_set_active", func(c *web.Context) (any, error) {
		cfg := petConfig(c)
		if v, present := c.Body["petId"]; present {
			if sv, ok := v.(string); ok && sv != "" {
				if _, ok := readPetManifest(sv); !ok {
					return nil, web.ErrNotFound("pet not found: " + sv)
				}
				cfg["activePetId"] = sv
			} else {
				cfg["activePetId"] = nil
			}
		} else if v, present := c.Body["pet_id"]; present {
			if sv, ok := v.(string); ok && sv != "" {
				cfg["activePetId"] = sv
			} else {
				cfg["activePetId"] = nil
			}
		}
		if e := savePetConfig(c, cfg); e != nil {
			return nil, e
		}
		Emit(c, "pet://active-changed", cfg)
		return cfg, nil
	})

	reg.Register("pet_save_window_state", func(c *web.Context) (any, error) {
		cfg := petConfig(c)
		for _, k := range []string{"x", "y", "scale", "alwaysOnTop", "enabled"} {
			if v, present := c.Body[k]; present {
				cfg[k] = v
			}
		}
		if e := savePetConfig(c, cfg); e != nil {
			return nil, e
		}
		return cfg, nil
	})

	reg.Register("pet_get_current_state", func(c *web.Context) (any, error) {
		// No live ACP→pet state mapper in this build; ambient state is idle.
		return "idle", nil
	})

	reg.Register("pet_celebrate", func(c *web.Context) (any, error) {
		kind := GetString(c, "kind")
		switch kind {
		case "jumping", "waving", "failed":
		default:
			return nil, web.ErrInvalidInput("kind must be jumping, waving or failed")
		}
		Emit(c, "pet://oneshot", kind)
		return nil, nil
	})
}

func writePetManifest(id string, m *petManifest) error {
	b, _ := json.MarshalIndent(m, "", "  ")
	if e := os.WriteFile(filepath.Join(petDir(id), petManifestFile), append(b, 0x0a), 0o644); e != nil {
		return web.ErrIO(e.Error())
	}
	return nil
}

func decodePetSprite(b64 string) ([]byte, error) {
	b64 = strings.TrimSpace(b64)
	if b64 == "" {
		return nil, web.ErrInvalidInput("spritesheetBase64 required")
	}
	if i := strings.Index(b64, ","); strings.HasPrefix(b64, "data:") && i >= 0 {
		b64 = b64[i+1:]
	}
	data, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, web.ErrInvalidInput("invalid base64 spritesheet")
	}
	if len(data) < petMinSprite {
		return nil, web.ErrInvalidInput("spritesheet too small")
	}
	if len(data) > petMaxSprite {
		return nil, web.ErrInvalidInput("spritesheet too large")
	}
	return data, nil
}
