// Annotating a screenshot before it goes to a conversation: freehand pen,
// rectangles, ellipses, arrows, text, mosaic over private regions, numbered
// step badges — what browser/screenshot-markup.ts does for a marked-up
// browser capture, generalized to any picture a person took of their screen.
//
// Everything here works in the IMAGE's pixels, never the screen's. The editor
// shows the picture scaled down to fit its dialog, but what the agent
// receives is the picture at its own size, so that is the only coordinate
// system a mark can live in without drifting when the dialog is resized. One
// renderer draws both the preview and the file that is sent — what the person
// sees is what goes.
//
// Marks are plain data on purpose: undo/redo is a stack of arrays, moving a
// mark or surviving a crop is arithmetic (shiftMarks, marksOutside), and
// nothing here needs to know how the editor around it is laid out.

import {
  MARKED_IMAGE_MAX_BYTES,
  arrowGeometry,
} from "@/lib/browser/screenshot-markup"

export type AnnotationTool =
  | "select"
  | "pen"
  | "rect"
  | "ellipse"
  | "arrow"
  | "text"
  | "mosaic"
  | "step"

export interface AnnotationPoint {
  x: number
  y: number
}

/** A mark, in the image's pixels. Every kind carries an id so the select
 *  tool can name what it grabbed and the undo stack can tell marks apart.
 *  A mosaic carries no colour — it pastes the pixelated picture over
 *  itself — and a step's number is its point. */
export type AnnotationMark =
  | { id: string; kind: "pen"; color: string; points: AnnotationPoint[] }
  | {
      id: string
      kind: "rect" | "ellipse"
      color: string
      x: number
      y: number
      width: number
      height: number
    }
  | {
      id: string
      kind: "arrow"
      color: string
      from: AnnotationPoint
      to: AnnotationPoint
    }
  | {
      id: string
      kind: "text"
      color: string
      x: number
      y: number
      text: string
    }
  | {
      id: string
      kind: "mosaic"
      x: number
      y: number
      width: number
      height: number
    }
  | {
      id: string
      kind: "step"
      color: string
      x: number
      y: number
      seq: number
    }

export interface AnnotationSize {
  width: number
  height: number
}

/** The palette the colour picker offers. It opens on the first one — Apple's
 *  system red, for the reason markup uses it: a colour a page rarely uses at
 *  full strength, so a mark reads as "somebody drew this" and not as part of
 *  the picture. */
export const ANNOTATION_COLORS = [
  "#ff3b30",
  "#ff9500",
  "#34c759",
  "#0a84ff",
  "#1d1d1f",
] as const

/** Where a new mark starts: the colour the picker opens on. */
const NEW_MARK_COLOR = ANNOTATION_COLORS[0]

/** Drawn under every stroke, so a mark stays visible on a red picture, a
 *  dark one and a photograph alike. */
const HALO_COLOR = "rgba(255, 255, 255, 0.92)"

const BADGE_TEXT_COLOR = "#ffffff"

/** The quality the backend uses for JPEG (`CAPTURE_JPEG_QUALITY`). */
const ANNOTATED_JPEG_QUALITY = 0.85

function clamp(value: number, low: number, high: number): number {
  return Math.max(low, Math.min(high, value))
}

let markSeq = 0

/** A fresh id for a mark. Unique for the page's lifetime, which is long
 *  enough: marks live and die inside one editor dialog. */
function newMarkId(): string {
  markSeq += 1
  return `mark-${markSeq}`
}

export interface AnnotationStyle {
  /** Width of a rect's outline, a pen's stroke and an arrow's shaft. */
  line: number
  /** The white edge drawn under every stroke, on each side of it. */
  halo: number
  /** How far an arrow's head reaches back from its tip. */
  head: number
  /** Radius of a step badge. */
  badge: number
  /** Face size of a text label. */
  font: number
}

