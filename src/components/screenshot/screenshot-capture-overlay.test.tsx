import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  within,
} from "@testing-library/react"
import { NextIntlClientProvider } from "next-intl"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import enMessages from "@/i18n/messages/en.json"

import { ScreenshotCaptureOverlay } from "./screenshot-capture-overlay"

const SE = enMessages.Folder.chat.screenshotEditor

// Every 2d call the overlay makes, the offscreen canvases included, in order.
const calls: Array<{ prop: string; args: unknown[] }> = []

// jsdom has no 2d context; this one records instead of painting.
function fakeContext(): CanvasRenderingContext2D {
  return new Proxy(
    {},
    {
      get: (_target, prop) => {
        if (typeof prop !== "string") return () => {}
        return (...args: unknown[]) => {
          calls.push({ prop, args })
        }
      },
      set: () => true,
    }
  ) as unknown as CanvasRenderingContext2D
}

/** A picture that loads on the next tick — jsdom decodes no images of its
 *  own. The frozen frame is 2000×1200. */
class LoadingImage {
  onload: (() => void) | null = null
  onerror: (() => void) | null = null
  naturalWidth = 2000
  naturalHeight = 1200
  private current = ""
  get src() {
    return this.current
  }
  set src(value: string) {
    this.current = value
    setTimeout(() => {
      if (value.includes("broken")) this.onerror?.()
      else this.onload?.()
    }, 0)
  }
}

// A 2000×1200 frame shown at 1000×600 on screen: one screen pixel is two
// frame pixels, so a drag of (50, 50) on the overlay is (100, 100) in the
// frame. Positions below are relative to the canvas's top-left corner on
// screen, which sits at the window's corner.
const FRAME = {
  monitorIndex: 0,
  pngBase64: "aGVsbG8=",
  width: 2000,
  height: 1200,
}

const BOX = { left: 0, top: 0, width: 1000, height: 600 }

let encode: (type: string) => Blob | null
// The size of the canvas the overlay last asked to encode: the crop must be
// exactly the selection's physical size for Rust to accept the picture.
let encodedSize: { width: number; height: number } | null

beforeEach(() => {
  calls.length = 0
  encode = (type) => new Blob([new Uint8Array(64)], { type })
  encodedSize = null
  vi.stubGlobal("Image", LoadingImage)
  vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue(
    fakeContext()
  )
  vi.spyOn(HTMLCanvasElement.prototype, "toBlob").mockImplementation(function (
    this: HTMLCanvasElement,
    callback,
    type = "image/png"
  ) {
    encodedSize = { width: this.width, height: this.height }
    callback(encode(type))
  })
  vi.spyOn(
    HTMLCanvasElement.prototype,
    "getBoundingClientRect"
  ).mockReturnValue({
    left: BOX.left,
    top: BOX.top,
    right: BOX.left + BOX.width,
    bottom: BOX.top + BOX.height,
    width: BOX.width,
    height: BOX.height,
    x: BOX.left,
    y: BOX.top,
    toJSON: () => ({}),
  } as DOMRect)
})

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

function renderOverlay(
  props: Partial<React.ComponentProps<typeof ScreenshotCaptureOverlay>> = {}
) {
  const onConfirm = vi.fn()
  const onCancel = vi.fn()
  const view = render(
    <NextIntlClientProvider locale="en" messages={enMessages}>
      <ScreenshotCaptureOverlay
        frame={FRAME}
        onConfirm={onConfirm}
        onCancel={onCancel}
        {...props}
      />
    </NextIntlClientProvider>
  )
  return { ...view, onConfirm, onCancel }
}

async function settle() {
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 0))
  })
}

function canvas(): HTMLCanvasElement {
  const element = document.querySelector("canvas")
  if (!element) throw new Error("the overlay has no canvas")
  return element as HTMLCanvasElement
}

// jsdom has no `PointerEvent`; a mouse event under the pointer's name, with
// the pointer's id on it, carries everything the overlay reads. `x`/`y` are
// relative to the canvas's top-left corner on screen.
function pointer(type: string, x: number, y: number, pointerId = 1) {
  const event = new MouseEvent(type, {
    bubbles: true,
    cancelable: true,
    button: 0,
    clientX: BOX.left + x,
    clientY: BOX.top + y,
  })
  Object.defineProperty(event, "pointerId", { value: pointerId })
  fireEvent(canvas(), event)
}

function drag(from: [number, number], to: [number, number]) {
  pointer("pointerdown", ...from)
  pointer("pointermove", (from[0] + to[0]) / 2, (from[1] + to[1]) / 2)
  pointer("pointermove", ...to)
  pointer("pointerup", ...to)
}

/** The distinct `lineTo` points painted, as "x,y" — every pen stroke is
 *  traced twice (white edge, then colour) over the same points, so one
 *  stroke is one entry per point, draft renders included. The screen-to-
 *  frame scale leaves float dust (110/1000 × 2000), so points round. */
function lineTos(): string[] {
  return [
    ...new Set(
      calls
        .filter((call) => call.prop === "lineTo")
        .map((call) =>
          JSON.stringify(
            call.args.map((value) =>
              typeof value === "number" ? Math.round(value) : value
            )
          )
        )
    ),
  ]
}

