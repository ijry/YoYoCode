package handlers

// Workspace background image (B11), mirroring commands/background.rs plus the
// backgrounds/ repository and backgrounds/marketplace.rs. Three disk-backed
// stateless commands store a single user-selected image verbatim at
// DataDir/backgrounds/background.img (bytes never re-encoded, so animated
// GIF/APNG/WebP still animate in the webview), and three market commands proxy
// wallhaven.cc through a host-allowlisted https client.
//
// Fidelity notes vs upstream:
//   - mime is sniffed from magic bytes on read; wire shape {mime, dataBase64}.
//   - validate_background's 40 Mpx decompression-bomb guard is reproduced with
//     a dependency-free header dimension read (PNG/GIF/JPEG/WebP). If a header
//     cannot be parsed the pixel cap is skipped (the MIN/MAX byte caps and the
//     format allowlist still apply), matching the upstream intent of a sanity
//     bound rather than a hard DoS boundary.
//   - the atomic write stages into a per-writer temp file then renames, and
//     sweeps stale staging debris, like write_background_atomic_in.

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"yoyocode/src-go/web"
)

const (
	bgMinBytes      = 64
	bgMaxBytes      = 16 * 1024 * 1024
	bgMaxPixels     = 40_000_000
	bgFilename      = "background.img"
	bgDirName       = "backgrounds"
	bgStaleTmpAge   = 600 * time.Second
	whSearchURL     = "https://wallhaven.cc/api/v1/search"
	whUserAgent     = "yoyocode-wallpaper-market/1.0"
	whPurity        = "100"
	whMaxSearchJSON = 4 * 1024 * 1024
	whMaxAssetBytes = 4 * 1024 * 1024
	whMaxDownload   = 16 * 1024 * 1024
	whMaxQueryChars = 128
	whSmallTimeout  = 30 * time.Second
	whDownloadTO    = 300 * time.Second
	whMaxRedirects  = 5
)

var bgTmpSeq uint64

// RegisterBackground wires the six background commands.
func RegisterBackground(reg *Registry) {
	reg.Register("background_read", func(c *web.Context) (any, error) {
		return readBackgroundAt(backgroundsRoot(c))
	})
	reg.Register("background_set", func(c *web.Context) (any, error) {
		b64, err := GetStringOrErr(c, "imageBase64", "image_base64")
		if err != nil {
			return nil, err
		}
		if err := setBackgroundAt(backgroundsRoot(c), b64); err != nil {
			return nil, err
		}
		return EmptyResult(), nil
	})
	reg.Register("background_clear", func(c *web.Context) (any, error) {
		if err := clearBackgroundAt(backgroundsRoot(c)); err != nil {
			return nil, err
		}
		return EmptyResult(), nil
	})
	reg.Register("background_market_search", func(c *web.Context) (any, error) {
		return marketSearch(GetString(c, "query"), GetString(c, "category"), int(GetInt(c, "page")))
	})
	reg.Register("background_market_asset", func(c *web.Context) (any, error) {
		u, err := GetStringOrErr(c, "url")
		if err != nil {
			return nil, err
		}
		return marketFetchAsset(u)
	})
	reg.Register("background_market_download", func(c *web.Context) (any, error) {
		u, err := GetStringOrErr(c, "url")
		if err != nil {
			return nil, err
		}
		src, err := GetStringOrErr(c, "sourceUrl", "source_url")
		if err != nil {
			return nil, err
		}
		if err := marketDownload(backgroundsRoot(c), u, src); err != nil {
			return nil, err
		}
		return EmptyResult(), nil
	})
}

