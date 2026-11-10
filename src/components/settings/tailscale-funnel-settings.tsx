"use client"

import { useCallback, useEffect, useRef, useState } from "react"
import { Check, Copy, ExternalLink, RefreshCw } from "lucide-react"
import { useTranslations } from "next-intl"
import {
  cancelTailscaleFunnelOperation,
  disableTailscaleFunnel,
  enableTailscaleFunnel,
  getTailscaleFunnelStatus,
  startTailscaleLogin,
  type TailscaleFunnelStatus,
} from "@/lib/api"
import { openUrl } from "@/lib/platform"
import { copyTextToClipboard } from "@/lib/utils"
import { useCopiedFlag } from "@/hooks/use-copied-flag"
import { BrowserLink } from "@/components/ui/browser-link"

const BUTTON_CLASS =
  "inline-flex h-8 items-center justify-center gap-1.5 rounded-md border border-input bg-background px-3 text-xs font-medium transition-colors hover:bg-accent hover:text-accent-foreground disabled:pointer-events-none disabled:opacity-50"

const ERROR_CODES = [
  "notInstalled",
  "unavailable",
  "needsLogin",
  "stopped",
  "conflict",
  "permissionDenied",
  "timeout",
  "invalidResponse",
  "failed",
  "cancelled",
  "unsupportedBind",
  "busy",
  "cleanupFailed",
  "notOwned",
] as const

type ErrorCode = (typeof ERROR_CODES)[number]
type Operation = "login" | "enable" | "disable" | "cancel"

function errorCode(error: unknown): ErrorCode | null {
  const code =
    typeof error === "string"
      ? error
      : error && typeof error === "object" && "message" in error
        ? String(error.message)
        : ""
  return ERROR_CODES.includes(code as ErrorCode) ? (code as ErrorCode) : null
}

function isPending(status: TailscaleFunnelStatus | null) {
  return status?.state === "starting" || status?.state === "authorizing"
}

// Only a confirmed HTTPS origin belongs in the service address selector.
// In particular, authorization links and credentials never enter that list.
function publicAddress(status: TailscaleFunnelStatus): string | null {
  if (!status.enabled || !status.url) return null
  try {
    const url = new URL(status.url)
    return url.protocol === "https:" && !url.username && !url.password
      ? url.origin
      : null
  } catch {
    return null
  }
}

