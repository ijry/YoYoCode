// Turning a drag on a screenshot overlay window into a rectangle in the
// frozen frame's pixels. The overlay window is sized to its monitor in
// physical pixels, but a webview reports pointer positions in CSS pixels —
// on any display scaled away from 100% (Windows 125%, a 2× Mac) the two
// disagree by the device pixel ratio, and a selection that is not scaled
// back up crops the wrong region of the picture. Pure arithmetic on plain
// rectangles, so it can be tested without a window.

import type { ScreenshotRect } from "./types"

/** A selection in the overlay window's CSS pixels, as the pointer events
 *  report it — always a normalized box (positive width and height), never a
 *  start point plus a drag vector. */
export interface CssRect {
  x: number
  y: number
  width: number
  height: number
}

/** Scale a CSS-pixel selection into the frame's physical pixels: each axis
 *  by its own frame-to-viewport ratio, every value rounded to a whole pixel
 *  — the Rust side crops whole pixels, and a fraction would be truncated by
 *  an amount nobody chose. */
export function cssRectToPhysical(
  rect: CssRect,
  viewport: { width: number; height: number },
  frame: { width: number; height: number }
): ScreenshotRect {
  const scaleX = frame.width / viewport.width
  const scaleY = frame.height / viewport.height
  return {
    x: Math.round(rect.x * scaleX),
    y: Math.round(rect.y * scaleY),
    width: Math.round(rect.width * scaleX),
    height: Math.round(rect.height * scaleY),
  }
}

/** Whether a selection is too small to be one. The same rule the markup
 *  editor's markFromDrag applies: a drag must reach `minPx` in SOME
 *  direction — a long thin strip is a real selection (it is how a line of
 *  text gets framed), and only a drag short in every direction is a click.
 *  A zero side is never a selection, however long the other one is. */
export function selectionTooSmall(rect: CssRect, minPx = 8): boolean {
  return (
    rect.width === 0 ||
    rect.height === 0 ||
    Math.max(rect.width, rect.height) < minPx
  )
}
