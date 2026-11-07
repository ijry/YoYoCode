import { describe, expect, it } from "vitest"

import {
  ANNOTATION_COLORS,
  annotationStyle,
  hitTestMark,
  marksOutside,
  mosaicBlockSize,
  shapeFromDrag,
  shiftMarks,
  type AnnotationMark,
} from "./annotation"

const rect = (
  id: string,
  x: number,
  y: number,
  width: number,
  height: number
): AnnotationMark => ({
  id,
  kind: "rect",
  color: "#ff3b30",
  x,
  y,
  width,
  height,
})

/** One mark of every kind, spread over a 200×200 picture. */
function allKinds(): AnnotationMark[] {
  return [
    {
      id: "pen",
      kind: "pen",
      color: "#0a84ff",
      points: [
        { x: 10, y: 10 },
        { x: 30, y: 40 },
      ],
    },
    rect("rect", 50, 60, 30, 20),
    {
      id: "ellipse",
      kind: "ellipse",
      color: "#34c759",
      x: 100,
      y: 20,
      width: 40,
      height: 20,
    },
    {
      id: "arrow",
      kind: "arrow",
      color: "#ff9500",
      from: { x: 5, y: 100 },
      to: { x: 60, y: 140 },
    },
    { id: "text", kind: "text", color: "#1d1d1f", x: 120, y: 80, text: "bug" },
    { id: "mosaic", kind: "mosaic", x: 20, y: 150, width: 50, height: 30 },
    { id: "step", kind: "step", color: "#ff3b30", x: 180, y: 40, seq: 3 },
  ]
}

describe("shapeFromDrag", () => {
  it("makes the same rect whichever corner it was dragged from", () => {
    const forward = shapeFromDrag(
      "rect",
      { x: 10, y: 20 },
      { x: 110, y: 70 },
      8
    )
    const backward = shapeFromDrag(
      "rect",
      { x: 110, y: 70 },
      { x: 10, y: 20 },
      8
    )
    expect(forward).toMatchObject({
      kind: "rect",
      x: 10,
      y: 20,
      width: 100,
      height: 50,
    })
    expect(backward).toMatchObject({
      kind: "rect",
      x: 10,
      y: 20,
      width: 100,
      height: 50,
    })
  })

  it("normalizes an ellipse the same way", () => {
    expect(
      shapeFromDrag("ellipse", { x: 30, y: 40 }, { x: 10, y: 0 }, 8)
    ).toMatchObject({ kind: "ellipse", x: 10, y: 0, width: 20, height: 40 })
  })

  it("points an arrow where the drag ended", () => {
    expect(
      shapeFromDrag("arrow", { x: 200, y: 10 }, { x: 20, y: 90 }, 8)
    ).toMatchObject({
      kind: "arrow",
      from: { x: 200, y: 10 },
      to: { x: 20, y: 90 },
    })
  })

  it("takes a drag short in every direction for a click", () => {
    expect(shapeFromDrag("rect", { x: 0, y: 0 }, { x: 5, y: 7 }, 8)).toBeNull()
    expect(
      shapeFromDrag("ellipse", { x: 0, y: 0 }, { x: 5, y: 7 }, 8)
    ).toBeNull()
    expect(shapeFromDrag("arrow", { x: 5, y: 5 }, { x: 9, y: 8 }, 8)).toBeNull()
  })

  // A long thin shape is how a line of text gets underlined.
  it("keeps a long thin shape, but not one with no height at all", () => {
    expect(
      shapeFromDrag("rect", { x: 0, y: 0 }, { x: 100, y: 5 }, 8)
    ).toMatchObject({ x: 0, y: 0, width: 100, height: 5 })
    expect(
      shapeFromDrag("rect", { x: 0, y: 0 }, { x: 100, y: 0 }, 8)
    ).toBeNull()
  })

  it("starts a mark in the palette's first colour, with a fresh id", () => {
    const mark = shapeFromDrag("rect", { x: 0, y: 0 }, { x: 30, y: 30 }, 8)
    expect(mark).toMatchObject({ color: ANNOTATION_COLORS[0] })
    expect(mark?.id).toBeTruthy()
  })
})