export function TailscaleFunnelSettings({
  onAddressChange,
  refreshVersion,
}: {
  onAddressChange: (
    url: string | null,
    select: "initial" | "enabled" | null
  ) => void
  refreshVersion: number
}) {
  const t = useTranslations("WebServiceSettings.funnel")
  const [status, setStatus] = useState<TailscaleFunnelStatus | null>(null)
  const [refreshing, setRefreshing] = useState(false)
  const [operation, setOperation] = useState<Operation | null>(null)
  const [failure, setFailure] = useState<ErrorCode | "loadFailed" | null>(null)
  const [copied, markCopied] = useCopiedFlag()
  const mounted = useRef(false)
  const revision = useRef(0)
  const refreshInFlight = useRef(false)
  const operationInFlight = useRef<Operation | null>(null)
  const initialRead = useRef(true)
  const selectWhenEnabled = useRef(false)
  const refreshedVersion = useRef(refreshVersion)
  const refreshRequired = useRef(false)

  const acceptStatus = useCallback(
    (next: TailscaleFunnelStatus) => {
      const url = publicAddress(next)
      onAddressChange(
        url,
        url && selectWhenEnabled.current
          ? "enabled"
          : url && initialRead.current
            ? "initial"
            : null
      )
      // Autostart restoration can take several status reads before publishing
      // a URL. Retain its initial-selection intent until it settles.
      if (url || (!isPending(next) && !next.desiredEnabled) || next.error) {
        initialRead.current = false
        selectWhenEnabled.current = false
      }
      setStatus(next)
      setFailure(next.error ? (errorCode(next.error) ?? "failed") : null)
    },
    [onAddressChange]
  )

  const refresh = useCallback(async () => {
    if (refreshInFlight.current || operationInFlight.current) return
    refreshInFlight.current = true
    const request = ++revision.current
    setRefreshing(true)
    try {
      const next = await getTailscaleFunnelStatus()
      if (
        mounted.current &&
        request === revision.current &&
        !refreshRequired.current
      )
        acceptStatus(next)
    } catch (error) {
      if (mounted.current && request === revision.current) {
        initialRead.current = false
        setFailure(errorCode(error) ?? "loadFailed")
      }
    } finally {
      if (mounted.current && request === revision.current) {
        refreshInFlight.current = false
        setRefreshing(false)
      }
    }
  }, [acceptStatus])

  useEffect(() => {
    mounted.current = true
    void refresh()
    const onFocus = () => void refresh()
    window.addEventListener("focus", onFocus)
    return () => {
      mounted.current = false
      revision.current += 1
      refreshInFlight.current = false
      operationInFlight.current = null
      window.removeEventListener("focus", onFocus)
    }
  }, [refresh])

  const pending = isPending(status)
  const active = status?.enabled || status?.desiredEnabled
  useEffect(() => {
    if (!pending && !active) return
    const timer = window.setInterval(
      () => {
        if (document.visibilityState === "visible") void refresh()
      },
      pending ? 2000 : 10000
    )
    return () => window.clearInterval(timer)
  }, [active, pending, refresh])

  async function runOperation(kind: Operation) {
    // Cancellation must remain possible while login/enable is still awaiting
    // its response. Its revision invalidates that older response.
    if (refreshInFlight.current) return
    if (operationInFlight.current && kind !== "cancel") return
    if (operationInFlight.current === "cancel") return
    operationInFlight.current = kind
    const request = ++revision.current
    selectWhenEnabled.current = kind === "enable"
    setOperation(kind)
    setFailure(null)
    const run = {
      login: startTailscaleLogin,
      enable: enableTailscaleFunnel,
      disable: disableTailscaleFunnel,
      cancel: cancelTailscaleFunnelOperation,
    }[kind]
    try {
      const next = await run()
      if (
        mounted.current &&
        request === revision.current &&
        !refreshRequired.current
      )
        acceptStatus(next)
    } catch (error) {
      if (mounted.current && request === revision.current) {
        selectWhenEnabled.current = false
        setFailure(errorCode(error) ?? "failed")
      }
    } finally {
      if (mounted.current && request === revision.current) {
        operationInFlight.current = null
        setOperation(null)
      }
    }
  }

  const busy = refreshing || operation !== null
  useEffect(() => {
    if (refreshedVersion.current !== refreshVersion) {
      refreshedVersion.current = refreshVersion
      refreshRequired.current = true
    }
    if (refreshRequired.current && !busy) {
      refreshRequired.current = false
      void refresh()
    }
  }, [busy, refresh, refreshVersion])

  const canCancel = pending || operation === "enable" || operation === "login"
  const canDisable = status?.owned || status?.desiredEnabled
  const canEnable =
    status?.installed &&
    !status.enabled &&
    (status.state === "ready" || status.state === "error")

  return (
    <section
      className="space-y-3 rounded-md border p-3"
      aria-label={t("title")}
    >
      <div className="flex items-center justify-between gap-3">
        <h4 className="text-sm font-medium">{t("title")}</h4>
        <button
          type="button"
          className={BUTTON_CLASS}
          onClick={() => void refresh()}
          disabled={busy}
          aria-label={t("refresh")}
          title={t("refresh")}
        >
          <RefreshCw
            className={`h-3.5 w-3.5 ${refreshing ? "animate-spin" : ""}`}
          />
        </button>
      </div>
      <p className="text-xs text-muted-foreground">{t("description")}</p>
      <p className="text-sm" role="status">
        {operation
          ? t("processing")
          : status
            ? t(`states.${status.state}`)
            : t("loading")}
      </p>
      {status?.enabled && !status.owned && !status.desiredEnabled && (
        <p className="text-xs text-muted-foreground">{t("externalManaged")}</p>
      )}
      {failure && (
        <p role="alert" className="text-sm text-destructive">
          {failure === "loadFailed" ? t("loadFailed") : t(`errors.${failure}`)}
        </p>
      )}
      <div className="flex flex-wrap gap-2">
        {status?.state === "notInstalled" && (
          <BrowserLink
            className={BUTTON_CLASS}
            href="https://tailscale.com/download"
          >
            <ExternalLink className="h-3.5 w-3.5" />
            {t("install")}
          </BrowserLink>
        )}
        {status?.state === "needsLogin" && !canCancel && (
          <button
            type="button"
            className={BUTTON_CLASS}
            disabled={busy}
            onClick={() => void runOperation("login")}
          >
            {t("login")}
          </button>
        )}
        {canEnable && !canCancel && (
          <button
            type="button"
            className={BUTTON_CLASS}
            disabled={busy}
            onClick={() => void runOperation("enable")}
          >
            {t("enable")}
          </button>
        )}
        {canDisable && !canCancel && (
          <button
            type="button"
            className={BUTTON_CLASS}
            disabled={busy}
            onClick={() => void runOperation("disable")}
          >
            {t("disable")}
          </button>
        )}
        {canCancel && (
          <button
            type="button"
            className={BUTTON_CLASS}
            disabled={refreshing || operation === "cancel"}
            onClick={() => void runOperation("cancel")}
          >
            {t("cancel")}
          </button>
        )}
      </div>
      {status?.authUrl && (
        <div className="space-y-2">
          <code className="block text-xs break-all select-all">
            {status.authUrl}
          </code>
          <div className="flex flex-wrap gap-2">
            <button
              type="button"
              className={BUTTON_CLASS}
              onClick={() => void openUrl(status.authUrl!)}
            >
              <ExternalLink className="h-3.5 w-3.5" />
              {t("openAuth")}
            </button>
            <button
              type="button"
              className={BUTTON_CLASS}
              onClick={async () => {
                if (await copyTextToClipboard(status.authUrl!)) markCopied()
              }}
            >
              {copied ? (
                <Check className="h-3.5 w-3.5" />
              ) : (
                <Copy className="h-3.5 w-3.5" />
              )}
              {t("copyAuth")}
            </button>
          </div>
        </div>
      )}
    </section>
  )
}
