"use client"

// One monitor's screenshot overlay window (label `screenshot-overlay-<n>`,
// created, positioned and shown by the Rust side). Reads the capture from
// the query string the window was opened with, pulls that monitor's frozen
// frame and hands the window to the capture overlay, which cuts and
// annotates a selection in place. The page keeps no state of its own:
// confirming and cancelling go straight back to the Rust commands, which
// close this window themselves. The one thing the page closes is itself
// when the capture it was opened for is already gone — a stale overlay that
// lost a race — best-effort, since nothing else will.

import { Suspense, useCallback, useEffect, useState } from "react"
import { useSearchParams } from "next/navigation"

import { ScreenshotCaptureOverlay } from "@/components/screenshot/screenshot-capture-overlay"
import type {
  MonitorFramePayload,
  ScreenshotRect,
} from "@/lib/screenshot/types"
import {
  screenshotCancelSelection,
  screenshotGetFrame,
  screenshotFinishSelection,
} from "@/lib/tauri"

function ScreenshotOverlayPageInner() {
  const searchParams = useSearchParams()
  const captureId = searchParams.get("captureId")
  const monitorIndex = Number(searchParams.get("monitorIndex") ?? "0")
  const [frame, setFrame] = useState<MonitorFramePayload | null>(null)

  const closeSelf = useCallback(async () => {
    try {
      const { getCurrentWindow } = await import("@tauri-apps/api/window")
      await getCurrentWindow().close()
    } catch {
      // Nothing left to try: the window is a Tauri webview by construction.
    }
  }, [])

  useEffect(() => {
    if (!captureId) {
      void closeSelf()
      return
    }
    let stale = false
    screenshotGetFrame(captureId, monitorIndex)
      .then((payload) => {
        if (!stale) setFrame(payload)
      })
      .catch(() => {
        if (stale) return
        // Stale overlay: the capture was finished or cancelled out from
        // under this window. Release whatever is left of it and close —
        // both best-effort, the Rust side may have cleaned up already.
        screenshotCancelSelection(captureId).catch(() => {})
        void closeSelf()
      })
    return () => {
      stale = true
    }
  }, [captureId, monitorIndex, closeSelf])

  const handleConfirm = useCallback(
    (rect: ScreenshotRect, annotatedPngBase64: string | null) => {
      if (!captureId) return
      // The overlay awaits finish along with copy or pin, keeping the
      // controls locked and showing an inline error if this command fails.
      return screenshotFinishSelection(
        captureId,
        monitorIndex,
        rect,
        annotatedPngBase64 ?? undefined
      )
    },
    [captureId, monitorIndex]
  )

  const handleCancel = useCallback(() => {
    if (!captureId) return
    // Same story as confirm: the cancel command closes this window when it
    // actually cancelled, and is a no-op once the capture has finished.
    screenshotCancelSelection(captureId).catch(() => {})
  }, [captureId])

  if (!frame) return null
  return (
    <ScreenshotCaptureOverlay
      frame={frame}
      onConfirm={handleConfirm}
      onCancel={handleCancel}
    />
  )
}

export default function ScreenshotOverlayPage() {
  return (
    <Suspense fallback={null}>
      <ScreenshotOverlayPageInner />
    </Suspense>
  )
}
