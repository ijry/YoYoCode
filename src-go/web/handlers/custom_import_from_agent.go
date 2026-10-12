package handlers

// custom_import_from_agent (upstream commands/custom_skills.rs). Copies selected
// GLOBAL skills from one agent's skill dirs into the central custom store, so a
// skill authored for a single agent becomes a shared custom skill. Per-skill
// results — found/copied, idempotent skip when already in the store, or a
// per-item error — so one bad id never aborts the batch. Project-scoped skills
// are deliberately excluded (workspace-specific).

import (
	"encoding/json"
	"os"
	"path/filepath"

	"yoyocode/src-go/web"
)

// RegisterCustomImportFromAgent wires custom_import_from_agent.
func RegisterCustomImportFromAgent(reg *Registry) {
	reg.Register("custom_import_from_agent", func(c *web.Context) (any, error) {
		agentType := GetString(c, "agentType", "agent_type")
		spec, ok := skillStorageSpecFor(agentType)
		if !ok {
			return nil, web.ErrTaskExecution("unsupported agent: " + agentType)
		}
		var ids []string
		if raw := GetRaw(c, "ids"); len(raw) > 0 {
			_ = json.Unmarshal(raw, &ids)
		}
		out := make([]map[string]any, 0, len(ids))
		for _, rawID := range ids {
			out = append(out, importOneSkillFromAgent(spec, rawID))
		}
		return out, nil
	})
}

// importOneSkillFromAgent copies one of an agent's global skills into the
// central store. Never bubbles — returns a per-skill result.
func importOneSkillFromAgent(spec skillStorageSpec, rawID string) map[string]any {
	item, found := locateExistingSkillAcrossDirs(spec.globalDirs, spec.kind, rawID, skillScopeGlobal)
	if !found {
		return map[string]any{
			"id":      rawID,
			"name":    rawID,
			"ok":      false,
			"skipped": false,
			"error":   "skill '" + rawID + "' not found in the agent's skill directory",
		}
	}
	id := asString(item["id"])
	name := asString(item["name"])

	// Already in the shared store (linked pack or previously imported)? Idempotent skip.
	dst := customCentralPath(id)
	if _, se := os.Stat(dst); se == nil || pathIsSymlink(dst) {
		return map[string]any{"id": id, "name": name, "ok": false, "skipped": true, "error": nil}
	}

	// Guard: refuse ids a built-in pack owns / malformed ids.
	if _, verr := validateSkillID(id); verr != nil {
		return map[string]any{"id": id, "name": name, "ok": false, "skipped": false, "error": verr.Error()}
	}

	src := asString(item["path"])
	var copyErr error
	if asString(item["layout"]) == skillLayoutDirectory.wire() {
		copyErr = copyDirRecursive(src, dst)
	} else {
		// Standalone .md → <dst>/SKILL.md
		copyErr = copyFileContents(src, filepath.Join(dst, "SKILL.md"))
	}
	if copyErr != nil {
		// Roll back a partial copy so a retry sees a clean slate (guarded: under
		// the store and not a link).
		if _, se := os.Stat(dst); se == nil && !pathIsSymlink(dst) {
			_ = os.RemoveAll(dst)
		}
		return map[string]any{"id": id, "name": name, "ok": false, "skipped": false, "error": copyErr.Error()}
	}
	return map[string]any{"id": id, "name": name, "ok": true, "skipped": false, "error": nil}
}
