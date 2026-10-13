package handlers

// acp_deepseek_splice.go is the comment-preserving block splice + atomic write
// for the DeepSeek settings document.
//
// The document is the USER's — it can carry any other plugin's section, and the
// harness' own writer patches it as a comment-preserving leaf diff. A
// whole-document YAML round trip would drop every comment and blank line in it,
// so the `models` block is replaced TEXTUALLY: every byte outside it is carried
// over verbatim, and the result is re-parsed and compared field by field before
// it is allowed to be written.

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"gopkg.in/yaml.v3"
)

// docLine is a line of the document, with the byte range it occupies (line
// terminator included) and its indentation.
type docLine struct {
	start, end int
	// indent is the column of the first non-whitespace byte, or -1 for a blank
	// line.
	indent  int
	comment bool
	text    string
}

func splitDocLines(raw string) []docLine {
	var out []docLine
	offset := 0
	for offset < len(raw) {
		i := strings.IndexByte(raw[offset:], '\n')
		end := len(raw)
		if i >= 0 {
			end = offset + i + 1
		}
		line := raw[offset:end]
		text := strings.TrimRight(line, "\n")
		text = strings.TrimRight(text, "\r")
		indent := -1
		for k := 0; k < len(text); k++ {
			if c := text[k]; c != ' ' && c != '\t' {
				indent = k
				break
			}
		}
		comment := indent >= 0 && text[indent] == '#'
		out = append(out, docLine{start: offset, end: end, indent: indent, comment: comment, text: text})
		offset = end
	}
	return out
}

// keyLineKind is how a line relates to the key being looked for.
type keyLineKind int

const (
	// keyLineOther is a different key (or not a key at all).
	keyLineOther keyLineKind = iota
	// keyLineBlock is `key:` opening a block — the value is on the lines below.
	keyLineBlock
	// keyLineInline is `key: <something>` on one line. A flow mapping or list
	// cannot be spliced line-wise, so it is refused rather than guessed at.
	keyLineInline
)

// classifyKeyLine classifies text against key, accepting the plain,
// single-quoted and double-quoted spellings.
func classifyKeyLine(text, key string) keyLineKind {
	trimmed := strings.TrimLeft(text, " \t")
	for _, spelling := range []string{key, "'" + key + "'", `"` + key + `"`} {
		rest, ok := strings.CutPrefix(trimmed, spelling)
		if !ok {
			continue
		}
		rest = strings.TrimLeft(rest, " \t")
		rest, ok = strings.CutPrefix(rest, ":")
		if !ok {
			continue
		}
		rest = strings.TrimSpace(rest)
		if rest == "" || strings.HasPrefix(rest, "#") {
			return keyLineBlock
		}
		return keyLineInline
	}
	return keyLineOther
}

// deepSeekModelYAML is the on-document shape of one entry (optional fields
// omitted rather than written as null).
type deepSeekModelYAML struct {
	ID                 string   `yaml:"id"`
	Name               string   `yaml:"name,omitempty"`
	Description        string   `yaml:"description,omitempty"`
	ContextWindow      *int64   `yaml:"contextWindow,omitempty"`
	MaxTokens          *int64   `yaml:"maxTokens,omitempty"`
	InputModalities    []string `yaml:"inputModalities,omitempty"`
	ImagePixelBudget   any      `yaml:"imagePixelBudget,omitempty"`
	ImageMaxBytes      *int64   `yaml:"imageMaxBytes,omitempty"`
	SystemPromptUpdate string   `yaml:"systemPromptUpdate,omitempty"`
}

func toYAMLModels(models []deepSeekCatalogModel) []deepSeekModelYAML {
	out := make([]deepSeekModelYAML, 0, len(models))
	for _, m := range models {
		e := deepSeekModelYAML{
			ID:            m.ID,
			ContextWindow: m.ContextWindow,
			MaxTokens:     m.MaxTokens,
			ImageMaxBytes: m.ImageMaxBytes,
		}
		if m.Name != nil {
			e.Name = *m.Name
		}
		if m.Description != nil {
			e.Description = *m.Description
		}
		if m.SystemPromptUpdate != nil {
			e.SystemPromptUpdate = *m.SystemPromptUpdate
		}
		e.InputModalities = m.InputModalities
		if m.ImagePixelBudget != nil {
			if m.ImagePixelBudget.IsPixels {
				e.ImagePixelBudget = m.ImagePixelBudget.Pixels
			} else {
				e.ImagePixelBudget = m.ImagePixelBudget.Named
			}
		}
		out = append(out, e)
	}
	return out
}

