"use client"

// Annotating the capture before it goes to the conversation: the same tools
// browser/screenshot-markup.ts gives a marked-up page — pen, shapes, arrows,
// text, mosaic over private regions, numbered steps — plus a crop to trim the
// picture to what matters. Marks live in the image's own pixels (see
// lib/screenshot/annotation.ts); this dialog is only the sheet they are drawn
// on: a preview scaled to fit, pointer events converted into image
// coordinates, and an undo stack of mark arrays.

import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type PointerEvent as ReactPointerEvent,
  type ReactNode,
} from "react"

import {
  Check,
  Circle,
  Crop,
  Eraser,
  Grid2x2,
  ListOrdered,
  Loader2,
  MousePointer2,
  MoveUpRight,
  Pencil,
  Redo2,
  Square,
  Type,
  Undo2,
  X,
} from "lucide-react"
import { useTranslations } from "next-intl"
import { toast } from "sonner"

import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import {
  ANNOTATION_COLORS,
  drawAnnotations,
  exportAnnotated,
  hitTestMark,
  marksOutside,
  mosaicBlockSize,
  shapeFromDrag,
  shiftMarks,
  type AnnotationMark,
  type AnnotationPoint,
  type AnnotationSize,
  type AnnotationTool,
} from "@/lib/screenshot/annotation"
import type { CroppedPayload } from "@/lib/screenshot/types"
import { cn, randomUUID } from "@/lib/utils"

/** A drag shorter than this many screen pixels, in every direction, is a
 *  click — not a mark. Carried into the picture's pixels by the preview
 *  scale, so the threshold means the same on a 4K capture as on a small
 *  one. */
const MIN_DRAG_SCREEN_PX = 8

/** Screen pixels of forgiveness around a mark when the select tool grabs
 *  it, before the preview scale turns them into image pixels for
 *  `hitTestMark`. */
const SELECT_SLACK_SCREEN_PX = 8

interface Bounds {
  x: number
  y: number
  width: number
  height: number
}

const TOOL_BUTTON = cn(
  "inline-flex h-full items-center gap-1.5 rounded-xl border border-transparent px-2.5 text-sm font-medium text-foreground/60 transition-all outline-none",
  "hover:text-foreground focus-visible:ring-[3px] focus-visible:ring-ring/50 [&_svg]:size-4 [&_svg]:shrink-0",
  "aria-pressed:bg-background aria-pressed:text-foreground dark:aria-pressed:border-input dark:aria-pressed:bg-input/30"
)

/** The picture pixelated for mosaic patches: scaled down to mosaic blocks
 *  and back up, so every block is one flat colour of what was under it. */
function buildMosaicSource(
  image: CanvasImageSource,
  size: AnnotationSize
): HTMLCanvasElement | null {
  const block = mosaicBlockSize(size)
  const small = document.createElement("canvas")
  small.width = Math.max(1, Math.floor(size.width / block))
  small.height = Math.max(1, Math.floor(size.height / block))
  const down = small.getContext("2d")
  if (!down) return null
  down.drawImage(image, 0, 0, small.width, small.height)
  const big = document.createElement("canvas")
  big.width = size.width
  big.height = size.height
  const up = big.getContext("2d")
  if (!up) return null
  up.imageSmoothingEnabled = false
  up.drawImage(small, 0, 0, big.width, big.height)
  return big
}

/** The crop rectangle a drag marks out, or null when the drag was too short
 *  to be one — the same threshold a mark drag answers to. */
function cropRectFromDrag(
  start: AnnotationPoint,
  end: AnnotationPoint,
  scale: number
): Bounds | null {
  const shape = shapeFromDrag("rect", start, end, MIN_DRAG_SCREEN_PX * scale)
  return shape && shape.kind === "rect"
    ? { x: shape.x, y: shape.y, width: shape.width, height: shape.height }
    : null
}

/** The mosaic patch a drag covers — a rect in everything but its colour:
 *  it pastes the pixelated picture over itself. */
