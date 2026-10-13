package acp

// binary_cache.go ports the core of acp::binary_cache: fetch a binary agent's
// platform archive, extract it into a per-version cache dir and return the
// launch entry. The full upstream cache (the uv toolchain, custom-version URL
// substitution, sha256 enforcement for custom agents, opportunistic sibling
// chmod) is reduced to the built-in binary agents' path, which is what
// acp_download_agent_binary needs.

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// binaryCacheRoot is where downloaded agent binaries live; set once at startup
// so ResolveSpawn can resolve a cached binary agent without a request context.
var binaryCacheRoot string

// SetBinaryCacheRoot records the cache root (the app data dir).
func SetBinaryCacheRoot(dir string) { binaryCacheRoot = dir }

// EnsureAgentBinary downloads (unless already cached) the current platform's
// archive for a binary agent and returns the launch entry path. A custom
// version is refused unless it templates the pinned URL — the built-ins do not,
// so only the pinned build can be fetched (matching upstream's backstop).
func EnsureAgentBinary(agentType, cacheRoot, versionOverride string, progress func(string)) (string, error) {
	ab, ok := BinaryDistFor(agentType)
	if !ok {
		return "", fmt.Errorf("not a binary agent on this platform: %s", agentType)
	}
	version := ab.Version
	if v := strings.TrimSpace(versionOverride); v != "" && v != ab.Version {
		return "", fmt.Errorf(
			"%s publishes no version-templated download URL, so it cannot install a custom version (%s); its pinned build is %s",
			agentType, v, ab.Version)
	}
	if cacheRoot == "" {
		return "", errors.New("binary cache root not configured")
	}
	target := CachedBinaryPath(cacheRoot, agentType, version)
	if target == "" {
		return "", fmt.Errorf("no platform entry for %s", agentType)
	}
	if fileExists(target) {
		report(progress, "already installed")
		return target, nil
	}
	cacheDir := filepath.Dir(target)
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", err
	}
	report(progress, "downloading "+ab.URL)
	archive, err := downloadToTemp(ab.URL)
	if err != nil {
		return "", err
	}
	defer os.Remove(archive)
	report(progress, "extracting")
	if err := installBinaryTree(archive, cacheDir, target, ab.Siblings); err != nil {
		return "", err
	}
	return target, nil
}

// installBinaryTree extracts archive into cacheDir, then validates that the
// launch entry (and every required sibling — e.g. Antigravity's
// localharness_external) is present, and marks them executable.
func installBinaryTree(archive, cacheDir, target string, siblings []string) error {
	if err := extractArchive(archive, cacheDir); err != nil {
		return err
	}
	if !fileExists(target) {
		return fmt.Errorf("archive did not contain %s", filepath.Base(target))
	}
	_ = os.Chmod(target, 0o755)
	for _, sib := range siblings {
		sp := filepath.Join(cacheDir, filepath.FromSlash(sib))
		if !fileExists(sp) {
			return fmt.Errorf("archive is missing required sibling %s", sib)
		}
		_ = os.Chmod(sp, 0o755)
	}
	return nil
}

func report(progress func(string), msg string) {
	if progress != nil {
		progress(msg)
	}
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

// downloadToTemp streams url into a temp file, keeping the archive extension so
// extraction can pick the format.
func downloadToTemp(url string) (string, error) {
	client := &http.Client{Timeout: 30 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download %s: HTTP %d", url, resp.StatusCode)
	}
	ext := ".bin"
	lower := strings.ToLower(url)
	switch {
	case strings.HasSuffix(lower, ".zip"):
		ext = ".zip"
	case strings.HasSuffix(lower, ".tar.gz"):
		ext = ".tar.gz"
	}
	f, err := os.CreateTemp("", "yoyocode-agent-*"+ext)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := io.Copy(f, resp.Body); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// extractArchive unpacks a .zip or .tar.gz archive into dest.
func extractArchive(archive, dest string) error {
	lower := strings.ToLower(archive)
	switch {
	case strings.HasSuffix(lower, ".zip"):
		return extractZip(archive, dest)
	case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"):
		return extractTarGz(archive, dest)
	}
	return fmt.Errorf("unsupported archive: %s", filepath.Base(archive))
}

func extractZip(archive, dest string) error {
	r, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer r.Close()
	for _, f := range r.File {
		p, err := safeJoin(dest, f.Name)
		if err != nil {
			return err
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(p, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(p, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
		if err != nil {
			rc.Close()
			return err
		}
		_, copyErr := io.Copy(out, rc)
		out.Close()
		rc.Close()
		if copyErr != nil {
			return copyErr
		}
	}
	return nil
}

func extractTarGz(archive, dest string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		p, err := safeJoin(dest, hdr.Name)
		if err != nil {
			return err
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(p, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				return err
			}
			out, err := os.OpenFile(p, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(hdr.Mode)&0o777)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(out, tr)
			out.Close()
			if copyErr != nil {
				return copyErr
			}
		}
	}
	return nil
}

// safeJoin joins an archive entry name onto dest, refusing absolute paths and
// traversal (zip-slip).
func safeJoin(dest, name string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(name))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || filepath.IsAbs(clean) {
		return "", fmt.Errorf("unsafe archive path: %s", name)
	}
	return filepath.Join(dest, clean), nil
}
