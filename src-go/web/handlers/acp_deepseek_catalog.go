package handlers

// acp_deepseek_catalog.go ports acp_load_deepseek_model_catalog /
// acp_update_deepseek_model_catalog: the DeepSeek Harness reads its advisory
// model catalog from `$DSH_HOME/settings.yaml` (default ~/.dsh) under the
// `llm-deepseek.models` section. The load side projects that section (or the
// adapter's built-in list it inherits) for the settings panel; the write side
// replaces / inserts / deletes that one key while leaving every other byte of
// the user's document alone (see acp_deepseek_splice.go).

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"yoyocode/src-go/web"
)

// deepSeekSectionKey is the settings-document section the adapter registers
// (its own short name). deepSeekModelsKey is the key inside it this module owns;
// everything else in the section is left untouched.
const (
	deepSeekSectionKey = "llm-deepseek"
	deepSeekModelsKey  = "models"
)

const (
	// deepSeekDefaultContextWindow is DEFAULT_CONTEXT_WINDOW in the adapter.
	deepSeekDefaultContextWindow int64 = 1_000_000
	// deepSeekDefaultRequestImagePixelBudget / …MaxBytes are the budget the
	// adapter materializes for a vision entry that declares none.
	deepSeekDefaultRequestImagePixelBudget int64 = 640_000
	deepSeekDefaultRequestImageMaxBytes    int64 = 1_048_576
	// deepSeekMaxSafeInteger is JS Number.MAX_SAFE_INTEGER; the adapter judges
	// its numeric fields with Number.isSafeInteger.
	deepSeekMaxSafeInteger int64 = 9_007_199_254_740_991
)

const (
	deepSeekModalityText  = "text"
	deepSeekModalityImage = "image"
	// deepSeekImagePixelBudgetLow is the one named pixel budget the adapter
	// accepts in place of a count.
	deepSeekImagePixelBudgetLow = "low"
	// deepSeekSystemPromptUpdateInHistory is the only value the adapter's
	// systemPromptUpdate accepts when present.
	deepSeekSystemPromptUpdateInHistory = "in-history"
)

// deepSeekImagePixelBudget is a vision entry's per-request pixel budget: a count
// or the adapter's named low-detail tier (`z.union([z.number(), "low"])`).
// Untagged, so a scalar lands on the arm its own type picks.
type deepSeekImagePixelBudget struct {
	Pixels   int64
	Named    string
	IsPixels bool
}

func (b *deepSeekImagePixelBudget) MarshalJSON() ([]byte, error) {
	if b.IsPixels {
		return json.Marshal(b.Pixels)
	}
	return json.Marshal(b.Named)
}

func (b *deepSeekImagePixelBudget) UnmarshalJSON(data []byte) error {
	var n int64
	if err := json.Unmarshal(data, &n); err == nil {
		b.IsPixels, b.Pixels, b.Named = true, n, ""
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		b.IsPixels, b.Pixels, b.Named = false, 0, s
		return nil
	}
	return fmt.Errorf("image pixel budget must be a whole number or a string")
}

// deepSeekCatalogModel is one entry of the advisory catalog, mirroring the
// adapter's DeepSeekCatalogModel. Field names are the YAML/JSON ones verbatim,
// so the same struct is both the settings-document shape and the wire shape.
// Optional fields are omitted on write rather than written as null.
type deepSeekCatalogModel struct {
	ID                 string                    `json:"id"`
	Name               *string                   `json:"name,omitempty"`
	Description        *string                   `json:"description,omitempty"`
	ContextWindow      *int64                    `json:"contextWindow,omitempty"`
	MaxTokens          *int64                    `json:"maxTokens,omitempty"`
	InputModalities    []string                  `json:"inputModalities,omitempty"`
	ImagePixelBudget   *deepSeekImagePixelBudget `json:"imagePixelBudget,omitempty"`
	ImageMaxBytes      *int64                    `json:"imageMaxBytes,omitempty"`
	SystemPromptUpdate *string                   `json:"systemPromptUpdate,omitempty"`
	// ImageDetail is the retired detail tier: read-only on purpose and NEVER
	// serialized (the adapter refuses a catalog entry that so much as carries
	// the key). Kept in the shape so a document an older build wrote still
	// parses, then reported through `invalid` and dropped on the next save.
	ImageDetail *string `json:"-"`
}

