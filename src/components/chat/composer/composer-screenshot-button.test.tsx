import { act, cleanup, render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { NextIntlClientProvider } from "next-intl"
import { toast } from "sonner"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import enMessages from "@/i18n/messages/en.json"
import type { ScreenshotCapturedEvent } from "@/lib/screenshot/types"

import { ComposerScreenshotButton } from "./composer-screenshot-button"

const MI = enMessages.Folder.chat.messageInput
const SE = enMessages.Folder.chat.screenshotEditor

// The desktop gate and the backend event delivery are both under the test's
// control: `events.handler` is whatever the component subscribed with.
const platform = vi.hoisted(() => ({
  isLocalDesktop: vi.fn(() => true),
  subscribe: vi.fn(() => Promise.resolve(() => {})),
}))
const events = vi.hoisted(() => ({
  handler: null as ((event: ScreenshotCapturedEvent) => void) | null,
}))
vi.mock("@/lib/platform", () => ({
  isLocalDesktop: platform.isLocalDesktop,
  subscribe: (
    _event: string,
    handler: (event: ScreenshotCapturedEvent) => void
  ) => {
    events.handler = handler
    return Promise.resolve(() => {})
  },
}))

const tauri = vi.hoisted(() => ({
  screenshotBeginSelection: vi.fn(),
  screenshotTakeCropped: vi.fn(),
}))
vi.mock("@/lib/tauri", () => tauri)

vi.mock("sonner", () => ({ toast: { error: vi.fn() } }))

function renderButton(
  props: Partial<React.ComponentProps<typeof ComposerScreenshotButton>> = {}
) {
  const onAttachImage = vi.fn(async (_file: File) => {})
  const view = render(
    <NextIntlClientProvider locale="en" messages={enMessages}>
      <ComposerScreenshotButton
        onAttachImage={props.onAttachImage ?? onAttachImage}
        {...props}
      />
    </NextIntlClientProvider>
  )
  return { ...view, onAttachImage }
}

/** Opens the menu, starts the first capture and answers the request id. */
async function startCapture(): Promise<string> {
  vi.mocked(tauri.screenshotBeginSelection).mockResolvedValue({
    captureId: "cap-1",
    requestId: "echoed-back",
    monitors: [],
  })
  await userEvent.click(screen.getByRole("button", { name: MI.screenshot }))
  await userEvent.click(screen.getByRole("menuitem", { name: MI.screenshot }))
  return vi.mocked(tauri.screenshotBeginSelection).mock.calls[0][0]
}

const CROPPED = {
  pngBase64: "aGVsbG8=",
  width: 640,
  height: 400,
  mime: "image/png",
}

function capturedEvent(requestId: string): ScreenshotCapturedEvent {
  return {
    captureId: "cap-1",
    requestId,
    width: CROPPED.width,
    height: CROPPED.height,
    mime: CROPPED.mime,
  }
}

beforeEach(() => {
  platform.isLocalDesktop.mockReturnValue(true)
  platform.subscribe.mockClear()
  events.handler = null
  vi.mocked(tauri.screenshotBeginSelection).mockReset()
  vi.mocked(tauri.screenshotTakeCropped).mockReset()
  vi.mocked(toast.error).mockClear()
})

afterEach(cleanup)

describe("ComposerScreenshotButton", () => {
  it("renders nothing outside a local desktop window", () => {
    platform.isLocalDesktop.mockReturnValue(false)
    const { container } = renderButton()
    expect(container).toBeEmptyDOMElement()
    expect(platform.subscribe).not.toHaveBeenCalled()
  })

  it("offers screenshot and hide-window capture on a desktop", async () => {
    renderButton()
    expect(
      screen.getByRole("button", { name: MI.screenshot })
    ).toBeInTheDocument()
    await userEvent.click(screen.getByRole("button", { name: MI.screenshot }))
    expect(
      screen.getByRole("menuitem", { name: MI.screenshot })
    ).toBeInTheDocument()
    expect(
      screen.getByRole("menuitem", { name: MI.screenshotHideWindow })
    ).toBeInTheDocument()
  })

  it("starts a capture with the window up from the first item", async () => {
    renderButton()
    await startCapture()
    expect(tauri.screenshotBeginSelection).toHaveBeenCalledTimes(1)
    expect(tauri.screenshotBeginSelection).toHaveBeenCalledWith(
      expect.any(String),
      false
    )
  })

  it("starts a capture with the window hidden from the second item", async () => {
    renderButton()
    vi.mocked(tauri.screenshotBeginSelection).mockResolvedValue({
      captureId: "cap-1",
      requestId: "echoed-back",
      monitors: [],
    })
    await userEvent.click(screen.getByRole("button", { name: MI.screenshot }))
    await userEvent.click(
      screen.getByRole("menuitem", { name: MI.screenshotHideWindow })
    )
    expect(tauri.screenshotBeginSelection).toHaveBeenCalledWith(
      expect.any(String),
      true
    )
  })

  it("attaches the finished capture without a second editor", async () => {
    vi.mocked(tauri.screenshotTakeCropped).mockResolvedValue(CROPPED)
    const { onAttachImage } = renderButton()
    const requestId = await startCapture()

    await act(async () => {
      events.handler?.(capturedEvent(requestId))
    })

    expect(tauri.screenshotTakeCropped).toHaveBeenCalledWith("cap-1")
    await waitFor(() => expect(onAttachImage).toHaveBeenCalledTimes(1))
    const file = onAttachImage.mock.calls[0][0] as File
    expect(file).toBeInstanceOf(File)
    expect(file.name).toBe("screenshot.png")
    expect(file.type).toBe("image/png")
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument()
  })

  it("ignores captures from another request", async () => {
    vi.mocked(tauri.screenshotTakeCropped).mockResolvedValue(CROPPED)
    const { onAttachImage } = renderButton()
    await startCapture()

    await act(async () => {
      events.handler?.(capturedEvent("someone-else"))
    })

    expect(tauri.screenshotTakeCropped).not.toHaveBeenCalled()
    expect(onAttachImage).not.toHaveBeenCalled()
  })

  it("reports a failure when the finished capture cannot be pulled", async () => {
    vi.mocked(tauri.screenshotTakeCropped).mockRejectedValue(new Error("boom"))
    const { onAttachImage } = renderButton()
    const requestId = await startCapture()

    await act(async () => {
      events.handler?.(capturedEvent(requestId))
    })

    await waitFor(() => expect(toast.error).toHaveBeenCalledWith(SE.failed))
    expect(onAttachImage).not.toHaveBeenCalled()
  })

  it("reports a failure when attaching the capture fails", async () => {
    vi.mocked(tauri.screenshotTakeCropped).mockResolvedValue(CROPPED)
    const onAttachImage = vi.fn(async () => {
      throw new Error("boom")
    })
    renderButton({ onAttachImage })
    const requestId = await startCapture()

    await act(async () => {
      events.handler?.(capturedEvent(requestId))
    })

    await waitFor(() => expect(toast.error).toHaveBeenCalledWith(SE.failed))
    expect(tauri.screenshotTakeCropped).toHaveBeenCalledWith("cap-1")
  })

  it("reports a permission refusal distinctly from a generic failure", async () => {
    renderButton()
    vi.mocked(tauri.screenshotBeginSelection)
      .mockRejectedValueOnce(new Error("screen recording permission denied"))
      .mockRejectedValueOnce(new Error("boom"))

    await userEvent.click(screen.getByRole("button", { name: MI.screenshot }))
    await userEvent.click(screen.getByRole("menuitem", { name: MI.screenshot }))
    await waitFor(() =>
      expect(toast.error).toHaveBeenCalledWith(SE.permissionDenied)
    )

    await userEvent.click(screen.getByRole("button", { name: MI.screenshot }))
    await userEvent.click(screen.getByRole("menuitem", { name: MI.screenshot }))
    await waitFor(() => expect(toast.error).toHaveBeenCalledWith(SE.failed))
  })
})
