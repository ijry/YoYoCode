package handlers

// upload_attachment (upstream web/handlers/files.rs). Streams one multipart
// file into <uploads-root>/<session-bucket>/, jailed under the uploads root,
// with a sanitized filename (collision-suffixed), a 20 MiB per-file cap, and an
// optional total-size quota (YOYOCODE_UPLOAD_MAX_TOTAL_BYTES). Returns the stored
// path the composer turns back into a file:// uri.

import (
	"bytes"
	"io"
	"mime"
	"mime/multipart"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"yoyocode/src-go/web"
)

const uploadMaxBytes = 20 * 1024 * 1024
const uploadFilenameMaxChars = 120
const uploadCollisionSuffixAttempts = 999
const uploadUUIDFallbackAttempts = 16

// RegisterUploadAttachment wires upload_attachment.
func RegisterUploadAttachment(reg *Registry) {
	reg.Register("upload_attachment", uploadAttachment)
}

func yoyocodeUploadsRoot() string {
	if v := strings.TrimSpace(os.Getenv("YOYOCODE_HOME")); v != "" {
		return filepath.Join(v, "uploads")
	}
	if v := strings.TrimSpace(os.Getenv("YOYOCODE_DATA_DIR")); v != "" {
		return filepath.Join(v, "uploads")
	}
	return filepath.Join(skillHomeDir(), ".yoyocode", "uploads")
}

func allDots(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c != '.' {
			return false
		}
	}
	return true
}

func sanitizeUploadFilename(raw string) string {
	base := raw
	if i := strings.LastIndexAny(raw, "/\\"); i >= 0 {
		base = raw[i+1:]
	}
	base = strings.TrimSpace(base)
	var b strings.Builder
	for _, c := range base {
		if c < 0x20 || c == 0x7f {
			continue
		}
		switch c {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|', 0:
			b.WriteByte('_')
		default:
			b.WriteRune(c)
		}
	}
	trimmed := strings.TrimRight(strings.TrimSpace(b.String()), ".")
	limited := truncateChars(trimmed, uploadFilenameMaxChars)
	if limited == "" || allDots(limited) {
		return "file"
	}
	return limited
}

func splitUploadFilename(name string) (string, string) {
	idx := strings.LastIndex(name, ".")
	if idx > 0 {
		return name[:idx], name[idx:]
	}
	return name, ""
}

func uploadFilenameWithStemSuffix(safeName, stemSuffix string) string {
	stem, ext := splitUploadFilename(safeName)
	suffixChars := len([]rune(stemSuffix))
	extBudget := uploadFilenameMaxChars - (suffixChars + 1)
	if extBudget < 0 {
		extBudget = 0
	}
	extPart := truncateChars(ext, extBudget)
	stemBudget := uploadFilenameMaxChars - (suffixChars + len([]rune(extPart)))
	if stemBudget < 1 {
		stemBudget = 1
	}
	stemPart := truncateChars(stem, stemBudget)
	if stemPart == "" || allDots(stemPart) {
		stemPart = "file"
	}
	return stemPart + stemSuffix + extPart
}

func uploadFilenameCandidate(safeName string, collisionIndex int) string {
	if collisionIndex == 0 {
		return safeName
	}
	return uploadFilenameWithStemSuffix(safeName, " ("+itoaInt(collisionIndex)+")")
}

func sanitizeSessionBucket(raw string) string {
	var b strings.Builder
	for _, c := range raw {
		switch {
		case (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z'):
			b.WriteRune(c)
		case c == '-' || c == '_':
			b.WriteRune(c)
		default:
			b.WriteByte('_')
		}
	}
	trimmed := strings.Trim(b.String(), "_")
	limited := truncateChars(trimmed, 80)
	if limited == "" {
		return "anon"
	}
	return limited
}

func currentUploadsTotalBytes(root string) int64 {
	var total int64
	_ = filepath.Walk(root, func(_ string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		total += info.Size()
		return nil
	})
	return total
}

func uploadQuotaCap() int64 {
	if v := strings.TrimSpace(os.Getenv("YOYOCODE_UPLOAD_MAX_TOTAL_BYTES")); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			return n
		}
	}
	return 0
}

// finalizeUploadName commits the staging file into bucketDir under the first
// available (collision-suffixed) name, mirroring finalize_with_available_upload_name.
func finalizeUploadName(staging, bucketDir, safeName string) (string, error) {
	for i := 0; i <= uploadCollisionSuffixAttempts; i++ {
		cand := uploadFilenameCandidate(safeName, i)
		if err := os.Link(staging, filepath.Join(bucketDir, cand)); err == nil {
			_ = os.Remove(staging)
			return cand, nil
		} else if !os.IsExist(err) {
			// Hard links may be unsupported (cross-device): fall back to rename
			// into a reserved name created O_EXCL.
			dst := filepath.Join(bucketDir, cand)
			if f, oerr := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644); oerr == nil {
				f.Close()
				if rerr := os.Rename(staging, dst); rerr != nil {
					_ = os.Remove(dst)
					return "", web.ErrIO("Failed to move staged upload into place: " + rerr.Error())
				}
				return cand, nil
			} else if !os.IsExist(oerr) {
				return "", web.ErrIO("Failed to move staged upload into place: " + oerr.Error())
			}
		}
	}
	for i := 0; i < uploadUUIDFallbackAttempts; i++ {
		cand := uploadFilenameWithStemSuffix(safeName, "-"+newChatUUID())
		if err := os.Link(staging, filepath.Join(bucketDir, cand)); err == nil {
			_ = os.Remove(staging)
			return cand, nil
		}
	}
	return "", web.ErrIO("Failed to choose an available upload filename")
}