// UnmarshalJSON reads the document/wire shape including `imageDetail` (which the
// default marshal path skips).
func (m *deepSeekCatalogModel) UnmarshalJSON(data []byte) error {
	var s struct {
		ID                 string                    `json:"id"`
		Name               *string                   `json:"name"`
		Description        *string                   `json:"description"`
		ContextWindow      *int64                    `json:"contextWindow"`
		MaxTokens          *int64                    `json:"maxTokens"`
		InputModalities    []string                  `json:"inputModalities"`
		ImagePixelBudget   *deepSeekImagePixelBudget `json:"imagePixelBudget"`
		ImageMaxBytes      *int64                    `json:"imageMaxBytes"`
		SystemPromptUpdate *string                   `json:"systemPromptUpdate"`
		ImageDetail        *string                   `json:"imageDetail"`
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	*m = deepSeekCatalogModel{
		ID:                 s.ID,
		Name:               s.Name,
		Description:        s.Description,
		ContextWindow:      s.ContextWindow,
		MaxTokens:          s.MaxTokens,
		InputModalities:    s.InputModalities,
		ImagePixelBudget:   s.ImagePixelBudget,
		ImageMaxBytes:      s.ImageMaxBytes,
		SystemPromptUpdate: s.SystemPromptUpdate,
		ImageDetail:        s.ImageDetail,
	}
	return nil
}

// acceptsImages is the gate for the three image request-limit fields, which the
// adapter refuses on a text-only entry.
func (m deepSeekCatalogModel) acceptsImages() bool {
	for _, mod := range m.InputModalities {
		if mod == deepSeekModalityImage {
			return true
		}
	}
	return false
}

// deepSeekModelCatalog is what the settings panel reads.
type deepSeekModelCatalog struct {
	// Path is the resolved settings.yaml path, shown so the user can find the
	// document we write.
	Path string `json:"path"`
	// Exists is whether that document exists at all.
	Exists bool `json:"exists"`
	// Configured is whether it declares `llm-deepseek.models`; false means
	// Models is the adapter's built-in list, inherited rather than stored.
	Configured bool `json:"configured"`
	// Models is the effective catalog: what is stored, else the built-in
	// defaults.
	Models []deepSeekCatalogModel `json:"models"`
	// Error is why the stored document could not be read (only when it exists
	// and is unusable).
	Error *string `json:"error"`
	// Invalid is why the stored list is one the agent will refuse (duplicate
	// ids, a non-positive context window, image limits on a text-only entry…).
	// Distinct from Error: this document was understood, so its rows are
	// editable and fixing them is the point.
	Invalid *string `json:"invalid"`
}

// dshSettingsPath is `$DSH_HOME/settings.yaml` (default `~/.dsh/settings.yaml`).
func dshSettingsPath() string { return filepath.Join(dshHomeDir(), "settings.yaml") }

// deepSeekTextOnly builds a text-only catalog entry at the adapter's defaults.
func deepSeekTextOnly(id, name, description string) deepSeekCatalogModel {
	ctx := deepSeekDefaultContextWindow
	return deepSeekCatalogModel{
		ID:            id,
		Name:          strPtr(name),
		Description:   strPtr(description),
		ContextWindow: &ctx,
	}
}

// deepSeekDefaultModels is the catalog a DeepSeek session inherits when the
// section declares no `models`. It mirrors the harness' own DEEPSEEK_MODELS (not
// the adapter's DEFAULT_MODELS), and pins the values the adapter would resolve
// omitted fields to, because saving from the panel pins this list.
func deepSeekDefaultModels() []deepSeekCatalogModel {
	ctx := deepSeekDefaultContextWindow
	budget := deepSeekDefaultRequestImagePixelBudget
	maxBytes := deepSeekDefaultRequestImageMaxBytes
	inHistory := deepSeekSystemPromptUpdateInHistory
	return []deepSeekCatalogModel{
		{
			ID:                 "deepseek-flash",
			Name:               strPtr("DeepSeek-V4.1-Flash"),
			Description:        strPtr("更快更省，且在各项指标上超越 V4 Pro；支持图像理解。日常编码的默认档。"),
			ContextWindow:      &ctx,
			InputModalities:    []string{deepSeekModalityText, deepSeekModalityImage},
			ImagePixelBudget:   &deepSeekImagePixelBudget{Pixels: budget, IsPixels: true},
			ImageMaxBytes:      &maxBytes,
			SystemPromptUpdate: &inHistory,
		},
		deepSeekTextOnly(
			"deepseek-v4-pro",
			"DeepSeek-V4-Pro",
			"旧的高价档，官方已宣布有序下线；除非有特定理由，优先用 Flash。",
		),
	}
}

func registerDeepSeekCatalog(reg *Registry) {
	reg.Register("acp_load_deepseek_model_catalog", func(c *web.Context) (any, error) {
		return loadDeepSeekModelCatalogAt(dshSettingsPath()), nil
	})
	reg.Register("acp_update_deepseek_model_catalog", func(c *web.Context) (any, error) {
		models, present, err := deepSeekModelsParam(c)
		if err != nil {
			return nil, web.ErrTaskExecution(err.Error())
		}
		if err := updateDeepSeekModelCatalog(dshSettingsPath(), models, present); err != nil {
			return nil, web.ErrTaskExecution(err.Error())
		}
		Emit(c, "agents://changed", map[string]any{})
		return nil, nil
	})
}

// deepSeekModelsParam resolves the `models` body field, distinguishing absent
// (clear) from present-but-empty (also clear, per upstream's contract).
func deepSeekModelsParam(c *web.Context) ([]deepSeekCatalogModel, bool, error) {
	raw, ok := c.Body[deepSeekModelsKey]
	if !ok || raw == nil {
		return nil, false, nil
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return nil, true, err
	}
	var models []deepSeekCatalogModel
	if err := json.Unmarshal(b, &models); err != nil {
		return nil, true, err
	}
	return models, true, nil
}

// loadDeepSeekModelCatalogAt reads the catalog for the settings panel. Never
// errors: a missing file is "inheriting the defaults", and an unreadable one is
// reported in-band.
func loadDeepSeekModelCatalogAt(path string) deepSeekModelCatalog {
	inherited := func(exists bool, errMsg string) deepSeekModelCatalog {
		out := deepSeekModelCatalog{
			Path:   path,
			Exists: exists,
			Models: deepSeekDefaultModels(),
		}
		if errMsg != "" {
			out.Error = &errMsg
		}
		return out
	}

	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return inherited(false, "")
		}
		return inherited(true, "could not read the settings document: "+err.Error())
	}

	models, configured, readErr := readDeepSeekModels(string(b))
	switch {
	case readErr != "":
		return inherited(true, readErr)
	case !configured:
		return inherited(true, "")
	default:
		out := deepSeekModelCatalog{
			Path:       path,
			Exists:     true,
			Configured: true,
			Models:     models,
		}
		if _, err := validateDeepSeekModels(models); err != "" {
			out.Invalid = &err
		}
		return out
	}
}

