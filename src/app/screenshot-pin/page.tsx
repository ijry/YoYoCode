"use client"

// One pinned screenshot window (label `screenshot-pin-<id>`, created,
// positioned and shown by the Rust side, which also files the picture under
// the app data root's screenshots/pins/<id>.png). Reads the picture's path
// from the query string the window was opened with and pulls the bytes
// through the read command — the only door to that file, jail-checked
// against the pins directory on the Rust side. The page keeps no state
// beyond the picture: a click on it closes the pin, and dragging it moves
// the window. A pin that cannot load its picture has nothing to show and
// closes itself — best-effort, the Rust side may have cleaned up already.

import {
  Suspense,
  useCallback,
  useEffect,
  useRef,
  useState,
  type PointerEvent as ReactPointerEvent,
} from "react"
import { useSearchParams } from "next/navigation"

import { screenshotReadPinImage } from "@/lib/tauri"

/** A press that travels further than this many pixels is a drag, not a
 *  click: the window drag takes over there, and the release must not close
 *  what was being moved. */
const DRAG_THRESHOLD_PX = 4

function ScreenshotPinPageInner() {
  const searchParams = useSearchParams()
  const src = searchParams.get("src")
  const [pngBase64, setPngBase64] = useState<string | null>(null)
  // The press the drag grows out of: where it began and whether it already
  // became a window drag. Refs, not state — the handlers read them across
  // events and a re-render must not lose the gesture in progress.
  const pressRef = useRef<{
    x: number
    y: number
    dragged: boolean
  } | null>(null)

  const closeSelf = useCallback(async () => {
    try {
      const { getCurrentWindow } = await import("@tauri-apps/api/window")
      await getCurrentWindow().close()
    } catch {
      // Nothing left to try: the window is a Tauri webview by construction.
    }
  }, [])

  useEffect(() => {
    if (!src) {
      void closeSelf()
      return
    }
    let stale = false
    screenshotReadPinImage(src)
      .then((data) => {
        if (!stale) setPngBase64(data)
      })
      .catch(() => {
        if (stale) return
        void closeSelf()
      })
    return () => {
      stale = true
    }
  }, [src, closeSelf])

  // A pin is dragged by its picture and closed by a click on it. The drag
  // region on the root only answers clicks that land on the root itself —
  // the picture fills the window — and Tauri's own drag path never yields
  // the mouseup back, so a click on a drag region would never fire. The
  // press is tracked here instead: one that travels past the threshold
  // starts the native window drag (the pointer belongs to it from there),
  // one that does not stays a click and closes on release.
  const onPointerDown = useCallback((event: ReactPointerEvent) => {
    if (event.button !== 0) return
    pressRef.current = { x: event.clientX, y: event.clientY, dragged: false }
  }, [])

  const onPointerMove = useCallback((event: ReactPointerEvent) => {
    const press = pressRef.current
    if (!press || press.dragged) return
    if (
      Math.abs(event.clientX - press.x) < DRAG_THRESHOLD_PX &&
      Math.abs(event.clientY - press.y) < DRAG_THRESHOLD_PX
    ) {
      return
    }
    press.dragged = true
    import("@tauri-apps/api/window")
      .then(({ getCurrentWindow }) => void getCurrentWindow().startDragging())
      .catch(() => {
        // Nothing to drag with: the click that follows closes instead.
      })
  }, [])

  const onClick = useCallback(() => {
    // The release of a drag the native loop took over never reaches this
    // handler; only an undragged press closes.
    if (pressRef.current?.dragged) return
    void closeSelf()
  }, [closeSelf])

  return (
    <div
      data-tauri-drag-region
      className="h-screen w-screen cursor-default select-none overflow-hidden"
      onPointerDown={onPointerDown}
      onPointerMove={onPointerMove}
      onClick={onClick}
    >
      {pngBase64 ? (
        // eslint-disable-next-line @next/next/no-img-element
        <img
          src={`data:image/png;base64,${pngBase64}`}
          alt=""
          draggable={false}
          className="block h-full w-full"
        />
      ) : null}
    </div>
  )
}

export default function ScreenshotPinPage() {
  return (
    <Suspense fallback={null}>
      <ScreenshotPinPageInner />
    </Suspense>
  )
}
