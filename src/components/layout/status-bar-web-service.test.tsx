import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react"
import { NextIntlClientProvider } from "next-intl"
import { beforeEach, describe, expect, it, vi } from "vitest"

import type {
  TailscaleFunnelStatus,
  WebServerInfo,
  WebServiceConfig,
} from "@/lib/api"

const api = vi.hoisted(() => ({
  getWebServerStatus: vi.fn<() => Promise<WebServerInfo | null>>(),
  getWebServiceConfig: vi.fn<() => Promise<WebServiceConfig>>(),
  getTailscaleFunnelStatus: vi.fn<() => Promise<TailscaleFunnelStatus>>(),
  startWebServer: vi.fn<() => Promise<WebServerInfo>>(),
  stopWebServer: vi.fn<() => Promise<void>>(),
  openSettingsWindow: vi.fn(async () => {}),
}))
vi.mock("@/lib/api", () => ({
  ...api,
  DEFAULT_WEB_SERVICE_HOST: "0.0.0.0",
  WEB_SERVICE_HOSTS: ["0.0.0.0", "127.0.0.1"],
  WEB_SERVICE_STATUS_CHANGED_EVENT: "web-service://status-changed",
}))
const platform = vi.hoisted(() => ({
  openUrl: vi.fn<(url: string) => Promise<void>>(async () => {}),
}))
const handlers = new Map<string, (p: unknown) => void>()
vi.mock("@/lib/platform", () => ({
  subscribe: vi.fn((event: string, handler: (p: unknown) => void) => {
    handlers.set(event, handler)
    return Promise.resolve(() => {})
  }),
  openUrl: platform.openUrl,
}))
const utils = vi.hoisted(() => ({
  copyTextToClipboard: vi.fn<(text: string) => Promise<boolean>>(
    async () => true
  ),
}))
vi.mock("@/lib/utils", async (importOriginal) => ({
  ...(await importOriginal<Record<string, unknown>>()),
  copyTextToClipboard: utils.copyTextToClipboard,
}))
vi.mock("qrcode.react", () => ({
  QRCodeSVG: ({
    value,
    "aria-label": label,
  }: {
    value: string
    "aria-label"?: string
  }) => <svg role="img" aria-label={label} data-value={value} />,
}))
const runtime = vi.hoisted(() => ({ desktop: true, remote: false }))
vi.mock("@/lib/transport", () => ({
  isDesktop: () => runtime.desktop,
  isRemoteDesktopMode: () => runtime.remote,
}))

import { StatusBarWebService } from "./status-bar-web-service"
import enMessages from "@/i18n/messages/en.json"

const PUBLIC_URL = "https://workstation.example.ts.net"
const LOOPBACK_URL = "http://127.0.0.1:3080"
const LAN_URL = "http://192.168.1.5:3080"

function info(overrides: Partial<WebServerInfo> = {}): WebServerInfo {
  return {
    port: 3080,
    token: "secret",
    addresses: [LOOPBACK_URL, LAN_URL],
    bindHost: "0.0.0.0",
    ...overrides,
  }
}

function funnel(
  overrides: Partial<TailscaleFunnelStatus> = {}
): TailscaleFunnelStatus {
  return {
    state: "ready",
    installed: true,
    enabled: false,
    desiredEnabled: false,
    owned: false,
    url: null,
    authUrl: null,
    error: null,
    prerequisite: null,
    ...overrides,
  }
}

function config(overrides: Partial<WebServiceConfig> = {}): WebServiceConfig {
  return {
    token: "secret",
    port: 3080,
    autoStart: false,
    host: null,
    ...overrides,
  }
}

/** connectionConfigCode v2 is UTF-8 JSON in unpadded base64url. */
function decodeConfigCode(code: string): Record<string, unknown> {
  const padded =
    code.replace(/-/g, "+").replace(/_/g, "/") +
    "=".repeat((4 - (code.length % 4)) % 4)
  return JSON.parse(Buffer.from(padded, "base64").toString("utf8"))
}

function mount() {
  return render(
    <NextIntlClientProvider locale="en" messages={enMessages}>
      <StatusBarWebService />
    </NextIntlClientProvider>
  )
}

function trigger() {
  return screen.getByRole("button", { name: "Web service" })
}

async function openPopover() {
  fireEvent.click(trigger())
  await waitFor(() => expect(api.getWebServiceConfig).toHaveBeenCalled())
  await waitFor(() => expect(api.getTailscaleFunnelStatus).toHaveBeenCalled())
}

