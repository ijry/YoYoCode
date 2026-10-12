package handlers

// Temp-reclaim + binary-cache maintenance (upstream commands/acp.rs +
// acp/temp_reclaim.rs). Scans the system temp dir for artifacts leaked by
// agent launches from before per-launch temp isolation, and reclaims them.
//
// SAFETY: upstream also reclaims whole PyInstaller "_MEI*" unpack DIRECTORIES
// on Windows, but only after a Windows-specific handle probe proves the tree
// is idle (opening every mapped .dll/.pyd with sharing denied). That probe
// needs a share-mode CreateFile the Go stdlib does not expose, so this port
// conservatively reclaims only the per-launch resource FILES
// (…__py_binary_resource_…), which are read once at startup and safe to unlink
// even while mapped (the inode survives). Directories are listed-but-skipped,
// never deleted — the safe subset of the upstream behaviour.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"yoyocode/src-go/web"
)

const (
	tempMinAge     = 15 * time.Minute
	tempMaxEntries = 500
)

func isPyBinaryResource(name string) bool { return strings.Contains(name, "__py_binary_resource_") }

// tempReclaimable: only single resource files, never directories (see SAFETY).
func tempReclaimable(name string, isDir bool) bool {
	if isDir {
		return false
	}
	return isPyBinaryResource(name)
}

func tempDirSize(path string) int64 {
	var total int64
	_ = filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err == nil && info != nil && !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return total
}

// RegisterACPTemp wires acp_scan_leaked_temp / acp_reclaim_leaked_temp /
// acp_clear_binary_cache.
func RegisterACPTemp(reg *Registry) {
	reg.Register("acp_scan_leaked_temp", func(c *web.Context) (any, error) {
		root := os.TempDir()
		entries := []map[string]any{}
		var totalBytes int64
		skipped := 0
		dir, err := os.ReadDir(root)
		if err != nil {
			return map[string]any{"root": root, "entries": entries, "total_bytes": 0, "skipped": 0}, nil
		}
		now := time.Now()
		for _, e := range dir {
			name := e.Name()
			info, serr := e.Info()
			if serr != nil {
				continue
			}
			if info.Mode()&os.ModeSymlink != 0 {
				continue
			}
			isDir := e.IsDir()
			if !tempReclaimable(name, isDir) {
				continue
			}
			age := now.Sub(info.ModTime())
			if age < tempMinAge {
				skipped++
				continue
			}
			if len(entries) >= tempMaxEntries {
				skipped++
				continue
			}
			p := filepath.Join(root, name)
			var bytes int64
			if isDir {
				bytes = tempDirSize(p)
			} else {
				bytes = info.Size()
			}
			totalBytes += bytes
			entries = append(entries, map[string]any{
				"path":      p,
				"bytes":     bytes,
				"age_hours": int64(age.Hours()),
				"is_dir":    isDir,
			})
		}
		sort.SliceStable(entries, func(i, j int) bool {
			return entries[i]["bytes"].(int64) > entries[j]["bytes"].(int64)
		})
		return map[string]any{"root": root, "entries": entries, "total_bytes": totalBytes, "skipped": skipped}, nil
	})

	reg.Register("acp_reclaim_leaked_temp", func(c *web.Context) (any, error) {
		root := os.TempDir()
		var paths []string
		if raw := GetRaw(c, "paths"); len(raw) > 0 {
			_ = json.Unmarshal(raw, &paths)
		}
		removed := 0
		var freed int64
		failed := []string{}
		now := time.Now()
		for _, raw := range paths {
			p := filepath.Clean(raw)
			// Must sit DIRECTLY under the temp root (parent == root), so a
			// "...." escape cannot qualify.
			if filepath.Dir(p) != filepath.Clean(root) {
				continue
			}
			name := filepath.Base(p)
			info, serr := os.Lstat(p)
			if serr != nil {
				continue
			}
			if info.Mode()&os.ModeSymlink != 0 {
				continue
			}
			isDir := info.IsDir()
			if !tempReclaimable(name, isDir) {
				continue
			}
			if now.Sub(info.ModTime()) < tempMinAge {
				continue
			}
			var bytes int64
			if isDir {
				bytes = tempDirSize(p)
			} else {
				bytes = info.Size()
			}
			var rerr error
			if isDir {
				rerr = os.RemoveAll(p)
			} else {
				rerr = os.Remove(p)
			}
			if rerr != nil {
				failed = append(failed, p+": "+rerr.Error())
				continue
			}
			removed++
			freed += bytes
		}
		return map[string]any{"removed": removed, "freed_bytes": freed, "failed": failed}, nil
	})

	reg.Register("acp_clear_binary_cache", func(c *web.Context) (any, error) {
		// This build downloads no agent binaries (binary-distribution agents are
		// provider-specific and not launched here), so there is no cache to
		// clear — a no-op, exactly as the upstream is for a non-binary agent.
		return nil, nil
	})
}
