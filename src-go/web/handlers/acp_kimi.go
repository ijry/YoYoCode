package handlers

// acp_kimi.go ports the Kimi Code config/catalog commands. It starts with
// acp_fetch_kimi_models: validate an API key + endpoint by listing the account's
// models (an OpenAI-compatible GET <base_url>/models), which doubles as a
// one-click connection test.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"yoyocode/src-go/web"
)

// RegisterACPKimi wires the Kimi Code config/catalog commands.
func RegisterACPKimi(reg *Registry) {
	reg.Register("acp_fetch_kimi_models", func(c *web.Context) (any, error) {
		ids, err := fetchKimiModels(
			strVal(c.Body["baseUrl"], c.Body["base_url"]),
			strVal(c.Body["apiKey"], c.Body["api_key"]),
		)
		if err != nil {
			return nil, web.ErrTaskExecution(err.Error())
		}
		return ids, nil
	})
}

// fetchKimiModels GETs <base_url>/models with the key as a Bearer token and
// returns the sorted, de-duplicated model ids. Surfaces the provider's own error
// message on a non-2xx response.
func fetchKimiModels(baseURL, apiKey string) ([]string, error) {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		return nil, fmt.Errorf("base URL is required to list models")
	}
	key := strings.TrimSpace(apiKey)
	if key == "" {
		return nil, fmt.Errorf("API key is required to list models")
	}
	client := &http.Client{Timeout: 20 * time.Second}
	req, err := http.NewRequest(http.MethodGet, base+"/models", nil)
	if err != nil {
		return nil, fmt.Errorf("list models request failed: %w", err)
	}
	req.Header.Set("authorization", "Bearer "+key)
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list models request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("list models returned invalid JSON: %w", err)
	}
	var parsed struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("list models returned invalid JSON: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := parsed.Error.Message
		if msg == "" {
			msg = "request rejected"
		}
		return nil, fmt.Errorf("%d: %s", resp.StatusCode, msg)
	}
	ids := make([]string, 0, len(parsed.Data))
	for _, m := range parsed.Data {
		if m.ID != "" {
			ids = append(ids, m.ID)
		}
	}
	sort.Strings(ids)
	out := ids[:0]
	for i, id := range ids {
		if i == 0 || id != ids[i-1] {
			out = append(out, id)
		}
	}
	return out, nil
}