beforeEach(() => {
  vi.clearAllMocks()
  handlers.clear()
  runtime.desktop = true
  runtime.remote = false
  utils.copyTextToClipboard.mockResolvedValue(true)
  api.getWebServerStatus.mockResolvedValue(null)
  api.getWebServiceConfig.mockResolvedValue(config())
  api.getTailscaleFunnelStatus.mockResolvedValue(funnel())
  api.startWebServer.mockResolvedValue(info())
  api.stopWebServer.mockResolvedValue(undefined)
})

describe("StatusBarWebService", () => {
  it("is not there in a browser, and asks nothing", async () => {
    runtime.desktop = false
    const { container } = mount()
    await act(async () => {})
    expect(container).toBeEmptyDOMElement()
    expect(api.getWebServerStatus).not.toHaveBeenCalled()
    expect(api.getTailscaleFunnelStatus).not.toHaveBeenCalled()
  })

  /** A remote-desktop window's calls go to the remote server, whose web
   *  service is not this computer's. */
  it("is not there in a remote-desktop window, and asks nothing", async () => {
    runtime.remote = true
    const { container } = mount()
    await act(async () => {})
    expect(container).toBeEmptyDOMElement()
    expect(api.getWebServerStatus).not.toHaveBeenCalled()
    expect(api.getTailscaleFunnelStatus).not.toHaveBeenCalled()
  })

  it("stays neutral while stopped and turns green while listening", async () => {
    mount()
    await waitFor(() =>
      expect(trigger()).toHaveAttribute("title", "Web service · stopped")
    )
    expect(trigger().className).not.toMatch(/emerald/)

    api.getWebServerStatus.mockResolvedValue(info())
    await act(async () => {
      handlers.get("web-service://status-changed")?.({ running: true })
    })
    await waitFor(() =>
      expect(trigger()).toHaveAttribute(
        "title",
        "Web service · listening on 0.0.0.0:3080 (reachable from your network)"
      )
    )
    expect(trigger().className).toMatch(/emerald/)
  })

  it("says a loopback bind reaches this computer only", async () => {
    api.getWebServerStatus.mockResolvedValue(
      info({ bindHost: "127.0.0.1", addresses: [LOOPBACK_URL] })
    )
    mount()
    await waitFor(() =>
      expect(trigger()).toHaveAttribute(
        "title",
        "Web service · listening on 127.0.0.1:3080 (this computer only)"
      )
    )
  })

  /** Every interface means the local network: that start asks first, and
   *  nothing starts until the person confirms. */
  it("asks before a start that opens the service to the network", async () => {
    mount()
    await waitFor(() => expect(api.getWebServerStatus).toHaveBeenCalled())
    await openPopover()
    fireEvent.click(screen.getByRole("button", { name: "Start web service" }))

    expect(
      await screen.findByText("Open Codeg to your network?")
    ).toBeInTheDocument()
    expect(api.startWebServer).not.toHaveBeenCalled()

    fireEvent.click(screen.getByRole("button", { name: "Start" }))
    await waitFor(() => expect(api.startWebServer).toHaveBeenCalledTimes(1))
    // What was confirmed is what gets bound; the token stays the saved one.
    expect(api.startWebServer).toHaveBeenCalledWith({
      host: "0.0.0.0",
      port: 3080,
    })
    await waitFor(() => expect(trigger().className).toMatch(/emerald/))
  })

  /** The popover read loopback when it opened; Settings has since switched to
   *  every interface. The start must re-read, ask, and bind what it asked
   *  about — never slip a network-wide start through unconfirmed. */
  it("re-reads the saved host at the moment of starting", async () => {
    api.getWebServiceConfig.mockResolvedValueOnce(config({ host: "127.0.0.1" }))
    mount()
    await waitFor(() => expect(api.getWebServerStatus).toHaveBeenCalled())
    await openPopover()
    await screen.findByText(/this computer only/)

    api.getWebServiceConfig.mockResolvedValue(config({ host: "0.0.0.0" }))
    fireEvent.click(screen.getByRole("button", { name: "Start web service" }))
    expect(
      await screen.findByText("Open Codeg to your network?")
    ).toBeInTheDocument()
    expect(screen.getByText(/listen on 0\.0\.0\.0:3080/)).toBeInTheDocument()
    expect(api.startWebServer).not.toHaveBeenCalled()

    fireEvent.click(screen.getByRole("button", { name: "Start" }))
    await waitFor(() =>
      expect(api.startWebServer).toHaveBeenCalledWith({
        host: "0.0.0.0",
        port: 3080,
      })
    )
  })

  it("starts nothing when the saved settings cannot be read", async () => {
    mount()
    await waitFor(() => expect(api.getWebServerStatus).toHaveBeenCalled())
    await openPopover()
    api.getWebServiceConfig.mockRejectedValue({ message: "db locked" })
    fireEvent.click(screen.getByRole("button", { name: "Start web service" }))
    expect(await screen.findByRole("alert")).toHaveTextContent("db locked")
    expect(api.startWebServer).not.toHaveBeenCalled()
    expect(screen.queryByText("Open Codeg to your network?")).toBeNull()
  })

  it("starts nothing when the confirmation is cancelled", async () => {
    mount()
    await waitFor(() => expect(api.getWebServerStatus).toHaveBeenCalled())
    await openPopover()
    fireEvent.click(screen.getByRole("button", { name: "Start web service" }))
    fireEvent.click(await screen.findByRole("button", { name: "Cancel" }))
    await act(async () => {})
    expect(api.startWebServer).not.toHaveBeenCalled()
  })

  it("starts a loopback-only service without asking", async () => {
    api.getWebServiceConfig.mockResolvedValue(config({ host: "127.0.0.1" }))
    api.startWebServer.mockResolvedValue(
      info({ bindHost: "127.0.0.1", addresses: [LOOPBACK_URL] })
    )
    mount()
    await waitFor(() => expect(api.getWebServerStatus).toHaveBeenCalled())
    await openPopover()
    await screen.findByText(/this computer only/)
    fireEvent.click(screen.getByRole("button", { name: "Start web service" }))
    await waitFor(() =>
      expect(api.startWebServer).toHaveBeenCalledWith({
        host: "127.0.0.1",
        port: 3080,
      })
    )
    expect(screen.queryByText("Open Codeg to your network?")).toBeNull()
  })

  it("stops without asking", async () => {
    api.getWebServerStatus.mockResolvedValue(info())
    mount()
    await waitFor(() => expect(trigger().className).toMatch(/emerald/))
    await openPopover()
    fireEvent.click(screen.getByRole("button", { name: "Stop web service" }))
    await waitFor(() => expect(api.stopWebServer).toHaveBeenCalledTimes(1))
    await waitFor(() => expect(trigger().className).not.toMatch(/emerald/))
  })

  it("shows the same localized bind error as Settings", async () => {
    api.getWebServiceConfig.mockResolvedValue(
      config({ host: "127.0.0.1", port: 4000 })
    )
    api.startWebServer.mockRejectedValue({ message: "web_server.port_in_use" })
    mount()
    await waitFor(() => expect(api.getWebServerStatus).toHaveBeenCalled())
    await openPopover()
    await screen.findByText(/this computer only/)
    fireEvent.click(screen.getByRole("button", { name: "Start web service" }))
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Port 4000 is already in use"
    )
  })

  it("opens the Web service settings page", async () => {
    mount()
    await waitFor(() => expect(api.getWebServerStatus).toHaveBeenCalled())
    await openPopover()
    fireEvent.click(
      screen.getByRole("button", { name: "Open Web service settings" })
    )
    expect(api.openSettingsWindow).toHaveBeenCalledWith("web-service")
  })

  /** A read issued before a broadcast is older than it, and must not
   *  overwrite what the broadcast's own re-read found. */
  it("drops a status answer overtaken by a broadcast", async () => {
    let resolveFirst!: (v: WebServerInfo | null) => void
    api.getWebServerStatus
      .mockImplementationOnce(
        () => new Promise((resolve) => (resolveFirst = resolve))
      )
      .mockResolvedValue(info())
    mount()
    await waitFor(() => expect(api.getWebServerStatus).toHaveBeenCalledTimes(1))
    await act(async () => {
      handlers.get("web-service://status-changed")?.({ running: true })
    })
    await waitFor(() => expect(trigger().className).toMatch(/emerald/))
    await act(async () => resolveFirst(null))
    expect(trigger().className).toMatch(/emerald/)
  })
})

