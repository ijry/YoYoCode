"use client"

// The selection overlay window the desktop screenshot flow opens over every
// monitor (label `screenshot-overlay-<n>`, created, positioned and shown by
// the Rust side). Placeholder render for now — the region picker that reads
// the frozen frame and drags the selection replaces this.
export default function ScreenshotOverlayPage() {
  return <div>capturing…</div>
}
