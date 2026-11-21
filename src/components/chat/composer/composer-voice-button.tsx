"use client"

import { useCallback, useEffect, useRef, useState } from "react"
import { createPortal } from "react-dom"
import { useLocale, useTranslations } from "next-intl"
import { Loader2, Mic, Square } from "lucide-react"
import { toast } from "sonner"

import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import {
  cancelVoiceStream,
  ensureVoiceReady,
  getVoiceStatus,
  getVoiceStreamStatus,
  listenVoicePartial,
  prepareVoiceStream,
  sendVoiceStreamChunk,
  startVoiceStream,
  stopVoiceStream,
  transcribeVoice,
  type VoiceStatus,
} from "@/lib/api"
import { extractAppCommandError } from "@/lib/app-error"
import {
  detectVoiceEngine,
  floatToPcm16,
  getSpeechRecognitionCtor,
  isMicrophonePermissionError,
  LocalPcmRecorder,
  localeSupportsStreaming,
  pcm16ToBase64,
  VOICE_MAX_PCM_BYTES,
  voiceLanguageForLocale,
  type SpeechRecognitionLike,
  type VoiceEngine,
} from "@/lib/voice-input"

type VoicePhase = "idle" | "listening" | "transcribing" | "preparing"

const WAVEFORM_WEIGHTS = [0.25, 0.5, 0.75, 0.5, 1, 0.5, 0.75, 0.5, 0.25]

export interface ComposerVoiceButtonProps {
  disabled?: boolean
  /** A normal-flow slot above the editor, outside the narrow toolbar controls. */
  previewContainer?: HTMLElement | null
  /** The transcript lands in the editor for review — never sent automatically. */
  onTranscript: (text: string) => void
}

/**
 * The composer's voice button. Two engines share one interaction:
 *
 *  - `web-speech` (Chrome/Edge): the browser's own SpeechRecognition — no
 *    download, no backend trip, works even in a remote desktop window.
 *  - `local`: 16 kHz PCM captured here, decoded on this machine. For zh/en
 *    locales the chunks stream into a local sherpa-onnx recognizer and the
 *    transcript arrives live (`voice://partial`); every other locale keeps
 *    the record-then-transcribe whisper.cpp path. First use of either model
 *    auto-downloads it in the app; no terminal command is ever required.
 *
 * Whichever engine produces text, it is handed to `onTranscript` so the user
 * can review it before sending (pi-voice's `pasteToEditor` contract).
 */