// renderModelsBlock renders `models:` as a block sequence indented to indent,
// with the line terminator the document already uses.
func renderModelsBlock(models []deepSeekCatalogModel, indent int, newline string) (string, error) {
	body, err := yaml.Marshal(toYAMLModels(models))
	if err != nil {
		return "", fmt.Errorf("could not serialize the model list: %w", err)
	}
	pad := strings.Repeat(" ", indent)
	var sb strings.Builder
	sb.WriteString(pad + deepSeekModelsKey + ":" + newline)
	for _, line := range strings.Split(strings.TrimRight(string(body), "\n"), "\n") {
		if line == "" {
			sb.WriteString(newline)
		} else {
			// yaml.v3 emits a top-level block sequence flush left; nest it one
			// level under its key, which is the shape a hand-written settings
			// document uses.
			sb.WriteString(pad + "  " + line + newline)
		}
	}
	return sb.String(), nil
}

// patchSettingsModels replaces, inserts or deletes `llm-deepseek.models` in
// existing, leaving every other byte of the document alone. A nil list removes
// the key. Returns the patched text, or a message describing why it refused.
func patchSettingsModels(existing string, models []deepSeekCatalogModel) (string, string) {
	newline := "\n"
	if strings.Contains(existing, "\r\n") {
		newline = "\r\n"
	}

	// A document we cannot parse is one we must not rewrite: the splice reasons
	// about indentation, and the verification needs a "before" to compare to.
	var before any
	hasBefore := false
	if strings.TrimSpace(existing) != "" {
		if err := yaml.Unmarshal([]byte(existing), &before); err != nil {
			return "", "could not parse the YAML: " + err.Error()
		}
		norm := normalizeYAML(before)
		if m, ok := norm.(map[string]any); ok {
			// A section that is neither a mapping nor absent (a list, a scalar)
			// is not a shape a `models` key can be added to at all.
			if sec, ok := m[deepSeekSectionKey]; ok && sec != nil {
				if _, isMap := sec.(map[string]any); !isMap {
					return "", fmt.Sprintf("`%s` is not a mapping", deepSeekSectionKey)
				}
			}
		} else if norm != nil {
			return "", "the settings document's root is not a mapping"
		}
		hasBefore = true
	}

	patched, msg := spliceModels(existing, models, newline)
	if msg != "" {
		return "", msg
	}
	if msg := verifyPatch(patched, before, hasBefore, models); msg != "" {
		return "", msg
	}
	return patched, ""
}