// backgroundsRoot resolves DataDir/backgrounds, mirroring paths.rs precedence:
// YOYOCODE_HOME override, then YOYOCODE_DATA_DIR, then the AppState data dir, then
// ~/.yoyocode. The dir is created lazily by the write path.
func backgroundsRoot(c *web.Context) string {
	if custom := os.Getenv("YOYOCODE_HOME"); custom != "" {
		return filepath.Join(custom, bgDirName)
	}
	if data := os.Getenv("YOYOCODE_DATA_DIR"); data != "" {
		return filepath.Join(data, bgDirName)
	}
	if st, ok := appState(c); ok && st != nil && st.DataDir != "" {
		return filepath.Join(st.DataDir, bgDirName)
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".yoyocode", bgDirName)
	}
	return filepath.Join(".yoyocode", bgDirName)
}

// ─── disk ops ────────────────────────────────────────────────────────────

// readBackgroundAt returns the stored background as {mime, dataBase64}, or nil
// (JSON null) when none is set. A missing file is the normal "no background"
// state, not an error.
func readBackgroundAt(root string) (any, error) {
	bytes, err := os.ReadFile(filepath.Join(root, bgFilename))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, web.ErrIO(err.Error())
	}
	return map[string]any{
		"mime":       sniffMime(bytes),
		"dataBase64": base64.StdEncoding.EncodeToString(bytes),
	}, nil
}

// setBackgroundAt decodes, validates and atomically overwrites the single file.
func setBackgroundAt(root, imageBase64 string) error {
	bytes, err := base64.StdEncoding.DecodeString(imageBase64)
	if err != nil {
		return web.ErrInvalidInput("Invalid base64 payload: " + err.Error())
	}
	if err := validateBackground(bytes); err != nil {
		return err
	}
	return writeBackgroundAtomicIn(root, bytes)
}

// clearBackgroundAt removes the stored background. Idempotent.
func clearBackgroundAt(root string) error {
	err := os.Remove(filepath.Join(root, bgFilename))
	if err != nil && !os.IsNotExist(err) {
		return web.ErrIO(err.Error())
	}
	return nil
}

// writeBackgroundAtomicIn stages into a per-writer sibling file then renames
// over the background, so concurrent writers are a clean last-writer-wins and a
// reader never sees a torn image.
func writeBackgroundAtomicIn(root string, bytes []byte) error {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return web.ErrIO(err.Error())
	}
	sweepStaleStagingFiles(root)
	finalPath := filepath.Join(root, bgFilename)
	tmpPath := filepath.Join(root, bgFilename+"."+strconv.Itoa(os.Getpid())+"."+strconv.FormatUint(atomic.AddUint64(&bgTmpSeq, 1)-1, 10)+".tmp")
	staged := func() error {
		f, err := os.Create(tmpPath)
		if err != nil {
			return web.ErrIO(err.Error())
		}
		if _, err := f.Write(bytes); err != nil {
			f.Close()
			return web.ErrIO(err.Error())
		}
		if err := f.Sync(); err != nil {
			f.Close()
			return web.ErrIO(err.Error())
		}
		f.Close()
		if err := os.Rename(tmpPath, finalPath); err != nil {
			return web.ErrIO(err.Error())
		}
		return nil
	}()
	if staged != nil {
		os.Remove(tmpPath)
	}
	return staged
}

// sweepStaleStagingFiles deletes crash debris (long-untouched *.tmp staging
// files). Best-effort; a failure here never fails the write that follows.
func sweepStaleStagingFiles(root string) {
	prefix := bgFilename + "."
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".tmp") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if time.Since(info.ModTime()) >= bgStaleTmpAge {
			os.Remove(filepath.Join(root, name))
		}
	}
}

// ─── validation & mime ─────────────────────────────────────────────────────

