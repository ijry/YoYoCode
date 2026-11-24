import { act, cleanup, render } from "@testing-library/react"
import { NextIntlClientProvider } from "next-intl"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import enMessages from "@/i18n/messages/en.json"
import type { VoicePrepareProgress } from "@/lib/api"

import { StatusBarVoicePrepare } from "./status-bar-voice-prepare"

const MI = enMessages.Folder.chat.messageInput

const listenVoicePrepare = vi.hoisted(() => vi.fn())

vi.mock("@/lib/api", () => ({
  listenVoicePrepare,
}))

function emit(progress: VoicePrepareProgress) {
  act(() => {
    listenVoicePrepare.mock.calls[0][0](progress)
  })
}

function renderSegment() {
  return render(
    <NextIntlClientProvider locale="en" messages={enMessages}>
      <StatusBarVoicePrepare />
    </NextIntlClientProvider>
  )
}

beforeEach(() => {
  listenVoicePrepare.mockReset().mockResolvedValue(() => {})
})

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals?.()
})

describe("StatusBarVoicePrepare", () => {
  it("stays hidden while nothing is being prepared", () => {
    renderSegment()
    expect(document.querySelector("[data-voice-prepare]")).toBeNull()
    expect(listenVoicePrepare).toHaveBeenCalledTimes(1)
  })

  it("shows the percent while the model downloads", () => {
    renderSegment()
    emit({
      stage: "downloading_stream_model",
      percent: 42.6,
      message: "",
    })

    const segment = document.querySelector("[data-voice-prepare]")
    expect(segment).not.toBeNull()
    expect(segment).toHaveTextContent("43%")
    expect(segment).toHaveAttribute(
      "title",
      MI.voicePreparingProgress.replace("{percent}", "43")
    )
  })

  it("falls back to the plain label when the host sent no percent", () => {
    renderSegment()
    emit({
      stage: "downloading_model",
      percent: -1,
      message: "",
    })

    expect(document.querySelector("[data-voice-prepare]")).toHaveTextContent(
      MI.voicePreparing
    )
  })

  it("names the extraction stage", () => {
    renderSegment()
    emit({ stage: "extracting", percent: -1, message: "" })
    expect(document.querySelector("[data-voice-prepare]")).toHaveTextContent(
      MI.voicePreparingExtracting
    )
  })

  it("hides again once the preparation is done", () => {
    renderSegment()
    emit({
      stage: "loading_model",
      percent: -1,
      message: "",
    })
    expect(document.querySelector("[data-voice-prepare]")).not.toBeNull()
    emit({ stage: "done", percent: 100, message: "" })
    expect(document.querySelector("[data-voice-prepare]")).toBeNull()
  })

  it("stops listening on unmount", async () => {
    const unsubscribe = vi.fn()
    listenVoicePrepare.mockResolvedValue(unsubscribe)
    const { unmount } = renderSegment()
    await act(async () => {})
    unmount()
    expect(unsubscribe).toHaveBeenCalledTimes(1)
  })
})
