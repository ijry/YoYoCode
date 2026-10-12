package db

// custom_agent schema (upstream db/entities/custom_agent.rs): user-registered
// ACP agents. Rows are the persistent form of a CustomAgentDef; the handler
// layer rebuilds launch metadata from them.

func init() {
	RegisterMigration(func(d *Database) error {
		_, err := d.Conn.Exec(`CREATE TABLE IF NOT EXISTS custom_agent (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			registry_id TEXT NOT NULL UNIQUE,
			name TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			version TEXT NOT NULL DEFAULT '',
			distribution_kind TEXT NOT NULL,
			spec_json TEXT NOT NULL,
			icon_url TEXT,
			skills_shared_store INTEGER NOT NULL DEFAULT 0,
			skills_dir TEXT,
			source TEXT NOT NULL DEFAULT 'registry',
			version_probe TEXT,
			supports_mcp INTEGER NOT NULL DEFAULT 1,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);`)
		return err
	})
}
