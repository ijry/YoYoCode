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

// The side channels the overlay's outgoing buttons reach for: the save
// dialog, the Rust commands behind save and pin.
const dialog = vi.hoisted(() => ({ save: vi.fn() }))
const tauri = vi.hoisted(() => ({
  screenshotSaveToFile: vi.fn(),
  screenshotPin: vi.fn(),
}))
// The window the overlay lives in: pinning reads its physical position to
// place the pin window on the desktop.
const windowApi = vi.hoisted(() => ({ outerPosition: vi.fn() }))
vi.mock("@tauri-apps/plugin-dialog", () => dialog)
vi.mock("@tauri-apps/api/window", () => ({
  getCurrentWindow: () => ({ outerPosition: windowApi.outerPosition }),
}))
vi.mock("@/lib/tauri", () => tauri)

// Just enough of ClipboardItem for copyImageToClipboard: the entries it was
// built with, readable back through the standard surface.
class FakeClipboardItem {
  private entries: Record<string, Blob | Promise<Blob>>
  constructor(entries: Record<string, Blob | Promise<Blob>>) {
    this.entries = entries
  }
  get types(): string[] {
    return Object.keys(this.entries)
  }
  getType(type: string): Promise<Blob> {
    return Promise.resolve(this.entries[type])
  }
}

const clipboardWrite = vi.fn(async (_items: FakeClipboardItem[]) => {})

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