// validateBackground verifies the payload is a real, bounded PNG/JPEG/WebP/GIF
// before it touches disk. Dimensions are read from the header (not a decode) so
// a hostile file's declared pixel count is bounded; an unparsable header skips
// only the pixel cap.
func validateBackground(bytes []byte) error {
	if len(bytes) < bgMinBytes {
		return web.ErrInvalidInput("Background image payload is too small to be valid.")
	}
	if len(bytes) > bgMaxBytes {
		return web.ErrInvalidInput("Background image exceeds 16 MiB cap.")
	}
	if !isSupportedImage(bytes) {
		return web.ErrInvalidInput("Background must be a PNG, JPEG, WebP or GIF image.")
	}
	if w, h, ok := imageDimensions(bytes); ok {
		if uint64(w)*uint64(h) > bgMaxPixels {
			return web.ErrInvalidInput("Background image is too large (" + strconv.Itoa(int(w)) + "x" + strconv.Itoa(int(h)) + "); the maximum is 40000000 pixels.")
		}
	}
	return nil
}

func isSupportedImage(b []byte) bool {
	return sniffFormat(b) != ""
}

// sniffFormat returns "png"/"jpeg"/"webp"/"gif" or "" from the magic bytes.
func sniffFormat(b []byte) string {
	if len(b) >= 8 && b[0] == 0x89 && b[1] == 'P' && b[2] == 'N' && b[3] == 'G' && b[4] == 0x0d && b[5] == 0x0a && b[6] == 0x1a && b[7] == 0x0a {
		return "png"
	}
	if len(b) >= 3 && b[0] == 0xFF && b[1] == 0xD8 && b[2] == 0xFF {
		return "jpeg"
	}
	if len(b) >= 12 && b[0] == 'R' && b[1] == 'I' && b[2] == 'F' && b[3] == 'F' && b[8] == 'W' && b[9] == 'E' && b[10] == 'B' && b[11] == 'P' {
		return "webp"
	}
	if len(b) >= 6 && b[0] == 'G' && b[1] == 'I' && b[2] == 'F' && b[3] == '8' && (b[4] == '9' || b[4] == '7') && b[5] == 'a' {
		return "gif"
	}
	return ""
}

// sniffMime maps the sniffed format to a mime string. The on-disk bytes were
// validated on write, so the PNG fallback only matters for an edited file.
func sniffMime(b []byte) string {
	switch sniffFormat(b) {
	case "jpeg":
		return "image/jpeg"
	case "webp":
		return "image/webp"
	case "gif":
		return "image/gif"
	default:
		return "image/png"
	}
}

// ─── header dimension read (dependency-free) ───────────────────────────────

// imageDimensions reads the container canvas size from the header without
// decoding pixels. Returns ok=false if the header cannot be parsed, in which
// case the caller skips the pixel cap.
func imageDimensions(b []byte) (uint32, uint32, bool) {
	switch sniffFormat(b) {
	case "png":
		return pngDimensions(b)
	case "gif":
		return gifDimensions(b)
	case "jpeg":
		return jpegDimensions(b)
	case "webp":
		return webpDimensions(b)
	}
	return 0, 0, false
}

func pngDimensions(b []byte) (uint32, uint32, bool) {
	// 8-byte signature, 4-byte length, "IHDR", width (BE u32) at 16, height at 20.
	if len(b) < 24 {
		return 0, 0, false
	}
	return binary.BigEndian.Uint32(b[16:20]), binary.BigEndian.Uint32(b[20:24]), true
}

func gifDimensions(b []byte) (uint32, uint32, bool) {
	// Logical screen descriptor: width (LE u16) at 6, height at 8.
	if len(b) < 10 {
		return 0, 0, false
	}
	return uint32(binary.LittleEndian.Uint16(b[6:8])), uint32(binary.LittleEndian.Uint16(b[8:10])), true
}