func spliceModels(existing string, models []deepSeekCatalogModel, newline string) (string, string) {
	lines := splitDocLines(existing)

	// ── The section header ───────────────────────────────────────────────
	section := -1
	for i, line := range lines {
		if line.indent != 0 || line.comment {
			continue
		}
		switch classifyKeyLine(line.text, deepSeekSectionKey) {
		case keyLineInline:
			return "", fmt.Sprintf("`%s` is written inline in this document; edit it by hand instead", deepSeekSectionKey)
		case keyLineBlock:
			section = i
		}
		if section >= 0 {
			break
		}
	}

	if section < 0 {
		// No section yet. Nothing to remove; otherwise append a fresh one.
		if models == nil {
			return existing, ""
		}
		block, err := renderModelsBlock(models, 2, newline)
		if err != nil {
			return "", err.Error()
		}
		out := existing
		if out != "" && !strings.HasSuffix(out, "\n") {
			out += newline
		}
		out += deepSeekSectionKey + ":" + newline
		out += block
		return out, ""
	}

	// ── The section body: every following line indented past column 0 ────
	// A line flush against the left margin ends it — including a comment.
	bodyEnd := len(lines)
	for i := section + 1; i < len(lines); i++ {
		if lines[i].indent == 0 {
			bodyEnd = i
			break
		}
	}

	// The indentation the section's own keys sit at.
	childIndent := 2
	for _, line := range lines[section+1 : bodyEnd] {
		if line.indent >= 0 && !line.comment {
			childIndent = line.indent
			break
		}
	}

	// ── The `models` block inside it ─────────────────────────────────────
	modelsAt := -1
	for i := section + 1; i < bodyEnd; i++ {
		line := lines[i]
		if line.indent != childIndent || line.comment {
			continue
		}
		switch classifyKeyLine(line.text, deepSeekModelsKey) {
		case keyLineInline:
			return "", fmt.Sprintf("`%s.%s` is written inline in this document; edit it by hand instead", deepSeekSectionKey, deepSeekModelsKey)
		case keyLineBlock:
			modelsAt = i
		}
		if modelsAt >= 0 {
			break
		}
	}

	var replaceFrom, replaceTo int
	if modelsAt >= 0 {
		// The block runs to the next line at or above the key's own indent —
		// except a sequence item sitting AT that indent, which is how both
		// yaml.v3's emitter and plenty of hand-written YAML write a list under
		// its key.
		end := bodyEnd
		for i := modelsAt + 1; i < bodyEnd; i++ {
			line := lines[i]
			if line.indent < 0 {
				continue
			}
			if line.indent > childIndent {
				continue
			}
			if line.indent == childIndent && strings.HasPrefix(strings.TrimLeft(line.text, " \t"), "-") {
				continue
			}
			end = i
			break
		}
		// Blank lines after the block separate it from what follows; leave them
		// where they are.
		for end > modelsAt+1 && lines[end-1].indent < 0 {
			end--
		}
		replaceFrom, replaceTo = modelsAt, end
	} else {
		// No block yet: append one after the section's last meaningful line, so
		// a trailing comment or blank line keeps introducing what it introduced.
		end := bodyEnd
		for end > section+1 && (lines[end-1].indent < 0 || lines[end-1].comment) {
			end--
		}
		replaceFrom, replaceTo = end, end
	}

	prefixEnd := len(existing)
	if replaceFrom < len(lines) {
		prefixEnd = lines[replaceFrom].start
	}
	suffixStart := len(existing)
	if replaceTo < len(lines) {
		suffixStart = lines[replaceTo].start
	}

	out := existing[:prefixEnd]
	// A document whose last line has no terminator would otherwise have the new
	// block glued onto it.
	if out != "" && !strings.HasSuffix(out, "\n") {
		out += newline
	}

	if models != nil {
		block, err := renderModelsBlock(models, childIndent, newline)
		if err != nil {
			return "", err.Error()
		}
		out += block
	} else {
		// Removing the only key of the section leaves `llm-deepseek:` with an
		// empty body, i.e. a NULL section — which the settings service rejects
		// ("must be an object of keys") and skips. Dropping the header reaches
		// the same place without the warning, so the header goes with the last
		// key. A comment-only body counts as empty.
		sectionIsEmpty := true
		for i := section + 1; i < bodyEnd; i++ {
			line := lines[i]
			if (i >= replaceFrom && i < replaceTo) || line.indent < 0 || line.comment {
				continue
			}
			sectionIsEmpty = false
			break
		}
		if sectionIsEmpty {
			headerStart := lines[section].start
			return existing[:headerStart] + existing[suffixStart:], ""
		}
	}

	return out + existing[suffixStart:], ""
}

