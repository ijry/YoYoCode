"use client"

import { useEffect, useState } from "react"
import { useTranslations } from "next-intl"

import { listenVoicePrepare, type VoicePrepareProgress } from "@/lib/api"

/**
 * 语音模型准备的常驻进度段：whisper 解码器 / sherpa 流式模型的首用下载动辄
 * 几分钟，loading toast 既挡聊天又像卡死（没有百分比、离开输入框就失联）。
 * 这里直接订阅 `voice://prepare` —— 挂在状态栏上与应用同生命周期，无论进度
 * 是哪个输入框触发的都看得见；`done` 即隐藏，失败仍走发起方的 error toast。
 */
export function StatusBarVoicePrepare() {
  const t = useTranslations("Folder.chat.messageInput")
  const [progress, setProgress] = useState<VoicePrepareProgress | null>(null)

  useEffect(() => {
    let active = true
    let unsub: (() => void) | null = null
    void listenVoicePrepare((event) => {
      if (!active) return
      setProgress(event.stage === "done" ? null : event)
    })
      .then((unsubscribe) => {
        if (active) unsub = unsubscribe
        else unsubscribe()
      })
      .catch(() => {})
    return () => {
      active = false
      unsub?.()
    }
  }, [])

  if (!progress) return null

  const label =
    progress.stage === "extracting"
      ? t("voicePreparingExtracting")
      : progress.percent >= 0
        ? t("voicePreparingProgress", {
            percent: Math.round(progress.percent),
          })
        : t("voicePreparing")

  return (
    <div
      data-voice-prepare=""
      className="flex items-center gap-1.5"
      title={label}
    >
      <div className="size-2.5 animate-spin rounded-full border-[1.5px] border-current border-t-transparent" />
      <span className="text-3xs">{label}</span>
    </div>
  )
}