func jpegDimensions(b []byte) (uint32, uint32, bool) {
	// Scan segments for a Start-Of-Frame marker (0xC0-0xCF except C4/C8/CC),
	// whose payload carries height (BE u16) then width.
	i := 2
	for i+9 < len(b) {
		if b[i] != 0xFF {
			i++
			continue
		}
		marker := b[i+1]
		if marker == 0xFF {
			i++
			continue
		}
		if marker == 0xD8 || marker == 0xD9 || (marker >= 0xD0 && marker <= 0xD7) || marker == 0x01 {
			i += 2
			continue
		}
		if i+4 > len(b) {
			return 0, 0, false
		}
		segLen := int(binary.BigEndian.Uint16(b[i+2 : i+4]))
		isSOF := (marker >= 0xC0 && marker <= 0xCF) && marker != 0xC4 && marker != 0xC8 && marker != 0xCC
		if isSOF {
			if i+9 > len(b) {
				return 0, 0, false
			}
			h := binary.BigEndian.Uint16(b[i+5 : i+7])
			w := binary.BigEndian.Uint16(b[i+7 : i+9])
			return uint32(w), uint32(h), true
		}
		i += 2 + segLen
	}
	return 0, 0, false
}

func webpDimensions(b []byte) (uint32, uint32, bool) {
	// RIFF(0..4) size(4..8) WEBP(8..12) then a chunk fourcc at 12.
	if len(b) < 16 {
		return 0, 0, false
	}
	switch string(b[12:16]) {
	case "VP8 ": // lossy: 16-bit LE width/height (14-bit) at 26/28.
		if len(b) < 30 {
			return 0, 0, false
		}
		w := uint32(binary.LittleEndian.Uint16(b[26:28]) & 0x3fff)
		h := uint32(binary.LittleEndian.Uint16(b[28:30]) & 0x3fff)
		return w, h, true
	case "VP8L": // lossless: 14-bit width-1/height-1 packed from byte 21.
		if len(b) < 25 {
			return 0, 0, false
		}
		b0, b1, b2, b3 := uint32(b[21]), uint32(b[22]), uint32(b[23]), uint32(b[24])
		w := (b0 | ((b1 & 0x3f) << 8)) + 1
		h := (((b1 >> 6) | (b2 << 2) | ((b3 & 0x0f) << 10)) & 0x3fff) + 1
		return w, h, true
	case "VP8X": // extended: 24-bit LE width-1 at 24, height-1 at 27.
		if len(b) < 30 {
			return 0, 0, false
		}
		w := (uint32(b[24]) | uint32(b[25])<<8 | uint32(b[26])<<16) + 1
		h := (uint32(b[27]) | uint32(b[28])<<8 | uint32(b[29])<<16) + 1
		return w, h, true
	}
	return 0, 0, false
}

// ─── wallhaven marketplace ─────────────────────────────────────────────────

func isAllowedWallhavenHost(host string) bool {
	return host == "wallhaven.cc" || strings.HasSuffix(host, ".wallhaven.cc")
}

// parseWallhavenHTTPSURL accepts only https URLs on wallhaven.cc or a subdomain
// with no embedded userinfo. Everything the market fetches funnels through here.
func parseWallhavenHTTPSURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, web.ErrInvalidInput("Marketplace URL must be a valid https wallhaven.cc URL.")
	}
	if u.Scheme != "https" {
		return nil, web.ErrInvalidInput("Marketplace URL must use https.")
	}
	if u.User != nil && u.User.String() != "" {
		return nil, web.ErrInvalidInput("Marketplace URL must not embed credentials.")
	}
	host := u.Hostname()
	if host == "" {
		return nil, web.ErrInvalidInput("Marketplace URL must name a host.")
	}
	if !isAllowedWallhavenHost(host) {
		return nil, web.ErrInvalidInput("Marketplace URL host must be wallhaven.cc or a subdomain.")
	}
	return u, nil
}

func wallhavenSourceURL(id string) string {
	return "https://wallhaven.cc/w/" + strings.TrimSpace(id)
}

// wallhavenCategories maps a filter to the wallhaven category bitmask
// (general=100/anime=010/people=001); unknown values are an error.
func wallhavenCategories(category string) (string, error) {
	switch category {
	case "", "all":
		return "111", nil
	case "general":
		return "100", nil
	case "anime":
		return "010", nil
	case "people":
		return "001", nil
	default:
		return "", web.ErrInvalidInput("Unknown wallpaper market category: " + category)
	}
}

