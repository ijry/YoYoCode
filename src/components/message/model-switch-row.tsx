"use client"

import { memo } from "react"
import { ArrowRightLeft } from "lucide-react"
import { useLocale, useTranslations } from "next-intl"
import type { ModelSwitchInfo } from "@/lib/types"

/** Session metadata, outside the assistant reply and its usage/footer. */
export const ModelSwitchRow = memo(function ModelSwitchRow({
  record,
}: {
  record: ModelSwitchInfo
}) {
  const t = useTranslations("Folder.chat.messageList")
  const locale = useLocale()
  const previous =
    record.previous_model_name?.trim() ||
    record.previous_model_id ||
    t("modelSwitchUnknown")
  const next = record.model_name?.trim() || record.model_id
  const date = new Date(record.created_at)
  const validDate = !Number.isNaN(date.getTime())
  const timestamp = validDate ? date.toLocaleString(locale) : record.created_at

  return (
    <div className="flex flex-wrap items-center justify-center gap-x-2 gap-y-1 px-1 py-2 text-xs text-muted-foreground">
      <ArrowRightLeft className="size-3.5 shrink-0" aria-hidden="true" />
      <span>
        {t(
          record.status === "confirmed"
            ? "modelSwitchConfirmed"
            : "modelSwitchAccepted"
        )}
      </span>
      <span className="min-w-0 break-words" dir="ltr">
        <bdi title={record.previous_model_id ?? undefined}>{previous}</bdi>
        {" → "}
        <bdi title={record.model_id}>{next}</bdi>
      </span>
      <time dateTime={record.created_at} title={timestamp} className="shrink-0">
        {validDate
          ? date.toLocaleTimeString(locale, {
              hour: "2-digit",
              minute: "2-digit",
              second: "2-digit",
            })
          : timestamp}
      </time>
    </div>
  )
})
