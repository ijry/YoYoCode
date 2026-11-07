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
import { MARKED_IMAGE_MAX_BYTES } from "@/lib/browser/screenshot-markup"

import { ScreenshotEditorDialog } from "./screenshot-editor-dialog"

const SE = enMessages.Folder.chat.screenshotEditor

const toasts = vi.hoisted(() => ({ error: vi.fn() }))
vi.mock("sonner", () => ({ toast: toasts }))

/** A picture that loads on the next tick — or fails to, for a `src` that
 *  says so. jsdom decodes no images of its own. */
class LoadingImage {
  onload: (() => void) | null = null
  onerror: (() => void) | null = null
  naturalWidth = 400
  naturalHeight = 250
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

// Every 2d call the sheet makes, the offscreen canvases included, in order.
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

// A 400×250 picture shown at 800×500 on screen: one screen pixel is half a
// picture pixel, so a drag of (50, 50) on the preview is (25, 25) in the
// picture. The picture sits away from the window's corner, as it does in a
// dialog; positions below are relative to its top-left corner on screen.
const ORIGIN = { x: 40, y: 30 }

const PAYLOAD = {
  pngBase64: "aGVsbG8=",
  width: 400,
  height: 250,
  mime: "image/png",
}

let encode: (type: string) => Blob | null

beforeEach(() => {
  calls.length = 0
  encode = (type) => new Blob([new Uint8Array(64)], { type })
  toasts.error.mockClear()
  vi.stubGlobal("Image", LoadingImage)
  vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue(
    fakeContext()
  )
  vi.spyOn(HTMLCanvasElement.prototype, "toBlob").mockImplementation(function (
    callback,
    type = "image/png"
  ) {
    callback(encode(type))
  })
  vi.spyOn(
    HTMLCanvasElement.prototype,
    "getBoundingClientRect"
  ).mockReturnValue({
    left: ORIGIN.x,
    top: ORIGIN.y,
    right: ORIGIN.x + 800,
    bottom: ORIGIN.y + 500,
    width: 800,
    height: 500,
    x: ORIGIN.x,
    y: ORIGIN.y,
    toJSON: () => ({}),
  } as DOMRect)
})

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

function renderDialog(
  props: Partial<React.ComponentProps<typeof ScreenshotEditorDialog>> = {}
) {
  const onConfirm = vi.fn()
  const onOpenChange = vi.fn()
  const view = render(
    <NextIntlClientProvider locale="en" messages={enMessages}>
      <ScreenshotEditorDialog
        open
        onOpenChange={onOpenChange}
        payload={PAYLOAD}
        onConfirm={onConfirm}
        {...props}
      />
    </NextIntlClientProvider>
  )
  return { ...view, onConfirm, onOpenChange }
}

async function settle() {
  await act(async () => {
    await new Promise((resolve) => setTimeout(resolve, 0))
  })
}

function canvas(): HTMLCanvasElement {
  return screen.getByRole("img", { name: SE.title })
}

function button(name: string) {
  return screen.getByRole("button", { name })
}

/** The distinct shapes of one kind the sheet painted, as "x,y,w,h" — every
 *  outline is drawn twice (white edge, then colour) over the same bounds,
 *  so one mark is one entry, not two. */
function drawnShapes(name: string): string[] {
  return [
    ...new Set(
      calls
        .filter((call) => call.prop === name)
        .map((call) => JSON.stringify(call.args))
    ),
  ]
}

// jsdom has no `PointerEvent`; a mouse event under the pointer's name, with
// the pointer's id on it, carries everything the sheet reads. `x`/`y` are
// relative to the picture's top-left corner on screen.
function pointer(
  type: string,
  x: number,
  y: number,
  target: Element = canvas(),
  pointerId = 1
) {
  const event = new MouseEvent(type, {
    bubbles: true,
    cancelable: true,
    button: 0,
    clientX: ORIGIN.x + x,
    clientY: ORIGIN.y + y,
  })
  Object.defineProperty(event, "pointerId", { value: pointerId })
  fireEvent(target, event)
}

function drag(from: [number, number], to: [number, number]) {
  pointer("pointerdown", ...from)
  pointer("pointermove", (from[0] + to[0]) / 2, (from[1] + to[1]) / 2)
  // The half-drawn shapes stay out of the way of what the tests assert on.
  calls.length = 0
  pointer("pointermove", ...to)
  pointer("pointerup", ...to)
}

describe("ScreenshotEditorDialog", () => {
  it("opens on the eight tools, the palette and the history buttons", async () => {
    renderDialog()
    await settle()
    expect(canvas()).toHaveAttribute("width", "400")
    expect(canvas()).toHaveAttribute("height", "250")
    for (const label of [
      SE.select,
      SE.pen,
      SE.rect,
      SE.ellipse,
      SE.arrow,
      SE.text,
      SE.mosaic,
      SE.step,
    ]) {
      expect(button(label)).toBeInTheDocument()
    }
    expect(button(SE.rect)).toHaveAttribute("aria-pressed", "true")
    expect(button(SE.undo)).toBeDisabled()
    expect(button(SE.redo)).toBeDisabled()
    expect(button(SE.clear)).toBeDisabled()
    expect(
      within(screen.getByRole("group", { name: SE.color })).getAllByRole(
        "button"
      )
    ).toHaveLength(5)
    // The crop controls appear only while cropping, and Apply waits for a
    // rectangle to apply.
    expect(screen.queryByRole("button", { name: SE.applyCrop })).toBeNull()
    fireEvent.click(button(SE.crop))
    expect(button(SE.applyCrop)).toBeDisabled()
    expect(button(SE.cancelCrop)).toBeEnabled()
    fireEvent.click(button(SE.cancelCrop))
    expect(screen.queryByRole("button", { name: SE.applyCrop })).toBeNull()
  })

  it("draws a rect, undoes it, redoes it and clears it", async () => {
    renderDialog()
    await settle()
    drag([10, 10], [60, 60])
    // (10,10)–(60,60) on screen is (5,5)–(30,30) in the picture.
    expect(drawnShapes("rect")).toEqual(["[5,5,25,25]"])
    expect(button(SE.undo)).toBeEnabled()

    calls.length = 0
    fireEvent.click(button(SE.undo))
    expect(drawnShapes("rect")).toEqual([])
    expect(button(SE.redo)).toBeEnabled()

    calls.length = 0
    fireEvent.click(button(SE.redo))
    expect(drawnShapes("rect")).toEqual(["[5,5,25,25]"])

    calls.length = 0
    fireEvent.click(button(SE.clear))
    expect(drawnShapes("rect")).toEqual([])
    expect(button(SE.redo)).toBeDisabled()
    // Clearing is one more state, so it is undoable too.
    expect(button(SE.undo)).toBeEnabled()
  })

  it("moves one mark with the select tool, and only that one", async () => {
    renderDialog()
    await settle()
    drag([10, 10], [60, 60]) // the rect now spans (5,5)–(30,30) in the picture
    fireEvent.click(button(SE.select))
    // Down on the rect's corner, where the grab slack matters.
    pointer("pointerdown", 60, 60)
    calls.length = 0
    pointer("pointermove", 80, 80)
    pointer("pointerup", 80, 80)
    // (30,30)→(40,40) in the picture: the mark moved by (10,10).
    expect(drawnShapes("rect")).toEqual(["[15,15,25,25]"])

    calls.length = 0
    fireEvent.click(button(SE.undo))
    expect(drawnShapes("rect")).toEqual(["[5,5,25,25]"])
  })

  it("applies a crop: the picture shrinks and the marks move with it", async () => {
    renderDialog()
    await settle()
    drag([10, 10], [60, 60]) // the rect now spans (5,5)–(30,30) in the picture
    fireEvent.click(button(SE.crop))
    pointer("pointerdown", 4, 4) // picture (2,2)
    pointer("pointermove", 200, 125)
    pointer("pointerup", 400, 250) // picture (200,125)
    expect(button(SE.applyCrop)).toBeEnabled()

    calls.length = 0
    fireEvent.click(button(SE.applyCrop))
    expect(screen.getByText("198×123")).toBeInTheDocument()
    // The marks moved back by where the crop begins: (5,5) → (3,3).
    expect(drawnShapes("rect")).toContain("[3,3,25,25]")
  })

  it("attaches the annotated picture as a PNG file on confirm", async () => {
    const { onConfirm, onOpenChange } = renderDialog()
    await settle()
    drag([10, 10], [60, 60])
    await act(async () => {
      fireEvent.click(button(SE.confirm))
      await new Promise((resolve) => setTimeout(resolve, 0))
    })
    expect(onConfirm).toHaveBeenCalledTimes(1)
    const file = onConfirm.mock.calls[0][0] as File
    expect(file).toBeInstanceOf(File)
    expect(file.name).toBe("screenshot.png")
    expect(file.type).toBe("image/png")
    expect(onOpenChange).not.toHaveBeenCalled()
  })

  it("falls back to JPEG when the PNG would be over the model's budget", async () => {
    encode = (type) =>
      new Blob(
        [
          new Uint8Array(
            type === "image/png" ? MARKED_IMAGE_MAX_BYTES + 1 : 64
          ),
        ],
        { type }
      )
    const { onConfirm } = renderDialog()
    await settle()
    await act(async () => {
      fireEvent.click(button(SE.confirm))
      await new Promise((resolve) => setTimeout(resolve, 0))
    })
    const encodedTypes = vi
      .mocked(HTMLCanvasElement.prototype.toBlob)
      .mock.calls.map((call) => call[1])
    expect(encodedTypes).toContain("image/jpeg")
    const file = onConfirm.mock.calls[0][0] as File
    expect(file.type).toBe("image/jpeg")
    expect(file.name).toBe("screenshot.jpg")
  })

  it("hands over the capture as it arrived when the picture never decoded", async () => {
    const { onConfirm } = renderDialog({
      payload: { ...PAYLOAD, pngBase64: "broken" },
    })
    await settle()
    // Nothing was decoded, so nothing was drawn anywhere.
    expect(drawnShapes("drawImage")).toEqual([])
    await act(async () => {
      fireEvent.click(button(SE.confirm))
      await new Promise((resolve) => setTimeout(resolve, 0))
    })
    expect(onConfirm).toHaveBeenCalledTimes(1)
    const file = onConfirm.mock.calls[0][0] as File
    expect(file.type).toBe("image/png")
    expect(file.name).toBe("screenshot.png")
  })

  it("says so, and keeps the sheet, when the picture cannot be encoded", async () => {
    encode = () => null
    const { onConfirm, onOpenChange } = renderDialog()
    await settle()
    drag([10, 10], [60, 60])
    await act(async () => {
      fireEvent.click(button(SE.confirm))
      await new Promise((resolve) => setTimeout(resolve, 0))
    })
    expect(toasts.error).toHaveBeenCalledWith(SE.failed)
    expect(onConfirm).not.toHaveBeenCalled()
    expect(onOpenChange).not.toHaveBeenCalled()
    expect(screen.getByRole("dialog")).toBeInTheDocument()
  })

  it("offers nothing to edit without a capture", () => {
    const { container } = renderDialog({ payload: null })
    expect(container.querySelector("canvas")).toBeNull()
    expect(screen.queryByRole("dialog")).toBeNull()
  })
})
