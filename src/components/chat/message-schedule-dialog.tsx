"use client"

import { useEffect, useState } from "react"
import { useLocale, useTranslations } from "next-intl"
import { CalendarClock, ChevronDown, TriangleAlert } from "lucide-react"
import { dateFnsLocale } from "@/lib/date-fns-locale"
import {
  defaultTimeForDate,
  formatScheduleDay,
  formatScheduleFull,
  isoToDateTimeLocalValue,
  isScheduleInPast,
  joinDateAndTime,
  parseDateTimeLocalValue,
  splitDateTimeLocalValue,
  toDateTimeLocalValue,
} from "@/lib/task-schedule"
import { Button } from "@/components/ui/button"
import { Calendar } from "@/components/ui/calendar"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover"
import { cn } from "@/lib/utils"

interface MessageScheduleDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  /**
   * The schedule being modified, if any. Seeds the fields and offers the
   * "back to ordinary queue" action; absent/blank starts the picker empty.
   */
  initialAt?: string | null
  /** True while the host call is in flight — the confirm/clear buttons wait. */
  busy?: boolean
  onConfirm: (iso: string) => void | Promise<void>
  onClear?: () => void | Promise<void>
}

/** `YYYY-MM-DD` ⇄ `Date`, both anchored to LOCAL midnight (same convention as
 *  the task scheduler, whose dialog this composition mirrors). */
function toDayValue(date: Date): string {
  return toDateTimeLocalValue(date).slice(0, 10)
}
function fromDayValue(day: string): Date | undefined {
  if (!day) return undefined
  const parsed = new Date(`${day}T00:00`)
  return Number.isNaN(parsed.getTime()) ? undefined : parsed
}

/**
 * Pick when one queued message should go out.
 *
 * The composition is the task scheduler's (calendar popover + time input), but
 * the contract is stricter: a message schedule is a delivery promise, so a
 * time already in the past is REFUSED here rather than accepted-with-hint —
 * the backend rejects it anyway, and telling the user "it will send soon-ish"
 * about a past instant would be a lie. The one-tap presets are the message
 * rhythms (30 minutes / 2 hours), not the task board's.
 */
