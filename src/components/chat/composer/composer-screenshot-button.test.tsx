import { cleanup, render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { NextIntlClientProvider } from "next-intl"
import { toast } from "sonner"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import enMessages from "@/i18n/messages/en.json"

import { ComposerScreenshotButton } from "./composer-screenshot-button"

const MI = enMessages.Folder.chat.messageInput
const SE = enMessages.Folder.chat.screenshotEditor

// The desktop gate is the test's control: the capture itself runs in the
// backend's own overlay windows, so there is no event flow to fake here.
const platform = vi.hoisted(() => ({
  isLocalDesktop: vi.fn(() => true),
}))
vi.mock("@/lib/platform", () => ({
  isLocalDesktop: platform.isLocalDesktop,
}))

const tauri = vi.hoisted(() => ({
  screenshotBeginSelection: vi.fn(),
}))
vi.mock("@/lib/tauri", () => tauri)

vi.mock("sonner", () => ({ toast: { error: vi.fn() } }))

function renderButton(
  props: Partial<React.ComponentProps<typeof ComposerScreenshotButton>> = {}
) {
  const view = render(
    <NextIntlClientProvider locale="en" messages={enMessages}>
      <ComposerScreenshotButton {...props} />
    </NextIntlClientProvider>
  )
  return view
}

/** Opens the menu and starts the first capture. */
async function startCapture() {
  vi.mocked(tauri.screenshotBeginSelection).mockResolvedValue({
    captureId: "cap-1",
    requestId: "echoed-back",
    monitors: [],
  })
  await userEvent.click(screen.getByRole("button", { name: MI.screenshot }))
  await userEvent.click(screen.getByRole("menuitem", { name: MI.screenshot }))
}

beforeEach(() => {
  platform.isLocalDesktop.mockReturnValue(true)
  vi.mocked(tauri.screenshotBeginSelection).mockReset()
  vi.mocked(toast.error).mockClear()
})

afterEach(cleanup)

describe("ComposerScreenshotButton", () => {
  it("renders nothing outside a local desktop window", () => {
    platform.isLocalDesktop.mockReturnValue(false)
    const { container } = renderButton()
    expect(container).toBeEmptyDOMElement()
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
