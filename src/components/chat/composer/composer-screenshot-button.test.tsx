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
  screenshotRequestPermission: vi.fn(),
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
  vi.mocked(tauri.screenshotRequestPermission).mockReset()
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

  it("offers an explicit grant action for a structured screen permission error", async () => {
    renderButton()
    tauri.screenshotBeginSelection.mockRejectedValue({
      code: "permission_denied",
      message: "Screen recording permission is required",
      detail: "Grant Screen Recording to the screenshot helper",
    })
    tauri.screenshotRequestPermission.mockResolvedValue(undefined)

    await userEvent.click(screen.getByRole("button", { name: MI.screenshot }))
    await userEvent.click(screen.getByRole("menuitem", { name: MI.screenshot }))
    await waitFor(() => expect(toast.error).toHaveBeenCalled())
    expect(tauri.screenshotRequestPermission).not.toHaveBeenCalled()
    const calls = vi.mocked(toast.error).mock.calls
    const call = calls[calls.length - 1]
    expect(call[0]).toBe(SE.permissionDenied)
    expect(call[1]?.action).toEqual(
      expect.objectContaining({ onClick: expect.any(Function) })
    )
    const action = call[1]!.action
    if (!action || typeof action !== "object" || !("onClick" in action)) {
      throw new Error("permission toast has no action")
    }
    render(<button onClick={action.onClick}>Grant permission</button>)
    await userEvent.click(screen.getByRole("button", { name: "Grant permission" }))
    await waitFor(() =>
      expect(tauri.screenshotRequestPermission).toHaveBeenCalledTimes(1)
    )
    // Requesting a grant neither assumes success nor starts another capture.
    expect(tauri.screenshotBeginSelection).toHaveBeenCalledTimes(1)
  })

  it("keeps permission-request failures visible", async () => {
    renderButton()
    tauri.screenshotBeginSelection.mockRejectedValue({
      code: "permission_denied",
      message: "Screen recording permission is required",
    })
    tauri.screenshotRequestPermission.mockRejectedValue(
      new Error("helper unavailable")
    )
    await userEvent.click(screen.getByRole("button", { name: MI.screenshot }))
    await userEvent.click(screen.getByRole("menuitem", { name: MI.screenshot }))
    await waitFor(() => expect(toast.error).toHaveBeenCalled())
    const calls = vi.mocked(toast.error).mock.calls
    const options = calls[calls.length - 1][1]!
    const action = options.action
    if (!action || typeof action !== "object" || !("onClick" in action)) {
      throw new Error("permission toast has no action")
    }
    render(<button onClick={action.onClick}>Grant permission</button>)
    await userEvent.click(screen.getByRole("button", { name: "Grant permission" }))
    await waitFor(() => expect(toast.error).toHaveBeenCalledWith(SE.failed))
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
