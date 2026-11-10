import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react"
import { renderToStaticMarkup } from "react-dom/server"
import { QRCodeSVG } from "qrcode.react"
import userEvent from "@testing-library/user-event"
import { NextIntlClientProvider } from "next-intl"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import enMessages from "@/i18n/messages/en.json"

const api = vi.hoisted(() => ({
  getWebServerStatus: vi.fn(),
  getWebServiceConfig: vi.fn(),
  updateWebServiceConfig: vi.fn(),
  startWebServer: vi.fn(),
  stopWebServer: vi.fn(),
  probeWebServicePort: vi.fn(),
  getTailscaleFunnelStatus: vi.fn(),
  enableTailscaleFunnel: vi.fn(),
  disableTailscaleFunnel: vi.fn(),
  startTailscaleLogin: vi.fn(),
  cancelTailscaleFunnelOperation: vi.fn(),
}))
const openUrl = vi.hoisted(() => vi.fn())
vi.mock("@/lib/api", () => api)
vi.mock("@/lib/platform", () => ({ openUrl }))

import { WebServiceSettings } from "./web-service-settings"

const PUBLIC_URL = "https://workstation.example.ts.net"
const LOCAL_URL = "http://127.0.0.1:3080"
const enabled = {
  state: "enabled",
  installed: true,
  enabled: true,
  desiredEnabled: true,
  owned: true,
  url: PUBLIC_URL,
  authUrl: null,
  error: null,
}
const disabled = {
  ...enabled,
  state: "ready",
  enabled: false,
  desiredEnabled: false,
  owned: false,
  url: null,
}

function renderSettings() {
  return render(
    <NextIntlClientProvider locale="en" messages={enMessages}>
      <WebServiceSettings />
    </NextIntlClientProvider>
  )
}

beforeEach(() => {
  vi.resetAllMocks()
  window.localStorage.clear()
  api.getWebServerStatus.mockResolvedValue({
    port: 3080,
    token: "private-token-never-in-url",
    addresses: [LOCAL_URL, "http://192.168.1.2:3080"],
  })
  api.getWebServiceConfig.mockResolvedValue({
    port: 3080,
    token: null,
    autoStart: false,
  })
  api.getTailscaleFunnelStatus.mockResolvedValue(disabled)
  api.enableTailscaleFunnel.mockResolvedValue(enabled)
  api.disableTailscaleFunnel.mockResolvedValue(disabled)
  api.updateWebServiceConfig.mockResolvedValue({})
  api.probeWebServicePort.mockRejectedValue(new Error("unavailable"))
})
afterEach(() => {
  cleanup()
  vi.useRealTimers()
  vi.restoreAllMocks()
})

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((done) => {
    resolve = done
  })
  return { promise, resolve }
}