// verifyPatch re-reads the patched document and refuses it unless it says
// exactly what the splice meant to say and nothing else moved. The splice
// reasons about text; this is the only check that reasons about the resulting
// YAML.
func verifyPatch(patched string, before any, hasBefore bool, models []deepSeekCatalogModel) string {
	unsafeEdit := func(detail string) string {
		return fmt.Sprintf(
			"could not update `%s.%s` without disturbing the rest of the document (%s); edit it by hand instead",
			deepSeekSectionKey, deepSeekModelsKey, detail)
	}

	var after any
	if strings.TrimSpace(patched) != "" {
		if err := yaml.Unmarshal([]byte(patched), &after); err != nil {
			return unsafeEdit("re-parse failed: " + err.Error())
		}
	}

	// What was written is what was asked for.
	stored, storedPresent := lookupSectionModels(after)
	if models != nil {
		expected, err := modelsToYAMLValue(models)
		if err != nil {
			return unsafeEdit("serialize failed: " + err.Error())
		}
		if !reflect.DeepEqual(normalizeYAML(stored), expected) {
			return unsafeEdit("the model list did not land where it was aimed")
		}
	} else if storedPresent {
		return unsafeEdit("the model list is still there")
	}

	// Nothing else changed. Comparing the PARSED documents (rather than the
	// text) is the point: a splice that ran off the end of the block would
	// change a neighbour's meaning, and that shows up here.
	beforeDoc := any(nil)
	if hasBefore {
		beforeDoc = before
	}
	if !reflect.DeepEqual(stripDeepSeekSection(beforeDoc), stripDeepSeekSection(after)) {
		return unsafeEdit("another section would have changed")
	}
	return ""
}

// lookupSectionModels returns the `llm-deepseek.models` value of a parsed
// document, plus whether that chain reached the key at all.
func lookupSectionModels(v any) (any, bool) {
	m, ok := normalizeYAML(v).(map[string]any)
	if !ok {
		return nil, false
	}
	sec, ok := m[deepSeekSectionKey].(map[string]any)
	if !ok {
		return nil, false
	}
	val, ok := sec[deepSeekModelsKey]
	return val, ok
}

// modelsToYAMLValue renders the model list and re-parses it, giving a value in
// the same normalized shape as a parsed document.
func modelsToYAMLValue(models []deepSeekCatalogModel) (any, error) {
	b, err := yaml.Marshal(toYAMLModels(models))
	if err != nil {
		return nil, err
	}
	var v any
	if err := yaml.Unmarshal(b, &v); err != nil {
		return nil, err
	}
	return normalizeYAML(v), nil
}

// stripDeepSeekSection returns the document with `llm-deepseek.models` (and the
// section header when `models` was all it held) removed, so "wrote the first
// one" and "removed the last one" — which legitimately add and drop the header —
// compare equal on both sides.
func stripDeepSeekSection(v any) map[string]any {
	m, ok := normalizeYAML(v).(map[string]any)
	if !ok {
		return map[string]any{}
	}
	sec, ok := m[deepSeekSectionKey].(map[string]any)
	if ok {
		delete(sec, deepSeekModelsKey)
		if len(sec) == 0 {
			delete(m, deepSeekSectionKey)
		} else {
			m[deepSeekSectionKey] = sec
		}
	} else if m[deepSeekSectionKey] == nil {
		// An empty header resolves to null; treat it as the absence it is.
		delete(m, deepSeekSectionKey)
	}
	// A section that is neither a mapping nor null is not ours to reshape: leave
	// it in the comparison so any change to it fails.
	return m
}

// normalizeYAML converts a decoded YAML value into a canonical form (string-keyed
// maps, int64 numbers) so parsed documents can be compared with reflect.DeepEqual.
func normalizeYAML(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[k] = normalizeYAML(val)
		}
		return out
	case map[any]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			ks, ok := k.(string)
			if !ok {
				ks = fmt.Sprint(k)
			}
			out[ks] = normalizeYAML(val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = normalizeYAML(val)
		}
		return out
	case int:
		return int64(t)
	case int64:
		return t
	case uint64:
		return int64(t)
	case float64:
		if t == float64(int64(t)) {
			return int64(t)
		}
		return t
	default:
		return v
	}
}

// ─── Atomic write ────────────────────────────────────────────────────────────

var deepSeekTempSeq atomic.Uint64