/** Stroke sizes for a picture of this size — the clamp formulas
 *  browser/screenshot-markup.ts draws by, plus a face for text. Proportional
 *  to the picture, so a mark on a narrow capture is not a smear and one on a
 *  wide capture is not a hairline — and bounded both ways, so neither end
 *  goes silly. Text is bounded harder: 16 px stays readable over a small
 *  capture's own lettering, 48 px does not bury what a 4K one shows. */
export function annotationStyle(size: AnnotationSize): AnnotationStyle {
  const line = clamp(Math.max(size.width, size.height) / 300, 2.5, 10)
  return {
    line,
    halo: Math.max(1.5, line * 0.4),
    head: line * 4.2,
    badge: Math.max(9, line * 2.5),
    font: clamp(Math.max(size.width, size.height) / 40, 16, 48),
  }
}

/** The side of one mosaic square, in image pixels. It grows with the
 *  picture — squares fine enough for a phone screenshot would take thousands
 *  of draws across a 4K one — but stops at 24, past which a patch stops
 *  reading as blurred content and starts reading as a coloured rectangle.
 *  Floored at 4 so the squares never get finer than the detail they are
 *  meant to swallow. */
export function mosaicBlockSize(size: AnnotationSize): number {
  return clamp(Math.max(size.width, size.height) / 60, 4, 24)
}

/** The mark a drag from `start` to `end` makes, or null when the drag was
 *  too short to be one (`min`, in image pixels). A rect or an ellipse is the
 *  same shape whichever corner it was dragged from, and counts once either
 *  side reaches `min` — a long thin one is how a line of text gets
 *  underlined; only a drag short in every direction is a click. An arrow
 *  points where the drag ended. A new mark starts in the palette's first
 *  colour; the editor's picker recolours the mark it keeps. */
export function shapeFromDrag(
  tool: "rect" | "ellipse" | "arrow",
  start: AnnotationPoint,
  end: AnnotationPoint,
  min: number
): AnnotationMark | null {
  if (tool === "arrow") {
    const length = Math.hypot(end.x - start.x, end.y - start.y)
    if (length === 0 || length < min) return null
    return {
      id: newMarkId(),
      kind: "arrow",
      color: NEW_MARK_COLOR,
      from: { ...start },
      to: { ...end },
    }
  }
  const width = Math.abs(end.x - start.x)
  const height = Math.abs(end.y - start.y)
  if (width === 0 || height === 0 || Math.max(width, height) < min) return null
  return {
    id: newMarkId(),
    kind: tool,
    color: NEW_MARK_COLOR,
    x: Math.min(start.x, end.x),
    y: Math.min(start.y, end.y),
    width,
    height,
  }
}

/** Move marks by an offset, in image pixels: the select tool's drag moves
 *  one mark, an applied crop moves them all by where the crop begins.
 *  Whatever locates a mark moves; its size, colour and text do not. New
 *  objects come back — the originals are what the undo stack keeps. */
export function shiftMarks(
  marks: readonly AnnotationMark[],
  dx: number,
  dy: number
): AnnotationMark[] {
  return marks.map((mark) => {
    if (mark.kind === "pen") {
      return {
        ...mark,
        points: mark.points.map((point) => ({
          x: point.x + dx,
          y: point.y + dy,
        })),
      }
    }
    if (mark.kind === "arrow") {
      return {
        ...mark,
        from: { x: mark.from.x + dx, y: mark.from.y + dy },
        to: { x: mark.to.x + dx, y: mark.to.y + dy },
      }
    }
    return { ...mark, x: mark.x + dx, y: mark.y + dy }
  })
}

/** The marks a crop would leave with nothing to show — the caller drops
 *  these. A rect, an ellipse or a mosaic is lost when it no longer touches
 *  the canvas at all: half over the edge is still half visible, so it stays.
 *  Every other kind lives at an anchor — a pen's first point, an arrow's
 *  tail, the corner a label or badge sits on — and is lost once that anchor
 *  is off the canvas. */
