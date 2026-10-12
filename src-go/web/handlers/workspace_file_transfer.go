package handlers

// Workspace file transfer over HTTP (upstream web/handlers/workspace_files.rs):
// upload a file into the opened workspace, download a single file, or download
// a directory as a streamed zip. Server mode has no native file dialog, so
// these move files between the browser/mobile client and the workspace. All
// paths are workspace-relative and jailed under the root (resolveTreePath +
// ensureNavigable); downloads stream straight to the client (c.Handled).

import (
	"archive/zip"
	"bytes"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"yoyocode/src-go/web"
)

// RegisterWorkspaceFileTransfer wires upload/download of workspace files.
func RegisterWorkspaceFileTransfer(reg *Registry) {
	reg.Register("upload_workspace_file", uploadWorkspaceFile)
	reg.Register("download_workspace_file", downloadWorkspaceFile)
	reg.Register("download_workspace_dir", downloadWorkspaceDir)
	registerWorkspaceDownloadTickets(reg)
}

// ── path sanitizers (mirror sanitize_segment / sanitize_relative_subpath /
// compute_final_rel) ─────────────────────────────────────────────────────────

func sanitizeSegment(raw string) string {
	var b strings.Builder
	for _, c := range raw {
		if c < 0x20 || c == 0x7f {
			continue // control chars dropped
		}
		switch c {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|', 0:
			b.WriteByte('_')
		default:
			b.WriteRune(c)
		}
	}
	trimmed := strings.TrimRight(strings.TrimSpace(b.String()), ".")
	if trimmed == "" {
		return "file"
	}
	allDots := true
	for _, c := range trimmed {
		if c != '.' {
			allDots = false
			break
		}
	}
	if allDots {
		return "file"
	}
	return trimmed
}

func sanitizeRelativeSubpath(raw string) (string, error) {
	rawParts := []string{}
	for _, s := range strings.FieldsFunc(raw, func(r rune) bool { return r == '/' || r == '\\' }) {
		if s == "" || s == "." {
			continue
		}
		rawParts = append(rawParts, s)
	}
	if len(rawParts) == 0 {
		return "", web.ErrInvalidInput("Invalid upload path")
	}
	for _, p := range rawParts {
		if p == ".." {
			return "", web.ErrInvalidInput("Path cannot contain '..'")
		}
	}
	out := make([]string, len(rawParts))
	for i, p := range rawParts {
		out[i] = sanitizeSegment(p)
	}
	return strings.Join(out, "/"), nil
}

func computeFinalRel(targetDir, relativePath, fileNameHint string) (string, error) {
	targetDirClean := strings.TrimRight(strings.TrimSpace(targetDir), "/\\")
	var body string
	if strings.TrimSpace(relativePath) != "" {
		b, err := sanitizeRelativeSubpath(relativePath)
		if err != nil {
			return "", err
		}
		body = b
	} else {
		last := fileNameHint
		if i := strings.LastIndexAny(fileNameHint, "/\\"); i >= 0 {
			last = fileNameHint[i+1:]
		}
		body = sanitizeSegment(last)
	}
	combined := body
	if targetDirClean != "" {
		dir, err := sanitizeRelativeSubpath(targetDirClean)
		if err != nil {
			return "", err
		}
		combined = dir + "/" + body
	}
	// Re-validate the joined path as relative components only (no "..").
	for _, seg := range strings.Split(combined, "/") {
		if seg == ".." {
			return "", web.ErrInvalidInput("Path cannot contain '..'")
		}
	}
	return combined, nil
}

// attachmentHeader mirrors attachment_header: a header-safe ASCII filename plus
// a UTF-8 filename* for clients that honor RFC 5987.
func attachmentHeader(name string) string {
	var safe strings.Builder
	for _, c := range name {
		if c < 0x20 || c == 0x7f || c == '"' || c == '\\' || c > 0x7f {
			safe.WriteByte('_')
		} else {
			safe.WriteRune(c)
		}
	}
	return "attachment; filename=\"" + safe.String() + "\"; filename*=UTF-8''" + url.PathEscape(name)
}

// ── download (single file) ───────────────────────────────────────────────────