function mosaicFromDrag(
  start: AnnotationPoint,
  end: AnnotationPoint,
  min: number
): AnnotationMark | null {
  const shape = shapeFromDrag("rect", start, end, min)
  return shape && shape.kind === "rect"
    ? {
        id: shape.id,
        kind: "mosaic",
        x: shape.x,
        y: shape.y,
        width: shape.width,
        height: shape.height,
      }
    : null
}

/** The capture as the frame store encoded it — the path taken when the
 *  picture could not be decoded for marking up: the same picture, with no
 *  marks, rather than a dead end. */
function payloadFile(payload: CroppedPayload): File | null {
  try {
    // `atob` reads the bytes as latin-1; each char code is one byte.
    const bytes = Uint8Array.from(atob(payload.pngBase64), (c) =>
      c.charCodeAt(0)
    )
    const extension = payload.mime === "image/jpeg" ? "jpg" : "png"
    return new File([bytes], `screenshot.${extension}`, {
      type: payload.mime || "image/png",
    })
  } catch {
    return null
  }
}

/** The annotated picture as an attachment, named for what it is. */
function annotatedFile(blob: Blob): File {
  const extension = blob.type === "image/jpeg" ? "jpg" : "png"
  return new File([blob], `screenshot.${extension}`, {
    type: blob.type || "image/png",
  })
}

export interface ScreenshotEditorDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  /** The cropped capture to annotate, pulled from the frame store. */
  payload: CroppedPayload | null
  /** Receives the annotated (or untouched) screenshot once confirmed. */
  onConfirm: (file: File) => void
}

/**
 * Mark up a captured screenshot before it is attached to a message.
 *
 * One sheet per capture, and a clean one: the sheet only exists while the
 * dialog is open, so every opening starts with no marks, no history and no
 * half-finished crop — even when the parent re-hands over the same capture.
 * Closing it by any route (Cancel, Escape, the close button) sends nothing;
 * the parent keeps the payload until the next capture replaces it.
 */
export function ScreenshotEditorDialog({
  open,
  payload,
  onOpenChange,
  onConfirm,
}: ScreenshotEditorDialogProps): ReactNode {
  if (!open || !payload) return null
  return (
    <EditorSheet
      payload={payload}
      onOpenChange={onOpenChange}
      onConfirm={onConfirm}
    />
  )
}

/** What a drag in progress is doing: drawing a mark, moving one, or
 *  marking out a crop. */
type Drag =
  | { mode: "draw"; pointerId: number; start: AnnotationPoint }
  | {
      mode: "move"
      pointerId: number
      markId: string
      start: AnnotationPoint
      original: AnnotationMark[]
    }
  | { mode: "crop"; pointerId: number; start: AnnotationPoint }

