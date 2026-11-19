const UNITS: ReadonlyArray<[Intl.RelativeTimeFormatUnit, number]> = [
  ["year", 365 * 24 * 3_600_000],
  ["month", 30 * 24 * 3_600_000],
  ["week", 7 * 24 * 3_600_000],
  ["day", 24 * 3_600_000],
  ["hour", 3_600_000],
  ["minute", 60_000],
]

const formatters = new Map<string, Intl.RelativeTimeFormat>()

function formatterFor(locale: string): Intl.RelativeTimeFormat {
  let formatter = formatters.get(locale)
  if (!formatter) {
    formatter = new Intl.RelativeTimeFormat(locale, { numeric: "auto" })
    formatters.set(locale, formatter)
  }
  return formatter
}

/**
 * How long ago `at` was, in the locale's words and its largest whole unit
 * ("6 minutes ago", "6 分钟前", "yesterday"); under a minute is "now". A time
 * ahead of `now` — a clock a little off — reads as now too.
 */
export function formatTimeAgo(at: number, now: number, locale: string): string {
  const formatter = formatterFor(locale)
  const elapsed = Math.max(0, now - at)
  for (const [unit, size] of UNITS) {
    if (elapsed >= size)
      return formatter.format(-Math.floor(elapsed / size), unit)
  }
  return formatter.format(0, "second")
}

/**
 * How long until `at`, in the locale's words and its largest whole unit
 * ("in 3 hours", "3小时后") — the future counterpart of [`formatTimeAgo`],
 * for deadlines like a quota window's reset. A sub-minute wait rounds up to
 * a minute (there is no useful "in 0 seconds"), and a time already behind
 * `now` — clock skew, a just-passed deadline — reads as an imminent minute
 * rather than negative anything.
 */
export function formatTimeIn(at: number, now: number, locale: string): string {
  const formatter = formatterFor(locale)
  const remaining = Math.max(0, at - now)
  for (const [unit, size] of UNITS) {
    if (remaining >= size)
      return formatter.format(Math.floor(remaining / size), unit)
  }
  return formatter.format(1, "minute")
}