// readDeepSeekModels parses `llm-deepseek.models` out of a settings document.
// `configured=false` (with no error) means the section/key is absent, i.e. the
// adapter's built-in catalog is inherited.
func readDeepSeekModels(raw string) (models []deepSeekCatalogModel, configured bool, errMsg string) {
	if strings.TrimSpace(raw) == "" {
		return nil, false, ""
	}
	var root any
	if err := yaml.Unmarshal([]byte(raw), &root); err != nil {
		return nil, false, "could not parse the YAML: " + err.Error()
	}
	if root == nil {
		return nil, false, ""
	}
	norm := normalizeYAML(root)
	m, ok := norm.(map[string]any)
	if !ok {
		return nil, false, "the settings document's root is not a mapping"
	}
	sectionRaw, ok := m[deepSeekSectionKey]
	if !ok || sectionRaw == nil {
		return nil, false, ""
	}
	section, ok := sectionRaw.(map[string]any)
	if !ok {
		return nil, false, fmt.Sprintf("`%s` is not a mapping", deepSeekSectionKey)
	}
	modelsRaw, ok := section[deepSeekModelsKey]
	if !ok || modelsRaw == nil {
		return nil, false, ""
	}
	entries, ok := modelsRaw.([]any)
	if !ok {
		return nil, false, fmt.Sprintf("`%s.%s` is not a list", deepSeekSectionKey, deepSeekModelsKey)
	}
	for _, entry := range entries {
		em, ok := entry.(map[string]any)
		if !ok {
			continue // caught with a better message by the decode below
		}
		for key := range em {
			if !containsStr(deepSeekKnownFields, key) {
				return nil, false, fmt.Sprintf(
					"`%s.%s` uses a field this build does not know (%s); edit the document by hand so nothing is lost",
					deepSeekSectionKey, deepSeekModelsKey, key)
			}
		}
	}
	jb, err := json.Marshal(entries)
	if err != nil {
		return nil, false, fmt.Sprintf("`%s.%s` has an unusable entry: %v", deepSeekSectionKey, deepSeekModelsKey, err)
	}
	var out []deepSeekCatalogModel
	if err := json.Unmarshal(jb, &out); err != nil {
		return nil, false, fmt.Sprintf("`%s.%s` has an unusable entry: %v", deepSeekSectionKey, deepSeekModelsKey, err)
	}
	return out, true, ""
}

