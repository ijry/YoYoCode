import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react"
import { useRef, useState } from "react"
import userEvent from "@testing-library/user-event"
import { NextIntlClientProvider } from "next-intl"
import { toast } from "sonner"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import enMessages from "@/i18n/messages/en.json"

import { ComposerVoiceButton } from "./composer-voice-button"
import type { RichComposerHandle } from "./rich-composer"
import { useComposerChromeFocus } from "./use-composer-chrome-focus"

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

function VoiceComposer({
  onTranscript,
}: {
  onTranscript: (text: string) => void
}) {
  const [previewContainer, setPreviewContainer] =
    useState<HTMLDivElement | null>(null)
  const editorRef = useRef<RichComposerHandle | null>(null)
  const chromeFocus = useComposerChromeFocus(editorRef)
  return (
    <div {...chromeFocus}>
      <div ref={setPreviewContainer} data-testid="voice-preview-container" />
      <div role="textbox">Existing draft</div>
      <ComposerVoiceButton
        onTranscript={onTranscript}
        previewContainer={previewContainer}
      />
    </div>
  )
}

function renderVoiceButton(onTranscript = vi.fn()) {
  return render(
    <NextIntlClientProvider locale="en" messages={enMessages}>
      <VoiceComposer onTranscript={onTranscript} />
    </NextIntlClientProvider>
  )
}

function allowRecording() {
  const processor = {
    onaudioprocess: null as
      | ((event: {
          inputBuffer: { getChannelData: () => Float32Array }
        }) => void)
      | null,
    connect: vi.fn(),
    disconnect: vi.fn(),
  }
  getUserMedia.mockResolvedValue({ getTracks: () => [{ stop: vi.fn() }] })
  vi.stubGlobal(
    "AudioContext",
    class {
      state = "running"
      destination = {}
      createMediaStreamSource() {
        return { connect: vi.fn(), disconnect: vi.fn() }
      }
      createScriptProcessor() {
        return processor
      }
      createGain() {
        return { gain: { value: 1 }, connect: vi.fn() }
      }
      close() {
        return Promise.resolve()
      }
    }
  )
  return (level: number) => {
    act(() => {
      processor.onaudioprocess?.({
        inputBuffer: {
          getChannelData: () => new Float32Array(1600).fill(level),
        },
      })
    })
  }
}

function emitPartial(text: string, sessionId = "stream-1", isFinal = false) {
  act(() => {
    voiceApi.listenVoicePartial.mock.calls[0][0]({
      sessionId,
      text,
      isFinal,
    })
  })
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
  voiceApi.sendVoiceStreamChunk
    .mockReset()
    .mockResolvedValue({ accepted: true })
  voiceApi.startVoiceStream
    .mockReset()
    .mockResolvedValue({ sessionId: "stream-1" })
  voiceApi.stopVoiceStream.mockReset().mockResolvedValue({ text: "Final text" })
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

  it("keeps live text in the preview above the unchanged draft", async () => {
    allowRecording()
    renderVoiceButton()
    await userEvent.click(screen.getByRole("button", { name: MI.voiceStart }))
    await screen.findByRole("button", { name: MI.voiceStop })

    const text = "继续完成尚未完成的任务，不要开始后续任务。".repeat(12)
    emitPartial(text)

    const preview = within(screen.getByTestId("voice-preview-container"))
    expect(preview.getByRole("status")).toHaveTextContent(text)
    expect(screen.getByRole("textbox")).toHaveTextContent("Existing draft")
  })

  it("keeps the latest live text in view as the preview grows", async () => {
    allowRecording()
    renderVoiceButton()
    await userEvent.click(screen.getByRole("button", { name: MI.voiceStart }))
    await screen.findByRole("button", { name: MI.voiceStop })
    emitPartial("First words")

    const preview = screen.getByRole("status")
    Object.defineProperty(preview, "scrollHeight", {
      configurable: true,
      value: 200,
    })
    emitPartial("Latest words")

    expect(preview.scrollTop).toBe(200)
  })

  it("preserves native mouse presses for selecting and scrolling the preview", async () => {
    allowRecording()
    renderVoiceButton()
    await userEvent.click(screen.getByRole("button", { name: MI.voiceStart }))
    await screen.findByRole("button", { name: MI.voiceStop })
    emitPartial("Current words")
    const preview = screen.getByRole("status")

    expect(fireEvent.pointerDown(preview)).toBe(true)
    expect(fireEvent.mouseDown(preview)).toBe(true)
  })

  it("ignores stale-session and final partials and clears live text after stopping", async () => {
    allowRecording()
    const onTranscript = vi.fn()
    renderVoiceButton(onTranscript)
    await userEvent.click(screen.getByRole("button", { name: MI.voiceStart }))
    const stop = await screen.findByRole("button", { name: MI.voiceStop })

    emitPartial("Stale text", "old-session")
    emitPartial("Final event", "stream-1", true)
    expect(screen.queryByRole("status")).not.toBeInTheDocument()

    emitPartial("Current words")
    expect(screen.getByRole("status")).toHaveTextContent("Current words")
    await userEvent.click(stop)

    await waitFor(() => {
      expect(onTranscript).toHaveBeenCalledExactlyOnceWith("Final text")
      expect(screen.queryByRole("status")).not.toBeInTheDocument()
    })
    expect(screen.getByRole("button", { name: MI.voiceStart })).toHaveAttribute(
      "aria-pressed",
      "false"
    )
    emitPartial("Late text")
    expect(screen.queryByRole("status")).not.toBeInTheDocument()
  })

  it("shows a waveform immediately and makes quiet speech visibly taller than silence", async () => {
    const emitLevel = allowRecording()
    renderVoiceButton()
    await userEvent.click(screen.getByRole("button", { name: MI.voiceStart }))
    const stop = await screen.findByRole("button", { name: MI.voiceStop })
    const waveform = stop.querySelector('span[aria-hidden="true"]')
    const bars = () => Array.from(waveform?.children ?? []) as HTMLElement[]

    expect(bars().length).toBeGreaterThan(3)
    emitLevel(0)
    const quietHeights = bars().map((bar) => parseFloat(bar.style.height))
    expect(Math.min(...quietHeights)).toBeGreaterThanOrEqual(4)
    emitLevel(0.01)
    const speechHeights = bars().map((bar) => parseFloat(bar.style.height))
    expect(Math.max(...speechHeights)).toBeGreaterThan(
      Math.max(...quietHeights)
    )
    expect(Math.max(...speechHeights)).toBeLessThanOrEqual(20)
  })
})
