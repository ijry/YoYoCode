package handlers

// Backup / restore read endpoints (upstream commands/backup/restore.rs +
// web/handlers/backup.rs). This implements the two advisory read routes that
// need neither the archive file-transfer machinery nor a running restore:
//   - backup_active_agents: which agents are live, so the UI can warn before a
//     restore to original locations. The headless build tracks no live ACP
//     agents at this layer, so it reports none (advisory only).
//   - backup_list_safety_snapshots: the restore safety snapshots on disk under
//     <data_dir>/.yoyocode-restore-backup/, newest first.
// The write/transfer surface (ticket/upload/stage/restore/rollback/cancel/
// download) is a larger archive-streaming subsystem handled separately.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"

	"yoyocode/src-go/web"
)

const (
	backupSafetyDir        = ".yoyocode-restore-backup"
	backupSnapshotManifest = ".yoyocode-snapshot.json"
)

func backupDirSize(dir string) int64 {
	var total int64
	_ = filepath.Walk(dir, func(_ string, info os.FileInfo, err error) error {
		if err == nil && info != nil && !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return total
}

// backupReadSnapshotManifest returns (createdAt, true) when the dir holds a
// valid v1/staging snapshot manifest.
func backupReadSnapshotManifest(dir string) (string, bool) {
	raw, err := os.ReadFile(filepath.Join(dir, backupSnapshotManifest))
	if err != nil {
		return "", false
	}
	var m struct {
		V         int    `json:"v"`
		Layout    string `json:"layout"`
		CreatedAt string `json:"created_at"`
	}
	if json.Unmarshal(raw, &m) != nil || m.V != 1 || m.Layout != "staging" {
		return "", false
	}
	return m.CreatedAt, true
}

// RegisterBackup wires the backup/restore advisory read endpoints.
func RegisterBackup(reg *Registry) {
	reg.Register("backup_active_agents", func(c *web.Context) (any, error) {
		// Advisory only; this build tracks no live ACP agents here.
		return []string{}, nil
	})

	reg.Register("backup_list_safety_snapshots", func(c *web.Context) (any, error) {
		root := filepath.Join(appDataDir(c), backupSafetyDir)
		entries, err := os.ReadDir(root)
		if err != nil {
			return []map[string]any{}, nil
		}
		type sortable struct {
			key  string
			snap map[string]any
		}
		var items []sortable
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			dir := filepath.Join(root, e.Name())
			createdAt, hasManifest := backupReadSnapshotManifest(dir)
			var createdVal any
			if hasManifest && createdAt != "" {
				createdVal = createdAt
			}
			snap := map[string]any{
				"id":                 e.Name(),
				"path":               dir,
				"created_at":         createdVal,
				"size_bytes":         backupDirSize(dir),
				"rollback_supported": hasManifest,
			}
			// Newest first: sort key is the created timestamp when known, else
			// the directory name (both monotonic in practice).
			key := e.Name()
			if createdAt != "" {
				key = createdAt
			}
			items = append(items, sortable{key: key, snap: snap})
		}
		sort.SliceStable(items, func(i, j int) bool { return items[i].key > items[j].key })
		out := make([]map[string]any, 0, len(items))
		for _, it := range items {
			out = append(out, it.snap)
		}
		return out, nil
	})
}
