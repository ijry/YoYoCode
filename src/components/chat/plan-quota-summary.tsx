import { useLocale, useTranslations } from "next-intl"

import { useMinuteNow } from "@/hooks/use-minute-now"
import { usePlanUsage } from "@/hooks/use-plan-usage"
import { formatTimeIn } from "@/lib/relative-time"
import { formatTokenCount } from "@/lib/token-format"
import type { PlanUsageEntry, PlanUsageSection } from "@/lib/types"

/** Fill color by how much of the window is gone — the same palette
 *  composer-context-usage uses for the context ring: quiet until the window
 *  is two-thirds spent, amber approaching the end, destructive spent. */
function barColor(percent: number): string {
  if (percent >= 100) return "bg-destructive"
  if (percent >= 70) return "bg-amber-500"
  return "bg-foreground/70"
}

function PlanUsageRow({
  entry,
  locale,
  now,
  t,
}: {
  entry: PlanUsageEntry
  locale: string
  now: number
  t: ReturnType<typeof useTranslations<"Folder.chat.messageInput">>
}) {
  const percent = Math.round(entry.used_percent)
  const deadline = entry.deadline_at
  const deadlineLabel = (() => {
    if (deadline == null) return null
    if (entry.deadline_kind === "resets") {
      return t("planQuota.resetsIn", {
        time: formatTimeIn(deadline, now, locale),
      })
    }
    // `expires` (or an untagged deadline): the moment the GRANTED quota ends
    // — a date, never a countdown. This distinction is the whole point of
    // `deadline_kind`; see the design doc.
    const date = new Intl.DateTimeFormat(locale, {
      year: "numeric",
      month: "short",
      day: "numeric",
    }).format(new Date(deadline))
    return t("planQuota.expiresAt", { date })
  })()
  const units =
    entry.used_units != null && entry.total_units != null
      ? t("planQuota.usedOfTotal", {
          used: formatTokenCount(entry.used_units),
          total: formatTokenCount(entry.total_units),
        })
      : null
  return (
    <div className="flex flex-col gap-1">
      <div className="flex items-baseline justify-between gap-2 text-xs">
        <span className="min-w-0 truncate text-foreground/90">
          {entry.label}
        </span>
        <span className="shrink-0 tabular-nums text-muted-foreground">
          {units ?? `${percent}%`}
        </span>
      </div>
      <div className="relative h-1 overflow-hidden rounded-full bg-muted">
        <div
          className={`absolute inset-y-0 left-0 ${barColor(entry.used_percent)}`}
          style={{ width: `${Math.min(100, Math.max(0, percent))}%` }}
        />
      </div>
      {deadlineLabel != null && (
        <div className="text-2xs text-muted-foreground">{deadlineLabel}</div>
      )}
    </div>
  )
}

function PlanUsageSectionBlock({
  section,
  now,
}: {
  section: PlanUsageSection
  now: number
}) {
  const t = useTranslations("Folder.chat.messageInput")
  const locale = useLocale()
  const title =
    section.title ||
    (section.id === "glm"
      ? t("planQuota.sectionGlm")
      : t("planQuota.sectionZcode"))
  return (
    <div className="flex flex-col gap-1.5">
      <div className="text-xs font-medium text-foreground/80">{title}</div>
      {section.entries.map((entry, index) => (
        <PlanUsageRow
          key={`${entry.label}-${index}`}
          entry={entry}
          locale={locale}
          now={now}
          t={t}
        />
      ))}
    </div>
  )
}

/**
 * The plan-quota summary above the model list in the session settings
 * picker. Renders NULL unless the adapter answered `account/usage_stats`
 * with at least one usable section — every failure (no connection, unknown
 * method, adapter error, empty report) degrades to nothing, because the
 * picker must never be blocked or visually broken by an enhancement.
 */
export function PlanQuotaSummary({
  connectionId,
}: {
  connectionId: string | null
}) {
  const report = usePlanUsage(connectionId)
  const now = useMinuteNow()
  if (!report || report.sections.length === 0) return null
  return (
    <div className="flex flex-col gap-2 px-2.5 py-1.5">
      {report.sections.map((section) => (
        <PlanUsageSectionBlock key={section.id} section={section} now={now} />
      ))}
    </div>
  )
}
