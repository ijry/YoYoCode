package handlers

import (
	"encoding/json"
	"time"

	"yoyocode/src-go/app_state"
	"yoyocode/src-go/db"
	"yoyocode/src-go/web"
)

// stateDB pulls the database from the handler context state.
func stateDB(c *web.Context) *db.Database {
	if st, ok := c.State.(*appstate.AppState); ok {
		return st.DB
	}
	return nil
}

// nowRFC3339 returns the current UTC time in the upstream timestamptz format.
func nowRFC3339() string { return time.Now().UTC().Format(time.RFC3339) }

// JSONUnmarshalFirst attempts to unmarshal text into out; tolerates leading
// non-JSON text (some upstream values embed JSON within other text).
func JSONUnmarshalFirst(text string, out any) error {
	if i := jsonStart(text); i >= 0 {
		text = text[i:]
	}
	b := []byte(text)
	for len(b) > 0 && (b[len(b)-1] == '\n' || b[len(b)-1] == ' ' || b[len(b)-1] == '\r') {
		b = b[:len(b)-1]
	}
	return json.Unmarshal(b, out)
}

func jsonStart(s string) int {
	for i, r := range s {
		if r == '{' || r == '[' || r == '"' || r == 't' || r == 'f' || r == 'n' || (r >= '0' && r <= '9') || r == '-' {
			return i
		}
		if r == ' ' || r == '\t' || r == '\r' || r == '\n' {
			continue
		}
		return -1
	}
	return -1
}