export function marksOutside(
  marks: readonly AnnotationMark[],
  size: AnnotationSize
): AnnotationMark[] {
  return marks.filter((mark) => {
    if (
      mark.kind === "rect" ||
      mark.kind === "ellipse" ||
      mark.kind === "mosaic"
    ) {
      return (
        mark.x + mark.width <= 0 ||
        mark.y + mark.height <= 0 ||
        mark.x >= size.width ||
        mark.y >= size.height
      )
    }
    // A pen with no points has nowhere to be, so it is not outside either:
    // kept, the way the renderer draws nothing for it instead of tripping
    // over its missing first point.
    if (mark.kind === "pen" && mark.points.length === 0) return false
    const anchor =
      mark.kind === "pen"
        ? mark.points[0]
        : mark.kind === "arrow"
          ? mark.from
          : mark
    return (
      anchor.x < 0 ||
      anchor.y < 0 ||
      anchor.x >= size.width ||
      anchor.y >= size.height
    )
  })
}

/** How far a click may land from a text label's anchor or a step badge's
 *  centre and still mean it. The select tool hands in a slack already scaled
 *  to the preview, but hit testing never knows the picture's size, so the
 *  badge radius here is nominal — bigger than the smallest badge drawn
 *  (annotationStyle floors badge at 9) and generous enough to grab. */
const BADGE_HIT_RADIUS = 12

function distanceToSegment(
  point: AnnotationPoint,
  a: AnnotationPoint,
  b: AnnotationPoint
): number {
  const dx = b.x - a.x
  const dy = b.y - a.y
  const lengthSq = dx * dx + dy * dy
  // A zero-length segment is a dot; measure to its one end.
  if (lengthSq === 0) return Math.hypot(point.x - a.x, point.y - a.y)
  const t = clamp(
    ((point.x - a.x) * dx + (point.y - a.y) * dy) / lengthSq,
    0,
    1
  )
  return Math.hypot(point.x - (a.x + t * dx), point.y - (a.y + t * dy))
}

/** Whether `point` (in image pixels) is close enough to `mark` to have
 *  meant it, with `slack` pixels of forgiveness. A rect, an ellipse or a
 *  mosaic is hit by its box, not its curve — the outline is what the person
 *  aims at, and an ellipse's box is generous in exactly the right direction.
 *  An arrow or a pen stroke is hit within `slack` of any of its segments. */
export function hitTestMark(
  mark: AnnotationMark,
  point: AnnotationPoint,
  slack: number
): boolean {
  switch (mark.kind) {
    case "rect":
    case "ellipse":
    case "mosaic":
      return (
        point.x >= mark.x - slack &&
        point.x <= mark.x + mark.width + slack &&
        point.y >= mark.y - slack &&
        point.y <= mark.y + mark.height + slack
      )
    case "arrow":
      return distanceToSegment(point, mark.from, mark.to) <= slack
    case "pen": {
      for (let i = 1; i < mark.points.length; i++) {
        if (
          distanceToSegment(point, mark.points[i - 1], mark.points[i]) <= slack
        ) {
          return true
        }
      }
      // A dot — one sampled point — is hit like a zero-length segment.
      if (mark.points.length === 1) {
        return distanceToSegment(point, mark.points[0], mark.points[0]) <= slack
      }
      return false
    }
    case "text":
    case "step":
      return (
        Math.hypot(point.x - mark.x, point.y - mark.y) <=
        BADGE_HIT_RADIUS + slack
      )
  }
}

/** Where a rect's or an ellipse's outline is drawn: the shape itself, with
 *  any side that runs along the picture's edge pulled in until its whole
 *  stroke shows. A stroke straddles its line, so a shape dragged to the edge
 *  would otherwise lose half of that side — the side that says where the
 *  shape ends. */
function visibleBounds(
  mark: { x: number; y: number; width: number; height: number },
  style: AnnotationStyle,
  size: AnnotationSize
): { x: number; y: number; width: number; height: number } {
  const edge = style.line / 2 + style.halo
  const left = Math.max(mark.x, edge)
  const top = Math.max(mark.y, edge)
  const right = Math.min(mark.x + mark.width, size.width - edge)
  const bottom = Math.min(mark.y + mark.height, size.height - edge)
  // Too small to pull in without turning it inside out: draw it as it is.
  if (right - left < 1 || bottom - top < 1) {
    return { x: mark.x, y: mark.y, width: mark.width, height: mark.height }
  }
  return { x: left, y: top, width: right - left, height: bottom - top }
}

