package terminal

// Wire types mirroring src-tauri/src/terminal/types.rs. The event payload and
// snapshot shapes are byte-for-byte what mcode-app reads off the firehose.

// TerminalEvent is one chunk of PTY output (or the exit marker), broadcast on
// terminal://output/{id} and terminal://exit/{id}.
type TerminalEvent struct {
	TerminalID string `json:"terminal_id"`
	Data       string `json:"data"`
	Seq        uint64 `json:"seq"`
}

// TerminalSnapshot is the re-attach payload behind terminal_snapshot.
type TerminalSnapshot struct {
	Alive bool   `json:"alive"`
	Data  string `json:"data"`
	Seq   uint64 `json:"seq"`
}

// TerminalInfo is one row of terminal_list.
type TerminalInfo struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}
