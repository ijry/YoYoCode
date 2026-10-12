package handlers

// Mirror upstream web/handlers/model_provider.rs.
//
// The user asked NOT to replicate model-provider *logic* (no connection
// refresh, no live re-wiring of agents). But mcode-app calls
// list/create/update/delete_model_provider as part of its settings surface, so
// for a seamless migration these must behave like plain storage CRUD and return
// the exact ModelProviderInfo shape — including api_key_masked.

import (
	"database/sql"
	"strings"

	"yoyocode/src-go/db"
	"yoyocode/src-go/web"
)

// maskAPIKey mirrors models::model_provider::mask_api_key — operates on runes
// (never bytes) so a multibyte key can't panic, keeps 4 chars at each edge.
func maskAPIKey(key string) string {
	chars := []rune(key)
	n := len(chars)
	bullet := "\u2022"
	if n <= 8 {
		return strings.Repeat(bullet, n)
	}
	prefix := string(chars[:4])
	suffix := string(chars[n-4:])
	mid := n
	if mid > 20 {
		mid = 20
	}
	return prefix + strings.Repeat(bullet, mid-8) + suffix
}

func modelProviderByID(d *db.Database, id int64) (map[string]any, error) {
	row := d.Conn.QueryRow(
		`SELECT id, name, api_url, api_key, agent_type, model, created_at, updated_at FROM model_provider WHERE id = ?`, id)
	var (
		pid                             int64
		name, apiURL, apiKey, agentType string
		model                           sql.NullString
		createdAt, updatedAt            string
	)
	err := row.Scan(&pid, &name, &apiURL, &apiKey, &agentType, &model, &createdAt, &updatedAt)
	if err == sql.ErrNoRows {
		return nil, web.ErrNotFound("model provider not found")
	}
	if err != nil {
		return nil, web.ErrDatabase(err.Error())
	}
	rec := map[string]any{
		"id":             pid,
		"name":           name,
		"api_url":        apiURL,
		"api_key":        apiKey,
		"api_key_masked": maskAPIKey(apiKey),
		"agent_type":     agentType,
		"model":          nil,
		"created_at":     createdAt,
		"updated_at":     updatedAt,
	}
	if model.Valid {
		rec["model"] = model.String
	}
	return rec, nil
}

// RegisterModelProvider wires provider storage CRUD (no live-connection logic).
func RegisterModelProvider(reg *Registry) {
	reg.Register("list_model_providers", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		rows, err := d.Conn.Query(
			`SELECT id, name, api_url, api_key, agent_type, model, created_at, updated_at FROM model_provider ORDER BY id ASC`)
		if err != nil {
			return nil, web.ErrDatabase(err.Error())
		}
		defer rows.Close()
		out := []map[string]any{}
		for rows.Next() {
			var (
				pid                             int64
				name, apiURL, apiKey, agentType string
				model                           sql.NullString
				createdAt, updatedAt            string
			)
			if rows.Scan(&pid, &name, &apiURL, &apiKey, &agentType, &model, &createdAt, &updatedAt) != nil {
				continue
			}
			rec := map[string]any{
				"id": pid, "name": name, "api_url": apiURL, "api_key": apiKey,
				"api_key_masked": maskAPIKey(apiKey), "agent_type": agentType,
				"model": nil, "created_at": createdAt, "updated_at": updatedAt,
			}
			if model.Valid {
				rec["model"] = model.String
			}
			out = append(out, rec)
		}
		return out, nil
	})

	reg.Register("create_model_provider", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		name := GetString(c, "name")
		apiURL := GetString(c, "apiUrl", "api_url")
		apiKey := GetString(c, "apiKey", "api_key")
		agentType := GetString(c, "agentType", "agent_type")
		model := GetString(c, "model")
		now := nowRFC3339()
		agentTypesJSON := "[]"
		if agentType != "" {
			agentTypesJSON = `["` + agentType + `"]`
		}
		res, err := d.Conn.Exec(
			`INSERT INTO model_provider (name, api_url, api_key, agent_types_json, agent_type, model, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?)`,
			name, apiURL, apiKey, agentTypesJSON, agentType, nullIfEmpty(model), now, now)
		if err != nil {
			return nil, web.ErrDatabase(err.Error())
		}
		id, _ := res.LastInsertId()
		return modelProviderByID(d, id)
	})

	reg.Register("update_model_provider", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		id, err := GetIntOrErr(c, "id")
		if err != nil {
			return nil, err
		}
		if _, err := modelProviderByID(d, id); err != nil {
			return nil, err
		}
		now := nowRFC3339()
		set := func(col string, val any) error {
			_, e := d.Conn.Exec(`UPDATE model_provider SET `+col+` = ?, updated_at = ? WHERE id = ?`, val, now, id)
			return e
		}
		if _, ok := c.Body["name"]; ok {
			if err := set("name", GetString(c, "name")); err != nil {
				return nil, web.ErrDatabase(err.Error())
			}
		}
		if _, ok := c.Body["apiUrl"]; ok {
			if err := set("api_url", GetString(c, "apiUrl", "api_url")); err != nil {
				return nil, web.ErrDatabase(err.Error())
			}
		} else if _, ok := c.Body["api_url"]; ok {
			if err := set("api_url", GetString(c, "apiUrl", "api_url")); err != nil {
				return nil, web.ErrDatabase(err.Error())
			}
		}
		if _, ok := c.Body["apiKey"]; ok {
			if err := set("api_key", GetString(c, "apiKey", "api_key")); err != nil {
				return nil, web.ErrDatabase(err.Error())
			}
		} else if _, ok := c.Body["api_key"]; ok {
			if err := set("api_key", GetString(c, "apiKey", "api_key")); err != nil {
				return nil, web.ErrDatabase(err.Error())
			}
		}
		if _, ok := c.Body["agentType"]; ok {
			if err := set("agent_type", GetString(c, "agentType", "agent_type")); err != nil {
				return nil, web.ErrDatabase(err.Error())
			}
		} else if _, ok := c.Body["agent_type"]; ok {
			if err := set("agent_type", GetString(c, "agentType", "agent_type")); err != nil {
				return nil, web.ErrDatabase(err.Error())
			}
		}
		if _, ok := c.Body["model"]; ok {
			if err := set("model", nullIfEmpty(GetString(c, "model"))); err != nil {
				return nil, web.ErrDatabase(err.Error())
			}
		}
		return modelProviderByID(d, id)
	})

	reg.Register("delete_model_provider", func(c *web.Context) (any, error) {
		d, err := requireDB(c)
		if err != nil {
			return nil, err
		}
		id, err := GetIntOrErr(c, "id")
		if err != nil {
			return nil, err
		}
		if _, err := d.Conn.Exec(`DELETE FROM model_provider WHERE id = ?`, id); err != nil {
			return nil, web.ErrDatabase(err.Error())
		}
		return EmptyResult(), nil
	})
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