func resolveDownloadFileTarget(rootPath, relPath string) (string, error) {
	if err := requireFolderDir(rootPath); err != nil {
		return "", err
	}
	target, err := resolveTreePath(rootPath, relPath)
	if err != nil {
		return "", err
	}
	info, serr := os.Stat(target)
	if serr != nil {
		return "", web.ErrNotFound("File does not exist")
	}
	if info.IsDir() {
		return "", web.ErrInvalidInput("Path is not a file")
	}
	if e := ensureNavigable(rootPath, target); e != nil {
		return "", e
	}
	return target, nil
}

func downloadWorkspaceFile(c *web.Context) (any, error) {
	rootPath := GetString(c, "rootPath", "root_path")
	relPath := GetString(c, "path")
	target, err := resolveDownloadFileTarget(rootPath, relPath)
	if err != nil {
		return nil, err
	}
	info, serr := os.Stat(target)
	if serr != nil {
		return nil, web.ErrIO(serr.Error())
	}
	f, oerr := os.Open(target)
	if oerr != nil {
		return nil, web.ErrIO(oerr.Error())
	}
	defer f.Close()
	name := filepath.Base(target)
	streamFileToWriter(c.W, f, info.Size(), name)
	c.Handled = true
	return nil, nil
}

// streamFileToWriter writes an open file to w with download headers.
func streamFileToWriter(w http.ResponseWriter, f *os.File, size int64, name string) {
	h := w.Header()
	h.Set("Content-Type", "application/octet-stream")
	h.Set("Content-Length", strconv.FormatInt(size, 10))
	h.Set("Content-Disposition", attachmentHeader(name))
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, f)
}

// ── download (directory as zip) ──────────────────────────────────────────────

func resolveDownloadDirTarget(rootPath, relPath string) (string, string, error) {
	if err := requireFolderDir(rootPath); err != nil {
		return "", "", err
	}
	if strings.TrimSpace(relPath) == "" {
		name := filepath.Base(rootPath)
		if name == "" || name == "." || name == string(filepath.Separator) {
			name = "workspace"
		}
		return rootPath, name, nil
	}
	resolved, err := resolveTreePath(rootPath, relPath)
	if err != nil {
		return "", "", err
	}
	info, serr := os.Stat(resolved)
	if serr != nil {
		return "", "", web.ErrNotFound("Directory does not exist")
	}
	if !info.IsDir() {
		return "", "", web.ErrInvalidInput("Path is not a directory")
	}
	if e := ensureNavigable(rootPath, resolved); e != nil {
		return "", "", e
	}
	name := filepath.Base(resolved)
	if name == "" {
		name = "folder"
	}
	return resolved, name, nil
}

func downloadWorkspaceDir(c *web.Context) (any, error) {
	rootPath := GetString(c, "rootPath", "root_path")
	relPath := GetString(c, "path")
	dirPath, dirName, err := resolveDownloadDirTarget(rootPath, relPath)
	if err != nil {
		return nil, err
	}
	streamZipToWriter(c.W, dirPath, dirName)
	c.Handled = true
	return nil, nil
}