/** The marks the two stroke passes draw: everything but the mosaic patches
 *  (pasted before any stroke) and the labels and badges (drawn after all of
 *  them). */
type OutlineMark = Extract<
  AnnotationMark,
  { kind: "pen" | "rect" | "ellipse" | "arrow" }
>

function traceShape(
  ctx: CanvasRenderingContext2D,
  mark: OutlineMark,
  style: AnnotationStyle,
  size: AnnotationSize
): void {
  ctx.beginPath()
  if (mark.kind === "pen") {
    if (mark.points.length === 0) return
    ctx.moveTo(mark.points[0].x, mark.points[0].y)
    for (let i = 1; i < mark.points.length; i++) {
      ctx.lineTo(mark.points[i].x, mark.points[i].y)
    }
    return
  }
  if (mark.kind === "arrow") {
    const { shaftEnd } = arrowGeometry(mark.from, mark.to, style)
    ctx.moveTo(mark.from.x, mark.from.y)
    ctx.lineTo(shaftEnd.x, shaftEnd.y)
    return
  }
  // Rect or ellipse: the same bounds, one path each. Their shared member
  // carries a union kind, so nothing above narrows it away — the bounds are
  // read off it either way, and only the path differs.
  const bounds = visibleBounds(mark, style, size)
  if (mark.kind === "rect") {
    ctx.rect(bounds.x, bounds.y, bounds.width, bounds.height)
    return
  }
  ctx.ellipse(
    bounds.x + bounds.width / 2,
    bounds.y + bounds.height / 2,
    bounds.width / 2,
    bounds.height / 2,
    0,
    0,
    Math.PI * 2
  )
}

function traceHead(
  ctx: CanvasRenderingContext2D,
  mark: Extract<AnnotationMark, { kind: "arrow" }>,
  style: AnnotationStyle
): void {
  const [tip, left, right] = arrowGeometry(mark.from, mark.to, style).head
  ctx.beginPath()
  ctx.moveTo(tip.x, tip.y)
  ctx.lineTo(left.x, left.y)
  ctx.lineTo(right.x, right.y)
  ctx.closePath()
}

function drawText(
  ctx: CanvasRenderingContext2D,
  mark: Extract<AnnotationMark, { kind: "text" }>,
  style: AnnotationStyle
): void {
  ctx.font = `600 ${style.font}px ui-sans-serif, system-ui, -apple-system, "Segoe UI", sans-serif`
  ctx.textAlign = "left"
  ctx.textBaseline = "top"
  // The same white edge the strokes get, so a label survives a busy picture.
  ctx.lineJoin = "round"
  ctx.lineWidth = style.halo * 2
  ctx.strokeStyle = HALO_COLOR
  ctx.strokeText(mark.text, mark.x, mark.y)
  ctx.fillStyle = mark.color
  ctx.fillText(mark.text, mark.x, mark.y)
}

function drawStepBadge(
  ctx: CanvasRenderingContext2D,
  mark: Extract<AnnotationMark, { kind: "step" }>,
  style: AnnotationStyle
): void {
  ctx.beginPath()
  ctx.arc(mark.x, mark.y, style.badge + style.halo, 0, Math.PI * 2)
  ctx.fillStyle = HALO_COLOR
  ctx.fill()
  ctx.beginPath()
  ctx.arc(mark.x, mark.y, style.badge, 0, Math.PI * 2)
  ctx.fillStyle = mark.color
  ctx.fill()
  const label = String(mark.seq)
  // Two digits get a smaller face so they stay inside the circle.
  const face = Math.round(style.badge * (label.length > 1 ? 1 : 1.25))
  ctx.font = `600 ${face}px ui-sans-serif, system-ui, -apple-system, "Segoe UI", sans-serif`
  ctx.fillStyle = BADGE_TEXT_COLOR
  ctx.textAlign = "center"
  ctx.textBaseline = "middle"
  ctx.fillText(label, mark.x, mark.y)
}

