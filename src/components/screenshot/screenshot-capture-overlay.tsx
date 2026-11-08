"use client"

// The full-screen capture overlay a screenshot overlay window renders, the
// whole flow on one surface: the frozen frame fills the window, the first
// drag cuts a rectangle, and a toolbar docks to its corner — annotate right
// there (pen, shapes, arrows, text, mosaic, numbered steps), re-drag the
// dimmed area to re-cut, then confirm hands the finished picture over. Marks
// live in the frame's own pixels (see lib/screenshot/annotation.ts); the
// selection lives in CSS pixels and converts on the way out
// (overlay-geometry.ts). Pure interaction: it reads props and fires
// onConfirm/onCancel — no invokes, no capture state, so the flow behaves the
// same in a test as on a monitor.

import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type PointerEvent as ReactPointerEvent,
  type ReactNode,
} from "react"

import {
  Check,
  Circle,
  Grid2x2,
  ListOrdered,
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

import {
  ANNOTATION_COLORS,
  drawAnnotations,
  hitTestMark,
  mosaicBlockSize,
  shapeFromDrag,
  shiftMarks,
  type AnnotationMark,
  type AnnotationPoint,
  type AnnotationSize,
  type AnnotationTool,
} from "@/lib/screenshot/annotation"
import {
  cssRectToPhysical,
  selectionTooSmall,
  type CssRect,
} from "@/lib/screenshot/overlay-geometry"
import type {
  MonitorFramePayload,
  ScreenshotRect,
} from "@/lib/screenshot/types"
import { cn, randomUUID } from "@/lib/utils"

/** How far outside the selection the dimming reaches: one box-shadow paints
 *  everything but the dragged box, so the shade never has to be kept in
 *  step with a moving rectangle the way four divs would. */
const DIM_SHADOW = "0 0 0 100000px rgba(0, 0, 0, 0.4)"

/** Rough height of the size badge: a selection dragged to the top edge
 *  shows its badge below itself rather than clipped off-screen. */
const BADGE_CLEARANCE = 24

/** A drag shorter than this many screen pixels, in every direction, is a
 *  click — not a mark. Carried into the frame's pixels by the screen-to-
 *  image scale, so the threshold means the same on a 4K capture as on a
 *  small one. */
const MIN_DRAG_SCREEN_PX = 8

/** Screen pixels of forgiveness around a mark when the select tool grabs
 *  it, before the scale turns them into frame pixels for `hitTestMark`. */
const SELECT_SLACK_SCREEN_PX = 8

/** Clearance between the selection's corner and the toolbar docked there. */
const TOOLBAR_GAP = 8

/** Closest the toolbar may come to the viewport edge, on either axis. */
const TOOLBAR_EDGE = 4

interface ScreenPoint {
  x: number
  y: number
}

interface ScreenBox {
  left: number
  top: number
  width: number
  height: number
}

/** The box two corners make, whichever way the drag ran. */
function normalizeRect(from: ScreenPoint, to: ScreenPoint): CssRect {
  return {
    x: Math.min(from.x, to.x),
    y: Math.min(from.y, to.y),
    width: Math.abs(from.x - to.x),
    height: Math.abs(from.y - to.y),
  }
}

/** The frame pixelated for mosaic patches: scaled down to mosaic blocks
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

/** The mosaic patch a drag covers — a rect in everything but its colour:
 *  it pastes the pixelated frame over itself. */
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

/** The annotated frame cropped to the physical selection, as base64 PNG —
 *  the exact pixels Rust pins against the rect. The composite draws at the
 *  frame's own size first (the same call the preview paints), then copies
 *  onto a canvas of exactly the selection's physical size, offset by where
 *  the selection begins. */
async function exportAnnotatedCrop(
  picture: { source: CanvasImageSource; mosaic: HTMLCanvasElement | null },
  marks: readonly AnnotationMark[],
  frameSize: AnnotationSize,
  physical: ScreenshotRect
): Promise<string> {
  const composite = document.createElement("canvas")
  composite.width = frameSize.width
  composite.height = frameSize.height
  const ctx = composite.getContext("2d")
  if (!ctx) throw new Error("the capture could not be drawn on")
  drawAnnotations(ctx, picture.source, picture.mosaic, marks, frameSize)
  const crop = document.createElement("canvas")
  crop.width = physical.width
  crop.height = physical.height
  const cropCtx = crop.getContext("2d")
  if (!cropCtx) throw new Error("the capture could not be cropped")
  cropCtx.drawImage(composite, -physical.x, -physical.y)
  const blob = await new Promise<Blob>((resolve, reject) => {
    crop.toBlob((encoded) => {
      if (encoded) resolve(encoded)
      else reject(new Error("the annotated capture could not be encoded"))
    }, "image/png")
  })
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onerror = () =>
      reject(new Error("the annotated capture could not be read"))
    reader.onload = () => {
      if (typeof reader.result !== "string") {
        reject(new Error("the annotated capture could not be read"))
        return
      }
      resolve(reader.result.slice(reader.result.indexOf(",") + 1))
    }
    reader.readAsDataURL(blob)
  })
}