// deepSeekKnownFields is every field of deepSeekCatalogModel as it is spelled in
// the document. `imageDetail` is listed even though the agent no longer accepts
// it — it is a key an older build once wrote, so reading and reporting it is the
// repair path.
var deepSeekKnownFields = []string{
	"id",
	"name",
	"description",
	"contextWindow",
	"maxTokens",
	"inputModalities",
	"imagePixelBudget",
	"imageMaxBytes",
	"systemPromptUpdate",
	"imageDetail",
}

// validateDeepSeekModels judges a submitted catalog by the adapter's own
// resolveModels rules. Returns the normalized list to store (ids/names trimmed),
// or a message describing the first problem.
func validateDeepSeekModels(models []deepSeekCatalogModel) ([]deepSeekCatalogModel, string) {
	var seen []string
	out := make([]deepSeekCatalogModel, 0, len(models))

	for _, model := range models {
		id := strings.TrimSpace(model.ID)
		if id == "" {
			return nil, "every model needs an id"
		}
		if containsStr(seen, id) {
			return nil, fmt.Sprintf("duplicate model id %q", id)
		}
		seen = append(seen, id)

		name := trimmedOptional(model.Name)
		description := trimmedOptional(model.Description)

		// Every numeric field must be a positive integer; a section the adapter
		// cannot resolve is dropped whole, so one bad number would take the
		// entire catalog with it. The upper bound is JS's safe-integer ceiling.
		bounded := func(value *int64, field string) (*int64, string) {
			if value == nil {
				return nil, ""
			}
			if *value == 0 {
				return nil, fmt.Sprintf("model %q has a %s of 0; it must be > 0", id, field)
			}
			if *value > deepSeekMaxSafeInteger {
				return nil, fmt.Sprintf("model %q has a %s above the maximum the agent accepts", id, field)
			}
			return value, ""
		}
		contextWindow, msg := bounded(model.ContextWindow, "context window")
		if msg != "" {
			return nil, msg
		}
		maxTokens, msg := bounded(model.MaxTokens, "max output tokens")
		if msg != "" {
			return nil, msg
		}
		imageMaxBytes, msg := bounded(model.ImageMaxBytes, "image byte cap")
		if msg != "" {
			return nil, msg
		}

		// The pixel budget takes a count OR the named `low` tier, so it is
		// judged on both arms rather than through bounded.
		var pixelBudget *deepSeekImagePixelBudget
		if model.ImagePixelBudget != nil {
			if model.ImagePixelBudget.IsPixels {
				v, msg := bounded(&model.ImagePixelBudget.Pixels, "image pixel budget")
				if msg != "" {
					return nil, msg
				}
				pixelBudget = &deepSeekImagePixelBudget{Pixels: *v, IsPixels: true}
			} else {
				named := strings.TrimSpace(model.ImagePixelBudget.Named)
				if named != deepSeekImagePixelBudgetLow {
					return nil, fmt.Sprintf(
						"model %q has an image pixel budget of %q; it must be %q or a whole number above 0",
						id, named, deepSeekImagePixelBudgetLow)
				}
				pixelBudget = &deepSeekImagePixelBudget{Named: named}
			}
		}

		// Retired in the adapter 0.9.0, and not by being ignored: the adapter
		// throws on the key's mere presence, so one of these takes down the
		// whole catalog.
		if model.ImageDetail != nil {
			return nil, fmt.Sprintf(
				"model %q still sets an image detail tier, which the agent no longer accepts; use the image pixel budget instead (saving from here removes it)",
				id)
		}

		var systemPromptUpdate *string
		if v := trimmedOptional(model.SystemPromptUpdate); v != nil {
			if *v != deepSeekSystemPromptUpdateInHistory {
				return nil, fmt.Sprintf(
					"model %q has an unknown system prompt update mode %q; the agent accepts only %q",
					id, *v, deepSeekSystemPromptUpdateInHistory)
			}
			systemPromptUpdate = v
		}

		var modalities []string
		if model.InputModalities != nil {
			if len(model.InputModalities) == 0 {
				return nil, fmt.Sprintf("model %q lists no input modalities", id)
			}
			for _, modality := range model.InputModalities {
				if modality != deepSeekModalityText && modality != deepSeekModalityImage {
					return nil, fmt.Sprintf("model %q has an unknown input modality %q", id, modality)
				}
				if containsStr(modalities, modality) {
					return nil, fmt.Sprintf("model %q repeats the input modality %q", id, modality)
				}
				modalities = append(modalities, modality)
			}
		}

		normalized := deepSeekCatalogModel{
			ID:                 id,
			Name:               name,
			Description:        description,
			ContextWindow:      contextWindow,
			MaxTokens:          maxTokens,
			InputModalities:    modalities,
			ImagePixelBudget:   pixelBudget,
			ImageMaxBytes:      imageMaxBytes,
			SystemPromptUpdate: systemPromptUpdate,
		}

		// The adapter refuses image request limits on a text-only entry
		// outright — the whole section would be dropped, not just the field.
		if !normalized.acceptsImages() && (normalized.ImagePixelBudget != nil || normalized.ImageMaxBytes != nil) {
			return nil, fmt.Sprintf("model %q is text-only, so it cannot declare image limits", normalized.ID)
		}
		out = append(out, normalized)
	}
	return out, ""
}

