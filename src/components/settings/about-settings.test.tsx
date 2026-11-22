import { cleanup, render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { NextIntlClientProvider } from "next-intl"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import enMessages from "@/i18n/messages/en.json"

const openUrl = vi.fn<(url: string) => Promise<void>>()
const openSettingsWindow = vi.fn<(section?: string) => Promise<void>>()

vi.mock("@/lib/platform", () => ({
  openUrl: (url: string) => openUrl(url),
}))
vi.mock("@/lib/api", () => ({
  openSettingsWindow: (section?: string) => openSettingsWindow(section),
}))
vi.mock("@/components/ui/scroll-area", () => ({
  ScrollArea: ({ children }: { children: React.ReactNode }) => (
    <div>{children}</div>
  ),
}))
vi.mock("./about-logo-animation", () => ({
  AboutLogoAnimation: ({ label }: { label: string }) => (
    <div data-testid="about-logo">{label}</div>
  ),
}))

import { AboutSettings } from "./about-settings"

function renderAbout() {
  return render(
    <NextIntlClientProvider locale="en" messages={enMessages}>
      <AboutSettings />
    </NextIntlClientProvider>
  )
}

beforeEach(() => {
  openUrl.mockClear()
  openSettingsWindow.mockClear()
  openSettingsWindow.mockResolvedValue(undefined)
})
afterEach(() => cleanup())

describe("AboutSettings", () => {
  it("keeps this fork, mobile pairing and upstream in separate groups", () => {
    renderAbout()

    expect(screen.getByText("This fork")).toBeInTheDocument()
    expect(screen.getByText("Connect from your phone")).toBeInTheDocument()
    expect(screen.getByText("Upstream: Codeg")).toBeInTheDocument()
    expect(screen.getByTestId("about-logo")).toHaveTextContent(
      "Animated YoYoCode jellyfish logo"
    )
  })

  it("opens fork links and upstream links at their own repositories", async () => {
    const user = userEvent.setup()
    renderAbout()

    await user.click(screen.getByRole("button", { name: "This repository" }))
    expect(openUrl).toHaveBeenCalledWith("https://github.com/ijry/YoYoCode")

    await user.click(
      screen.getByRole("button", { name: "Upstream repository" })
    )
    expect(openUrl).toHaveBeenCalledWith(
      "https://github.com/spacering-net/codeg"
    )

    await user.click(screen.getByRole("button", { name: "Documentation" }))
    expect(openUrl).toHaveBeenCalledWith("https://ijry.github.io/yoyocode-docs")
  })

  it("exposes the mobile app download and Web Service settings", async () => {
    const user = userEvent.setup()
    renderAbout()

    await user.click(screen.getByRole("button", { name: "Get the mobile app" }))
    expect(openUrl).toHaveBeenCalledWith("https://getmcode.lingyun.net")

    await user.click(
      screen.getByRole("button", { name: "Web Service settings" })
    )
    expect(openSettingsWindow).toHaveBeenCalledWith("web-service")
  })
})