/**
 * Draw the picture and its annotations. The same call paints the preview and
 * the file that is sent.
 *
 * Mosaic patches go down first: they replace the picture under them, and a
 * patch must not pixelate a stroke that crosses it. Then three passes, the
 * way markup draws — every white edge first, then every stroke, then every
 * label and badge. Drawn mark by mark, a later mark's white edge would cut a
 * gap through an earlier mark wherever the two cross, and a later stroke
 * would run over an earlier label's number.
 */
export function drawAnnotations(
  ctx: CanvasRenderingContext2D,
  image: CanvasImageSource,
  mosaicSource: CanvasImageSource | null,
  marks: readonly AnnotationMark[],
  size: AnnotationSize
): void {
  const style = annotationStyle(size)
  ctx.clearRect(0, 0, size.width, size.height)
  ctx.drawImage(image, 0, 0, size.width, size.height)
  ctx.lineCap = "round"
  ctx.lineJoin = "round"

  for (const mark of marks) {
    if (mark.kind !== "mosaic") continue
    // Nothing to paste before the editor has pixelated the picture; a mosaic
    // mark then draws as nothing rather than as a hole.
    if (!mosaicSource) continue
    ctx.drawImage(
      mosaicSource,
      mark.x,
      mark.y,
      mark.width,
      mark.height,
      mark.x,
      mark.y,
      mark.width,
      mark.height
    )
  }

  const outlines = marks.filter(
    (mark): mark is OutlineMark =>
      mark.kind === "pen" ||
      mark.kind === "rect" ||
      mark.kind === "ellipse" ||
      mark.kind === "arrow"
  )

  ctx.strokeStyle = HALO_COLOR
  for (const mark of outlines) {
    ctx.lineWidth = style.line + style.halo * 2
    traceShape(ctx, mark, style, size)
    ctx.stroke()
    if (mark.kind === "arrow") {
      ctx.lineWidth = style.halo * 2
      traceHead(ctx, mark, style)
      ctx.stroke()
    }
  }

  for (const mark of outlines) {
    ctx.strokeStyle = mark.color
    ctx.lineWidth = style.line
    traceShape(ctx, mark, style, size)
    ctx.stroke()
    if (mark.kind === "arrow") {
      ctx.fillStyle = mark.color
      traceHead(ctx, mark, style)
      ctx.fill()
    }
  }

  for (const mark of marks) {
    if (mark.kind === "text") drawText(ctx, mark, style)
    if (mark.kind === "step") drawStepBadge(ctx, mark, style)
  }
}

function encode(
  canvas: HTMLCanvasElement,
  type: string,
  quality?: number
): Promise<Blob> {
  return new Promise((resolve, reject) => {
    canvas.toBlob(
      (blob) => {
        if (blob) resolve(blob)
        else reject(new Error("the annotated screenshot could not be encoded"))
      },
      type,
      quality
    )
  })
}

/** The picture with its annotations drawn in, at the picture's own size:
 *  PNG, or JPEG when the PNG would be heavier than a vision model takes —
 *  the same budget a marked browser capture is held to. */
export async function exportAnnotated(
  image: CanvasImageSource,
  mosaicSource: CanvasImageSource | null,
  marks: readonly AnnotationMark[],
  size: AnnotationSize
): Promise<Blob> {
  const canvas = document.createElement("canvas")
  canvas.width = size.width
  canvas.height = size.height
  const ctx = canvas.getContext("2d")
  if (!ctx) throw new Error("the screenshot could not be drawn on")
  drawAnnotations(ctx, image, mosaicSource, marks, size)
  const png = await encode(canvas, "image/png")
  if (png.size <= MARKED_IMAGE_MAX_BYTES) return png
  return encode(canvas, "image/jpeg", ANNOTATED_JPEG_QUALITY)
}
