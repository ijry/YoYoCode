import { cleanup, render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { NextIntlClientProvider } from "next-intl"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import enMessages from "@/i18n/messages/en.json"
import type { TurnLimitContinuationMode } from "@/lib/types"

const getSettings = vi.fn<() => Promise<{ mode: TurnLimitContinuationMode }>>()
const updateSettings =
  vi.fn<
    (
      m: TurnLimitContinuationMode
    ) => Promise<{ mode: TurnLimitContinuationMode }>
  >()
const toastError = vi.fn()

vi.mock("@/lib/api", () => ({
  getTurnLimitContinuationSettings: () => getSettings(),
  updateTurnLimitContinuationSettings: (m: TurnLimitContinuationMode) =>
    updateSettings(m),
}))
vi.mock("sonner", () => ({ toast: { error: (m: string) => toastError(m) } }))

import { TurnLimitContinuationSettingsSection } from "./turn-limit-continuation-settings"

function renderSection() {
  return render(
    <NextIntlClientProvider locale="en" messages={enMessages}>
      <TurnLimitContinuationSettingsSection />
    </NextIntlClientProvider>
  )
}

beforeEach(() => {
  toastError.mockClear()
  getSettings.mockReset()
  updateSettings.mockReset()
  getSettings.mockResolvedValue({ mode: "new_session" })
})
afterEach(() => cleanup())

describe("TurnLimitContinuationSettingsSection", () => {
  it("shows the stored mode", async () => {
    getSettings.mockResolvedValue({ mode: "off" })
    renderSection()
    expect(await screen.findByText("Off")).toBeInTheDocument()
  })

  it("persists a new choice", async () => {
    const user = userEvent.setup()
    updateSettings.mockResolvedValue({ mode: "compact" })
    renderSection()

    await user.click(await screen.findByRole("combobox"))
    await user.click(
      await screen.findByRole("option", {
        name: "Compact then continue (unverified)",
      })
    )

    await waitFor(() => expect(updateSettings).toHaveBeenCalledWith("compact"))
    expect(
      await screen.findByText(
        /Note: compaction keeps the same session and may not reset the provider's turn counter\./
      )
    ).toBeInTheDocument()
  })

  it("reverts the picker when the write fails", async () => {
    const user = userEvent.setup()
    updateSettings.mockRejectedValue(new Error("db locked"))
    renderSection()

    await user.click(await screen.findByRole("combobox"))
    await user.click(
      await screen.findByRole("option", {
        name: "Compact then continue (unverified)",
      })
    )

    await waitFor(() => expect(toastError).toHaveBeenCalled())
    expect(
      await screen.findByText("Continue in a new session")
    ).toBeInTheDocument()
  })
})