// trimmedOptional trims a string pointer, mapping blank/absent to nil.
func trimmedOptional(s *string) *string {
	if s == nil {
		return nil
	}
	v := strings.TrimSpace(*s)
	if v == "" {
		return nil
	}
	return &v
}

// updateDeepSeekModelCatalog writes (or clears) `llm-deepseek.models` in the
// settings document. A nil/empty list removes the key so the adapter's built-in
// catalog is inherited again.
func updateDeepSeekModelCatalog(path string, models []deepSeekCatalogModel, present bool) error {
	var validated []deepSeekCatalogModel
	if present && len(models) > 0 {
		v, msg := validateDeepSeekModels(models)
		if msg != "" {
			return fmt.Errorf("invalid DeepSeek model list: %s", msg)
		}
		validated = v
	}

	// Read immediately before patching: the document is shared with the harness
	// and a hand editor, so the merge is against what is on disk now.
	existing := ""
	b, err := os.ReadFile(path)
	switch {
	case err == nil:
		existing = string(b)
	case os.IsNotExist(err):
	default:
		return fmt.Errorf("could not read the DeepSeek settings document: %w", err)
	}

	patched, msg := patchSettingsModels(existing, validated)
	if msg != "" {
		return fmt.Errorf("DeepSeek settings: %s", msg)
	}
	if patched == existing {
		return nil
	}
	return writeSettingsDocument(path, patched)
}