// Native z-order: the large front window covers the smaller rear window.
const WINDOW_FRAME = {
  ...FRAME,
  windows: [
    { x: 200, y: 100, width: 800, height: 600 },
    { x: 300, y: 200, width: 200, height: 200 },
    { x: 1200, y: 700, width: 400, height: 200 },
  ],
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
  dialog.save.mockReset()
  tauri.screenshotSaveToFile.mockReset()
  tauri.screenshotPin.mockReset()
  tauri.screenshotPin.mockResolvedValue("screenshot-pin-1a2b3c4d")
  windowApi.outerPosition.mockReset()
  windowApi.outerPosition.mockResolvedValue({ x: 100, y: 50 })
  clipboardWrite.mockClear()
  vi.stubGlobal("ClipboardItem", FakeClipboardItem)
  Object.defineProperty(navigator, "clipboard", {
    value: { write: clipboardWrite },
    configurable: true,
  })
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

// A pointer event on one of the selection's resize handles — its press is
// stopped by the handle itself, the moves that follow land on the canvas
// like any other drag. Coordinates are as in `pointer`.
function handlePointer(
  dir: string,
  type: string,
  x: number,
  y: number,
  pointerId = 1
) {
  const element = document.querySelector(`[data-resize-handle="${dir}"]`)
  if (!element) throw new Error(`no ${dir} resize handle`)
  const event = new MouseEvent(type, {
    bubbles: true,
    cancelable: true,
    button: 0,
    clientX: BOX.left + x,
    clientY: BOX.top + y,
  })
  Object.defineProperty(event, "pointerId", { value: pointerId })
  fireEvent(element as HTMLElement, event)
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

/** The visible cutout, shared by a hover preview and a committed selection. */
function cutout(): HTMLElement | null {
  return document.querySelector<HTMLElement>(".border-white")
}

describe("native window selection", () => {
  it("previews the frontmost window with its CSS size but no toolbar or handles", () => {
    renderOverlay({ frame: WINDOW_FRAME })
    pointer("pointermove", 175, 125)
    expect(cutout()).toHaveStyle({
      left: "100px",
      top: "50px",
      width: "400px",
      height: "300px",
      boxShadow: "0 0 0 100000px rgba(0, 0, 0, 0.4)",
    })
    expect(screen.getByText("400×300")).toBeInTheDocument()
    expect(screen.queryByRole("toolbar")).toBeNull()
    expect(document.querySelectorAll("[data-resize-handle]")).toHaveLength(0)
  })

  it("follows a different hovered window and clears when no window is hit", () => {
    renderOverlay({ frame: WINDOW_FRAME })
    pointer("pointermove", 175, 125)
    pointer("pointermove", 650, 400)
    expect(cutout()).toHaveStyle({
      left: "600px",
      top: "350px",
      width: "200px",
      height: "100px",
    })
    expect(screen.getByText("200×100")).toBeInTheDocument()
    expect(screen.queryByText("400×300")).toBeNull()
    pointer("pointermove", 900, 550)
    expect(cutout()).toBeNull()
    expect(screen.queryByRole("toolbar")).toBeNull()
  })

  it("clears idle hover on pointer leave", () => {
    renderOverlay({ frame: WINDOW_FRAME })
    pointer("pointermove", 175, 125)
    expect(cutout()).not.toBeNull()
    fireEvent.pointerLeave(canvas().parentElement!)
    expect(cutout()).toBeNull()
    expect(screen.queryByRole("toolbar")).toBeNull()
  })

  it("selects on down/up without a prior move and waits for image decoding before output", async () => {
    const { onConfirm } = renderOverlay({ frame: WINDOW_FRAME })
    pointer("pointerdown", 175, 125)
    expect(screen.queryByRole("toolbar")).toBeNull()
    pointer("pointerup", 175, 125)
    expect(screen.getByRole("toolbar")).toBeInTheDocument()
    expect(document.querySelectorAll("[data-resize-handle]")).toHaveLength(8)
    expect(screen.getByText("400×300")).toBeInTheDocument()
    for (const name of [SE.copy, SE.pin, SE.save]) {
      expect(screen.getByRole("button", { name })).toBeDisabled()
    }
    expect(onConfirm).not.toHaveBeenCalled()
    await settle()
    for (const name of [SE.copy, SE.pin, SE.save]) {
      expect(screen.getByRole("button", { name })).toBeEnabled()
    }
  })

  it("uses the down-point candidate even if the hover preview is stale", () => {
    renderOverlay({ frame: WINDOW_FRAME })
    pointer("pointermove", 175, 125)
    pointer("pointerdown", 650, 400)
    pointer("pointerup", 650, 400)
    expect(screen.getByRole("toolbar")).toBeInTheDocument()
    expect(cutout()).toHaveStyle({ left: "600px", top: "350px" })
    expect(screen.getByText("200×100")).toBeInTheDocument()
  })

  it.each([false, true])(
    "snaps small jitter across the window edge (move event: %s)",
    (withMove) => {
      renderOverlay({ frame: WINDOW_FRAME })
      pointer("pointerdown", 499, 349)
      if (withMove) pointer("pointermove", 503, 352)
      pointer("pointerup", 503, 352)
      expect(screen.getByRole("toolbar")).toBeInTheDocument()
      expect(screen.getByText("400×300")).toBeInTheDocument()
      expect(cutout()).toHaveStyle({ left: "100px", top: "50px" })
    }
  )

  it("drags a manual selection from the original down point instead of moving the window", () => {
    renderOverlay({ frame: WINDOW_FRAME })
    pointer("pointermove", 175, 125)
    pointer("pointerdown", 175, 125)
    pointer("pointermove", 178, 128)
    pointer("pointermove", 275, 225)
    expect(screen.queryByRole("toolbar")).toBeNull()
    expect(document.querySelectorAll("[data-resize-handle]")).toHaveLength(0)
    pointer("pointerup", 275, 225)
    expect(cutout()).toHaveStyle({
      left: "175px",
      top: "125px",
      width: "100px",
      height: "100px",
    })
    expect(screen.getByRole("toolbar")).toBeInTheDocument()
  })

  it.each([
    { end: [350, 125], move: [350, 125] },
    { end: [175, 125], move: [350, 125] },
    { end: [175, 125], move: [180, 125] },
    { end: [175, 125], move: [175, 130] },
  ])("never snaps after crossing the click threshold: %j", ({ end, move }) => {
    renderOverlay({ frame: WINDOW_FRAME })
    pointer("pointerdown", 175, 125)
    pointer("pointermove", move[0], move[1])
    pointer("pointerup", end[0], end[1])
    expect(cutout()).toBeNull()
    expect(screen.queryByRole("toolbar")).toBeNull()
    expect(document.querySelectorAll("[data-resize-handle]")).toHaveLength(0)
  })

  it.each([false, true])(
    "uses the release coordinates for the final manual rect (move event: %s)",
    (withMove) => {
      renderOverlay({ frame: WINDOW_FRAME })
      pointer("pointerdown", 175, 125)
      if (withMove) pointer("pointermove", 200, 150)
      pointer("pointerup", 325, 205)
      expect(cutout()).toHaveStyle({
        left: "175px",
        top: "125px",
        width: "150px",
        height: "80px",
      })
      expect(screen.getByText("150×80")).toBeInTheDocument()
      expect(screen.getByRole("toolbar")).toBeInTheDocument()
    }
  )

  it("does not snap a horizontal drag reported only on release", () => {
    renderOverlay({ frame: WINDOW_FRAME })
    pointer("pointerdown", 175, 125)
    pointer("pointerup", 350, 125)
    expect(cutout()).toBeNull()
    expect(screen.queryByRole("toolbar")).toBeNull()
  })

  it("keeps a committed selection and re-cuts the dim area even over another window", () => {
    renderOverlay({ frame: WINDOW_FRAME })
    pointer("pointerdown", 175, 125)
    pointer("pointerup", 175, 125)
    pointer("pointermove", 650, 400)
    expect(screen.getByText("400×300")).toBeInTheDocument()
    expect(cutout()).toHaveStyle({ left: "100px", top: "50px" })
    fireEvent.pointerLeave(canvas().parentElement!)
    expect(screen.getByRole("toolbar")).toBeInTheDocument()

    pointer("pointerdown", 650, 400)
    pointer("pointerup", 650, 400)
    expect(screen.getByText("400×300")).toBeInTheDocument()
    expect(cutout()).toHaveStyle({ left: "100px", top: "50px" })

    drag([650, 400], [750, 460])
    expect(screen.getByText("100×60")).toBeInTheDocument()
    expect(cutout()).toHaveStyle({ left: "650px", top: "400px" })
    expect(screen.getByRole("toolbar")).toHaveStyle({
      left: "750px",
      top: "468px",
    })
    pointer("pointerdown", 175, 125)
    pointer("pointerup", 175, 125)
    expect(screen.getByText("100×60")).toBeInTheDocument()
  })

  it.each(["pointercancel", "lostpointercapture", "Escape"])(
    "%s discards a window candidate without committing it",
    (type) => {
      const { onConfirm, onCancel } = renderOverlay({ frame: WINDOW_FRAME })
      pointer("pointermove", 175, 125)
      pointer("pointerdown", 175, 125)
      pointer("pointermove", 178, 128)
      if (type === "Escape") fireEvent.keyDown(window, { key: type })
      else pointer(type, 178, 128)
      pointer("pointerup", 178, 128)
      expect(cutout()).toBeNull()
      expect(screen.queryByRole("toolbar")).toBeNull()
      expect(onConfirm).not.toHaveBeenCalled()
      expect(onCancel).not.toHaveBeenCalled()
    }
  )

  it("Escape clears an idle preview and cancels without selecting", () => {
    const { onConfirm, onCancel } = renderOverlay({ frame: WINDOW_FRAME })
    pointer("pointermove", 175, 125)
    fireEvent.keyDown(window, { key: "Escape" })
    expect(cutout()).toBeNull()
    expect(screen.queryByRole("toolbar")).toBeNull()
    expect(onCancel).toHaveBeenCalledTimes(1)
    expect(onConfirm).not.toHaveBeenCalled()
  })

  it("ignores other pointers while the window-selection pointer is down", () => {
    renderOverlay({ frame: WINDOW_FRAME })
    pointer("pointerdown", 175, 125, 1)
    pointer("pointerdown", 650, 400, 2)
    pointer("pointermove", 700, 450, 2)
    pointer("pointerup", 700, 450, 2)
    pointer("pointercancel", 175, 125, 2)
    pointer("lostpointercapture", 175, 125, 2)
    expect(screen.queryByRole("toolbar")).toBeNull()
    pointer("pointerup", 175, 125, 1)
    expect(screen.getByText("400×300")).toBeInTheDocument()
    expect(screen.getByRole("toolbar")).toBeInTheDocument()
  })

  it("does nothing for a click outside windows despite a stale preview", () => {
    renderOverlay({ frame: WINDOW_FRAME })
    pointer("pointermove", 175, 125)
    pointer("pointerdown", 900, 550)
    pointer("pointerup", 900, 550)
    expect(cutout()).toBeNull()
    expect(screen.queryByRole("toolbar")).toBeNull()
  })

  it.each([FRAME, { ...FRAME, windows: [] }])(
    "preserves manual selection with absent or empty windows: %j",
    (frame) => {
      renderOverlay({ frame })
      pointer("pointermove", 175, 125)
      pointer("pointerdown", 175, 125)
      pointer("pointerup", 175, 125)
      expect(cutout()).toBeNull()
      expect(screen.queryByRole("toolbar")).toBeNull()
      drag([100, 50], [300, 150])
      expect(screen.getByText("200×100")).toBeInTheDocument()
      expect(screen.getByRole("toolbar")).toBeInTheDocument()
    }
  )

  it("exports a clicked window at its exact physical rect with fractional CSS and unequal scales", async () => {
    const { onConfirm } = renderOverlay({
      frame: {
        ...FRAME,
        width: 2500,
        height: 1800,
        windows: [{ x: 201, y: 101, width: 803, height: 605 }],
      },
    })
    await settle()
    pointer("pointerdown", 175, 125)
    pointer("pointerup", 175, 125)
    expect(screen.getByText("321×202")).toBeInTheDocument()
    fireEvent.click(screen.getByRole("button", { name: SE.copy }))
    await flush()
    expect(clipboardWrite).toHaveBeenCalledTimes(1)
    expect(onConfirm).toHaveBeenCalledTimes(1)
    expect(onConfirm.mock.calls[0][0]).toEqual({
      x: 201,
      y: 101,
      width: 803,
      height: 605,
    })
    expect(encodedSize).toEqual({ width: 803, height: 605 })
    const offset = calls.find(
      (call) => call.prop === "drawImage" && call.args.length === 3
    )
    expect(offset?.args.slice(1)).toEqual([-201, -101])
    const item = clipboardWrite.mock.calls[0][0][0]
    expect((await item.getType("image/png")).type).toBe("image/png")
  })
})

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
      SE.pin,
      SE.save,
      SE.copy,
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
      fireEvent.click(screen.getByRole("button", { name: SE.copy }))
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

  it("shows eight resize handles and resizes from the south-east one", async () => {
    renderOverlay()
    await settle()
    drag([100, 50], [300, 150])
    const handles = document.querySelectorAll("[data-resize-handle]")
    expect(handles).toHaveLength(8)
    expect(document.querySelector('[data-resize-handle="nw"]')).toHaveStyle({
      cursor: "nwse-resize",
    })
    expect(document.querySelector('[data-resize-handle="ne"]')).toHaveStyle({
      cursor: "nesw-resize",
    })
    expect(document.querySelector('[data-resize-handle="e"]')).toHaveStyle({
      cursor: "ew-resize",
    })
    expect(document.querySelector('[data-resize-handle="s"]')).toHaveStyle({
      cursor: "ns-resize",
    })

    // The south-east corner dragged out: the opposite corner stays put,
    // and the toolbar docks to the grown selection's new corner.
    handlePointer("se", "pointerdown", 300, 150)
    pointer("pointermove", 400, 200)
    pointer("pointerup", 400, 200)
    expect(screen.getByText("300×150")).toBeInTheDocument()
    const toolbar = screen.getByRole("toolbar", { name: SE.title })
    expect(toolbar.style.left).toBe("400px")
    expect(toolbar.style.top).toBe("208px")
  })

  it("normalizes a resize that crosses the anchor and holds the minimum size", async () => {
    renderOverlay()
    await settle()
    drag([100, 50], [300, 150])

    // The west handle dragged past the east edge flips the rect around it.
    handlePointer("w", "pointerdown", 100, 100)
    pointer("pointermove", 350, 100)
    pointer("pointerup", 350, 100)
    expect(screen.getByText("50×100")).toBeInTheDocument()

    // A handle dragged against its anchor stops at the 8 px floor.
    handlePointer("se", "pointerdown", 350, 150)
    pointer("pointermove", 305, 150)
    pointer("pointerup", 305, 150)
    expect(screen.getByText("8×100")).toBeInTheDocument()
  })

  it("clamps a handle drag to the viewport", async () => {
    renderOverlay()
    await settle()
    drag([100, 50], [300, 150])
    handlePointer("se", "pointerdown", 300, 150)
    pointer("pointermove", 2000, 1200)
    pointer("pointerup", 2000, 1200)
    expect(screen.getByText("900×550")).toBeInTheDocument()
  })

  it("moves the whole selection from a drag inside it, clamped to the viewport", async () => {
    const { onConfirm } = renderOverlay()
    await settle()
    drag([100, 50], [300, 150])

    // Inside the selection with the select tool: the drag hauls the whole
    // rect (+60, +30), toolbar and badge following.
    pointer("pointerdown", 200, 100)
    pointer("pointermove", 260, 130)
    pointer("pointerup", 260, 130)
    expect(screen.getByText("200×100")).toBeInTheDocument()
    const toolbar = screen.getByRole("toolbar", { name: SE.title })
    expect(toolbar.style.left).toBe("360px")
    expect(toolbar.style.top).toBe("188px")

    // Hauling past the right edge pins the selection against it.
    pointer("pointerdown", 200, 100)
    pointer("pointermove", 2000, 130)
    pointer("pointerup", 2000, 130)
    expect(screen.getByText("200×100")).toBeInTheDocument()

    // The moved rect converts to the frame's physical pixels on confirm.
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: SE.copy }))
    })
    await flush()
    expect(onConfirm.mock.calls[0][0]).toEqual({
      x: 1600,
      y: 220,
      width: 400,
      height: 200,
    })
  })

  it("still hauls a grabbed mark instead of the selection", async () => {
    renderOverlay()
    await settle()
    drag([100, 50], [300, 150])
    fireEvent.click(screen.getByRole("button", { name: SE.pen }))
    // A stroke at screen (150,80)–(160,90), i.e. frame (300,160)–(320,180).
    pointer("pointerdown", 150, 80)
    pointer("pointermove", 160, 90)
    pointer("pointerup", 160, 90)
    fireEvent.click(screen.getByRole("button", { name: SE.select }))

    // A press right on the stroke moves the mark; the selection and its
    // toolbar stay exactly where they were.
    calls.length = 0
    pointer("pointerdown", 155, 85)
    pointer("pointermove", 200, 120)
    pointer("pointerup", 200, 120)
    expect(screen.getByText("200×100")).toBeInTheDocument()
    expect(screen.getByRole("toolbar", { name: SE.title }).style.left).toBe(
      "300px"
    )
    expect(lineTos()).toEqual(["[410,250]"])
  })

  it("copies the annotated crop to the clipboard, then finishes", async () => {
    const { onConfirm } = renderOverlay()
    await settle()
    drag([100, 50], [300, 150])
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: SE.copy }))
    })
    await flush()
    expect(clipboardWrite).toHaveBeenCalledTimes(1)
    const item = clipboardWrite.mock.calls[0][0][0] as FakeClipboardItem
    expect(item.types).toEqual(["image/png"])
    const blob = await item.getType("image/png")
    expect(blob.type).toBe("image/png")
    // The finished capture still goes out with the physical rect and the
    // PNG Rust pins against it.
    expect(onConfirm).toHaveBeenCalledTimes(1)
    expect(onConfirm.mock.calls[0][0]).toEqual({
      x: 200,
      y: 100,
      width: 400,
      height: 200,
    })
    expect(atob(onConfirm.mock.calls[0][1] as string).length).toBe(64)
  })

  it("keeps the overlay open when the clipboard refuses the picture", async () => {
    const consoleError = vi.spyOn(console, "error").mockImplementation(() => {})
    const { onConfirm } = renderOverlay()
    await settle()
    drag([100, 50], [300, 150])
    clipboardWrite.mockRejectedValueOnce(new Error("denied"))
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: SE.copy }))
    })
    await flush()
    expect(onConfirm).not.toHaveBeenCalled()
    // The toolbar is still up: the capture can be pressed again.
    expect(screen.getByRole("toolbar", { name: SE.title })).toBeInTheDocument()
    expect(consoleError).toHaveBeenCalled()
    expect(screen.getByRole("alert")).toHaveTextContent(SE.failed)
    expect(screen.getByText("200×100")).toBeInTheDocument()
  })

  it("pins the annotated crop at its absolute screen position, then finishes", async () => {
    const { onConfirm } = renderOverlay()
    await settle()
    drag([100, 50], [300, 150])
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: SE.pin }))
    })
    await flush()
    // The pin window is placed at the overlay window's own physical
    // position (100, 50) plus the selection's offset inside it (200, 100),
    // sized to the selection's physical size.
    expect(tauri.screenshotPin).toHaveBeenCalledTimes(1)
    expect(tauri.screenshotPin.mock.calls[0].slice(1)).toEqual([
      300, 150, 400, 200,
    ])
    // The capture finishes through the same confirm path as the copy
    // button, carrying the same picture that was pinned.
    expect(onConfirm).toHaveBeenCalledTimes(1)
    expect(onConfirm.mock.calls[0][0]).toEqual({
      x: 200,
      y: 100,
      width: 400,
      height: 200,
    })
    expect(tauri.screenshotPin.mock.calls[0][0]).toBe(
      onConfirm.mock.calls[0][1]
    )
    expect(atob(tauri.screenshotPin.mock.calls[0][0] as string).length).toBe(64)
  })

  it("keeps the overlay open when the pin fails", async () => {
    const consoleError = vi.spyOn(console, "error").mockImplementation(() => {})
    const { onConfirm } = renderOverlay()
    await settle()
    drag([100, 50], [300, 150])
    tauri.screenshotPin.mockRejectedValueOnce(new Error("pin refused"))
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: SE.pin }))
    })
    await flush()
    // A pin that never landed does not finish the capture either.
    expect(onConfirm).not.toHaveBeenCalled()
    // The toolbar is still up: the capture can be pressed again.
    expect(screen.getByRole("toolbar", { name: SE.title })).toBeInTheDocument()
    expect(consoleError).toHaveBeenCalled()
    expect(screen.getByRole("alert")).toHaveTextContent(SE.failed)
    expect(screen.getByText("200×100")).toBeInTheDocument()
  })

  it("saves the annotated crop through the dialog and the save command", async () => {
    const { onConfirm } = renderOverlay()
    await settle()
    drag([100, 50], [300, 150])
    dialog.save.mockResolvedValueOnce("D:\\shots\\capture.png")
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: SE.save }))
    })
    await flush()
    expect(dialog.save).toHaveBeenCalledTimes(1)
    expect(dialog.save.mock.calls[0][0].defaultPath).toMatch(
      /^截图_\d{8}-\d{6}\.png$/
    )
    expect(dialog.save.mock.calls[0][0].filters).toEqual([
      { name: "PNG", extensions: ["png"] },
    ])
    expect(tauri.screenshotSaveToFile).toHaveBeenCalledTimes(1)
    expect(
      atob(tauri.screenshotSaveToFile.mock.calls[0][0] as string).length
    ).toBe(64)
    expect(tauri.screenshotSaveToFile.mock.calls[0][1]).toBe(
      "D:\\shots\\capture.png"
    )
    // Saving is not finishing: the overlay stays up.
    expect(onConfirm).not.toHaveBeenCalled()
    expect(screen.getByRole("toolbar", { name: SE.title })).toBeInTheDocument()
  })

  it("disables output until the frozen picture has decoded", async () => {
    const { onConfirm, onCancel } = renderOverlay()
    drag([100, 50], [300, 150])
    for (const name of [SE.copy, SE.pin, SE.save]) {
      expect(screen.getByRole("button", { name })).toBeDisabled()
      fireEvent.click(screen.getByRole("button", { name }))
    }
    expect(onConfirm).not.toHaveBeenCalled()
    expect(onCancel).not.toHaveBeenCalled()
    expect(clipboardWrite).not.toHaveBeenCalled()
    await settle()
    for (const name of [SE.copy, SE.pin, SE.save]) {
      expect(screen.getByRole("button", { name })).toBeEnabled()
    }
  })

  it("shows a failed image load without copying or finishing the selection", async () => {
    const { onConfirm, onCancel } = renderOverlay({
      frame: { ...FRAME, pngBase64: "broken" },
    })
    await settle()
    expect(screen.getByRole("alert")).toHaveTextContent(SE.failed)
    drag([100, 50], [300, 150])
    for (const name of [SE.copy, SE.pin, SE.save]) {
      expect(screen.getByRole("button", { name })).toBeDisabled()
      fireEvent.click(screen.getByRole("button", { name }))
    }
    expect(onConfirm).not.toHaveBeenCalled()
    expect(onCancel).not.toHaveBeenCalled()
    expect(clipboardWrite).not.toHaveBeenCalled()
    expect(tauri.screenshotPin).not.toHaveBeenCalled()
    expect(dialog.save).not.toHaveBeenCalled()
    expect(screen.getByText("200×100")).toBeInTheDocument()
  })

  it("shows an export failure and clears it when a retry succeeds", async () => {
    vi.spyOn(console, "error").mockImplementation(() => {})
    const { onConfirm } = renderOverlay()
    await settle()
    drag([100, 50], [300, 150])
    encode = () => null
    fireEvent.click(screen.getByRole("button", { name: SE.copy }))
    await flush()
    expect(screen.getByRole("alert")).toHaveTextContent(SE.failed)
    expect(onConfirm).not.toHaveBeenCalled()
    expect(clipboardWrite).not.toHaveBeenCalled()
    expect(screen.getByRole("button", { name: SE.copy })).toBeEnabled()
    expect(screen.getByText("200×100")).toBeInTheDocument()

    encode = (type) => new Blob([new Uint8Array(64)], { type })
    fireEvent.click(screen.getByRole("button", { name: SE.copy }))
    await flush()
    expect(screen.queryByRole("alert")).toBeNull()
    expect(onConfirm).toHaveBeenCalledTimes(1)
  })

  it.each([SE.copy, SE.pin])(
    "%s keeps outputs and editing locked until finishing settles",
    async (name) => {
      let finish!: () => void
      const pending = new Promise<void>((resolve) => {
        finish = resolve
      })
      const onConfirm = vi.fn(() => pending)
      const { onCancel } = renderOverlay({ onConfirm })
      await settle()
      drag([100, 50], [300, 150])
      const button = screen.getByRole("button", { name })
      // These clicks share a render: the gate must close synchronously.
      act(() => {
        button.dispatchEvent(new MouseEvent("click", { bubbles: true }))
        button.dispatchEvent(new MouseEvent("click", { bubbles: true }))
        screen.getByRole("button", { name: SE.save }).click()
      })
      await flush()
      expect(onConfirm).toHaveBeenCalledTimes(1)
      expect(clipboardWrite).toHaveBeenCalledTimes(name === SE.copy ? 1 : 0)
      expect(tauri.screenshotPin).toHaveBeenCalledTimes(name === SE.pin ? 1 : 0)
      expect(dialog.save).not.toHaveBeenCalled()
      for (const button of screen.getAllByRole("button")) {
        expect(button).toBeDisabled()
      }
      drag([200, 100], [260, 130])
      handlePointer("se", "pointerdown", 300, 150)
      pointer("pointermove", 400, 200)
      pointer("pointerup", 400, 200)
      fireEvent.keyDown(window, { key: "Escape" })
      expect(onCancel).not.toHaveBeenCalled()
      expect(screen.getByText("200×100")).toBeInTheDocument()
      expect(document.querySelectorAll("[data-resize-handle]")).toHaveLength(8)
      await act(async () => finish())
      expect(screen.getByRole("button", { name })).toBeEnabled()
    }
  )

  it.each([SE.copy, SE.pin])(
    "%s shows a rejected finish and retains the selection",
    async (name) => {
      vi.spyOn(console, "error").mockImplementation(() => {})
      let rejectFinish!: (error: Error) => void
      const pending = new Promise<void>((_resolve, reject) => {
        rejectFinish = reject
      })
      // Keep the red run's unawaited promise from becoming an unrelated
      // unhandled rejection; the UI must still observe the original one.
      void pending.catch(() => {})
      const onConfirm = vi.fn(() => pending)
      renderOverlay({ onConfirm })
      await settle()
      drag([100, 50], [300, 150])
      fireEvent.click(screen.getByRole("button", { name }))
      await flush()
      expect(onConfirm).toHaveBeenCalledTimes(1)
      await act(async () => rejectFinish(new Error("finish refused")))
      expect(screen.getByRole("alert")).toHaveTextContent(SE.failed)
      expect(screen.getByRole("button", { name })).toBeEnabled()
      expect(screen.getByText("200×100")).toBeInTheDocument()
    }
  )

  it.each(["dialog", "write"])("shows a save %s failure", async (stage) => {
    vi.spyOn(console, "error").mockImplementation(() => {})
    const { onConfirm } = renderOverlay()
    await settle()
    drag([100, 50], [300, 150])
    if (stage === "dialog") {
      dialog.save.mockRejectedValueOnce(new Error("dialog refused"))
    } else {
      dialog.save.mockResolvedValueOnce("D:\\shots\\capture.png")
      tauri.screenshotSaveToFile.mockRejectedValueOnce(new Error("disk full"))
    }
    fireEvent.click(screen.getByRole("button", { name: SE.save }))
    await flush()
    expect(screen.getByRole("alert")).toHaveTextContent(SE.failed)
    expect(onConfirm).not.toHaveBeenCalled()
    expect(screen.getByRole("button", { name: SE.save })).toBeEnabled()
    expect(screen.getByText("200×100")).toBeInTheDocument()
  })

  it("saves nothing when the save dialog is cancelled", async () => {
    const { onConfirm } = renderOverlay()
    await settle()
    drag([100, 50], [300, 150])
    dialog.save.mockResolvedValueOnce(null)
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: SE.save }))
    })
    await flush()
    expect(tauri.screenshotSaveToFile).not.toHaveBeenCalled()
    expect(onConfirm).not.toHaveBeenCalled()
    expect(screen.getByRole("toolbar", { name: SE.title })).toBeInTheDocument()
  })
})
