import { useEffect, useState } from "react"

import { fetchAccountUsage } from "@/lib/api"
import type { PlanUsageReport } from "@/lib/types"

/**
 * The connected adapter's plan-quota report, fetched ONCE on mount. The
 * caller renders this only inside the model picker popover (Radix unmounts
 * closed popovers), so a mount IS "the popover opened" — the backend's 30s
 * TTL plus its permanent unsupported verdict make every re-open cheap, and
 * this hook deliberately keeps no client-side cache of its own. Any failure,
 * and an empty report, read as "nothing to show": the summary is an
 * enhancement that must never surface an error or block the picker.
 */
export function usePlanUsage(
  connectionId: string | null
): PlanUsageReport | null {
  const [report, setReport] = useState<PlanUsageReport | null>(null)
  useEffect(() => {
    if (!connectionId) return
    let cancelled = false
    fetchAccountUsage(connectionId)
      .then((data) => {
        if (cancelled) return
        setReport(data.sections.length > 0 ? data : null)
      })
      .catch((error) => {
        if (cancelled) return
        console.warn("[usePlanUsage] failed to fetch plan usage:", error)
        setReport(null)
      })
    return () => {
      cancelled = true
    }
  }, [connectionId])
  return report
}