function EditorSheet({
  payload,
  onOpenChange,
  onConfirm,
}: {
  payload: CroppedPayload
  onOpenChange: (open: boolean) => void
  onConfirm: (file: File) => void
}): ReactNode {
  const t = useTranslations("Folder.chat.screenshotEditor")
  const canvasRef = useRef<HTMLCanvasElement | null>(null)
  // The decoded picture and the pixelated twin the mosaic patches paste
  // from, held together because they are made together — on load, and on an
  // applied crop.
  const [picture, setPicture] = useState<{
    source: CanvasImageSource
    mosaic: HTMLCanvasElement | null
  } | null>(null)
  // The payload's numbers seed the layout; the decoded image is the
  // authority and replaces them the moment it arrives.
  const [size, setSize] = useState<AnnotationSize>({
    width: payload.width,
    height: payload.height,
  })
  const [tool, setTool] = useState<AnnotationTool>("rect")
  const [color, setColor] = useState<string>(ANNOTATION_COLORS[0])
  const [marks, setMarks] = useState<AnnotationMark[]>([])
  // Every state the marks have been in: the sheet as it was before each
  // change, and the ones undone on the way back forward.
  const [undoStack, setUndoStack] = useState<AnnotationMark[][]>([])
  const [redoStack, setRedoStack] = useState<AnnotationMark[][]>([])
  const [draft, setDraft] = useState<AnnotationMark | null>(null)
  const [cropMode, setCropMode] = useState(false)
  const [cropRect, setCropRect] = useState<Bounds | null>(null)
  const [textInput, setTextInput] = useState<{
    x: number
    y: number
    value: string
  } | null>(null)
  const [stepSeq, setStepSeq] = useState(1)
  const [exporting, setExporting] = useState(false)
  const dragRef = useRef<Drag | null>(null)
  // The picture is made asynchronously, and the sheet can be let go while it
  // encodes — Escape, the close button, Cancel. One that finishes after that
  // must hand over nothing.
  const liveRef = useRef(true)
  useEffect(() => {
    liveRef.current = true
    return () => {
      liveRef.current = false
    }
  }, [])

  const pushHistory = useCallback((previous: AnnotationMark[]) => {
    setUndoStack((stack) => [...stack, previous])
    setRedoStack([])
  }, [])

  // The picture, decoded from the frame store's PNG, and its mosaic twin
  // pixelated from it the moment it arrives.
  useEffect(() => {
    let live = true
    const element = new Image()
    element.onload = () => {
      if (!live) return
      const natural = {
        width: element.naturalWidth,
        height: element.naturalHeight,
      }
      setSize(natural)
      setPicture({
        source: element,
        mosaic: buildMosaicSource(element, natural),
      })
    }
    element.onerror = () => {
      // Nothing to draw on. The sheet stays up, and Confirm hands over the
      // capture exactly as it arrived (see `handleConfirm`).
      if (live) setPicture(null)
    }
    element.src = `data:${payload.mime || "image/png"};base64,${payload.pngBase64}`
    return () => {
      live = false
      element.onload = null
      element.onerror = null
    }
  }, [payload])

  // One renderer for the preview: the picture, the marks (with the one
  // still being drawn), and — while cropping — the dimmed picture and the
  // rectangle being dragged out.
  useEffect(() => {
    const canvas = canvasRef.current
    if (!canvas || !picture) return
    const ctx = canvas.getContext("2d")
    if (!ctx) return
    drawAnnotations(
      ctx,
      picture.source,
      picture.mosaic,
      draft ? [...marks, draft] : marks,
      size
    )
    if (!cropMode) return
    ctx.fillStyle = "rgba(0, 0, 0, 0.45)"
    ctx.fillRect(0, 0, size.width, size.height)
    if (cropRect) {
      ctx.strokeStyle = "#ffffff"
      ctx.lineWidth = Math.max(1, size.width / 400)
      ctx.setLineDash([8, 6])
      ctx.strokeRect(cropRect.x, cropRect.y, cropRect.width, cropRect.height)
      ctx.setLineDash([])
    }
  }, [picture, marks, draft, size, cropMode, cropRect])

  /** A pointer position in the picture's own pixels, held to the picture's
   *  edges, plus how many picture pixels one screen pixel is — the factor
   *  that carries the drag threshold and the select slack across the
   *  preview's scaling. */
  const pointOf = useCallback(
    (event: ReactPointerEvent<HTMLDivElement>) => {
      const canvas = canvasRef.current
      if (!canvas) return null
      const rect = canvas.getBoundingClientRect()
      if (rect.width <= 0 || rect.height <= 0) return null
      const x = ((event.clientX - rect.left) / rect.width) * size.width
      const y = ((event.clientY - rect.top) / rect.height) * size.height
      return {
        point: {
          x: Math.max(0, Math.min(size.width, x)),
          y: Math.max(0, Math.min(size.height, y)),
        },
        scale: size.width / rect.width,
      }
    },
    [size]
  )

  const undo = useCallback(() => {
    if (undoStack.length === 0) return
    const previous = undoStack[undoStack.length - 1]
    setUndoStack(undoStack.slice(0, -1))
    setRedoStack([marks, ...redoStack])
    setMarks(previous)
  }, [undoStack, redoStack, marks])

  const redo = useCallback(() => {
    if (redoStack.length === 0) return
    const [next, ...rest] = redoStack
    setRedoStack(rest)
    setUndoStack([...undoStack, marks])
    setMarks(next)
  }, [redoStack, undoStack, marks])

  const clear = useCallback(() => {
    if (marks.length === 0) return
    // Clearing is one more state, so it is as undoable as a stroke is.
    pushHistory(marks)
    setMarks([])
  }, [marks, pushHistory])

  /** Picking a tool leaves the crop and any label being written behind —
   *  the tools are modes, and half of one must not survive into another. */
  const pickTool = useCallback((next: AnnotationTool) => {
    setTool(next)
    setTextInput(null)
    setCropMode(false)
    setCropRect(null)
  }, [])

  const enterCrop = useCallback(() => {
    setTextInput(null)
    setCropMode(true)
    setCropRect(null)
  }, [])

  const exitCrop = useCallback(() => {
    setCropMode(false)
    setCropRect(null)
  }, [])

  /** Apply the dragged-out crop: the picture is re-cut on an offscreen
   *  canvas (its pixelated twin rebuilt from the cut), and the marks move
   *  back by where the crop begins, dropping the ones left with nothing to
   *  show. One history entry — the marks as they were before the cut. */
  const applyCrop = useCallback(() => {
    if (!cropRect || !picture) return
    const x = Math.max(0, Math.round(cropRect.x))
    const y = Math.max(0, Math.round(cropRect.y))
    const width = Math.max(
      1,
      Math.min(Math.round(cropRect.width), Math.round(size.width) - x)
    )
    const height = Math.max(
      1,
      Math.min(Math.round(cropRect.height), Math.round(size.height) - y)
    )
    const cropped = document.createElement("canvas")
    cropped.width = width
    cropped.height = height
    const ctx = cropped.getContext("2d")
    if (!ctx) {
      exitCrop()
      return
    }
    ctx.drawImage(picture.source, x, y, width, height, 0, 0, width, height)
    const nextSize = { width, height }
    const shifted = shiftMarks(marks, -x, -y)
    // marksOutside names the marks the cut would leave with nothing to
    // show; those are the ones that go.
    const lost = new Set(marksOutside(shifted, nextSize).map((mark) => mark.id))
    pushHistory(marks)
    setPicture({
      source: cropped,
      mosaic: buildMosaicSource(cropped, nextSize),
    })
    setSize(nextSize)
    setMarks(shifted.filter((mark) => !lost.has(mark.id)))
    exitCrop()
  }, [cropRect, picture, size, marks, pushHistory, exitCrop])

  const commitText = useCallback(() => {
    const at = textInput
    setTextInput(null)
    if (!at) return
    // Nothing typed, nothing placed.
    const text = at.value.trim()
    if (!text) return
    pushHistory(marks)
    setMarks((current) => [
      ...current,
      { id: randomUUID(), kind: "text", color, x: at.x, y: at.y, text },
    ])
  }, [textInput, marks, color, pushHistory])

  const onPointerDown = (event: ReactPointerEvent<HTMLDivElement>) => {
    if (event.button !== 0 || exporting) return
    const at = pointOf(event)
    if (!at) return
    event.preventDefault()
    // Keep the drag when the pointer leaves the picture: without the
    // capture, a mark dragged past the edge would stop following at it.
    event.currentTarget.setPointerCapture?.(event.pointerId)

    if (cropMode) {
      dragRef.current = {
        mode: "crop",
        pointerId: event.pointerId,
        start: at.point,
      }
      setCropRect(null)
      return
    }
    if (tool === "select") {
      // The topmost mark under the pointer wins; empty space takes nothing.
      const slack = SELECT_SLACK_SCREEN_PX * at.scale
      for (let i = marks.length - 1; i >= 0; i--) {
        if (hitTestMark(marks[i], at.point, slack)) {
          dragRef.current = {
            mode: "move",
            pointerId: event.pointerId,
            markId: marks[i].id,
            start: at.point,
            original: marks,
          }
          return
        }
      }
      return
    }
    if (tool === "text") {
      setTextInput({ x: at.point.x, y: at.point.y, value: "" })
      return
    }
    if (tool === "step") {
      pushHistory(marks)
      const seq = stepSeq
      setMarks((current) => [
        ...current,
        {
          id: randomUUID(),
          kind: "step",
          color,
          x: at.point.x,
          y: at.point.y,
          seq,
        },
      ])
      setStepSeq(seq + 1)
      return
    }
    dragRef.current = {
      mode: "draw",
      pointerId: event.pointerId,
      start: at.point,
    }
    // A pen starts as a dot and grows with the pointer; the drag shapes
    // stay empty until there is one worth showing.
    setDraft(
      tool === "pen"
        ? { id: randomUUID(), kind: "pen", color, points: [at.point] }
        : null
    )
  }

  const onPointerMove = (event: ReactPointerEvent<HTMLDivElement>) => {
    const drag = dragRef.current
    if (!drag || drag.pointerId !== event.pointerId) return
    const at = pointOf(event)
    if (!at) return
    if (drag.mode === "draw") {
      const min = MIN_DRAG_SCREEN_PX * at.scale
      if (tool === "pen") {
        setDraft((current) =>
          current && current.kind === "pen"
            ? { ...current, points: [...current.points, at.point] }
            : current
        )
      } else if (tool === "mosaic") {
        setDraft(mosaicFromDrag(drag.start, at.point, min))
      } else if (tool === "rect" || tool === "ellipse" || tool === "arrow") {
        setDraft(shapeFromDrag(tool, drag.start, at.point, min))
      }
      return
    }
    if (drag.mode === "move") {
      const dx = at.point.x - drag.start.x
      const dy = at.point.y - drag.start.y
      // Only the grabbed mark moves, and from where it was when the drag
      // began — so a wobbling pointer does not accumulate wobble.
      setMarks(
        drag.original.map((mark) =>
          mark.id === drag.markId ? shiftMarks([mark], dx, dy)[0] : mark
        )
      )
      return
    }
    setCropRect(cropRectFromDrag(drag.start, at.point, at.scale))
  }

  const onPointerUp = (event: ReactPointerEvent<HTMLDivElement>) => {
    const drag = dragRef.current
    if (!drag || drag.pointerId !== event.pointerId) return
    dragRef.current = null
    if (drag.mode === "move") {
      const at = pointOf(event)
      const moved =
        at && (at.point.x !== drag.start.x || at.point.y !== drag.start.y)
      // One entry per drag: the marks as they were before it began. A click
      // on a mark that moves nothing is not history.
      if (moved) {
        setUndoStack((stack) => [...stack, drag.original])
        setRedoStack([])
      }
      return
    }
    if (drag.mode === "crop") {
      const at = pointOf(event)
      if (at) setCropRect(cropRectFromDrag(drag.start, at.point, at.scale))
      return
    }
    const committed = draft
    setDraft(null)
    if (!committed) return
    pushHistory(marks)
    setMarks((current) => [...current, committed])
  }

  /** Drop the drag in progress — a cancelled pointer, a lost capture, or
   *  Escape. A mark being drawn never existed; a mark being moved goes
   *  back where it was; a crop being dragged out is forgotten. */
  const cancelDrag = useCallback(() => {
    const drag = dragRef.current
    dragRef.current = null
    if (!drag) return
    if (drag.mode === "draw") setDraft(null)
    if (drag.mode === "move") setMarks(drag.original)
    if (drag.mode === "crop") setCropRect(null)
  }, [])

  // Only the pointer that is drawing can call its drag off: another one
  // being cancelled or let go of says nothing about it.
  const cancelDragOf = (event: ReactPointerEvent<HTMLDivElement>) => {
    if (dragRef.current?.pointerId === event.pointerId) cancelDrag()
  }

  const onEscapeKeyDown = (event: KeyboardEvent) => {
    // Escape peels one layer at a time: the drag in progress, the label
    // being written, the crop mode — and only then the sheet.
    if (dragRef.current) {
      event.preventDefault()
      cancelDrag()
      return
    }
    if (textInput) {
      event.preventDefault()
      setTextInput(null)
      return
    }
    if (cropMode) {
      event.preventDefault()
      exitCrop()
    }
  }

  const handleConfirm = useCallback(() => {
    if (exporting) return
    // The picture never decoded: hand over the capture exactly as the frame
    // store encoded it — the same picture with no marks — rather than
    // dead-ending.
    if (!picture) {
      const file = payloadFile(payload)
      if (file) onConfirm(file)
      else toast.error(t("failed"))
      return
    }
    setExporting(true)
    exportAnnotated(picture.source, picture.mosaic, marks, size)
      .then((blob) => {
        if (!liveRef.current) return
        onConfirm(annotatedFile(blob))
      })
      .catch(() => {
        if (!liveRef.current) return
        // The sheet stays up with everything on it: the person can try
        // again, or let the capture go as it arrived.
        toast.error(t("failed"))
      })
      .finally(() => {
        if (liveRef.current) setExporting(false)
      })
  }, [exporting, picture, marks, size, payload, onConfirm, t])

  const tools: Array<{ kind: AnnotationTool; label: string; icon: ReactNode }> =
    [
      { kind: "select", label: t("select"), icon: <MousePointer2 /> },
      { kind: "pen", label: t("pen"), icon: <Pencil /> },
      { kind: "rect", label: t("rect"), icon: <Square /> },
      { kind: "ellipse", label: t("ellipse"), icon: <Circle /> },
      { kind: "arrow", label: t("arrow"), icon: <MoveUpRight /> },
      { kind: "text", label: t("text"), icon: <Type /> },
      { kind: "mosaic", label: t("mosaic"), icon: <Grid2x2 /> },
      { kind: "step", label: t("step"), icon: <ListOrdered /> },
    ]

  return (
    <Dialog open onOpenChange={onOpenChange}>
      <DialogContent
        className="max-w-[min(1100px,calc(100vw-2rem))] gap-4 p-5"
        aria-describedby={undefined}
        onOpenAutoFocus={(event) => {
          // Onto the sheet itself rather than the first tool: a focus ring
          // on "Select" beside a pressed "Rectangle" reads as two tools
          // chosen at once. Escape reaches it here, and Tab still walks in.
          event.preventDefault()
          if (event.target instanceof HTMLElement) event.target.focus()
        }}
        onEscapeKeyDown={onEscapeKeyDown}
        onPointerDownOutside={(event) => {
          // A stray click beside the picture must not throw the marks away.
          if (marks.length > 0) event.preventDefault()
        }}
      >
        <DialogHeader>
          <DialogTitle>{t("title")}</DialogTitle>
        </DialogHeader>
        <div className="flex flex-wrap items-center gap-2">
          <div
            role="group"
            aria-label={t("title")}
            className="inline-flex h-9 flex-wrap items-center rounded-4xl bg-muted p-[3px]"
          >
            {tools.map((entry) => (
              <button
                key={entry.kind}
                type="button"
                className={TOOL_BUTTON}
                aria-pressed={tool === entry.kind && !cropMode}
                onClick={() => pickTool(entry.kind)}
              >
                {entry.icon}
                {entry.label}
              </button>
            ))}
          </div>
          <div
            role="group"
            aria-label={t("color")}
            className="flex items-center gap-1"
          >
            {ANNOTATION_COLORS.map((swatch) => (
              <button
                key={swatch}
                type="button"
                aria-label={swatch}
                title={swatch}
                aria-pressed={color === swatch}
                className={cn(
                  "size-5 rounded-full border border-border outline-none transition-all",
                  "focus-visible:ring-[3px] focus-visible:ring-ring/50",
                  "aria-pressed:ring-2 aria-pressed:ring-ring aria-pressed:ring-offset-1"
                )}
                style={{ backgroundColor: swatch }}
                onClick={() => setColor(swatch)}
              />
            ))}
          </div>
          <div className="ms-auto flex items-center gap-1">
            {cropMode ? (
              <>
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  onClick={applyCrop}
                  disabled={!cropRect}
                >
                  <Check />
                  {t("applyCrop")}
                </Button>
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  onClick={exitCrop}
                >
                  <X />
                  {t("cancelCrop")}
                </Button>
              </>
            ) : (
              <Button
                type="button"
                variant="ghost"
                size="sm"
                onClick={enterCrop}
              >
                <Crop />
                {t("crop")}
              </Button>
            )}
            <Button
              type="button"
              variant="ghost"
              size="sm"
              onClick={undo}
              disabled={undoStack.length === 0 || exporting}
            >
              <Undo2 />
              {t("undo")}
            </Button>
            <Button
              type="button"
              variant="ghost"
              size="sm"
              onClick={redo}
              disabled={redoStack.length === 0 || exporting}
            >
              <Redo2 />
              {t("redo")}
            </Button>
            <Button
              type="button"
              variant="ghost"
              size="sm"
              onClick={clear}
              disabled={marks.length === 0 || exporting}
            >
              <Eraser />
              {t("clear")}
            </Button>
          </div>
        </div>
        {/* The whole well is the drawing surface, not just the picture in it:
            a mark at the picture's edge is easiest started just outside it. */}
        <div
          className="relative flex min-h-0 min-w-0 cursor-crosshair touch-none items-center justify-center rounded-2xl bg-muted/40 p-2 select-none"
          onPointerDown={onPointerDown}
          onPointerMove={onPointerMove}
          onPointerUp={onPointerUp}
          onPointerCancel={cancelDragOf}
          onLostPointerCapture={cancelDragOf}
        >
          <canvas
            ref={canvasRef}
            width={size.width}
            height={size.height}
            role="img"
            aria-label={t("title")}
            // The canvas holds the picture at its own resolution and is
            // scaled down to fit — never the other way round — so the file
            // carries at least what the preview shows, on any display
            // density.
            className="block h-auto max-h-[70vh] w-auto max-w-[70vw] rounded-lg shadow-sm ring-1 ring-border"
          />
          {textInput ? (
            <input
              autoFocus
              value={textInput.value}
              placeholder={t("textPlaceholder")}
              onChange={(event) =>
                setTextInput({ ...textInput, value: event.target.value })
              }
              // The well beneath must not read a press on the label as a
              // new anchor.
              onPointerDown={(event) => event.stopPropagation()}
              onKeyDown={(event) => {
                if (event.key === "Enter") {
                  event.preventDefault()
                  commitText()
                } else if (event.key === "Escape") {
                  event.preventDefault()
                  setTextInput(null)
                }
              }}
              onBlur={() => setTextInput(null)}
              className="absolute min-w-24 -translate-y-1/2 rounded-md bg-background/95 px-2 py-1 text-sm shadow-md ring-1 ring-border outline-none"
              style={{
                left: `${(textInput.x / size.width) * 100}%`,
                top: `${(textInput.y / size.height) * 100}%`,
              }}
            />
          ) : null}
        </div>
        <DialogFooter className="items-center sm:justify-between">
          <p className="text-xs text-muted-foreground">
            {size.width}×{size.height}
          </p>
          <div className="flex flex-col-reverse gap-2 sm:flex-row">
            <Button
              type="button"
              variant="outline"
              onClick={() => onOpenChange(false)}
            >
              {t("cancel")}
            </Button>
            <Button type="button" onClick={handleConfirm} disabled={exporting}>
              {exporting ? <Loader2 className="animate-spin" /> : null}
              {t("confirm")}
            </Button>
          </div>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
