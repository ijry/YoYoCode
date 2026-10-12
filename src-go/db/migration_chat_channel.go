package db

// chat_channel schema (upstream db/entities/chat_channel.rs): a configured
// outbound chat integration (Lark / Telegram / WeChat). The per-channel token
// is NOT stored here — upstream keeps it in the OS keyring; the Go build keeps
// it in a dedicated app_metadata key (see handlers/chat_channel.go).

func init() {
	RegisterMigration(func(d *Database) error {
		_, err := d.Conn.Exec(`CREATE TABLE IF NOT EXISTS chat_channel (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			channel_type TEXT NOT NULL,
			enabled INTEGER NOT NULL DEFAULT 0,
			config_json TEXT NOT NULL DEFAULT '{}',
			event_filter_json TEXT,
			daily_report_enabled INTEGER NOT NULL DEFAULT 0,
			daily_report_time TEXT,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);`)
		return err
	})
	RegisterMigration(func(d *Database) error {
		_, err := d.Conn.Exec(`CREATE TABLE IF NOT EXISTS chat_channel_message_log (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			channel_id INTEGER NOT NULL,
			direction TEXT NOT NULL,
			message_type TEXT NOT NULL,
			content_preview TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL,
			error_detail TEXT,
			created_at TEXT NOT NULL
		);`)
		return err
	})
}