const TOOL_BUTTON = cn(
  "flex size-7 items-center justify-center rounded-md text-white/70 outline-none transition-colors",
  "hover:bg-white/10 hover:text-white focus-visible:ring-2 focus-visible:ring-white/40",
  "aria-pressed:bg-white/15 aria-pressed:text-white",
  "[&_svg]:size-4 [&_svg]:shrink-0"
)

const ROUND_BUTTON = cn(
  "flex size-7 items-center justify-center rounded-md text-white outline-none transition-colors",
  "focus-visible:ring-2 focus-visible:ring-white/40 disabled:opacity-40",
  "[&_svg]:size-4 [&_svg]:shrink-0"
)

/** What a drag in progress is doing: cutting a selection, drawing a mark,
 *  or moving one. */
type Drag =
  | { mode: "selection"; pointerId: number; start: ScreenPoint }
  | { mode: "draw"; pointerId: number; start: AnnotationPoint }
  | {
      mode: "move"
      pointerId: number
      markId: string
      start: AnnotationPoint
      original: AnnotationMark[]
    }

export interface ScreenshotCaptureOverlayProps {
  frame: MonitorFramePayload
  /** Receives the confirmed selection in the frame's physical pixels and,
   *  when anything was drawn, the annotated picture as base64 PNG. Null
   *  hands the bare rect over: Rust crops the raw frozen frame instead. */
  onConfirm: (rect: ScreenshotRect, annotatedPngBase64: string | null) => void
  onCancel: () => void
}

/**
 * Cut a region out of the frozen frame and annotate it in place.
 *
 * The first drag cuts a selection and the toolbar docks to its corner; a
 * drag on the dimmed area (or a select-tool drag that misses every mark)
 * re-cuts it, keeping the marks — they live in frame pixels, and the export
 * crops whatever the last selection covers. Escape cancels the whole
 * capture; the check mark exports the composite.
 */