func uploadAttachment(c *web.Context) (any, error) {
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

	uploadsRoot := yoyocodeUploadsRoot()
	if e := os.MkdirAll(uploadsRoot, 0o755); e != nil {
		return nil, web.ErrIO("Failed to create uploads root: " + e.Error())
	}

	// Quota check before staging any bytes.
	if cap := uploadQuotaCap(); cap > 0 {
		reserve := int64(uploadMaxBytes)
		if c.R != nil {
			if cl, perr := strconv.ParseInt(c.R.Header.Get("Content-Length"), 10, 64); perr == nil && cl > 0 && cl < reserve {
				reserve = cl
			}
		}
		if currentUploadsTotalBytes(uploadsRoot)+reserve > cap {
			return nil, web.ErrIO("Upload quota exceeded for this server")
		}
	}

	tmpDir := filepath.Join(uploadsRoot, ".tmp")
	if e := os.MkdirAll(tmpDir, 0o755); e != nil {
		return nil, web.ErrIO("Failed to create tmp directory: " + e.Error())
	}
	if fi, e := os.Lstat(tmpDir); e == nil && fi.Mode()&os.ModeSymlink != 0 {
		return nil, web.ErrIO("Refusing to use a symlinked uploads tmp directory")
	}

	mr := multipart.NewReader(bytes.NewReader(c.RawBody), boundary)
	var bucket = "anon"
	var rawName = "file"
	var mimeType any
	var written int64
	fileSeen := false
	staging := filepath.Join(tmpDir, "upload-"+newChatUUID()+".part")

	for {
		part, perr := mr.NextPart()
		if perr == io.EOF {
			break
		}
		if perr != nil {
			return nil, web.ErrIO("Invalid multipart upload: " + perr.Error())
		}
		switch part.FormName() {
		case "session_id", "sessionId":
			b, _ := io.ReadAll(part)
			if t := strings.TrimSpace(string(b)); t != "" {
				bucket = sanitizeSessionBucket(t)
			}
		case "file":
			if fileSeen {
				return nil, web.ErrIO("Multiple `file` fields are not supported per request")
			}
			fileSeen = true
			if fn := part.FileName(); fn != "" {
				rawName = fn
			}
			if ctype := part.Header.Get("Content-Type"); ctype != "" {
				mimeType = ctype
			}
			out, oerr := os.OpenFile(staging, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
			if oerr != nil {
				return nil, web.ErrIO("Failed to create staging file: " + oerr.Error())
			}
			n, cerr := io.Copy(out, &limitedReader{r: part, max: uploadMaxBytes + 1})
			out.Close()
			if cerr != nil {
				_ = os.Remove(staging)
				return nil, web.ErrIO("Failed to write chunk: " + cerr.Error())
			}
			if n > uploadMaxBytes {
				_ = os.Remove(staging)
				return nil, web.ErrIO("Upload exceeds the maximum allowed size")
			}
			written = n
		default:
			_, _ = io.Copy(io.Discard, part)
		}
		part.Close()
	}

	if !fileSeen {
		_ = os.Remove(staging)
		return nil, web.ErrIO("Missing `file` field in multipart upload")
	}
	if written == 0 {
		_ = os.Remove(staging)
		return nil, web.ErrIO("Uploaded file is empty")
	}

	safeName := sanitizeUploadFilename(rawName)
	dir := filepath.Join(uploadsRoot, bucket)
	if e := os.MkdirAll(dir, 0o755); e != nil {
		_ = os.Remove(staging)
		return nil, web.ErrIO("Failed to create uploads directory: " + e.Error())
	}
	if fi, e := os.Lstat(dir); e == nil && fi.Mode()&os.ModeSymlink != 0 {
		_ = os.Remove(staging)
		return nil, web.ErrIO("Refusing to use a symlinked upload bucket")
	}
	// Jail: the bucket must resolve inside the uploads root.
	if e := ensureNavigable(uploadsRoot, dir); e != nil {
		_ = os.Remove(staging)
		return nil, web.ErrInvalidInput("Resolved path escapes uploads root")
	}

	finalName, ferr := finalizeUploadName(staging, dir, safeName)
	if ferr != nil {
		_ = os.Remove(staging)
		return nil, ferr
	}
	finalPath := filepath.Join(dir, finalName)
	if e := ensureNavigable(uploadsRoot, finalPath); e != nil {
		_ = os.Remove(finalPath)
		return nil, e
	}
	return map[string]any{
		"path":     finalPath,
		"name":     finalName,
		"size":     written,
		"mimeType": mimeType,
	}, nil
}

// limitedReader reads at most max bytes, then returns what it has (so the caller
// can detect an over-limit payload by n == max).
type limitedReader struct {
	r   io.Reader
	max int64
	n   int64
}

func (l *limitedReader) Read(p []byte) (int, error) {
	if l.n >= l.max {
		return 0, io.EOF
	}
	if int64(len(p)) > l.max-l.n {
		p = p[:l.max-l.n]
	}
	n, err := l.r.Read(p)
	l.n += int64(n)
	return n, err
}