// marketClient builds an http.Client whose redirect policy re-applies the host
// allowlist to every hop — validating only the first URL would leave the
// allowlist bypassable on a redirect.
func marketClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout: timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if req.URL.Scheme != "https" || !isAllowedWallhavenHost(req.URL.Hostname()) {
				return web.ErrNetwork("refused a redirect off wallhaven.cc to " + req.URL.String())
			}
			if len(via) > whMaxRedirects {
				return web.ErrNetwork("more than 5 redirects")
			}
			return nil
		},
	}
}

func marketGet(u string, timeout time.Duration) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil, web.ErrNetwork(err.Error())
	}
	req.Header.Set("User-Agent", whUserAgent)
	return marketClient(timeout).Do(req)
}

// readCapped reads a body with a hard ceiling, rejecting on a declared
// Content-Length and again on accumulated bytes.
func readCapped(resp *http.Response, cap int64, what string) ([]byte, error) {
	capMiB := strconv.FormatInt(cap/(1024*1024), 10)
	if resp.ContentLength > 0 && resp.ContentLength > cap {
		return nil, web.ErrNetwork(what + " exceeds " + capMiB + " MiB cap.")
	}
	buf, err := io.ReadAll(io.LimitReader(resp.Body, cap+1))
	if err != nil {
		return nil, web.ErrNetwork(what + " failed mid-transfer: " + err.Error())
	}
	if int64(len(buf)) > cap {
		return nil, web.ErrNetwork(what + " exceeds " + capMiB + " MiB cap.")
	}
	return buf, nil
}

// fetchImageCapped fetches an allowlisted image, enforcing a content-type and a
// byte ceiling. The byte-level sniff in validateBackground remains the final
// authority for downloads; this is the early, cheap rejection.
func fetchImageCapped(u *url.URL, cap int64, timeout time.Duration, what string) (string, []byte, error) {
	resp, err := marketGet(u.String(), timeout)
	if err != nil {
		return "", nil, web.ErrNetwork(what + " failed: " + err.Error())
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", nil, web.ErrNetwork(what + " returned HTTP " + strconv.Itoa(resp.StatusCode))
	}
	ct := strings.ToLower(strings.TrimSpace(strings.SplitN(resp.Header.Get("Content-Type"), ";", 2)[0]))
	if ct != "image/jpeg" && ct != "image/png" && ct != "image/webp" {
		return "", nil, web.ErrNetwork(what + " returned unsupported content-type " + ct)
	}
	bytes, err := readCapped(resp, cap, what)
	if err != nil {
		return "", nil, err
	}
	return ct, bytes, nil
}

// ─── search / asset / download ─────────────────────────────────────────────

func marketSearch(query, category string, page int) (any, error) {
	query = strings.TrimSpace(query)
	if len([]rune(query)) > whMaxQueryChars {
		return nil, web.ErrInvalidInput("Search query exceeds 128 characters.")
	}
	if page < 1 {
		page = 1
	}
	categories, err := wallhavenCategories(category)
	if err != nil {
		return nil, err
	}
	u, err := url.Parse(whSearchURL)
	if err != nil {
		return nil, web.ErrNetwork("invalid search URL: " + err.Error())
	}
	q := u.Query()
	q.Set("categories", categories)
	q.Set("purity", whPurity)
	q.Set("page", strconv.Itoa(page))
	if query != "" {
		q.Set("q", query)
		q.Set("sorting", "relevance")
	} else {
		q.Set("sorting", "toplist")
		q.Set("topRange", "1M")
	}
	u.RawQuery = q.Encode()

	resp, err := marketGet(u.String(), whSmallTimeout)
	if err != nil {
		return nil, web.ErrNetwork("wallhaven search failed: " + err.Error())
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, web.ErrNetwork("wallhaven search returned HTTP " + strconv.Itoa(resp.StatusCode))
	}
	body, err := readCapped(resp, whMaxSearchJSON, "wallhaven search payload")
	if err != nil {
		return nil, err
	}
	return parseSearchPayload(body)
}

