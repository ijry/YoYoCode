import { cleanup, render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { NextIntlClientProvider } from "next-intl"
import { toast } from "sonner"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import enMessages from "@/i18n/messages/en.json"

import { ComposerVoiceButton } from "./composer-voice-button"

const MI = enMessages.Folder.chat.messageInput

const voiceApi = vi.hoisted(() => ({
  cancelVoiceStream: vi.fn(),
  ensureVoiceReady: vi.fn(),
  getVoiceStatus: vi.fn(),
  getVoiceStreamStatus: vi.fn(),
  listenVoicePartial: vi.fn(),
  listenVoicePrepare: vi.fn(),
  prepareVoiceStream: vi.fn(),
  sendVoiceStreamChunk: vi.fn(),
  startVoiceStream: vi.fn(),
  stopVoiceStream: vi.fn(),
  transcribeVoice: vi.fn(),
}))

vi.mock("@/lib/api", () => voiceApi)
vi.mock("sonner", () => ({
  toast: { error: vi.fn(), loading: vi.fn(), success: vi.fn() },
}))

const getUserMedia = vi.fn()

function renderVoiceButton() {
  return render(
    <NextIntlClientProvider locale="en" messages={enMessages}>
      <ComposerVoiceButton onTranscript={vi.fn()} />
    </NextIntlClientProvider>
  )
}

beforeEach(() => {
  vi.stubGlobal("__TAURI_INTERNALS__", {})
  // WKWebView may expose this even though its speech service cannot start.
  vi.stubGlobal("webkitSpeechRecognition", class {})
  vi.stubGlobal("AudioContext", class {})
  vi.stubGlobal("navigator", {
    platform: "MacIntel",
    userAgent: "",
    mediaDevices: { getUserMedia },
  })
  getUserMedia
    .mockReset()
    .mockRejectedValue(new DOMException("", "NotAllowedError"))
  voiceApi.cancelVoiceStream.mockReset().mockResolvedValue({ cancelled: true })
  voiceApi.ensureVoiceReady.mockReset()
  voiceApi.getVoiceStatus.mockReset().mockResolvedValue({
    available: true,
    binary: null,
    model: null,
    reason: null,
    needsManualInstall: false,
  })
  voiceApi.getVoiceStreamStatus.mockReset().mockResolvedValue({
    available: true,
    loaded: true,
    modelDir: "/models/streaming",
    reason: null,
  })
  voiceApi.listenVoicePartial.mockReset().mockResolvedValue(() => {})
  voiceApi.listenVoicePrepare.mockReset().mockResolvedValue(() => {})
  voiceApi.prepareVoiceStream.mockReset()
  voiceApi.sendVoiceStreamChunk.mockReset()
  voiceApi.startVoiceStream
    .mockReset()
    .mockResolvedValue({ sessionId: "stream-1" })
  voiceApi.stopVoiceStream.mockReset()
  voiceApi.transcribeVoice.mockReset()
  vi.mocked(toast.error).mockClear()
})

afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
})

describe("ComposerVoiceButton on Tauri macOS", () => {
  it("starts local streaming and explains a microphone permission denial", async () => {
    renderVoiceButton()

    expect(
      screen.queryByRole("button", { name: MI.voiceSwitchEngine })
    ).not.toBeInTheDocument()
    await userEvent.click(screen.getByRole("button", { name: MI.voiceStart }))

    await waitFor(() => {
      expect(voiceApi.startVoiceStream).toHaveBeenCalledTimes(1)
      expect(toast.error).toHaveBeenCalledWith(MI.voicePermissionDenied)
    })
    expect(voiceApi.cancelVoiceStream).toHaveBeenCalledWith("stream-1")
  })
})