export function ScreenshotCaptureOverlay({
  frame,
  onConfirm,
  onCancel,
}: ScreenshotCaptureOverlayProps): ReactNode {
  const t = useTranslations("Folder.chat.screenshotEditor")
  const canvasRef = useRef<HTMLCanvasElement | null>(null)

  const frameSize = useMemo<AnnotationSize>(
    () => ({ width: frame.width, height: frame.height }),
    [frame]
  )
  const [rect, setRect] = useState<CssRect | null>(null)
  const [selecting, setSelecting] = useState(false)
  const [tool, setTool] = useState<AnnotationTool>("select")
  const [color, setColor] = useState<string>(ANNOTATION_COLORS[0])
  const [marks, setMarks] = useState<AnnotationMark[]>([])
  // Every state the marks have been in: the canvas as it was before each
  // change, and the ones undone on the way back forward.
  const [undoStack, setUndoStack] = useState<AnnotationMark[][]>([])
  const [redoStack, setRedoStack] = useState<AnnotationMark[][]>([])
  const [draft, setDraft] = useState<AnnotationMark | null>(null)
  const [textInput, setTextInput] = useState<{
    x: number
    y: number
    value: string
  } | null>(null)
  const [stepSeq, setStepSeq] = useState(1)
  const [exporting, setExporting] = useState(false)
  // The decoded frame and the pixelated twin the mosaic patches paste from,
  // held together because they are made together.
  const [picture, setPicture] = useState<{
    source: CanvasImageSource
    mosaic: HTMLCanvasElement | null
  } | null>(null)
  // Where the canvas sits on screen, measured off the element once: the
  // toolbar docks against these viewport bounds, which never change while
  // the overlay window is up.
  const [canvasBox, setCanvasBox] = useState<ScreenBox>({
    left: 0,
    top: 0,
    width: 0,
    height: 0,
  })
  const [toolbarSize, setToolbarSize] = useState({ width: 0, height: 0 })
  // The toolbar element itself, handed over by a callback ref so its size
  // can be measured the moment it mounts (see the layout effect below).
  const [toolbar, setToolbar] = useState<HTMLDivElement | null>(null)

  // The drag lives in refs: the handlers below read them across events, and
  // a re-render must never lose where a drag began or what it replaced.
  const dragRef = useRef<Drag | null>(null)
  const priorRectRef = useRef<CssRect | null>(null)
  const rectRef = useRef<CssRect | null>(null)
  // The frame is decoded and the export encoded asynchronously, and the
  // overlay can be torn down in between — the Rust side closes the window
  // once a confirm lands. One that finishes after that hands over nothing.
  const liveRef = useRef(true)
  useEffect(() => {
    liveRef.current = true
    return () => {
      liveRef.current = false
    }
  }, [])

  const setSelection = useCallback((next: CssRect | null) => {
    rectRef.current = next
    setRect(next)
  }, [])

  const pushHistory = useCallback((previous: AnnotationMark[]) => {
    setUndoStack((stack) => [...stack, previous])
    setRedoStack([])
  }, [])

  // The frame, decoded from the frozen capture, and its mosaic twin
  // pixelated from it the moment it arrives.
  useEffect(() => {
    let live = true
    const element = new Image()
    element.onload = () => {
      if (!live) return
      setPicture({
        source: element,
        mosaic: buildMosaicSource(element, frameSize),
      })
    }
    element.onerror = () => {
      // Nothing to draw on. The overlay stays up, and Confirm hands over
      // the bare rect (see `confirmSelection`).
      if (live) setPicture(null)
    }
    element.src = `data:image/png;base64,${frame.pngBase64}`
    return () => {
      live = false
      element.onload = null
      element.onerror = null
    }
  }, [frame, frameSize])

  // Measure the canvas once it mounts — a capture overlay window never
  // resizes, so the one measure taken as the overlay appears stays good.
  useLayoutEffect(() => {
    const canvas = canvasRef.current
    if (!canvas) return
    const box = canvas.getBoundingClientRect()
    setCanvasBox((current) =>
      current.left === box.left &&
      current.top === box.top &&
      current.width === box.width &&
      current.height === box.height
        ? current
        : { left: box.left, top: box.top, width: box.width, height: box.height }
    )
    // Only refs and setters are touched here, so the empty dependency list
    // is honest: the canvas is mounted by the overlay and never replaced.
  }, [])

  // The toolbar is measured, not guessed: its size decides how it docks to
  // the selection's corner without running off-screen. Measuring on the
  // element itself (a callback ref hands it over) re-runs whenever it
  // mounts — after a selection lands, or when a re-drag takes it away and
  // brings it back.
  useLayoutEffect(() => {
    if (!toolbar) return
    const box = toolbar.getBoundingClientRect()
    // Measuring layout is the one thing an effect may do that render cannot;
    // the guard keeps an unchanged size from re-rendering forever.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setToolbarSize((current) =>
      current.width === box.width && current.height === box.height
        ? current
        : { width: box.width, height: box.height }
    )
  }, [toolbar])

  // One renderer for the whole surface: the frozen frame with the marks
  // (and the one still being drawn) on top, at the frame's own resolution —
  // the export crops this composite, so what the person sees is what goes.
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
      frameSize
    )
  }, [picture, marks, draft, frameSize])

  /** A pointer position as the frame sees it — in the frame's own pixels,
   *  held to its edges — plus the same spot in screen pixels and how many
   *  frame pixels one screen pixel is. */
  const canvasPoint = useCallback(
    (event: { clientX: number; clientY: number }) => {
      const canvas = canvasRef.current
      if (!canvas) return null
      const box = canvas.getBoundingClientRect()
      if (box.width <= 0 || box.height <= 0) return null
      const x = ((event.clientX - box.left) / box.width) * frame.width
      const y = ((event.clientY - box.top) / box.height) * frame.height
      return {
        point: {
          x: Math.max(0, Math.min(frame.width, x)),
          y: Math.max(0, Math.min(frame.height, y)),
        },
        screen: {
          x: event.clientX - box.left,
          y: event.clientY - box.top,
        },
        scale: frame.width / box.width,
      }
    },
    [frame]
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

  /** Picking a tool leaves any label being written behind — the tools are
   *  modes, and half of one must not survive into another. */
  const pickTool = useCallback((next: AnnotationTool) => {
    setTool(next)
    setTextInput(null)
  }, [])

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
    const at = canvasPoint(event)
    if (!at) return
    event.preventDefault()
    // Keep the drag when the pointer leaves the window: without the
    // capture, a selection dragged past the edge would stop following it.
    event.currentTarget.setPointerCapture?.(event.pointerId)

    const current = rectRef.current
    const inside =
      current !== null &&
      at.screen.x >= current.x &&
      at.screen.x <= current.x + current.width &&
      at.screen.y >= current.y &&
      at.screen.y <= current.y + current.height

    if (current && inside) {
      if (tool === "select") {
        // The topmost mark under the pointer wins; a miss on all of them
        // falls through to a fresh selection below.
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
      } else if (tool === "text") {
        setTextInput({ x: at.point.x, y: at.point.y, value: "" })
        return
      } else if (tool === "step") {
        pushHistory(marks)
        const seq = stepSeq
        setMarks((currentMarks) => [
          ...currentMarks,
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
      } else {
        dragRef.current = {
          mode: "draw",
          pointerId: event.pointerId,
          start: at.point,
        }
        // A pen starts as a dot and grows with the pointer; the drag
        // shapes stay empty until there is one worth showing.
        setDraft(
          tool === "pen"
            ? { id: randomUUID(), kind: "pen", color, points: [at.point] }
            : null
        )
        return
      }
    }

    // A new drag replaces the old selection — but remembers it, because a
    // press that never grows into a drag hands the old one back on
    // pointer-up. It starts anywhere: on the dimmed area, on a selection
    // with a select tool that grabbed no mark, or before any selection
    // exists at all.
    priorRectRef.current = current
    dragRef.current = {
      mode: "selection",
      pointerId: event.pointerId,
      start: at.screen,
    }
    setSelecting(true)
    setSelection({ x: at.screen.x, y: at.screen.y, width: 0, height: 0 })
  }

  const onPointerMove = (event: ReactPointerEvent<HTMLDivElement>) => {
    const drag = dragRef.current
    if (!drag || drag.pointerId !== event.pointerId) return
    const at = canvasPoint(event)
    if (!at) return
    if (drag.mode === "selection") {
      setSelection(normalizeRect(drag.start, at.screen))
      return
    }
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
    // A move drag shifts only the grabbed mark, and from where it was when
    // the drag began — a wobbling pointer does not accumulate wobble.
    const dx = at.point.x - drag.start.x
    const dy = at.point.y - drag.start.y
    setMarks(
      drag.original.map((mark) =>
        mark.id === drag.markId ? shiftMarks([mark], dx, dy)[0] : mark
      )
    )
  }

  const onPointerUp = (event: ReactPointerEvent<HTMLDivElement>) => {
    const drag = dragRef.current
    if (!drag || drag.pointerId !== event.pointerId) return
    dragRef.current = null
    if (drag.mode === "selection") {
      setSelecting(false)
      const current = rectRef.current
      if (current && selectionTooSmall(current)) {
        setSelection(priorRectRef.current)
      }
      return
    }
    if (drag.mode === "move") {
      const at = canvasPoint(event)
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
    const committed = draft
    setDraft(null)
    if (!committed) return
    pushHistory(marks)
    setMarks((current) => [...current, committed])
  }

  /** Drop the drag in progress — a cancelled pointer, a lost capture, or
   *  Escape. A mark being drawn never existed; a mark being moved goes
   *  back where it was; a selection being re-cut hands the old one back. */
  const cancelDrag = useCallback(() => {
    const drag = dragRef.current
    dragRef.current = null
    if (!drag) return
    if (drag.mode === "draw") setDraft(null)
    if (drag.mode === "move") setMarks(drag.original)
    if (drag.mode === "selection") {
      setSelecting(false)
      setSelection(priorRectRef.current)
    }
  }, [setSelection])

  // Only the pointer that is dragging can call its drag off: another one
  // being cancelled or let go of says nothing about it.
  const cancelDragOf = (event: ReactPointerEvent<HTMLDivElement>) => {
    if (dragRef.current?.pointerId === event.pointerId) cancelDrag()
  }

  // Escape peels one layer at a time: the label being written, the drag in
  // progress — and only then the whole capture. The text input answers its
  // own Escape first (it preventDefaults), so it never lands here.
  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key !== "Escape" || event.defaultPrevented) return
      if (textInput) {
        event.preventDefault()
        setTextInput(null)
        return
      }
      if (dragRef.current) {
        event.preventDefault()
        cancelDrag()
        return
      }
      onCancel()
    }
    window.addEventListener("keydown", onKeyDown)
    return () => window.removeEventListener("keydown", onKeyDown)
  }, [textInput, cancelDrag, onCancel])

  const confirmSelection = useCallback(() => {
    if (exporting) return
    const canvas = canvasRef.current
    const current = rectRef.current
    if (!canvas || !current || selectionTooSmall(current)) return
    const box = canvas.getBoundingClientRect()
    const physical = cssRectToPhysical(
      current,
      { width: box.width, height: box.height },
      frameSize
    )
    // The frame never decoded: there is nothing to composite, so hand the
    // bare rect over and let Rust crop the raw frozen frame.
    if (!picture) {
      onConfirm(physical, null)
      return
    }
    setExporting(true)
    exportAnnotatedCrop(picture, marks, frameSize, physical)
      .then((base64) => {
        if (liveRef.current) onConfirm(physical, base64)
      })
      .catch(() => {
        // The overlay window has no toast surface; landing the capture
        // without its marks beats a silent dead button.
        if (liveRef.current) onConfirm(physical, null)
      })
      .finally(() => {
        if (liveRef.current) setExporting(false)
      })
  }, [exporting, picture, marks, frameSize, onConfirm])

  /** Where the toolbar docks: under the selection's bottom-right corner,
   *  flipped above it near the viewport bottom, and pulled back inside the
   *  viewport when the corner sits close to an edge. */
  const toolbarPlacement = (): { left: number; top: number } => {
    if (!rect) return { left: 0, top: 0 }
    const flip =
      rect.y + rect.height + TOOLBAR_GAP + toolbarSize.height > canvasBox.height
    const top = flip
      ? Math.max(TOOLBAR_EDGE, rect.y - TOOLBAR_GAP - toolbarSize.height)
      : rect.y + rect.height + TOOLBAR_GAP
    const maxLeft = Math.max(
      TOOLBAR_EDGE,
      canvasBox.width - toolbarSize.width - TOOLBAR_EDGE
    )
    const left = Math.min(
      Math.max(rect.x + rect.width - toolbarSize.width, TOOLBAR_EDGE),
      maxLeft
    )
    return { left, top }
  }

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
    <div
      className="fixed inset-0 cursor-crosshair touch-none select-none overflow-hidden"
      onPointerDown={onPointerDown}
      onPointerMove={onPointerMove}
      onPointerUp={onPointerUp}
      onPointerCancel={cancelDragOf}
      onLostPointerCapture={cancelDragOf}
    >
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
      {/* Marks paint on a canvas sized to the frame and CSS-scaled to the
          viewport beside it: every pointer position converts into frame
          pixels, so the export is 1:1 with what was drawn. */}
      <canvas
        ref={canvasRef}
        width={frame.width}
        height={frame.height}
        aria-hidden
        className="absolute inset-0 h-full w-full"
      />
      {rect ? (
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
      ) : (
        <div className="absolute inset-0 bg-black/40" />
      )}
      {rect && !selectionTooSmall(rect) ? (
        <div
          className="absolute rounded-sm bg-black/70 px-1.5 py-0.5 text-xs leading-none text-white tabular-nums"
          style={{
            left: rect.x,
            top: rect.y >= BADGE_CLEARANCE ? rect.y - 22 : rect.y + 4,
          }}
        >
          {`${Math.round(rect.width)}×${Math.round(rect.height)}`}
        </div>
      ) : null}
      {rect && !selectionTooSmall(rect) && !selecting ? (
        <div
          ref={setToolbar}
          role="toolbar"
          aria-label={t("title")}
          className="absolute z-10 flex items-center gap-0.5 rounded-lg bg-neutral-900/95 p-1 shadow-xl ring-1 ring-white/15"
          style={toolbarPlacement()}
          // A press on the toolbar is not a drag on the picture: keep it
          // from starting a re-selection under the buttons.
          onPointerDown={(event) => event.stopPropagation()}
        >
          {tools.map((entry) => (
            <button
              key={entry.kind}
              type="button"
              className={TOOL_BUTTON}
              aria-label={entry.label}
              title={entry.label}
              aria-pressed={tool === entry.kind}
              onClick={() => pickTool(entry.kind)}
            >
              {entry.icon}
            </button>
          ))}
          <div className="mx-1 h-5 w-px bg-white/15" aria-hidden />
          <button
            type="button"
            className={ROUND_BUTTON}
            aria-label={t("undo")}
            title={t("undo")}
            disabled={undoStack.length === 0 || exporting}
            onClick={undo}
          >
            <Undo2 />
          </button>
          <button
            type="button"
            className={ROUND_BUTTON}
            aria-label={t("redo")}
            title={t("redo")}
            disabled={redoStack.length === 0 || exporting}
            onClick={redo}
          >
            <Redo2 />
          </button>
          <div className="mx-1 h-5 w-px bg-white/15" aria-hidden />
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
                  "size-4 rounded-full border border-white/30 outline-none transition-all",
                  "focus-visible:ring-2 focus-visible:ring-white/40",
                  "aria-pressed:ring-2 aria-pressed:ring-white aria-pressed:ring-offset-1 aria-pressed:ring-offset-neutral-900"
                )}
                style={{ backgroundColor: swatch }}
                onClick={() => setColor(swatch)}
              />
            ))}
          </div>
          <div className="mx-1 h-5 w-px bg-white/15" aria-hidden />
          <button
            type="button"
            className={cn(ROUND_BUTTON, "bg-emerald-600 hover:bg-emerald-500")}
            aria-label={t("confirm")}
            title={t("confirm")}
            disabled={exporting}
            onClick={confirmSelection}
          >
            <Check />
          </button>
          <button
            type="button"
            className={cn(ROUND_BUTTON, "bg-red-600 hover:bg-red-500")}
            aria-label={t("cancel")}
            title={t("cancel")}
            onClick={onCancel}
          >
            <X />
          </button>
        </div>
      ) : null}
      {textInput ? (
        <input
          autoFocus
          value={textInput.value}
          placeholder={t("textPlaceholder")}
          onChange={(event) =>
            setTextInput({ ...textInput, value: event.target.value })
          }
          // The surface beneath must not read a press on the label as a
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
          className="absolute z-10 min-w-24 -translate-y-1/2 rounded-md bg-neutral-900/95 px-2 py-1 text-sm text-white shadow-md ring-1 ring-white/20 outline-none"
          style={{
            left: `${(textInput.x / frame.width) * 100}%`,
            top: `${(textInput.y / frame.height) * 100}%`,
          }}
        />
      ) : null}
    </div>
  )
}