export function ComposerVoiceButton({
  disabled = false,
  previewContainer,
  onTranscript,
}: ComposerVoiceButtonProps) {
  const t = useTranslations("Folder.chat.messageInput")
  const locale = useLocale()
  const [phase, setPhase] = useState<VoicePhase>("idle")
  // Lazy initializer keeps the probe out of an effect (no cascading render).
  // On SSR this is "none"; the client re-probes on hydration.
  const [engine, setEngine] = useState<VoiceEngine>(() => detectVoiceEngine())
  const [manualInstall, setManualInstall] = useState(false)
  const [levels, setLevels] = useState<number[]>([])
  // Live partial transcript while a streaming session is recording.
  const [partialText, setPartialText] = useState<string | null>(null)
  const previewRef = useRef<HTMLDivElement | null>(null)

  useEffect(() => {
    const preview = previewRef.current
    if (preview) preview.scrollTop = preview.scrollHeight
  }, [partialText, previewContainer])

  const recorderRef = useRef<LocalPcmRecorder | null>(null)
  const recognitionRef = useRef<SpeechRecognitionLike | null>(null)
  const phaseRef = useRef<VoicePhase>("idle")
  const engineRef = useRef<VoiceEngine>(engine)
  const localStatusRef = useRef<VoiceStatus | null>(null)
  const mountedRef = useRef(true)
  // Streaming session state. `chunkChain` serializes the ~100 ms uploads so
  // the recognizer hears audio in capture order; stop waits for the chain.
  const streamSessionRef = useRef<string | null>(null)
  const chunkChainRef = useRef<Promise<unknown>>(Promise.resolve())
  const streamFailuresRef = useRef(0)

  const setPhaseSafe = useCallback((next: VoicePhase) => {
    phaseRef.current = next
    if (mountedRef.current) setPhase(next)
  }, [])

  useEffect(() => {
    mountedRef.current = true
    return () => {
      mountedRef.current = false
      recorderRef.current?.cancel()
      recognitionRef.current?.abort()
      const sessionId = streamSessionRef.current
      streamSessionRef.current = null
      if (sessionId) void cancelVoiceStream(sessionId).catch(() => {})
    }
  }, [])

  // Probe local readiness lazily. It is only a cache: if the decoder is not
  // installed yet, selecting the local engine still triggers auto-download.
  useEffect(() => {
    let active = true
    void getVoiceStatus()
      .then((status) => {
        if (!active) return
        localStatusRef.current = status
        setManualInstall(status.needsManualInstall)
      })
      .catch(() => {
        if (active) localStatusRef.current = null
      })
    return () => {
      active = false
    }
  }, [])

  // Live partials for the streaming engine. The session filter drops events
  // from stale sessions (e.g. an aborted recording whose last chunk raced
  // the stop call).
  useEffect(() => {
    let active = true
    let unsub: (() => void) | null = null
    void listenVoicePartial((event) => {
      if (!active || event.isFinal) return
      if (streamSessionRef.current !== event.sessionId) return
      setPartialText(event.text)
    })
      .then((fn) => {
        if (active) unsub = fn
        else fn()
      })
      .catch(() => {})
    return () => {
      active = false
      unsub?.()
    }
  }, [])

  // Pre-warm the streaming recognizer on mount (~1.5 s load) so the first
  // click is instant. Only when the model is already installed: never start a
  // download from a passive mount — that happens on first mic click.
  useEffect(() => {
    if (!localeSupportsStreaming(locale)) return
    let active = true
    void getVoiceStreamStatus()
      .then((status) => {
        if (!active || !status.available || status.loaded) return
        return prepareVoiceStream().catch(() => {})
      })
      .catch(() => {})
    return () => {
      active = false
    }
  }, [locale])

  const ensureLocalReady = useCallback(async () => {
    const current = localStatusRef.current
    if (current?.available) return true
    if (current?.needsManualInstall) {
      toast.error(t("voiceManualInstallRequired"))
      return false
    }

    setPhaseSafe("preparing")
    // 下载进度在状态栏常驻显示（StatusBarVoicePrepare）；这里不再叠加 loading
    // toast —— 几分钟的下载用它表示既挡聊天又像卡死。成功提示保留。
    try {
      const status = await ensureVoiceReady()
      localStatusRef.current = status
      setManualInstall(status.needsManualInstall)
      if (!status.available) {
        toast.error(t("voiceManualInstallRequired"))
        return false
      }
      toast.success(t("voiceReady"))
      return true
    } catch (error) {
      const appError = extractAppCommandError(error)
      toast.error(
        appError?.code === "dependency_missing"
          ? t("voiceManualInstallRequired")
          : t("voicePrepareFailed")
      )
      return false
    } finally {
      setPhaseSafe("idle")
    }
  }, [setPhaseSafe, t])

  const startWebSpeech = useCallback(() => {
    const ctor = getSpeechRecognitionCtor()
    if (!ctor) {
      toast.error(t("voiceUnavailable"))
      return
    }
    const recognition = new ctor()
    recognition.lang = voiceLanguageForLocale(locale).speech
    recognition.continuous = false
    recognition.interimResults = false
    recognition.maxAlternatives = 1
    const finish = () => {
      recognitionRef.current = null
      if (phaseRef.current === "listening") setPhaseSafe("idle")
    }
    recognition.onresult = (event) => {
      const result = event.results[event.results.length - 1]
      const text = result?.[0]?.transcript?.trim()
      if (text) onTranscript(text)
    }
    recognition.onerror = (event) => {
      if (event.error === "no-speech" || event.error === "aborted") return
      toast.error(
        isMicrophonePermissionError(event.error)
          ? t("voicePermissionDenied")
          : t("voiceFailed")
      )
    }
    recognition.onend = finish
    recognitionRef.current = recognition
    try {
      recognition.start()
      setPhaseSafe("listening")
    } catch {
      toast.error(t("voiceFailed"))
      finish()
    }
  }, [locale, onTranscript, setPhaseSafe, t])

  // Ordered upload of one ~100 ms batch. The chain (not unbounded parallel
  // calls) keeps transport backpressure without dropping audio.
  const enqueueStreamChunk = useCallback((batch: Float32Array) => {
    const sessionId = streamSessionRef.current
    if (!sessionId) return
    const base64 = pcm16ToBase64(floatToPcm16(batch))
    chunkChainRef.current = chunkChainRef.current.then(() => {
      if (streamSessionRef.current !== sessionId) return
      return sendVoiceStreamChunk(sessionId, base64).catch(() => {
        streamFailuresRef.current += 1
      })
    })
  }, [])

  const startStreaming = useCallback(async () => {
    setPhaseSafe("preparing")
    // 下载进度在状态栏常驻显示（StatusBarVoicePrepare），不再叠加 loading toast。
    try {
      const status = await getVoiceStreamStatus()
      if (!status.loaded) {
        await prepareVoiceStream()
        toast.success(t("voiceReady"))
      }
    } catch {
      // Dependency missing is impossible here (sherpa-onnx is compiled in);
      // the realistic failures are download/load errors.
      toast.error(t("voicePrepareFailed"))
      setPhaseSafe("idle")
      return
    }

    try {
      const { sessionId } = await startVoiceStream()
      if (!mountedRef.current) {
        void cancelVoiceStream(sessionId).catch(() => {})
        return
      }
      streamSessionRef.current = sessionId
      chunkChainRef.current = Promise.resolve()
      streamFailuresRef.current = 0
      setPartialText(null)
      setLevels([])
      const recorder = new LocalPcmRecorder({
        onLevel: (level) => setLevels((prev) => [...prev.slice(-23), level]),
        onFrame: enqueueStreamChunk,
      })
      recorderRef.current = recorder
      await recorder.start()
      setPhaseSafe("listening")
    } catch (error) {
      const sessionId = streamSessionRef.current
      streamSessionRef.current = null
      recorderRef.current?.cancel()
      recorderRef.current = null
      if (sessionId) void cancelVoiceStream(sessionId).catch(() => {})
      toast.error(
        isMicrophonePermissionError(error)
          ? t("voicePermissionDenied")
          : t("voiceFailed")
      )
      setPhaseSafe("idle")
    }
  }, [enqueueStreamChunk, setPhaseSafe, t])

  const stopStreaming = useCallback(async () => {
    const sessionId = streamSessionRef.current
    streamSessionRef.current = null
    recorderRef.current?.cancel()
    recorderRef.current = null
    if (!sessionId) {
      setPhaseSafe("idle")
      return
    }
    setPhaseSafe("transcribing")
    try {
      await chunkChainRef.current
      const final = await stopVoiceStream(sessionId)
      if (final.text) onTranscript(final.text)
      else if (streamFailuresRef.current > 0) toast.error(t("voiceFailed"))
    } catch {
      toast.error(t("voiceFailed"))
    }
    setPartialText(null)
    setPhaseSafe("idle")
  }, [onTranscript, setPhaseSafe, t])

  const startLocal = useCallback(async () => {
    // zh/en locales stream (live partials); everything else records first and
    // decodes with whisper. Each path prepares its own model on first use.
    if (localeSupportsStreaming(locale)) {
      await startStreaming()
      return
    }
    // First-run install happens here, before asking for the microphone, so a
    // slow download never leaves a half-started recorder behind.
    if (!(await ensureLocalReady())) return
    setLevels([])
    const recorder = new LocalPcmRecorder({
      onLevel: (level) => setLevels((prev) => [...prev.slice(-23), level]),
    })
    recorderRef.current = recorder
    try {
      await recorder.start()
      setPhaseSafe("listening")
    } catch (error) {
      recorder.cancel()
      recorderRef.current = null
      toast.error(
        isMicrophonePermissionError(error)
          ? t("voicePermissionDenied")
          : t("voiceFailed")
      )
      setPhaseSafe("idle")
    }
  }, [ensureLocalReady, locale, setPhaseSafe, startStreaming, t])

  const stopLocal = useCallback(async () => {
    const recorder = recorderRef.current
    recorderRef.current = null
    if (!recorder) return
    setPhaseSafe("transcribing")
    try {
      const wav = await recorder.stop()
      if (!wav) {
        setPhaseSafe("idle")
        return
      }
      if (wav.byteLength > VOICE_MAX_PCM_BYTES + 44) {
        toast.error(t("voiceFailed"))
        setPhaseSafe("idle")
        return
      }
      const result = await transcribeVoice(
        wav,
        voiceLanguageForLocale(locale).whisper
      )
      if (result.text) onTranscript(result.text)
      setPhaseSafe("idle")
    } catch (error) {
      const appError = extractAppCommandError(error)
      toast.error(
        appError?.code === "dependency_missing"
          ? t("voiceManualInstallRequired")
          : t("voiceFailed")
      )
      setPhaseSafe("idle")
    }
  }, [locale, onTranscript, setPhaseSafe, t])

  const abort = useCallback(() => {
    const sessionId = streamSessionRef.current
    streamSessionRef.current = null
    if (sessionId) void cancelVoiceStream(sessionId).catch(() => {})
    recorderRef.current?.cancel()
    recorderRef.current = null
    recognitionRef.current?.abort()
    recognitionRef.current = null
    setPartialText(null)
    if (phaseRef.current === "listening") setPhaseSafe("idle")
  }, [setPhaseSafe])

  const toggle = useCallback(() => {
    if (
      disabled ||
      phaseRef.current === "transcribing" ||
      phaseRef.current === "preparing"
    )
      return
    if (phaseRef.current === "listening") {
      if (engineRef.current === "web-speech") {
        recognitionRef.current?.stop()
        setPhaseSafe("idle")
      } else if (streamSessionRef.current) {
        void stopStreaming()
      } else {
        void stopLocal()
      }
      return
    }
    const active = engineRef.current
    if (active === "web-speech") startWebSpeech()
    else if (active === "local") void startLocal()
    else toast.error(t("voiceUnavailable"))
  }, [
    disabled,
    setPhaseSafe,
    startLocal,
    startWebSpeech,
    stopLocal,
    stopStreaming,
    t,
  ])

  const switchToLocal = useCallback(() => {
    abort()
    engineRef.current = "local"
    setEngine("local")
    void startLocal()
  }, [abort, startLocal])

  const switchToWeb = useCallback(() => {
    abort()
    engineRef.current = "web-speech"
    setEngine("web-speech")
  }, [abort])

  const busy = phase !== "idle"
  const webSpeechAvailable = getSpeechRecognitionCtor() !== null
  const label =
    phase === "preparing"
      ? t("voicePreparing")
      : phase === "transcribing"
        ? t("voiceTranscribing")
        : phase === "listening"
          ? t("voiceStop")
          : t("voiceStart")

  // Non-editable content owns selection and scrolling instead of the
  // composer's click-the-chrome-to-type handler.
  const preview = partialText ? (
    <div
      contentEditable={false}
      className="mx-3 mt-3 flex min-w-0 items-start gap-2 rounded-lg bg-muted/60 px-3 py-2 text-muted-foreground"
    >
      <Mic
        className="mt-0.5 size-3.5 shrink-0 text-destructive"
        aria-hidden="true"
      />
      <div
        ref={previewRef}
        role="status"
        aria-live="polite"
        aria-atomic="true"
        className="max-h-24 min-w-0 flex-1 overflow-y-auto overscroll-contain whitespace-pre-wrap text-sm leading-relaxed [overflow-wrap:anywhere]"
      >
        {partialText}
      </div>
    </div>
  ) : null

  return (
    <div className="flex shrink-0 items-center gap-0.5">
      {preview && previewContainer && createPortal(preview, previewContainer)}
      <Button
        type="button"
        disabled={
          disabled ||
          phase === "transcribing" ||
          phase === "preparing" ||
          engine === "none"
        }
        variant="ghost"
        size="icon-xs"
        className={
          phase === "listening"
            ? "h-6 w-auto shrink-0 gap-2 bg-destructive/10 px-2 text-destructive hover:bg-destructive/15 hover:text-destructive"
            : "shrink-0 text-muted-foreground"
        }
        title={label}
        aria-label={label}
        aria-pressed={phase === "listening"}
        onClick={toggle}
      >
        {phase === "listening" ? (
          <>
            <Square className="size-3 fill-current" aria-hidden="true" />
            <span
              className="pointer-events-none flex h-5 items-center gap-0.5"
              aria-hidden="true"
            >
              {WAVEFORM_WEIGHTS.map((weight, index) => {
                const level =
                  levels[levels.length - 1 - index] ??
                  levels[levels.length - 1] ??
                  0
                const amplitude = Math.min(1, Math.sqrt(Math.max(0, level)) * 4)
                return (
                  <span
                    key={index}
                    className="w-0.5 rounded-full bg-current transition-[height] duration-100 ease-out motion-reduce:transition-none"
                    style={{ height: `${4 + weight * (4 + amplitude * 12)}px` }}
                  />
                )
              })}
            </span>
          </>
        ) : phase === "preparing" || phase === "transcribing" ? (
          <Loader2 className="size-4 animate-spin motion-reduce:animate-none" />
        ) : (
          <Mic className="size-4" />
        )}
      </Button>
      {!manualInstall && engine === "web-speech" && !busy && (
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button
              type="button"
              disabled={disabled}
              variant="ghost"
              size="icon-xs"
              className="shrink-0 text-muted-foreground"
              title={t("voiceEngineWeb")}
              aria-label={t("voiceSwitchEngine")}
            >
              <span className="text-[10px]">▾</span>
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent side="top" align="start" className="min-w-44">
            <DropdownMenuItem onSelect={() => switchToLocal()}>
              {t("voiceEngineLocal")}
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      )}
      {engine === "local" && webSpeechAvailable && !busy && (
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button
              type="button"
              disabled={disabled}
              variant="ghost"
              size="icon-xs"
              className="shrink-0 text-muted-foreground"
              title={t("voiceEngineLocal")}
              aria-label={t("voiceSwitchEngine")}
            >
              <span className="text-[10px]">▾</span>
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent side="top" align="start" className="min-w-44">
            <DropdownMenuItem onSelect={() => switchToWeb()}>
              {t("voiceEngineWeb")}
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      )}
    </div>
  )
}