// parseSearchPayload turns a wallhaven listing into the stable MarketSearchPage
// contract, dropping any entry whose full/thumb URL is not wallhaven-hosted and
// deriving sourceUrl from the id rather than trusting the listing.
func parseSearchPayload(body []byte) (any, error) {
	var payload struct {
		Data []struct {
			ID     string `json:"id"`
			Path   string `json:"path"`
			Thumbs struct {
				Small string `json:"small"`
			} `json:"thumbs"`
			DimensionX uint32 `json:"dimension_x"`
			DimensionY uint32 `json:"dimension_y"`
			FileSize   uint64 `json:"file_size"`
			Category   string `json:"category"`
		} `json:"data"`
		Meta struct {
			CurrentPage uint32 `json:"current_page"`
			LastPage    uint32 `json:"last_page"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, web.ErrNetwork("wallhaven returned malformed JSON: " + err.Error())
	}

	items := make([]map[string]any, 0, len(payload.Data))
	for _, it := range payload.Data {
		fullURL, thumbURL := it.Path, it.Thumbs.Small
		if fullURL == "" || thumbURL == "" {
			continue
		}
		if _, err := parseWallhavenHTTPSURL(fullURL); err != nil {
			continue
		}
		if _, err := parseWallhavenHTTPSURL(thumbURL); err != nil {
			continue
		}
		items = append(items, map[string]any{
			"id":            it.ID,
			"thumbUrl":      thumbURL,
			"fullUrl":       fullURL,
			"sourceUrl":     wallhavenSourceURL(it.ID),
			"width":         it.DimensionX,
			"height":        it.DimensionY,
			"fileSizeBytes": it.FileSize,
			"category":      it.Category,
		})
	}
	page := payload.Meta.CurrentPage
	if page < 1 {
		page = 1
	}
	lastPage := payload.Meta.LastPage
	if lastPage < 1 {
		lastPage = 1
	}
	return map[string]any{"items": items, "page": page, "lastPage": lastPage}, nil
}

// marketFetchAsset proxies one allowlisted thumbnail as {mime, dataBase64}.
func marketFetchAsset(raw string) (any, error) {
	u, err := parseWallhavenHTTPSURL(raw)
	if err != nil {
		return nil, err
	}
	mime, bytes, err := fetchImageCapped(u, whMaxAssetBytes, whSmallTimeout, "wallhaven thumbnail")
	if err != nil {
		return nil, err
	}
	return map[string]any{"mime": mime, "dataBase64": base64.StdEncoding.EncodeToString(bytes)}, nil
}

// marketDownload fetches a full image through the same validation and atomic
// write as a manual pick. sourceUrl must be the real https://wallhaven.cc/w/<id>
// page shape so a download cannot be attributed to a bogus source.
func marketDownload(root, raw, sourceRaw string) error {
	fullURL, err := parseWallhavenHTTPSURL(raw)
	if err != nil {
		return err
	}
	source, err := parseWallhavenHTTPSURL(sourceRaw)
	if err != nil {
		return err
	}
	if source.Hostname() != "wallhaven.cc" || !strings.HasPrefix(source.Path, "/w/") {
		return web.ErrInvalidInput("sourceUrl must be a https://wallhaven.cc/w/<id> page URL.")
	}
	_, bytes, err := fetchImageCapped(fullURL, whMaxDownload, whDownloadTO, "wallpaper download")
	if err != nil {
		return err
	}
	if err := validateBackground(bytes); err != nil {
		return err
	}
	return writeBackgroundAtomicIn(root, bytes)
}
