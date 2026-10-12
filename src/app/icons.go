package app

// Simple original monochrome UI glyphs (24x24). Icon tints them to the
// surrounding text color, so a single fill is all each needs. Parsed once at
// init; a malformed glyph degrades to no icon rather than panicking.
import "github.com/egoist/mygo/ui"

func mustIcon(data string) *ui.SVG {
	s, err := ui.ParseSVG([]byte(data))
	if err != nil {
		return nil
	}
	return s
}

var (
	iconWorkspace = mustIcon(`<svg viewBox="0 0 24 24"><rect x="3" y="4" width="18" height="16" rx="2" fill="none" stroke="black" stroke-width="2"/><line x1="3" y1="9" x2="21" y2="9" stroke="black" stroke-width="2"/><line x1="9" y1="9" x2="9" y2="20" stroke="black" stroke-width="2"/></svg>`)
	iconSettings  = mustIcon(`<svg viewBox="0 0 24 24"><line x1="4" y1="7" x2="20" y2="7" stroke="black" stroke-width="2"/><line x1="4" y1="12" x2="20" y2="12" stroke="black" stroke-width="2"/><line x1="4" y1="17" x2="20" y2="17" stroke="black" stroke-width="2"/><circle cx="9" cy="7" r="2.6" fill="black"/><circle cx="15" cy="12" r="2.6" fill="black"/><circle cx="8" cy="17" r="2.6" fill="black"/></svg>`)
	iconPet       = mustIcon(`<svg viewBox="0 0 24 24"><circle cx="7" cy="9" r="2" fill="black"/><circle cx="12" cy="7" r="2" fill="black"/><circle cx="17" cy="9" r="2" fill="black"/><ellipse cx="12" cy="15.5" rx="5" ry="4" fill="black"/></svg>`)
	iconFolder    = mustIcon(`<svg viewBox="0 0 24 24"><path d="M3 6a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v9a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V6z" fill="black"/></svg>`)
	iconChat      = mustIcon(`<svg viewBox="0 0 24 24"><path d="M4 5a2 2 0 0 1 2-2h12a2 2 0 0 1 2 2v9a2 2 0 0 1-2 2H9l-5 4V5z" fill="black"/></svg>`)
	// Original geometric glyphs for the top nav route rows.
	iconNewChat     = mustIcon(`<svg viewBox="0 0 24 24"><path d="M4 5a2 2 0 0 1 2-2h12a2 2 0 0 1 2 2v9a2 2 0 0 1-2 2H9l-5 4V5z" fill="none" stroke="black" stroke-width="2"/><line x1="12" y1="7" x2="12" y2="12" stroke="black" stroke-width="2"/><line x1="9.5" y1="9.5" x2="14.5" y2="9.5" stroke="black" stroke-width="2"/></svg>`)
	iconAutomations = mustIcon(`<svg viewBox="0 0 24 24"><path d="M13 2 4 14h6l-1 8 9-12h-6l1-8z" fill="black"/></svg>`)
	iconTasks       = mustIcon(`<svg viewBox="0 0 24 24"><path d="M4 6l2 2 3-3" fill="none" stroke="black" stroke-width="2"/><path d="M4 15l2 2 3-3" fill="none" stroke="black" stroke-width="2"/><line x1="12" y1="6" x2="20" y2="6" stroke="black" stroke-width="2"/><line x1="12" y1="15" x2="20" y2="15" stroke="black" stroke-width="2"/></svg>`)
	iconForge       = mustIcon(`<svg viewBox="0 0 24 24"><rect x="3" y="4" width="18" height="5" rx="1.5" fill="none" stroke="black" stroke-width="2"/><rect x="3" y="12" width="18" height="8" rx="1.5" fill="none" stroke="black" stroke-width="2"/><line x1="7" y1="6.5" x2="7" y2="6.5" stroke="black" stroke-width="2"/></svg>`)
	iconCanvas      = mustIcon(`<svg viewBox="0 0 24 24"><circle cx="6" cy="6" r="2.5" fill="none" stroke="black" stroke-width="2"/><circle cx="18" cy="9" r="2.5" fill="none" stroke="black" stroke-width="2"/><circle cx="9" cy="18" r="2.5" fill="none" stroke="black" stroke-width="2"/><line x1="8" y1="7" x2="16" y2="8.5" stroke="black" stroke-width="2"/><line x1="7" y1="8" x2="9" y2="15.5" stroke="black" stroke-width="2"/></svg>`)
	iconPinned      = mustIcon(`<svg viewBox="0 0 24 24"><path d="M12 2l3 6 6 .5-4.5 4 1.5 6-6-3.5-6 3.5 1.5-6L3 8.5 9 8z" fill="black"/></svg>`)
	iconRecent      = mustIcon(`<svg viewBox="0 0 24 24"><circle cx="12" cy="12" r="9" fill="none" stroke="black" stroke-width="2"/><path d="M12 7v5l4 2" fill="none" stroke="black" stroke-width="2"/></svg>`)

	// Conversation-detail glyphs. Tool cards mirror upstream's lucide set: a
	// wrench for a tool, a clock while it runs, a check / cross when it
	// settles, and a chevron that flips with the card's open state.
	iconWrench   = mustIcon(`<svg viewBox="0 0 24 24"><path d="M14.7 6.3a4 4 0 0 0 5 5l-9 9a2.1 2.1 0 0 1-3-3l9-9z" fill="none" stroke="black" stroke-width="2" stroke-linejoin="round"/></svg>`)
	iconClock    = mustIcon(`<svg viewBox="0 0 24 24"><circle cx="12" cy="12" r="9" fill="none" stroke="black" stroke-width="2"/><path d="M12 7v5l3.5 2" fill="none" stroke="black" stroke-width="2" stroke-linecap="round"/></svg>`)
	iconCheck    = mustIcon(`<svg viewBox="0 0 24 24"><circle cx="12" cy="12" r="9" fill="none" stroke="black" stroke-width="2"/><path d="M8 12.5l2.6 2.6L16 9.5" fill="none" stroke="black" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg>`)
	iconCross    = mustIcon(`<svg viewBox="0 0 24 24"><circle cx="12" cy="12" r="9" fill="none" stroke="black" stroke-width="2"/><path d="M9 9l6 6M15 9l-6 6" fill="none" stroke="black" stroke-width="2" stroke-linecap="round"/></svg>`)
	iconChevron  = mustIcon(`<svg viewBox="0 0 24 24"><path d="M9 6l6 6-6 6" fill="none" stroke="black" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg>`)
	iconChevronD = mustIcon(`<svg viewBox="0 0 24 24"><path d="M6 9l6 6 6-6" fill="none" stroke="black" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg>`)
	iconSend     = mustIcon(`<svg viewBox="0 0 24 24"><path d="M12 19V5" fill="none" stroke="black" stroke-width="2" stroke-linecap="round"/><path d="M6 11l6-6 6 6" fill="none" stroke="black" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg>`)
	iconStop     = mustIcon(`<svg viewBox="0 0 24 24"><rect x="7" y="7" width="10" height="10" rx="2" fill="black"/></svg>`)
	iconAlert    = mustIcon(`<svg viewBox="0 0 24 24"><circle cx="12" cy="12" r="9" fill="none" stroke="black" stroke-width="2"/><path d="M12 8v5" fill="none" stroke="black" stroke-width="2" stroke-linecap="round"/><circle cx="12" cy="16.2" r="1.1" fill="black"/></svg>`)
	iconInfo     = mustIcon(`<svg viewBox="0 0 24 24"><circle cx="12" cy="12" r="9" fill="none" stroke="black" stroke-width="2"/><path d="M12 11v5" fill="none" stroke="black" stroke-width="2" stroke-linecap="round"/><circle cx="12" cy="7.8" r="1.1" fill="black"/></svg>`)
)
