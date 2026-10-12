package db

import "fmt"

// migrate applies the schema in dependency order.
// Tables are named exactly as upstream (snake_case) so future schema diffs
// against upstream are trivial.
func (d *Database) migrate() error {
	stmts := []string{
		// 1. app_metadata (upstream m20260211_000001_init)
		`CREATE TABLE IF NOT EXISTS app_metadata (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			key TEXT NOT NULL UNIQUE,
			value TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			deleted_at TEXT
		);`,
		// 2. folder
		`CREATE TABLE IF NOT EXISTS folder (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			path TEXT NOT NULL UNIQUE,
			git_branch TEXT,
			default_agent_type TEXT,
			last_opened_at TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			deleted_at TEXT,
			is_open INTEGER DEFAULT 0,
			sort_order INTEGER DEFAULT 0,
			folder_group_id INTEGER,
			folder_id INTEGER,
			is_chat INTEGER DEFAULT 0,
			alias TEXT,
			color TEXT,
			parent_id INTEGER,
			kind TEXT DEFAULT 'regular'
		);`,
		// 3. conversation
		`CREATE TABLE IF NOT EXISTS conversation (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			folder_id INTEGER NOT NULL,
			title TEXT,
			agent_type TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'in_progress',
			model TEXT,
			git_branch TEXT,
			external_id TEXT,
			parent_id INTEGER,
			message_count INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			deleted_at TEXT,
			title_locked INTEGER DEFAULT 0,
			pinned_at TEXT,
			kind TEXT DEFAULT 'agent',
			tags TEXT,
			branch_tag TEXT,
			last_message_at TEXT
		);`,
		`CREATE INDEX IF NOT EXISTS idx_conversation_folder_id ON conversation(folder_id);`,
		`CREATE INDEX IF NOT EXISTS idx_conversation_parent_id ON conversation(parent_id);`,
		// 4. conversation_tag (upstream entities/conversation_tag.rs)
		`CREATE TABLE IF NOT EXISTS conversation_tag (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			folder_id INTEGER,
			name TEXT NOT NULL,
			color TEXT NOT NULL,
			sort_order INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);`,
		// 5. conversation_tag_link
		`CREATE TABLE IF NOT EXISTS conversation_tag_link (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			conversation_id INTEGER NOT NULL,
			tag_id INTEGER NOT NULL,
			created_at TEXT NOT NULL,
			UNIQUE(conversation_id, tag_id)
		);`,
		// 6. folder_group
		`CREATE TABLE IF NOT EXISTS folder_group (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			color TEXT NOT NULL DEFAULT 'inherit',
			sort_order INTEGER DEFAULT 0,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);`,
		// 7. folder_commands table
		`CREATE TABLE IF NOT EXISTS folder_command (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			folder_id INTEGER NOT NULL,
			name TEXT NOT NULL,
			command TEXT NOT NULL,
			sort_order INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);`,
		// 7b. folder_link (upstream entities/folder_link.rs): symlinked dirs.
		`CREATE TABLE IF NOT EXISTS folder_link (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			folder_id INTEGER NOT NULL,
			name TEXT NOT NULL,
			target_path TEXT NOT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_folder_link_folder ON folder_link(folder_id);`,
		// 8. opened_tab (upstream entities/opened_tab.rs)
		`CREATE TABLE IF NOT EXISTS opened_tab (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			folder_id INTEGER NOT NULL,
			conversation_id INTEGER,
			agent_type TEXT NOT NULL,
			position INTEGER NOT NULL DEFAULT 0,
			is_active INTEGER NOT NULL DEFAULT 0,
			is_pinned INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);`,
		// 9. quick_message (upstream entities/quick_message.rs)
		`CREATE TABLE IF NOT EXISTS quick_message (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			title TEXT NOT NULL,
			content TEXT NOT NULL,
			sort_order INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);`,
		// 10. work_task (upstream entities/work_task.rs)
		`CREATE TABLE IF NOT EXISTS work_task (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			folder_id INTEGER NOT NULL,
			title TEXT NOT NULL,
			config TEXT NOT NULL DEFAULT '{}',
			status TEXT NOT NULL DEFAULT 'todo',
			failure_reason TEXT,
			last_error TEXT,
			run_seq INTEGER NOT NULL DEFAULT 0,
			sort_order INTEGER NOT NULL DEFAULT 0,
			worktree_folder_id INTEGER,
			conversation_id INTEGER,
			connection_id TEXT,
			base_branch TEXT,
			base_sha TEXT,
			work_branch TEXT,
			merge_state TEXT,
			pending_merge TEXT,
			cleanup_state TEXT,
			verdict TEXT,
			result_summary TEXT,
			files_changed INTEGER,
			additions INTEGER,
			deletions INTEGER,
			merge_commit TEXT,
			completion_kind TEXT,
			preflight TEXT,
			archived_at TEXT,
			scheduled_at TEXT,
			source_kind TEXT,
			source_key TEXT,
			source_meta TEXT,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			started_at TEXT,
			settled_at TEXT,
			finished_at TEXT,
			deleted_at TEXT
		);`,
		`CREATE INDEX IF NOT EXISTS idx_work_task_folder ON work_task(folder_id);`,
		// 10b. work_task_event (append-only timeline)
		`CREATE TABLE IF NOT EXISTS work_task_event (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			task_id INTEGER NOT NULL,
			kind TEXT NOT NULL,
			actor TEXT NOT NULL,
			payload TEXT,
			created_at TEXT NOT NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_work_task_event_task ON work_task_event(task_id);`,
		// 10c. work_task_settings (per-folder config blob)
		`CREATE TABLE IF NOT EXISTS work_task_settings (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			folder_id INTEGER NOT NULL UNIQUE,
			config TEXT NOT NULL DEFAULT '{}',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);`,
		// 10d. work_task_template (saved task templates, upsert by name)
		`CREATE TABLE IF NOT EXISTS work_task_template (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL UNIQUE,
			title TEXT NOT NULL,
			config TEXT NOT NULL DEFAULT '{}',
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);`,
		// 12. automation + automation_run (upstream entities/automation*.rs).
		`CREATE TABLE IF NOT EXISTS automation (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			enabled INTEGER NOT NULL DEFAULT 1,
			trigger_kind TEXT NOT NULL DEFAULT 'schedule',
			cron TEXT,
			timezone TEXT NOT NULL DEFAULT 'UTC',
			next_run_at TEXT,
			agent_type TEXT NOT NULL,
			root_folder_id INTEGER,
			isolation TEXT NOT NULL DEFAULT 'worktree_per_run',
			branch TEXT,
			is_remote_branch INTEGER NOT NULL DEFAULT 0,
			config TEXT NOT NULL DEFAULT '{}',
			last_run_at TEXT,
			last_run_status TEXT,
			last_run_conversation_id INTEGER,
			unseen_failures INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			deleted_at TEXT
		);`,
		`CREATE TABLE IF NOT EXISTS automation_run (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			automation_id INTEGER NOT NULL,
			status TEXT NOT NULL DEFAULT 'running',
			trigger TEXT NOT NULL,
			scheduled_for TEXT,
			started_at TEXT,
			ended_at TEXT,
			conversation_id INTEGER,
			connection_id TEXT,
			worktree_folder_id INTEGER,
			stop_reason TEXT,
			error TEXT,
			summary TEXT,
			created_at TEXT NOT NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_automation_run_automation ON automation_run(automation_id);`,
		// 13. canvas_board + canvas_node (upstream entities/canvas_*.rs).
		`CREATE TABLE IF NOT EXISTS canvas_board (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT,
			description TEXT,
			color TEXT,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS canvas_node (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			board_id INTEGER NOT NULL,
			kind TEXT NOT NULL,
			folder_id INTEGER,
			folder_group_id INTEGER,
			agent_type TEXT,
			conversation_id INTEGER,
			member_ids TEXT,
			title TEXT,
			content TEXT,
			path TEXT,
			color TEXT,
			collapsed INTEGER NOT NULL DEFAULT 0,
			grid_columns INTEGER NOT NULL DEFAULT 0,
			grid_rows INTEGER NOT NULL DEFAULT 0,
			x REAL NOT NULL DEFAULT 0,
			y REAL NOT NULL DEFAULT 0,
			width REAL NOT NULL DEFAULT 0,
			height REAL NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_canvas_node_board ON canvas_node(board_id);`,
		// 14. token_usage_turn + token_usage_sync (upstream entities/token_usage_*.rs).
		`CREATE TABLE IF NOT EXISTS token_usage_turn (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			conversation_id INTEGER NOT NULL,
			turn_key TEXT NOT NULL,
			occurred_at TEXT NOT NULL,
			model TEXT,
			input_tokens INTEGER NOT NULL DEFAULT 0,
			output_tokens INTEGER NOT NULL DEFAULT 0,
			cache_creation_tokens INTEGER NOT NULL DEFAULT 0,
			cache_read_tokens INTEGER NOT NULL DEFAULT 0,
			total_tokens INTEGER NOT NULL DEFAULT 0,
			duration_ms INTEGER NOT NULL DEFAULT 0
		);`,
		`CREATE INDEX IF NOT EXISTS idx_token_usage_turn_conv ON token_usage_turn(conversation_id);`,
		`CREATE INDEX IF NOT EXISTS idx_token_usage_turn_occurred ON token_usage_turn(occurred_at);`,
		`CREATE TABLE IF NOT EXISTS token_usage_sync (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			conversation_id INTEGER NOT NULL UNIQUE,
			source_updated_at TEXT NOT NULL,
			turn_count INTEGER NOT NULL DEFAULT 0,
			total_tokens INTEGER NOT NULL DEFAULT 0,
			synced_at TEXT NOT NULL
		);`,
		// 11. model_provider (upstream entities/model_provider.rs). Provider
		// logic is intentionally not replicated, but mcode-app lists providers,
		// so the table + empty-list handlers must exist for a clean migration.
		`CREATE TABLE IF NOT EXISTS model_provider (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			api_url TEXT NOT NULL,
			api_key TEXT NOT NULL,
			agent_types_json TEXT NOT NULL DEFAULT '[]',
			agent_type TEXT NOT NULL DEFAULT '',
			model TEXT,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);`,
		// 12. agent_setting (upstream entities/agent_setting.rs): per-agent
		// enabled/order/installed-version/env persisted for the ACP engine.
		`CREATE TABLE IF NOT EXISTS agent_setting (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			agent_type TEXT NOT NULL UNIQUE,
			registry_id TEXT NOT NULL DEFAULT '',
			enabled INTEGER NOT NULL DEFAULT 1,
			sort_order INTEGER NOT NULL DEFAULT 0,
			installed_version TEXT,
			env_json TEXT,
			model_provider_id INTEGER,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);`,
	}
	for _, s := range stmts {
		if _, err := d.Conn.Exec(s); err != nil {
			return fmt.Errorf("migrate: %w\nstmt: %s", err, s)
		}
	}
	// Feature-module schema (see RegisterMigration).
	for _, fn := range extraMigrations {
		if err := fn(d); err != nil {
			return fmt.Errorf("migrate (module): %w", err)
		}
	}
	return nil
}
