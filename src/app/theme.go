package app

// yoyocodeTheme mirrors upstream's default (zinc / neutral) design tokens
// from src/app/globals.css: a white canvas, off-white panels, light-gray
// borders, near-black text and a dark "primary" accent. These are functional
// color tokens, applied to mygo's Theme so the native UI reads like upstream.
import "github.com/egoist/mygo/ui"

var themeLight = &ui.Theme{
	Dark:           false,
	Background:     ui.Hex("#ffffff"),
	Surface:        ui.Hex("#fafafa"),
	SurfaceHover:   ui.Hex("#f1f1f3"),
	SurfacePressed: ui.Hex("#e4e4e7"),
	Border:         ui.Hex("#e4e4e7"),
	Text:           ui.Hex("#18181b"),
	TextMuted:      ui.Hex("#71717a"),
	Accent:         ui.Hex("#18181b"),
	AccentHover:    ui.Hex("#27272a"),
	AccentPressed:  ui.Hex("#3f3f46"),
	AccentText:     ui.Hex("#fafafa"),
	Danger:         ui.Hex("#ef4444"),
	Warning:        ui.Hex("#f59e0b"),
	Success:        ui.Hex("#16a34a"),
	Selection:      ui.Hex("#e4e4e7"),
	Focus:          ui.Hex("#a1a1aa"),
	Scrollbar:      ui.Hex("#d4d4d8"),
	Radius:         8,
	Spacing:        4,
	FontSize:       14,
}

// themeDark mirrors upstream's neutral dark tokens (globals.css
// [data-theme="neutral"].dark): a near-black canvas, dark-gray panels, a light
// "primary" with dark text, and off-white body text. Grayscale only — the same
// functional role mapping as themeLight, applied when appearance mode is dark.
var themeDark = &ui.Theme{
	Dark:           true,
	Background:     ui.Hex("#18181b"),
	Surface:        ui.Hex("#1f1f22"),
	SurfaceHover:   ui.Hex("#27272a"),
	SurfacePressed: ui.Hex("#3f3f46"),
	Border:         ui.Hex("#3f3f46"),
	Text:           ui.Hex("#fafafa"),
	TextMuted:      ui.Hex("#a1a1aa"),
	Accent:         ui.Hex("#e4e4e7"),
	AccentHover:    ui.Hex("#d4d4d8"),
	AccentPressed:  ui.Hex("#a1a1aa"),
	AccentText:     ui.Hex("#18181b"),
	Danger:         ui.Hex("#f87171"),
	Warning:        ui.Hex("#fbbf24"),
	Success:        ui.Hex("#4ade80"),
	Selection:      ui.Hex("#3f3f46"),
	Focus:          ui.Hex("#71717a"),
	Scrollbar:      ui.Hex("#52525b"),
	Radius:         8,
	Spacing:        4,
	FontSize:       14,
}

// resolveTheme picks the Theme for an appearance mode ("light" | "dark" |
// "system"). The native host has no in-process OS-appearance probe, so
// "system" resolves to light (upstream's documented fallback when unknown).
func resolveTheme(mode string) *ui.Theme {
	if mode == "dark" {
		return themeDark
	}
	return themeLight
}