describe("shiftMarks", () => {
  it("moves every kind of mark by the same offset", () => {
    const shifted = shiftMarks(allKinds(), 5, -3)
    expect(shifted[0]).toMatchObject({
      points: [
        { x: 15, y: 7 },
        { x: 35, y: 37 },
      ],
    })
    expect(shifted[1]).toMatchObject({ x: 55, y: 57, width: 30, height: 20 })
    expect(shifted[2]).toMatchObject({ x: 105, y: 17, width: 40, height: 20 })
    expect(shifted[3]).toMatchObject({
      from: { x: 10, y: 97 },
      to: { x: 65, y: 137 },
    })
    expect(shifted[4]).toMatchObject({ x: 125, y: 77, text: "bug" })
    expect(shifted[5]).toMatchObject({ x: 25, y: 147, width: 50, height: 30 })
    expect(shifted[6]).toMatchObject({ x: 185, y: 37, seq: 3 })
  })

  it("leaves the originals alone — they are what the undo stack keeps", () => {
    const marks = allKinds()
    shiftMarks(marks, 10, 10)
    expect(marks[0]).toMatchObject({
      points: [
        { x: 10, y: 10 },
        { x: 30, y: 40 },
      ],
    })
    expect(marks[1]).toMatchObject({ x: 50, y: 60 })
  })
})

describe("marksOutside", () => {
  const size = { width: 100, height: 100 }

  it("loses a rect-like mark only when it no longer touches the canvas", () => {
    const lost = marksOutside(
      [
        rect("gone-left", -60, 10, 50, 20),
        rect("kept-left", -30, 10, 50, 20),
        rect("gone-right", 110, 10, 50, 20),
        rect("kept-right", 95, 10, 50, 20),
      ],
      size
    )
    expect(lost.map((mark) => mark.id)).toEqual(["gone-left", "gone-right"])
  })

  it("judges every other kind by its anchor", () => {
    const lost = marksOutside(
      [
        {
          id: "pen",
          kind: "pen",
          color: "#0a84ff",
          points: [
            { x: -5, y: 50 },
            { x: 40, y: 50 },
          ],
        },
        {
          id: "arrow",
          kind: "arrow",
          color: "#ff9500",
          from: { x: 120, y: 50 },
          to: { x: 60, y: 50 },
        },
        {
          id: "text",
          kind: "text",
          color: "#1d1d1f",
          x: -1,
          y: 50,
          text: "hi",
        },
        { id: "step", kind: "step", color: "#ff3b30", x: 100, y: 50, seq: 1 },
      ],
      size
    )
    expect(lost.map((mark) => mark.id)).toEqual([
      "pen",
      "arrow",
      "text",
      "step",
    ])
  })

  it("keeps an anchor still on the canvas even when the rest hangs off", () => {
    const lost = marksOutside(
      [
        {
          id: "pen",
          kind: "pen",
          color: "#0a84ff",
          points: [
            { x: 0, y: 50 },
            { x: 200, y: 50 },
          ],
        },
        {
          id: "arrow",
          kind: "arrow",
          color: "#ff9500",
          from: { x: 99, y: 50 },
          to: { x: 300, y: 50 },
        },
        { id: "mosaic", kind: "mosaic", x: -20, y: -20, width: 60, height: 60 },
      ],
      size
    )
    expect(lost).toEqual([])
  })

  // A pen a crop emptied has nowhere to be, so it is not outside either:
  // kept, the way the renderer draws nothing for it rather than crashing on
  // its missing first point.
  it("never reports a pen with no points as outside", () => {
    const empty: AnnotationMark = {
      id: "pen",
      kind: "pen",
      color: "#0a84ff",
      points: [],
    }
    expect(marksOutside([empty], size)).toEqual([])
  })
})

