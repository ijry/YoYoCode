import { describe, expect, it } from "vitest"

import {
  cssRectToPhysical,
  physicalRectToCss,
  selectionTooSmall,
  windowAtPoint,
  type CssRect,
} from "./overlay-geometry"

function rect(x: number, y: number, width: number, height: number): CssRect {
  return { x, y, width, height }
}

describe("cssRectToPhysical", () => {
  it("doubles every value at a 2× device pixel ratio", () => {
    expect(
      cssRectToPhysical(
        rect(10, 20, 100, 50),
        { width: 800, height: 600 },
        { width: 1600, height: 1200 }
      )
    ).toEqual(rect(20, 40, 200, 100))
  })

  it("maps a full-viewport drag onto the whole 2560×1440 frame", () => {
    expect(
      cssRectToPhysical(
        rect(0, 0, 1280, 720),
        { width: 1280, height: 720 },
        { width: 2560, height: 1440 }
      )
    ).toEqual(rect(0, 0, 2560, 1440))
  })

  it("rounds fractional results at 125% Windows scaling", () => {
    expect(
      cssRectToPhysical(
        rect(3, 7, 33, 41),
        { width: 1000, height: 800 },
        { width: 1250, height: 1000 }
      )
    ).toEqual(rect(4, 9, 41, 51))
  })

  it("rounds half up at 150% Windows scaling", () => {
    expect(
      cssRectToPhysical(
        rect(11, 13, 200, 100),
        { width: 1280, height: 720 },
        { width: 1920, height: 1080 }
      )
    ).toEqual(rect(17, 20, 300, 150))
  })
})

describe("physicalRectToCss", () => {
  it("keeps fractional coordinates with independent axis scales", () => {
    const css = physicalRectToCss(
      rect(11, 13, 101, 79),
      { width: 1000, height: 600 },
      { width: 2500, height: 1800 }
    )
    expect(css.x).toBeCloseTo(4.4)
    expect(css.y).toBeCloseTo(13 / 3)
    expect(css.width).toBeCloseTo(40.4)
    expect(css.height).toBeCloseTo(79 / 3)
    expect(
      cssRectToPhysical(
        css,
        { width: 1000, height: 600 },
        { width: 2500, height: 1800 }
      )
    ).toEqual(rect(11, 13, 101, 79))
  })

  it.each([0, -1, NaN, Infinity])(
    "returns an empty rectangle for invalid frame or viewport dimensions (%s)",
    (dimension) => {
      for (const axis of ["width", "height"] as const) {
        const invalid = { width: 1000, height: 600, [axis]: dimension }
        expect(
          physicalRectToCss(rect(10, 20, 30, 40), invalid, {
            width: 2000,
            height: 1200,
          })
        ).toEqual(rect(0, 0, 0, 0))
        expect(
          physicalRectToCss(
            rect(10, 20, 30, 40),
            { width: 1000, height: 600 },
            invalid
          )
        ).toEqual(rect(0, 0, 0, 0))
      }
    }
  )
})

describe("windowAtPoint", () => {
  it("chooses the frontmost overlap even when a rear window is smaller", () => {
    const front = rect(20, 30, 500, 400)
    const rear = rect(100, 100, 50, 50)
    const windows = [front, rear]
    expect(windowAtPoint(windows, { x: 125, y: 125 })).toBe(front)
    expect(windows).toEqual([front, rear])
    expect(windowAtPoint([rear, front], { x: 125, y: 125 })).toBe(rear)
  })

  it("includes left/top and excludes right/bottom edges", () => {
    const candidate = rect(10, 20, 30, 40)
    expect(windowAtPoint([candidate], { x: 10, y: 20 })).toBe(candidate)
    expect(windowAtPoint([candidate], { x: 39.9, y: 59.9 })).toBe(candidate)
    expect(windowAtPoint([candidate], { x: 40, y: 30 })).toBeNull()
    expect(windowAtPoint([candidate], { x: 20, y: 60 })).toBeNull()
    expect(windowAtPoint([candidate], { x: 9.9, y: 30 })).toBeNull()
    expect(windowAtPoint([candidate], { x: 20, y: 19.9 })).toBeNull()
  })

  it("returns no hit outside all windows or with no candidates", () => {
    expect(windowAtPoint([], { x: 20, y: 30 })).toBeNull()
    expect(windowAtPoint([rect(10, 20, 30, 40)], { x: 80, y: 90 })).toBeNull()
    expect(windowAtPoint([rect(10, 20, 30, 40)], { x: NaN, y: 30 })).toBeNull()
  })

  it("skips non-finite coordinates and non-positive or non-finite sizes", () => {
    const valid = rect(10, 20, 30, 40)
    expect(
      windowAtPoint(
        [
          rect(-Infinity, 0, Infinity, 100),
          rect(0, -Infinity, 100, Infinity),
          rect(NaN, 0, 100, 100),
          rect(0, NaN, 100, 100),
          rect(0, 0, Infinity, 100),
          rect(0, 0, 100, Infinity),
          rect(0, 0, NaN, 100),
          rect(0, 0, 100, NaN),
          rect(20, 30, 0, 100),
          rect(20, 30, 100, 0),
          rect(20, 30, -100, 100),
          rect(20, 30, 100, -100),
          valid,
        ],
        { x: 20, y: 30 }
      )
    ).toBe(valid)
  })
})

describe("selectionTooSmall", () => {
  it("rejects a drag short in every direction", () => {
    expect(selectionTooSmall(rect(40, 40, 3, 5))).toBe(true)
  })

  it("accepts a long thin strip reaching the minimum on one side", () => {
    expect(selectionTooSmall(rect(0, 100, 8, 300))).toBe(false)
  })

  it("accepts a hairline strip when the other side reaches the minimum", () => {
    expect(selectionTooSmall(rect(0, 100, 1, 300))).toBe(false)
  })

  it("rejects a zero side, however long the other one is", () => {
    expect(selectionTooSmall(rect(40, 40, 0, 300))).toBe(true)
    expect(selectionTooSmall(rect(40, 40, 300, 0))).toBe(true)
  })

  it("accepts a drag that reaches the minimum exactly", () => {
    expect(selectionTooSmall(rect(40, 40, 8, 8))).toBe(false)
  })

  it("honours a custom minimum", () => {
    expect(selectionTooSmall(rect(40, 40, 5, 5), 4)).toBe(false)
    expect(selectionTooSmall(rect(40, 40, 5, 5), 6)).toBe(true)
  })
})