// writeSettingsDocument writes the settings document the way the harness' own
// provider writes it: into a sibling temporary file that is then renamed over
// the target, under a 0700 parent, owner-only. The atomic swap matters — this
// document can hold any other plugin's section, and a truncate-first write would
// leave everyone's settings half-written on an I/O error or crash.
func writeSettingsDocument(path, body string) error {
	ioErr := func(what string, err error) error {
		return fmt.Errorf("could not %s the DeepSeek settings: %w", what, err)
	}

	// Follow a symlink to the file it names, so the rename replaces the real
	// document rather than the link.
	target, err := resolveWriteTarget(path)
	if err != nil {
		return ioErr("resolve the path of", err)
	}

	parent := filepath.Dir(target)
	if _, err := os.Stat(parent); os.IsNotExist(err) {
		mode := os.FileMode(0o755)
		if runtime.GOOS != "windows" {
			mode = 0o700
		}
		if err := os.MkdirAll(parent, mode); err != nil {
			return ioErr("create the harness home for", err)
		}
	}

	// The mode the swapped-in file must end up with: an existing document keeps
	// its own minus any world bits, a fresh one is owner-only.
	mode := os.FileMode(0o600)
	if fi, err := os.Stat(target); err == nil {
		mode = fi.Mode().Perm() & 0o777 &^ 0o007
	}

	temp, f, err := createDeepSeekTempFile(parent, filepath.Base(target), mode)
	if err != nil {
		return ioErr("create a temporary file for", err)
	}
	cleanup := func() {
		_ = f.Close()
		_ = os.Remove(temp)
	}
	if _, err := f.WriteString(body); err != nil {
		cleanup()
		return ioErr("write", err)
	}
	// OpenFile's mode is masked by the umask, so a group-shared document would
	// come back tightened without this.
	_ = os.Chmod(temp, mode)
	// Flush before the swap: a rename can otherwise be durable while the bytes
	// it points at are not.
	if err := f.Sync(); err != nil {
		cleanup()
		return ioErr("write", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(temp)
		return ioErr("write", err)
	}
	if err := os.Rename(temp, target); err != nil {
		_ = os.Remove(temp)
		return ioErr("store", err)
	}
	return nil
}

// resolveWriteTarget is the file a write to path should actually land on.
// canonicalize answers it whenever the document exists; when the path is a
// symlink whose target has not been created yet, the link must still be followed
// (an open(O_CREAT) creates the target, so replacing the link would quietly
// break a dotfiles setup on the first save).
func resolveWriteTarget(path string) (string, error) {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved, nil
	}
	// The OS gives up around 32–40 hops; a settings document behind more than a
	// handful of DANGLING links is not a setup to guess at.
	current := path
	for i := 0; i < 8; i++ {
		end, err := isEndOfChain(current)
		if err != nil {
			return "", err
		}
		if end {
			return current, nil
		}
		link, err := os.Readlink(current)
		if err != nil {
			return "", err
		}
		if filepath.IsAbs(link) {
			current = link
		} else {
			// A relative link resolves against the directory holding the link.
			current = filepath.Join(filepath.Dir(current), link)
		}
		// The target may itself exist (only the last hop was dangling).
		if resolved, err := filepath.EvalSymlinks(current); err == nil {
			return resolved, nil
		}
	}
	end, err := isEndOfChain(current)
	if err != nil {
		return "", err
	}
	if end {
		return current, nil
	}
	return "", fmt.Errorf("%s is behind too many symbolic links", path)
}

// isEndOfChain reports whether the chain ends at this path: nothing there (the
// file to create), or something that is not a link.
func isEndOfChain(candidate string) (bool, error) {
	fi, err := os.Lstat(candidate)
	if err != nil {
		if os.IsNotExist(err) {
			return true, nil
		}
		return false, err
	}
	return fi.Mode()&os.ModeSymlink == 0, nil
}

// createDeepSeekTempFile creates the temporary file the new document is staged
// in, beside target (a rename is only atomic within one filesystem). The name is
// unique per CALL and the file is created with O_EXCL, so two saves racing never
// stage into one file.
func createDeepSeekTempFile(parent, stem string, mode os.FileMode) (string, *os.File, error) {
	var last error
	for i := 0; i < 8; i++ {
		seq := deepSeekTempSeq.Add(1) - 1
		nonce := time.Now().UnixNano()
		name := fmt.Sprintf(".%s.yoyocode-%d-%d-%d.tmp", stem, os.Getpid(), seq, nonce)
		temp := filepath.Join(parent, name)
		f, err := os.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
		if err == nil {
			return temp, f, nil
		}
		if os.IsExist(err) {
			last = err
			continue
		}
		return "", nil, err
	}
	if last == nil {
		last = os.ErrExist
	}
	return "", nil, last
}
