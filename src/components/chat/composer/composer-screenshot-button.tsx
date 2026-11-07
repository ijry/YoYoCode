"use client"

import { useCallback, useEffect, useRef, useState } from "react"
import { useTranslations } from "next-intl"
import { Camera, Monitor } from "lucide-react"
import { toast } from "sonner"

import { ScreenshotEditorDialog } from "@/components/screenshot/screenshot-editor-dialog"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { isLocalDesktop, subscribe } from "@/lib/platform"
import type {
  CroppedPayload,
  ScreenshotCapturedEvent,
} from "@/lib/screenshot/types"
import { randomUUID } from "@/lib/utils"
import { screenshotBeginSelection, screenshotTakeCropped } from "@/lib/tauri"

/** The backend event fired once a selection has been cropped (metadata only —
 *  the picture itself is pulled with `screenshotTakeCropped`). */
const CAPTURED_EVENT = "screenshot://captured"

/** Hover this long before the menu opens on its own: a pass over the button
 *  on the way somewhere else must not flash a menu. A click opens it at once. */
const HOVER_OPEN_DELAY_MS = 200

export interface ComposerScreenshotButtonProps {
  disabled?: boolean
  /** Receives the annotated (or untouched) screenshot once confirmed. */
  onAttachImage: (file: File) => Promise<void>
}

/**
 * The composer's screenshot button, sitting right of the "+" menu — desktop
 * only, so web and remote-desktop windows render nothing here.
 *
 * Hovering opens a small menu with two ways to capture: with the window up,
 * or with the main window hidden first so it cannot photobomb the picture.
 * The selection happens in the backend's own overlay windows; this component
 * waits for the `screenshot://captured` event, pulls the cropped picture and
 * hands it to the annotation editor.
 */
export function ComposerScreenshotButton({
  disabled = false,
  onAttachImage,
}: ComposerScreenshotButtonProps) {
  const t = useTranslations("Folder.chat.messageInput")
  const te = useTranslations("Folder.chat.screenshotEditor")
  const [menuOpen, setMenuOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  const [editorOpen, setEditorOpen] = useState(false)
  const [payload, setPayload] = useState<CroppedPayload | null>(null)
  // The subscription lives for the component's whole life, but every capture
  // carries a fresh request id — the ref lets the handler check the latest
  // one without re-subscribing.
  const requestIdRef = useRef<string | null>(null)
  const hoverTimerRef = useRef<number | null>(null)

  const clearHoverTimer = useCallback(() => {
    if (hoverTimerRef.current !== null) {
      window.clearTimeout(hoverTimerRef.current)
      hoverTimerRef.current = null
    }
  }, [])

  useEffect(() => clearHoverTimer, [clearHoverTimer])

  const failureLabel = useCallback(
    (error: unknown) => {
      const message = error instanceof Error ? error.message : String(error)
      // macOS denies screen capture with a permission-shaped error (TCC);
      // name the missing grant instead of a generic failure.
      return /permission|权限|screen recording/i.test(message)
        ? te("permissionDenied")
        : te("failed")
    },
    [te]
  )

  const beginCapture = useCallback(
    async (hideMain: boolean) => {
      setMenuOpen(false)
      setBusy(true)
      const requestId = randomUUID()
      requestIdRef.current = requestId
      try {
        await screenshotBeginSelection(requestId, hideMain)
      } catch (error) {
        requestIdRef.current = null
        toast.error(failureLabel(error))
      } finally {
        setBusy(false)
      }
    },
    [failureLabel]
  )

  const handleCaptured = useCallback(
    (event: ScreenshotCapturedEvent) => {
      // Taking the crop releases the frame store entry — one read, no second.
      screenshotTakeCropped(event.captureId)
        .then((cropped) => {
          setPayload(cropped)
          setEditorOpen(true)
        })
        .catch((error) => {
          toast.error(failureLabel(error))
        })
    },
    [failureLabel]
  )

  useEffect(() => {
    if (!isLocalDesktop()) return
    let active = true
    let unlisten: (() => void) | null = null
    subscribe<ScreenshotCapturedEvent>(CAPTURED_EVENT, (event) => {
      // A completion this button never asked for — a capture from earlier
      // that outlived its welcome — is not ours to show.
      if (event.requestId !== requestIdRef.current) return
      requestIdRef.current = null
      handleCaptured(event)
    })
      .then((fn) => {
        if (active) unlisten = fn
        else fn()
      })
      .catch((error) => {
        console.error(
          "[ComposerScreenshotButton] failed to listen for captures:",
          error
        )
      })
    return () => {
      active = false
      unlisten?.()
    }
  }, [handleCaptured])

  const handleTriggerMouseEnter = useCallback(() => {
    if (disabled) return
    clearHoverTimer()
    hoverTimerRef.current = window.setTimeout(() => {
      hoverTimerRef.current = null
      setMenuOpen(true)
    }, HOVER_OPEN_DELAY_MS)
  }, [clearHoverTimer, disabled])

  const handleEditorConfirm = useCallback(
    (file: File) => {
      void onAttachImage(file)
        .then(() => setEditorOpen(false))
        .catch((error) => {
          console.error(
            "[ComposerScreenshotButton] attach screenshot failed:",
            error
          )
          // The editor stays open so the annotated picture is not lost —
          // the user can retry the attach or copy it out by hand.
          toast.error(te("failed"))
        })
    },
    [onAttachImage, te]
  )

  if (!isLocalDesktop()) return null

  return (
    <>
      <DropdownMenu open={menuOpen} onOpenChange={setMenuOpen}>
        <DropdownMenuTrigger asChild>
          <Button
            type="button"
            disabled={disabled || busy}
            variant="ghost"
            size="icon-xs"
            className="shrink-0 text-muted-foreground"
            title={t("screenshot")}
            aria-label={t("screenshot")}
            onMouseEnter={handleTriggerMouseEnter}
            onMouseLeave={clearHoverTimer}
            onClick={clearHoverTimer}
          >
            <Camera className="size-4" />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent side="top" align="start" className="min-w-44">
          <DropdownMenuItem
            disabled={disabled || busy}
            onClick={() => void beginCapture(false)}
          >
            <Camera className="size-4" />
            {t("screenshot")}
          </DropdownMenuItem>
          <DropdownMenuItem
            disabled={disabled || busy}
            onClick={() => void beginCapture(true)}
          >
            <Monitor className="size-4" />
            {t("screenshotHideWindow")}
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
      <ScreenshotEditorDialog
        open={editorOpen}
        onOpenChange={setEditorOpen}
        payload={payload}
        onConfirm={handleEditorConfirm}
      />
    </>
  )
}