describe("hitTestMark", () => {
  it("hits a rect inside it and within the slack, misses farther out", () => {
    const mark = rect("rect", 20, 20, 60, 40)
    expect(hitTestMark(mark, { x: 50, y: 40 }, 4)).toBe(true)
    expect(hitTestMark(mark, { x: 17, y: 40 }, 4)).toBe(true)
    expect(hitTestMark(mark, { x: 14, y: 40 }, 4)).toBe(false)
    expect(hitTestMark(mark, { x: 300, y: 40 }, 4)).toBe(false)
  })

  it("takes an ellipse or a mosaic by its box", () => {
    const ellipse: AnnotationMark = {
      id: "ellipse",
      kind: "ellipse",
      color: "#34c759",
      x: 10,
      y: 10,
      width: 40,
      height: 20,
    }
    expect(hitTestMark(ellipse, { x: 48, y: 20 }, 4)).toBe(true)
    const mosaic: AnnotationMark = {
      id: "mosaic",
      kind: "mosaic",
      x: 10,
      y: 10,
      width: 40,
      height: 20,
    }
    expect(hitTestMark(mosaic, { x: 48, y: 20 }, 4)).toBe(true)
    expect(hitTestMark(mosaic, { x: 56, y: 20 }, 4)).toBe(false)
  })

  it("hits an arrow along its shaft", () => {
    const mark: AnnotationMark = {
      id: "arrow",
      kind: "arrow",
      color: "#ff9500",
      from: { x: 10, y: 10 },
      to: { x: 110, y: 10 },
    }
    expect(hitTestMark(mark, { x: 60, y: 13 }, 4)).toBe(true)
    expect(hitTestMark(mark, { x: 60, y: 20 }, 4)).toBe(false)
  })

  it("hits a pen stroke on any of its segments", () => {
    const mark: AnnotationMark = {
      id: "pen",
      kind: "pen",
      color: "#0a84ff",
      points: [
        { x: 10, y: 10 },
        { x: 50, y: 10 },
        { x: 50, y: 50 },
      ],
    }
    expect(hitTestMark(mark, { x: 30, y: 12 }, 4)).toBe(true)
    expect(hitTestMark(mark, { x: 48, y: 30 }, 4)).toBe(true)
    expect(hitTestMark(mark, { x: 20, y: 30 }, 4)).toBe(false)
  })

  it("hits a step badge near its centre", () => {
    const mark: AnnotationMark = {
      id: "step",
      kind: "step",
      color: "#ff3b30",
      x: 50,
      y: 60,
      seq: 2,
    }
    expect(hitTestMark(mark, { x: 53, y: 58 }, 4)).toBe(true)
    expect(hitTestMark(mark, { x: 70, y: 60 }, 4)).toBe(false)
  })

  it("hits text around its anchor like a badge", () => {
    const mark: AnnotationMark = {
      id: "text",
      kind: "text",
      color: "#1d1d1f",
      x: 50,
      y: 60,
      text: "here",
    }
    expect(hitTestMark(mark, { x: 55, y: 63 }, 4)).toBe(true)
    expect(hitTestMark(mark, { x: 80, y: 60 }, 4)).toBe(false)
  })
})

describe("annotationStyle", () => {
  it("scales with the picture and stays within bounds at both ends", () => {
    expect(annotationStyle({ width: 120, height: 80 }).line).toBe(2.5)
    expect(annotationStyle({ width: 9000, height: 9000 }).line).toBe(10)
    expect(annotationStyle({ width: 1500, height: 900 }).line).toBeCloseTo(5)
  })

  it("keeps the text face readable on any picture", () => {
    expect(annotationStyle({ width: 120, height: 80 }).font).toBe(16)
    expect(annotationStyle({ width: 9000, height: 9000 }).font).toBe(48)
    expect(annotationStyle({ width: 1500, height: 900 }).font).toBeCloseTo(37.5)
  })
})

describe("mosaicBlockSize", () => {
  it("grows with the picture and stops at the ceiling", () => {
    expect(mosaicBlockSize({ width: 3840, height: 2160 })).toBe(24)
    expect(mosaicBlockSize({ width: 1200, height: 600 })).toBe(20)
  })

  it("never gets finer than the detail it is meant to swallow", () => {
    expect(mosaicBlockSize({ width: 240, height: 180 })).toBe(4)
  })
})

describe("ANNOTATION_COLORS", () => {
  it("opens on the red the marks are drawn in by default", () => {
    expect(ANNOTATION_COLORS[0]).toBe("#ff3b30")
  })
})
