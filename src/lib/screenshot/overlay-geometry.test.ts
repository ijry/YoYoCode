import { describe, expect, it } from "vitest"

import {
  cssRectToPhysical,
  selectionTooSmall,
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