describe("Web service Funnel addresses", () => {
  it("prefers a confirmed public URL over a remembered local address", async () => {
    window.localStorage.setItem("webService.displayHost", "127.0.0.1")
    api.getTailscaleFunnelStatus.mockResolvedValue(enabled)
    const user = userEvent.setup()
    renderSettings()
    await waitFor(() =>
      expect(screen.getByRole("combobox")).toHaveTextContent(PUBLIC_URL)
    )
    await user.click(screen.getByRole("button", { name: "Open", exact: true }))
    expect(openUrl).toHaveBeenCalledWith(PUBLIC_URL)
    await user.click(screen.getByRole("combobox"))
    expect(screen.getAllByRole("option")[0]).toHaveTextContent(PUBLIC_URL)
  })

  it.each(["starting", "stopped"])(
    "selects the public URL when initial %s restoration finishes on poll",
    async (state) => {
      vi.useFakeTimers()
      window.localStorage.setItem("webService.displayHost", "127.0.0.1")
      api.getTailscaleFunnelStatus
        .mockResolvedValueOnce({ ...disabled, state, desiredEnabled: true })
        .mockResolvedValue(enabled)
      renderSettings()
      await act(async () => {})
      expect(screen.getByRole("combobox")).toHaveTextContent(LOCAL_URL)
      await act(async () => vi.advanceTimersByTimeAsync(10000))
      expect(screen.getByRole("combobox")).toHaveTextContent(PUBLIC_URL)
    }
  )

  it("preserves a manual address choice while restoration is pending", async () => {
    api.getTailscaleFunnelStatus.mockResolvedValue({
      ...disabled,
      state: "starting",
      desiredEnabled: true,
    })
    const user = userEvent.setup()
    renderSettings()
    await screen.findByRole("button", { name: "Cancel operation" })
    await user.click(screen.getByRole("combobox"))
    await user.click(
      screen.getByRole("option", { name: "http://192.168.1.2:3080" })
    )
    api.getTailscaleFunnelStatus.mockResolvedValue(enabled)
    await user.click(
      screen.getByRole("button", { name: "Refresh Tailscale status" })
    )
    expect(screen.getByRole("combobox")).toHaveTextContent(
      "http://192.168.1.2:3080"
    )
    await user.click(screen.getByRole("combobox"))
    expect(screen.getAllByRole("option")[0]).toHaveTextContent(PUBLIC_URL)
  })

  it("preserves a manual address choice while the initial status is pending", async () => {
    const pending = deferred<typeof enabled>()
    api.getTailscaleFunnelStatus.mockReturnValue(pending.promise)
    const user = userEvent.setup()
    renderSettings()
    await user.click(await screen.findByRole("combobox"))
    await user.click(
      screen.getByRole("option", { name: "http://192.168.1.2:3080" })
    )
    await act(async () => pending.resolve(enabled))
    expect(screen.getByRole("combobox")).toHaveTextContent(
      "http://192.168.1.2:3080"
    )
  })

  it("selects the new public URL on enable and falls back on disable", async () => {
    const user = userEvent.setup()
    renderSettings()
    await user.click(
      await screen.findByRole("button", { name: "Enable public access" })
    )
    await waitFor(() =>
      expect(screen.getByRole("combobox")).toHaveTextContent(PUBLIC_URL)
    )
    await user.click(
      screen.getByRole("button", { name: "Disable public access" })
    )
    await waitFor(() =>
      expect(screen.getByRole("combobox")).toHaveTextContent(LOCAL_URL)
    )
    expect(screen.queryByText(PUBLIC_URL)).not.toBeInTheDocument()
  })

  it("keeps the local service usable when the backend lacks Funnel support", async () => {
    api.getTailscaleFunnelStatus.mockRejectedValue(new Error("Unknown command"))
    renderSettings()
    await waitFor(() =>
      expect(screen.getByRole("combobox")).toHaveTextContent(LOCAL_URL)
    )
    expect(
      await screen.findByText(/Unable to read Tailscale status/)
    ).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "Stop" })).toBeEnabled()
  })

  it("never shows an authorization URL as a public service address", async () => {
    api.getTailscaleFunnelStatus.mockResolvedValue({
      ...disabled,
      state: "authorizing",
      authUrl: "https://login.tailscale.com/f/example",
    })
    renderSettings()
    expect(
      await screen.findByRole("button", { name: "Open authorization page" })
    ).toBeEnabled()
    expect(screen.getByRole("combobox")).toHaveTextContent(LOCAL_URL)
  })

  it("keeps a manually selected local address after an ordinary refresh", async () => {
    api.getTailscaleFunnelStatus.mockResolvedValue(enabled)
    const user = userEvent.setup()
    renderSettings()
    await waitFor(() =>
      expect(screen.getByRole("combobox")).toHaveTextContent(PUBLIC_URL)
    )
    await user.click(screen.getByRole("combobox"))
    await user.click(
      screen.getByRole("option", { name: "http://192.168.1.2:3080" })
    )
    await user.click(
      screen.getByRole("button", { name: "Refresh Tailscale status" })
    )
    expect(screen.getByRole("combobox")).toHaveTextContent(
      "http://192.168.1.2:3080"
    )
  })

  it("loads local controls even while the initial Funnel request is pending", async () => {
    const pending = deferred<typeof enabled>()
    api.getTailscaleFunnelStatus.mockReturnValue(pending.promise)
    renderSettings()
    expect(await screen.findByRole("button", { name: "Stop" })).toBeEnabled()
    expect(screen.getByRole("combobox")).toHaveTextContent(LOCAL_URL)
    await act(async () => pending.resolve(enabled))
    expect(screen.getByRole("combobox")).toHaveTextContent(PUBLIC_URL)
  })

  it("keeps a failed disable visible and retains the confirmed public address", async () => {
    api.getTailscaleFunnelStatus.mockResolvedValue(enabled)
    api.disableTailscaleFunnel.mockResolvedValue({
      ...enabled,
      error: "cleanupFailed",
    })
    const user = userEvent.setup()
    renderSettings()
    await user.click(
      await screen.findByRole("button", { name: "Disable public access" })
    )
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Could not remove public access"
    )
    expect(screen.getByRole("combobox")).toHaveTextContent(PUBLIC_URL)
  })

  it("opens and copies login authorization, then cancels the pending operation", async () => {
    const authUrl = "https://login.tailscale.com/f/example"
    api.getTailscaleFunnelStatus.mockResolvedValue({
      ...disabled,
      state: "needsLogin",
    })
    api.startTailscaleLogin.mockResolvedValue({
      ...disabled,
      state: "authorizing",
      authUrl,
    })
    api.cancelTailscaleFunnelOperation.mockResolvedValue(disabled)
    const user = userEvent.setup()
    const writeText = vi.spyOn(navigator.clipboard, "writeText")
    renderSettings()
    await user.click(
      await screen.findByRole("button", { name: "Log in to Tailscale" })
    )
    await user.click(
      await screen.findByRole("button", { name: "Open authorization page" })
    )
    expect(openUrl).toHaveBeenCalledWith(authUrl)
    await user.click(
      screen.getByRole("button", { name: "Copy authorization URL" })
    )
    expect(writeText).toHaveBeenCalledWith(authUrl)
    expect(screen.getByRole("combobox")).toHaveTextContent(LOCAL_URL)
    await user.click(screen.getByRole("button", { name: "Cancel operation" }))
    expect(
      await screen.findByRole("button", { name: "Enable public access" })
    ).toBeEnabled()
    expect(
      screen.queryByRole("button", { name: "Open authorization page" })
    ).not.toBeInTheDocument()
  })

  it("does not overlap status requests and refreshes on window focus", async () => {
    const pending = deferred<typeof enabled>()
    api.getTailscaleFunnelStatus
      .mockResolvedValueOnce(disabled)
      .mockReturnValueOnce(pending.promise)
    const user = userEvent.setup()
    renderSettings()
    await screen.findByRole("button", { name: "Enable public access" })
    fireEvent.focus(window)
    fireEvent.focus(window)
    await user.click(
      screen.getByRole("button", { name: "Refresh Tailscale status" })
    )
    expect(api.getTailscaleFunnelStatus).toHaveBeenCalledTimes(2)
    await act(async () => pending.resolve(enabled))
    expect(
      screen.getByRole("button", { name: "Disable public access" })
    ).toBeEnabled()
    // Ordinary refresh adds the option without changing the current choice.
    expect(screen.getByRole("combobox")).toHaveTextContent(LOCAL_URL)
  })

  it("polls pending enable until confirmed and selects the new public URL", async () => {
    vi.useFakeTimers()
    api.enableTailscaleFunnel.mockResolvedValue({
      ...disabled,
      state: "starting",
      desiredEnabled: true,
    })
    renderSettings()
    await act(async () => {})
    fireEvent.click(
      screen.getByRole("button", { name: "Enable public access" })
    )
    await act(async () => {})
    expect(
      screen.getByRole("button", { name: "Cancel operation" })
    ).toBeEnabled()
    api.getTailscaleFunnelStatus.mockResolvedValue(enabled)
    await act(async () => vi.advanceTimersByTimeAsync(2500))
    expect(screen.getByRole("combobox")).toHaveTextContent(PUBLIC_URL)
    expect(
      screen.getByRole("button", { name: "Disable public access" })
    ).toBeEnabled()
  })

  it("refreshes active public access and removes a URL disabled outside the page", async () => {
    vi.useFakeTimers()
    api.getTailscaleFunnelStatus
      .mockResolvedValueOnce(enabled)
      .mockResolvedValue(disabled)
    renderSettings()
    await act(async () => {})
    expect(screen.getByRole("combobox")).toHaveTextContent(PUBLIC_URL)
    await act(async () => vi.advanceTimersByTimeAsync(10000))
    expect(screen.getByRole("combobox")).toHaveTextContent(LOCAL_URL)
    expect(screen.queryByText(PUBLIC_URL)).not.toBeInTheDocument()
  })

  it("ignores an old operation response after cancellation", async () => {
    const pending = deferred<typeof enabled>()
    api.enableTailscaleFunnel.mockReturnValue(pending.promise)
    api.cancelTailscaleFunnelOperation.mockResolvedValue(disabled)
    const user = userEvent.setup()
    renderSettings()
    await user.click(
      await screen.findByRole("button", { name: "Enable public access" })
    )
    await user.click(screen.getByRole("button", { name: "Cancel operation" }))
    await act(async () => pending.resolve(enabled))
    expect(
      screen.getByRole("button", { name: "Enable public access" })
    ).toBeEnabled()
    expect(screen.getByRole("combobox")).toHaveTextContent(LOCAL_URL)
  })

  it("does not update an unmounted settings page from a late response", async () => {
    const pending = deferred<typeof enabled>()
    api.getTailscaleFunnelStatus.mockReturnValueOnce(pending.promise)
    const first = renderSettings()
    first.unmount()
    renderSettings()
    await screen.findByRole("button", { name: "Enable public access" })
    await act(async () => pending.resolve(enabled))
    expect(screen.getByRole("combobox")).toHaveTextContent(LOCAL_URL)
  })

  it("reconciles local and Funnel status when stopping returns cleanupFailed", async () => {
    api.getTailscaleFunnelStatus
      .mockResolvedValueOnce(enabled)
      .mockResolvedValue(disabled)
    api.stopWebServer.mockRejectedValue(new Error("cleanupFailed"))
    const user = userEvent.setup()
    renderSettings()
    await screen.findByRole("button", { name: "Disable public access" })
    api.getWebServerStatus.mockResolvedValue(null)
    await user.click(screen.getByRole("button", { name: "Stop" }))
    expect(
      await screen.findByRole("button", { name: "Start", exact: true })
    ).toBeEnabled()
    expect(
      await screen.findByText(/Could not remove public access/)
    ).toBeInTheDocument()
    expect(screen.queryByText(PUBLIC_URL)).not.toBeInTheDocument()
  })

  it("refreshes Funnel after starting the local service", async () => {
    api.getWebServerStatus.mockResolvedValue(null)
    api.startWebServer.mockResolvedValue({
      port: 3080,
      token: "private-token",
      addresses: [LOCAL_URL],
    })
    api.getTailscaleFunnelStatus
      .mockResolvedValueOnce({ ...disabled, state: "stopped" })
      .mockResolvedValue(enabled)
    const user = userEvent.setup()
    renderSettings()
    await screen.findByText("Start Tailscale and the Web service to continue")
    await user.click(screen.getByRole("button", { name: "Start", exact: true }))
    expect(
      await screen.findByRole("button", { name: "Disable public access" })
    ).toBeEnabled()
    await user.click(screen.getByRole("combobox"))
    expect(screen.getAllByRole("option")[0]).toHaveTextContent(PUBLIC_URL)
  })

  it("shows externally managed public access without offering to disable it", async () => {
    api.getTailscaleFunnelStatus.mockResolvedValue({
      ...enabled,
      owned: false,
      desiredEnabled: false,
    })
    renderSettings()
    expect(
      await screen.findByText(
        "This public access is managed outside Codeg. Manage it in Tailscale."
      )
    ).toBeInTheDocument()
    expect(screen.getByRole("combobox")).toHaveTextContent(PUBLIC_URL)
    expect(
      screen.queryByRole("button", { name: "Disable public access" })
    ).not.toBeInTheDocument()
  })

  it("allows disabling saved public access intent before ownership is confirmed", async () => {
    api.getTailscaleFunnelStatus.mockResolvedValue({
      ...disabled,
      desiredEnabled: true,
      state: "stopped",
    })
    renderSettings()
    expect(
      await screen.findByRole("button", { name: "Disable public access" })
    ).toBeEnabled()
  })

  it("reports a failed Funnel operation while leaving local controls usable", async () => {
    api.enableTailscaleFunnel.mockRejectedValue(new Error("permissionDenied"))
    const user = userEvent.setup()
    renderSettings()
    await user.click(
      await screen.findByRole("button", { name: "Enable public access" })
    )
    expect(
      await screen.findByText("Tailscale permission was denied")
    ).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "Stop" })).toBeEnabled()
  })

  it("shows the official install link when Tailscale is not installed", async () => {
    api.getTailscaleFunnelStatus.mockResolvedValue({
      ...disabled,
      state: "notInstalled",
      installed: false,
    })
    renderSettings()
    const installLink = await screen.findByRole("link", {
      name: "Install Tailscale",
    })
    expect(installLink).toHaveAttribute(
      "href",
      "https://tailscale.com/download"
    )
    expect(
      screen.queryByRole("button", { name: "Enable public access" })
    ).not.toBeInTheDocument()
  })

  it("copies and displays only the public URL in its QR dialog", async () => {
    api.getTailscaleFunnelStatus.mockResolvedValue(enabled)
    const user = userEvent.setup()
    const writeText = vi.spyOn(navigator.clipboard, "writeText")
    renderSettings()
    await waitFor(() =>
      expect(screen.getByRole("combobox")).toHaveTextContent(PUBLIC_URL)
    )
    await user.click(
      within(screen.getByRole("combobox").parentElement!).getByRole("button", {
        name: "Copy",
      })
    )
    expect(writeText).toHaveBeenCalledWith(PUBLIC_URL)
    await user.click(screen.getByRole("button", { name: "QR Code" }))
    const dialog = await screen.findByRole("dialog")
    expect(dialog).toHaveTextContent(PUBLIC_URL)
    expect(dialog).not.toHaveTextContent("private-token-never-in-url")
    const expectedQr = document.createElement("div")
    expectedQr.innerHTML = renderToStaticMarkup(
      <QRCodeSVG value={PUBLIC_URL} size={208} marginSize={0} />
    )
    expect(
      dialog
        .querySelector("svg path[fill='currentColor']")
        ?.getAttribute("d") ??
        dialog.querySelector("svg path:last-child")?.getAttribute("d")
    ).toBe(expectedQr.querySelector("path:last-child")?.getAttribute("d"))
  })
})