/** Lets a FileReader-based read run to completion inside an act. */
async function flush() {
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 0))
    await new Promise((resolve) => setTimeout(resolve, 0))
  })
}

describe("ScreenshotCaptureOverlay", () => {
  it("shows the toolbar only once a selection has been dragged out", async () => {
    renderOverlay()
    await settle()
    expect(screen.queryByRole("toolbar")).toBeNull()

    // Mid-drag the toolbar is still away; it docks when the drag lands.
    pointer("pointerdown", 100, 50)
    pointer("pointermove", 200, 100)
    expect(screen.queryByRole("toolbar")).toBeNull()
    pointer("pointermove", 300, 150)
    pointer("pointerup", 300, 150)

    const toolbar = screen.getByRole("toolbar", { name: SE.title })
    expect(toolbar.style.left).toBe("300px")
    expect(toolbar.style.top).toBe("158px")
    for (const label of [
      SE.select,
      SE.pen,
      SE.rect,
      SE.ellipse,
      SE.arrow,
      SE.text,
      SE.mosaic,
      SE.step,
      SE.undo,
      SE.redo,
      SE.confirm,
      SE.cancel,
    ]) {
      expect(screen.getByRole("button", { name: label })).toBeInTheDocument()
    }
    expect(screen.getByRole("button", { name: SE.select })).toHaveAttribute(
      "aria-pressed",
      "true"
    )
    expect(screen.getByRole("button", { name: SE.undo })).toBeDisabled()
    expect(screen.getByRole("button", { name: SE.redo })).toBeDisabled()
    expect(
      within(screen.getByRole("group", { name: SE.color })).getAllByRole(
        "button"
      )
    ).toHaveLength(5)
    expect(screen.getByText("200×100")).toBeInTheDocument()
  })

  it("draws a pen mark, undoes it away and redoes it back", async () => {
    renderOverlay()
    await settle()
    drag([100, 50], [300, 150])
    fireEvent.click(screen.getByRole("button", { name: SE.pen }))
    // (100,100)–(120,120) on screen is (200,200)–(240,240) in the frame.
    pointer("pointerdown", 100, 100)
    pointer("pointermove", 110, 110)
    pointer("pointermove", 120, 120)
    pointer("pointerup", 120, 120)
    expect(lineTos()).toEqual(["[220,220]", "[240,240]"])
    expect(screen.getByRole("button", { name: SE.undo })).toBeEnabled()

    calls.length = 0
    fireEvent.click(screen.getByRole("button", { name: SE.undo }))
    expect(lineTos()).toEqual([])
    expect(screen.getByRole("button", { name: SE.redo })).toBeEnabled()

    calls.length = 0
    fireEvent.click(screen.getByRole("button", { name: SE.redo }))
    expect(lineTos()).toEqual(["[220,220]", "[240,240]"])
  })

  it("confirms with the physical rect, a PNG of exactly its size and the crop offset", async () => {
    const { onConfirm } = renderOverlay()
    await settle()
    drag([100, 50], [300, 150])
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: SE.confirm }))
    })
    await flush()
    expect(onConfirm).toHaveBeenCalledTimes(1)
    // The drag in CSS pixels doubles into the frame's physical pixels.
    expect(onConfirm.mock.calls[0][0]).toEqual({
      x: 200,
      y: 100,
      width: 400,
      height: 200,
    })
    // The encoded canvas is exactly the selection's physical size, painted
    // from the full composite offset by where the selection begins.
    expect(encodedSize).toEqual({ width: 400, height: 200 })
    const offset = calls.find(
      (call) => call.prop === "drawImage" && call.args.length === 3
    )
    expect(offset).toBeDefined()
    expect(offset?.args[1]).toBe(-200)
    expect(offset?.args[2]).toBe(-100)
    expect((offset?.args[0] as HTMLCanvasElement).width).toBe(2000)
    // The base64 the Rust side will decode as that same PNG.
    const base64 = onConfirm.mock.calls[0][1] as string
    expect(atob(base64).length).toBe(64)
  })

  it("cancels the whole capture on Escape", async () => {
    const { onConfirm, onCancel } = renderOverlay()
    await settle()
    drag([100, 50], [300, 150])
    fireEvent.keyDown(window, { key: "Escape" })
    expect(onCancel).toHaveBeenCalledTimes(1)
    expect(onConfirm).not.toHaveBeenCalled()
  })

  it("re-cuts the selection from a drag on the dimmed area, toolbar following", async () => {
    renderOverlay()
    await settle()
    drag([100, 50], [300, 150])
    expect(screen.getByText("200×100")).toBeInTheDocument()

    // (600,400) is outside the first selection: the drag replaces it, and
    // the toolbar docks to the new corner.
    drag([600, 400], [700, 460])
    const toolbar = screen.getByRole("toolbar", { name: SE.title })
    expect(toolbar.style.left).toBe("700px")
    expect(toolbar.style.top).toBe("468px")
    expect(screen.getByText("100×60")).toBeInTheDocument()

    // A press on the dim that never grows into a drag hands the selection
    // back the way it was.
    pointer("pointerdown", 800, 550)
    pointer("pointerup", 803, 553)
    expect(screen.getByText("100×60")).toBeInTheDocument()
    expect(screen.getByRole("toolbar", { name: SE.title }).style.left).toBe(
      "700px"
    )
  })
})