// streamZipToWriter writes dirPath as a zip to w (symlinks skipped).
func streamZipToWriter(w http.ResponseWriter, dirPath, dirName string) {
	h := w.Header()
	h.Set("Content-Type", "application/zip")
	h.Set("Content-Disposition", attachmentHeader(dirName+".zip"))
	w.WriteHeader(http.StatusOK)
	zw := zip.NewWriter(w)
	defer zw.Close()
	_ = filepath.Walk(dirPath, func(path string, info os.FileInfo, werr error) error {
		if werr != nil || info.IsDir() {
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		rel, rerr := filepath.Rel(dirPath, path)
		if rerr != nil {
			return nil
		}
		zipName := filepath.ToSlash(filepath.Join(dirName, rel))
		zwf, cerr := zw.Create(zipName)
		if cerr != nil {
			return nil
		}
		src, oerr := os.Open(path)
		if oerr != nil {
			return nil
		}
		defer src.Close()
		_, _ = io.Copy(zwf, src)
		return nil
	})
}

// ── upload ───────────────────────────────────────────────────────────────────

func uploadWorkspaceFile(c *web.Context) (any, error) {
	ct := ""
	if c.R != nil {
		ct = c.R.Header.Get("Content-Type")
	}
	mediaType, params, err := mime.ParseMediaType(ct)
	if err != nil || !strings.HasPrefix(mediaType, "multipart/") {
		return nil, web.ErrInvalidInput("expected multipart/form-data upload")
	}
	boundary := params["boundary"]
	if boundary == "" {
		return nil, web.ErrInvalidInput("multipart boundary missing")
	}
	mr := multipart.NewReader(bytes.NewReader(c.RawBody), boundary)

	var rootPath, targetPath, relativePath string
	var result map[string]any

	for {
		part, perr := mr.NextPart()
		if perr == io.EOF {
			break
		}
		if perr != nil {
			return nil, web.ErrIO("invalid multipart upload: " + perr.Error())
		}
		field := part.FormName()
		switch field {
		case "root_path", "rootPath":
			b, _ := io.ReadAll(part)
			rootPath = string(b)
		case "target_path", "targetPath":
			b, _ := io.ReadAll(part)
			targetPath = string(b)
		case "relative_path", "relativePath":
			b, _ := io.ReadAll(part)
			relativePath = string(b)
		case "file":
			if result != nil {
				return nil, web.ErrInvalidInput("Multiple `file` fields are not supported per request")
			}
			if strings.TrimSpace(rootPath) == "" {
				return nil, web.ErrInvalidInput("root_path field must appear before the file field")
			}
			if err := requireFolderDir(rootPath); err != nil {
				return nil, web.ErrNotFound("Workspace folder does not exist")
			}
			nameHint := part.FileName()
			if nameHint == "" {
				nameHint = "file"
			}
			finalRel, frErr := computeFinalRel(targetPath, relativePath, nameHint)
			if frErr != nil {
				return nil, frErr
			}
			finalAbs, raErr := resolveTreePath(rootPath, finalRel)
			if raErr != nil {
				return nil, raErr
			}
			parent := filepath.Dir(finalAbs)
			if mkErr := os.MkdirAll(parent, 0o755); mkErr != nil {
				return nil, web.ErrIO("Failed to create upload directory: " + mkErr.Error())
			}
			if e := ensureNavigable(rootPath, finalAbs); e != nil {
				return nil, web.ErrInvalidInput("Resolved path escapes workspace root")
			}
			if info, se := os.Stat(finalAbs); se == nil {
				if info.IsDir() {
					return nil, web.ErrInvalidInput("Refusing to overwrite an existing directory with a file")
				}
				return nil, web.ErrAlreadyExists("A file with this name already exists")
			}
			staging := filepath.Join(parent, ".yoyocode-upload-"+newChatUUID()+".part")
			out, ocErr := os.OpenFile(staging, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
			if ocErr != nil {
				return nil, web.ErrIO("Failed to create staging file: " + ocErr.Error())
			}
			written, cpErr := io.Copy(out, part)
			out.Close()
			if cpErr != nil {
				_ = os.Remove(staging)
				return nil, web.ErrIO("Failed to write chunk: " + cpErr.Error())
			}
			// Commit atomically: hard-link (errors if the dest exists) then drop
			// staging; fall back to rename on filesystems without hard links.
			commitErr := os.Link(staging, finalAbs)
			if commitErr == nil {
				_ = os.Remove(staging)
			} else if os.IsExist(commitErr) {
				_ = os.Remove(staging)
				return nil, web.ErrAlreadyExists("A file with this name already exists")
			} else {
				if rnErr := os.Rename(staging, finalAbs); rnErr != nil {
					_ = os.Remove(staging)
					return nil, web.ErrIO("Failed to commit upload")
				}
			}
			if e := ensureNavigable(rootPath, finalAbs); e != nil {
				_ = os.Remove(finalAbs)
				return nil, e
			}
			if _, se := os.Lstat(finalAbs); se != nil {
				return nil, web.ErrIO("upload commit verification failed")
			}
			result = map[string]any{
				"path": finalRel,
				"name": filepath.Base(finalAbs),
				"size": written,
			}
		}
		part.Close()
	}

	if result == nil {
		return nil, web.ErrInvalidInput("no file field in upload")
	}
	return result, nil
}

// baseName returns filepath.Base(p), or fallback for empty / root results.
func baseName(p, fallback string) string {
	n := filepath.Base(p)
	if n == "" || n == "." || n == string(filepath.Separator) {
		return fallback
	}
	return n
}

// openForDownload opens target and returns its handle + info.
func openForDownload(target string) (*os.File, os.FileInfo, error) {
	info, err := os.Stat(target)
	if err != nil {
		return nil, nil, err
	}
	f, oerr := os.Open(target)
	if oerr != nil {
		return nil, nil, oerr
	}
	return f, info, nil
}