describe("StatusBarWebService remote access (Tailscale + MCode)", () => {
  it("guides through setup while the Web service is stopped", async () => {
    mount()
    await waitFor(() => expect(api.getWebServerStatus).toHaveBeenCalled())
    await openPopover()

    expect(
      await screen.findByText("Start the web service to pair a phone.")
    ).toBeInTheDocument()
    expect(
      screen.queryByRole("button", { name: "Show connection QR code" })
    ).toBeNull()
    expect(screen.getByText("Off")).toBeInTheDocument()
    expect(
      screen.getByText("Enable it in settings to reach Codeg from any network.")
    ).toBeInTheDocument()

    fireEvent.click(
      screen.getByRole("button", { name: "Configure public access" })
    )
    expect(api.openSettingsWindow).toHaveBeenCalledWith("web-service")
  })

  it("shows the public endpoint and pairs the phone over it", async () => {
    api.getWebServerStatus.mockResolvedValue(info({ hostName: "Studio-Mac" }))
    api.getTailscaleFunnelStatus.mockResolvedValue(
      funnel({
        state: "enabled",
        enabled: true,
        desiredEnabled: true,
        owned: true,
        url: `${PUBLIC_URL}/`,
      })
    )
    mount()
    await waitFor(() => expect(trigger().className).toMatch(/emerald/))
    await openPopover()

    expect(await screen.findByText(PUBLIC_URL)).toBeInTheDocument()
    expect(screen.getByText("On")).toBeInTheDocument()

    fireEvent.click(screen.getByRole("button", { name: "Copy public address" }))
    await waitFor(() =>
      expect(utils.copyTextToClipboard).toHaveBeenCalledWith(PUBLIC_URL)
    )

    // The phone must use the public HTTPS endpoint, not a LAN-only address.
    fireEvent.click(
      screen.getByRole("button", { name: "Copy configuration code" })
    )
    await waitFor(() =>
      expect(utils.copyTextToClipboard).toHaveBeenCalledTimes(2)
    )
    const code = utils.copyTextToClipboard.mock.calls[1][0]
    expect(decodeConfigCode(code)).toMatchObject({
      name: "Studio-Mac",
      directBaseUrl: PUBLIC_URL,
      directToken: "secret",
    })
  })

  it("opens a phone pairing QR code for the LAN address without Funnel", async () => {
    api.getWebServerStatus.mockResolvedValue(info())
    mount()
    await waitFor(() => expect(trigger().className).toMatch(/emerald/))
    await openPopover()

    fireEvent.click(
      await screen.findByRole("button", { name: "Show connection QR code" })
    )
    const dialog = await screen.findByRole("dialog")
    expect(
      within(dialog).getByText("Connect from your phone")
    ).toBeInTheDocument()
    expect(within(dialog).getByText(LAN_URL)).toBeInTheDocument()

    const qr = within(dialog).getByRole("img", {
      name: "MCode connection QR code",
    })
    expect(decodeConfigCode(qr.getAttribute("data-value") ?? "")).toMatchObject(
      { directBaseUrl: LAN_URL, directToken: "secret" }
    )

    fireEvent.click(
      within(dialog).getByRole("button", { name: "Copy configuration code" })
    )
    await waitFor(() =>
      expect(utils.copyTextToClipboard).toHaveBeenCalledTimes(1)
    )
    fireEvent.click(
      within(dialog).getByRole("button", { name: "Get the mobile app" })
    )
    expect(platform.openUrl).toHaveBeenCalledWith(
      "https://getmcode.lingyun.net"
    )
  })

  it("keeps phone pairing usable when Tailscale status is unavailable", async () => {
    api.getWebServerStatus.mockResolvedValue(info())
    api.getTailscaleFunnelStatus.mockRejectedValue(new Error("tailscale down"))
    mount()
    await waitFor(() => expect(trigger().className).toMatch(/emerald/))
    await openPopover()

    expect(await screen.findByText("Unavailable")).toBeInTheDocument()
    expect(
      screen.queryByRole("button", { name: "Copy public address" })
    ).toBeNull()
    expect(
      screen.getByRole("button", { name: "Show connection QR code" })
    ).toBeInTheDocument()
  })

  it("never offers a credential-bearing URL as a public endpoint", async () => {
    api.getWebServerStatus.mockResolvedValue(info())
    api.getTailscaleFunnelStatus.mockResolvedValue(
      funnel({
        state: "enabled",
        enabled: true,
        url: "https://user:pass@workstation.example.ts.net",
      })
    )
    mount()
    await waitFor(() => expect(trigger().className).toMatch(/emerald/))
    await openPopover()

    await waitFor(() => expect(screen.getByText("Off")).toBeInTheDocument())
    expect(
      screen.queryByRole("button", { name: "Copy public address" })
    ).toBeNull()
    expect(
      screen.getByRole("button", { name: "Show connection QR code" })
    ).toBeInTheDocument()
  })
})