export function MessageScheduleDialog({
  open,
  onOpenChange,
  initialAt,
  busy = false,
  onConfirm,
  onClear,
}: MessageScheduleDialogProps) {
  const t = useTranslations("Folder.chat.messageQueue")
  const locale = useLocale()
  const [date, setDate] = useState("")
  const [time, setTime] = useState("")
  const [calendarOpen, setCalendarOpen] = useState(false)
  // Frozen at open: the presets are a snapshot of the moment the dialog
  // appeared; a ticking `now` would move "In 30 minutes" under the pointer.
  const [openedAt, setOpenedAt] = useState(() => Date.now())

  useEffect(() => {
    if (!open) return
    setOpenedAt(Date.now())
    const planned = initialAt ? isoToDateTimeLocalValue(initialAt) : null
    const parts = splitDateTimeLocalValue(planned ?? "")
    setDate(parts.date)
    setTime(parts.time)
    setCalendarOpen(false)
    // `initialAt` seeds the fields but deliberately does not re-run this:
    // while the dialog is open the inputs belong to the user.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, initialAt])

  const value = joinDateAndTime(date, time)
  const picked = parseDateTimeLocalValue(value)
  const isPast = picked != null && isScheduleInPast(picked, openedAt)

  const pickDay = (day: Date | undefined) => {
    if (!day) return
    const dayValue = toDayValue(day)
    setDate(dayValue)
    // Only ever fills a BLANK time — an hour the user chose is never moved.
    if (!time) setTime(defaultTimeForDate(dayValue, new Date(openedAt)))
    setCalendarOpen(false)
  }

  const presets = [
    {
      labelKey: "preset30m" as const,
      value: toDateTimeLocalValue(new Date(openedAt + 30 * 60_000)),
    },
    {
      labelKey: "preset2h" as const,
      value: toDateTimeLocalValue(new Date(openedAt + 2 * 3_600_000)),
    },
  ]

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-[28rem]">
        <DialogHeader>
          <DialogTitle>{t("scheduleTitle")}</DialogTitle>
          <DialogDescription>{t("scheduleDescription")}</DialogDescription>
        </DialogHeader>

        <div className="flex flex-col gap-3">
          <div className="flex items-end gap-2">
            <div className="flex min-w-0 flex-1 flex-col gap-1.5">
              <Label htmlFor="message-schedule-date">
                {t("scheduleDateLabel")}
              </Label>
              <Popover open={calendarOpen} onOpenChange={setCalendarOpen}>
                <PopoverTrigger asChild>
                  <Button
                    id="message-schedule-date"
                    type="button"
                    variant="outline"
                    className={cn(
                      "h-9 w-full justify-between px-3 font-normal",
                      !date && "text-muted-foreground"
                    )}
                  >
                    <span className="truncate">
                      {date
                        ? formatScheduleDay(date, locale)
                        : t("schedulePickDate")}
                    </span>
                    <ChevronDown
                      className="size-4 shrink-0 opacity-50"
                      aria-hidden="true"
                    />
                  </Button>
                </PopoverTrigger>
                <PopoverContent align="start" className="w-auto p-0">
                  <Calendar
                    mode="single"
                    autoFocus
                    locale={dateFnsLocale(locale)}
                    selected={fromDayValue(date)}
                    defaultMonth={fromDayValue(date)}
                    onSelect={pickDay}
                  />
                </PopoverContent>
              </Popover>
            </div>
            <div className="flex w-32 shrink-0 flex-col gap-1.5">
              <Label htmlFor="message-schedule-time">
                {t("scheduleTimeLabel")}
              </Label>
              <Input
                id="message-schedule-time"
                type="time"
                value={time}
                onChange={(e) => setTime(e.target.value)}
                className="px-3 [&::-webkit-calendar-picker-indicator]:hidden"
              />
            </div>
          </div>

          <div className="flex flex-wrap gap-1.5">
            {presets.map((preset) => (
              <Button
                key={preset.labelKey}
                type="button"
                size="sm"
                variant="ghost"
                aria-pressed={value === preset.value}
                className={cn(
                  "h-7 rounded-full bg-muted/70 px-3 text-xs font-medium hover:bg-muted",
                  value === preset.value && "bg-primary/10 text-primary"
                )}
                onClick={() => {
                  const parts = splitDateTimeLocalValue(preset.value)
                  setDate(parts.date)
                  setTime(parts.time)
                }}
              >
                {t(preset.labelKey)}
              </Button>
            ))}
          </div>

          <p
            className={cn(
              "flex items-center gap-1.5 text-xs",
              isPast
                ? "text-amber-600 dark:text-amber-400"
                : "text-muted-foreground"
            )}
          >
            {isPast ? (
              <>
                <TriangleAlert
                  className="size-3.5 shrink-0"
                  aria-hidden="true"
                />
                {t("schedulePastHint")}
              </>
            ) : picked ? (
              <>
                <CalendarClock
                  className="size-3.5 shrink-0"
                  aria-hidden="true"
                />
                {t("schedulePreview", {
                  time: formatScheduleFull(picked.toISOString()),
                })}
              </>
            ) : null}
          </p>
        </div>

        <DialogFooter>
          {/* Converting back to ordinary keeps the item — unlike a cancel,
              it is a decision, so it sits beside the confirm. */}
          {initialAt && onClear ? (
            <Button
              type="button"
              variant="ghost"
              className="mr-auto text-muted-foreground hover:text-foreground"
              onClick={() => void onClear()}
              disabled={busy}
            >
              {t("scheduleClear")}
            </Button>
          ) : null}
          <Button
            type="button"
            variant="ghost"
            onClick={() => onOpenChange(false)}
            disabled={busy}
          >
            {t("scheduleCancel")}
          </Button>
          <Button
            type="button"
            // A past instant is refused, not warned-then-accepted: the host
            // rejects it, so confirming could only fail.
            disabled={busy || picked == null || isPast}
            onClick={() => picked && void onConfirm(picked.toISOString())}
          >
            {t("scheduleConfirm")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
