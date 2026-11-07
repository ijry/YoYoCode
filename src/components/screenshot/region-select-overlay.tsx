"use client"

// The full-screen region picker a screenshot overlay window renders. The
// frozen frame fills the window and the person drags a rectangle over it;
// what leaves this component is that rectangle converted back into the
// frame's own pixels (overlay-geometry.ts), ready for the Rust side to
// crop. Pure interaction: it reads props and fires onConfirm/onCancel — no
// invokes, no capture state, so the drag behaves the same in a test as on
// a monitor.

import { useCallback, useEffect, useRef, useState } from "react"
import type { ReactNode } from "react"

import {
  cssRectToPhysical,
  selectionTooSmall,
  type CssRect,
} from "@/lib/screenshot/overlay-geometry"
import type {
  MonitorFramePayload,
  ScreenshotRect,
} from "@/lib/screenshot/types"

/** How far outside the selection the dimming reaches: one box-shadow paints
 *  everything but the dragged box, so the shade never has to be kept in
 *  step with a moving rectangle the way four divs would. */
const DIM_SHADOW = "0 0 0 100000px rgba(0, 0, 0, 0.4)"

/** Rough height of the size badge: a selection dragged to the top edge
 *  shows its badge below itself rather than clipped off-screen. */
const BADGE_CLEARANCE = 24

interface Point {
  x: number
  y: number
}

/** The box two corners make, whichever way the drag ran. */
function normalizeRect(from: Point, to: Point): CssRect {
  return {
    x: Math.min(from.x, to.x),
    y: Math.min(from.y, to.y),
    width: Math.abs(from.x - to.x),
    height: Math.abs(from.y - to.y),
  }
}

export function RegionSelectOverlay(props: {
  frame: MonitorFramePayload
  onConfirm: (rect: ScreenshotRect) => void
  onCancel: () => void
}): ReactNode {
  const { frame, onConfirm, onCancel } = props
  const [rect, setRect] = useState<CssRect | null>(null)
  // The drag lives in refs: the listeners below are window-level and read
  // it across events, and a re-render must never lose where a drag began.
  const dragStartRef = useRef<Point | null>(null)
  const priorRectRef = useRef<CssRect | null>(null)
  const rectRef = useRef<CssRect | null>(null)

  const confirmRect = useCallback(
    (selection: CssRect) => {
      onConfirm(
        cssRectToPhysical(
          selection,
          { width: window.innerWidth, height: window.innerHeight },
          { width: frame.width, height: frame.height }
        )
      )
    },
    [onConfirm, frame]
  )

  useEffect(() => {
    const setSelection = (next: CssRect | null) => {
      rectRef.current = next
      setRect(next)
    }

    const handlePointerDown = (event: PointerEvent) => {
      if (event.button !== 0) return
      // A new drag replaces the old selection — but remembers it, because a
      // press that never grows into a drag hands the old one back on
      // pointer-up. Without that, a double-click's second press would erase
      // the very rect its dblclick is about to confirm.
      priorRectRef.current = rectRef.current
      dragStartRef.current = { x: event.clientX, y: event.clientY }
      setSelection({
        x: event.clientX,
        y: event.clientY,
        width: 0,
        height: 0,
      })
    }

    const handlePointerMove = (event: PointerEvent) => {
      const start = dragStartRef.current
      if (!start) return
      setSelection(normalizeRect(start, { x: event.clientX, y: event.clientY }))
    }

    const handlePointerUp = () => {
      if (!dragStartRef.current) return
      dragStartRef.current = null
      const current = rectRef.current
      if (current && selectionTooSmall(current)) {
        setSelection(priorRectRef.current)
      }
    }

    const confirmIfBigEnough = () => {
      const current = rectRef.current
      if (current && !selectionTooSmall(current)) confirmRect(current)
    }

    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        onCancel()
        return
      }
      if (event.key === "Enter") confirmIfBigEnough()
    }

    window.addEventListener("pointerdown", handlePointerDown)
    window.addEventListener("pointermove", handlePointerMove)
    window.addEventListener("pointerup", handlePointerUp)
    window.addEventListener("dblclick", confirmIfBigEnough)
    window.addEventListener("keydown", handleKeyDown)
    return () => {
      window.removeEventListener("pointerdown", handlePointerDown)
      window.removeEventListener("pointermove", handlePointerMove)
      window.removeEventListener("pointerup", handlePointerUp)
      window.removeEventListener("dblclick", confirmIfBigEnough)
      window.removeEventListener("keydown", handleKeyDown)
    }
  }, [confirmRect, onCancel])

  const hasSelection = rect !== null && rect.width > 0 && rect.height > 0

  return (
    <div className="fixed inset-0 cursor-crosshair select-none overflow-hidden">
      {/* The frame is the whole monitor at its physical pixel size; the
          browser scales it to the (CSS-pixel) viewport, so a drag in CSS
          pixels maps back onto the picture exactly — see
          cssRectToPhysical. */}
      {/* eslint-disable-next-line @next/next/no-img-element */}
      <img
        src={`data:image/png;base64,${frame.pngBase64}`}
        alt=""
        draggable={false}
        className="absolute inset-0 h-full w-full"
      />
      {hasSelection && rect ? (
        <>
          <div
            className="absolute border border-white"
            style={{
              left: rect.x,
              top: rect.y,
              width: rect.width,
              height: rect.height,
              boxShadow: DIM_SHADOW,
            }}
          />
          {!selectionTooSmall(rect) && (
            <div
              className="absolute rounded-sm bg-black/70 px-1.5 py-0.5 text-xs leading-none text-white tabular-nums"
              style={{
                left: rect.x,
                top: rect.y >= BADGE_CLEARANCE ? rect.y - 22 : rect.y + 4,
              }}
            >
              {`${Math.round(rect.width)}×${Math.round(rect.height)}`}
            </div>
          )}
        </>
      ) : (
        <div className="absolute inset-0 bg-black/40" />
      )}
    </div>
  )
}
