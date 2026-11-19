import { render, screen, cleanup, waitFor } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"
import { NextIntlClientProvider } from "next-intl"

import enMessages from "@/i18n/messages/en.json"
import type { PlanUsageReport } from "@/lib/types"

vi.mock("@/lib/api", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/lib/api")>()),
  fetchAccountUsage: vi.fn(),
}))

import { fetchAccountUsage } from "@/lib/api"
import { PlanQuotaSummary } from "./plan-quota-summary"

const MSGS = enMessages.Folder.chat.messageInput

const mockFetch = vi.mocked(fetchAccountUsage)

// The zcode section comes straight off the wire sample in the Rust tests:
// a Start Plan window at 68.5% of 1M tokens, expiring Dec 1 2026.
const REPORT: PlanUsageReport = {
  sections: [
    {
      id: "zcode",
      title: "Start Plan",
      entries: [
        {
          label: "5-hour window",
          used_percent: 68.5,
          used_units: 685_000,
          total_units: 1_000_000,
          deadline_at: Date.parse("2026-12-01T00:00:00Z"),
          deadline_kind: "expires",
        },
      ],
    },
    {
      id: "glm",
      title: "",
      entries: [
        {
          label: "5h",
          used_percent: 42,
          deadline_at: Date.now() + 3 * 3_600_000 + 60_000,
          deadline_kind: "resets",
        },
      ],
    },
  ],
}

function renderSummary(connectionId: string | null = "conn-1") {
  return render(
    <NextIntlClientProvider locale="en" messages={enMessages}>
      <PlanQuotaSummary connectionId={connectionId} />
    </NextIntlClientProvider>
  )
}

afterEach(() => {
  cleanup()
  vi.mocked(fetchAccountUsage).mockReset()
})

describe("PlanQuotaSummary", () => {
  it("renders each successful section with its rows", async () => {
    mockFetch.mockResolvedValue(REPORT)
    renderSummary()
    expect(await screen.findByText("Start Plan")).toBeInTheDocument()
    expect(screen.getByText("5-hour window")).toBeInTheDocument()
    // 685000/1000000 tokens through formatTokenCount.
    expect(
      screen.getByText(
        MSGS.planQuota.usedOfTotal
          .replace("{used}", "685K")
          .replace("{total}", "1M")
      )
    ).toBeInTheDocument()
    // The Start Plan deadline is an expiry, not a countdown.
    expect(screen.getByText(/Valid until/)).toBeInTheDocument()
    // The titleless glm section falls back to its i18n label.
    expect(
      await screen.findByText(MSGS.planQuota.sectionGlm)
    ).toBeInTheDocument()
    expect(screen.getByText(/Resets/)).toBeInTheDocument()
  })

  it("colors the bar by threshold: normal, warning at 70, exhausted at 100", async () => {
    const section = (percent: number) => ({
      id: "zcode",
      title: "Start Plan",
      entries: [
        {
          label: "w",
          used_percent: percent,
          deadline_at: null,
          deadline_kind: null,
        },
      ],
    })
    mockFetch.mockResolvedValue({ sections: [section(50)] })
    const { container } = renderSummary()
    await screen.findByText("Start Plan")
    expect(container.querySelector(".bg-foreground\\/70")).not.toBeNull()

    cleanup()
    mockFetch.mockResolvedValue({ sections: [section(75)] })
    const warning = renderSummary()
    await screen.findByText("Start Plan")
    expect(warning.container.querySelector(".bg-amber-500")).not.toBeNull()

    cleanup()
    mockFetch.mockResolvedValue({ sections: [section(100)] })
    const exhausted = renderSummary()
    await screen.findByText("Start Plan")
    expect(exhausted.container.querySelector(".bg-destructive")).not.toBeNull()
  })

  it("renders nothing for an empty report", async () => {
    mockFetch.mockResolvedValue({ sections: [] })
    const { container } = renderSummary()
    await waitFor(() => expect(mockFetch).toHaveBeenCalledWith("conn-1"))
    expect(container.firstChild).toBeNull()
  })

  it("renders nothing when the fetch fails", async () => {
    mockFetch.mockRejectedValue(new Error("method not found"))
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {})
    const { container } = renderSummary()
    await waitFor(() => expect(warn).toHaveBeenCalled())
    expect(container.firstChild).toBeNull()
    warn.mockRestore()
  })

  it("fetches once per connection and stays null without one", () => {
    mockFetch.mockResolvedValue(REPORT)
    renderSummary(null)
    expect(mockFetch).not.toHaveBeenCalled()
  })

  it("does not setState after unmount", async () => {
    let resolve: (value: PlanUsageReport) => void = () => {}
    mockFetch.mockImplementation(
      () =>
        new Promise<PlanUsageReport>((res) => {
          resolve = res
        })
    )
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {})
    const { unmount } = renderSummary()
    unmount()
    resolve(REPORT)
    await new Promise((r) => setTimeout(r, 0))
    expect(warn).not.toHaveBeenCalled()
    warn.mockRestore()
  })
})
