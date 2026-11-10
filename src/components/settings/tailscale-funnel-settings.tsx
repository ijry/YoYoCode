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
import { isDesktop, openUrl } from "@/lib/platform"
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
  "dnsNotReady",
  "deviceApproval",
] as const

type ErrorCode = (typeof ERROR_CODES)[number]
type Operation = "enable" | "disable" | "cancel"
type Stage = "startWebService" | "connect" | "authorize" | "publish"
type SetupFlow = {
  request: number
  stopIntent: number
  abort: AbortController
  openedUrls: Set<string>
  mutated: boolean
}

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

function authorizationAddress(value: string | null): string | null {
  if (!value) return null
  try {
    const url = new URL(value)
    return url.protocol === "https:" &&
      !url.username &&
      !url.password &&
      !url.port &&
      (url.hostname === "login.tailscale.com" ||
        url.hostname === "tailscale.com")
      ? url.href
      : null
  } catch {
    return null
  }
}

function needsConnection(status: TailscaleFunnelStatus) {
  return (
    status.prerequisite === "login" ||
    status.prerequisite === "connect" ||
    (!status.prerequisite &&
      (status.state === "needsLogin" || status.state === "stopped"))
  )
}

function needsManualSetup(status: TailscaleFunnelStatus) {
  return (
    !status.installed ||
    status.prerequisite === "install" ||
    status.prerequisite === "startDaemon" ||
    status.prerequisite === "approveDevice" ||
    status.prerequisite === "configureDns" ||
    status.error === "deviceApproval" ||
    status.error === "dnsNotReady" ||
    status.state === "unavailable" ||
    status.state === "conflict"
  )
}

export function TailscaleFunnelSettings({
  onAddressChange,
  refreshVersion,
  serviceRunning,
  serviceBusy,
  ensureWebServiceRunning,
  stopIntent,
  stopIntentVersion,
}: {
  onAddressChange: (
    url: string | null,
    select: "initial" | "enabled" | null
  ) => void
  refreshVersion: number
  serviceRunning: boolean
  serviceBusy: boolean
  ensureWebServiceRunning: () => Promise<boolean>
  stopIntent: { readonly current: number }
  stopIntentVersion: number
}) {
  const t = useTranslations("WebServiceSettings.funnel")
  const [status, setStatus] = useState<TailscaleFunnelStatus | null>(null)
  const [refreshing, setRefreshing] = useState(false)
  const [operation, setOperation] = useState<Operation | null>(null)
  const [failure, setFailure] = useState<ErrorCode | "loadFailed" | null>(null)
  const [stage, setStage] = useState<Stage | null>(null)
  const [openFailed, setOpenFailed] = useState(false)
  const [copied, markCopied] = useCopiedFlag()
  const flow = useRef<SetupFlow | null>(null)
  const handledStopIntent = useRef(stopIntentVersion)
  const mounted = useRef(false)
  const revision = useRef(0)
  const refreshInFlight = useRef(false)
  const operationInFlight = useRef<Operation | null>(null)
  const initialRead = useRef(true)
  const selectWhenEnabled = useRef(false)
  const refreshedVersion = useRef(refreshVersion)
  const refreshRequired = useRef(false)
  const selfStartRefresh = useRef(false)

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
        if (url || !flow.current) selectWhenEnabled.current = false
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
    const intent = stopIntent.current
    setRefreshing(true)
    try {
      const next = await getTailscaleFunnelStatus()
      if (
        mounted.current &&
        request === revision.current &&
        intent === stopIntent.current &&
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
  }, [acceptStatus, stopIntent])

  useEffect(() => {
    mounted.current = true
    void refresh()
    const onFocus = () => void refresh()
    window.addEventListener("focus", onFocus)
    return () => {
      mounted.current = false
      const ownedFlow = flow.current
      ownedFlow?.abort.abort()
      flow.current = null
      // Leaving a historical restoration alone is intentional. Only this
      // page's user-initiated operation belongs to this cleanup.
      if (ownedFlow?.mutated) {
        void cancelTailscaleFunnelOperation().catch(() => {})
      }
      revision.current += 1
      refreshInFlight.current = false
      operationInFlight.current = null
      window.removeEventListener("focus", onFocus)
    }
  }, [refresh])

  const pending = isPending(status)
  const active = status?.enabled || status?.desiredEnabled
  useEffect(() => {
    if (operation || serviceBusy || (!pending && !active)) return
    const timer = window.setInterval(
      () => {
        if (document.visibilityState === "visible") void refresh()
      },
      pending ? 2000 : 10000
    )
    return () => window.clearInterval(timer)
  }, [active, pending, refresh, operation, serviceBusy])

  useEffect(() => {
    if (handledStopIntent.current === stopIntentVersion) return
    handledStopIntent.current = stopIntentVersion
    // The parent's ref changes synchronously, before stopWebServer awaits.
    // This effect only clears presentation and any waiting timer.
    flow.current?.abort.abort()
    flow.current = null
    revision.current += 1
    operationInFlight.current = null
    refreshInFlight.current = false
    selectWhenEnabled.current = false
    setOperation(null)
    setStage(null)
    setRefreshing(false)
  }, [stopIntentVersion])

  function isCurrent(setup: SetupFlow) {
    return (
      mounted.current &&
      flow.current === setup &&
      setup.request === revision.current &&
      setup.stopIntent === stopIntent.current &&
      !setup.abort.signal.aborted
    )
  }

  async function openAuthorization(url: string, request = revision.current) {
    try {
      await openUrl(url)
    } catch {
      if (mounted.current && request === revision.current) setOpenFailed(true)
    }
  }

  // Each wait belongs to the initiating sequence. Background effects only read
  // status; they never start login, retry enable, or open authorization pages.
  async function waitUntilSettled(
    setup: SetupFlow,
    next: TailscaleFunnelStatus,
    pendingStage: Stage
  ): Promise<TailscaleFunnelStatus | null> {
    while (isCurrent(setup)) {
      acceptStatus(next)
      const url = authorizationAddress(next.authUrl)
      if (isPending(next)) setStage(url ? "authorize" : pendingStage)
      if (
        setup.mutated &&
        url &&
        !next.error &&
        isDesktop() &&
        !setup.openedUrls.has(url)
      ) {
        setup.openedUrls.add(url)
        void openAuthorization(url, setup.request)
      }
      if (!isPending(next) || next.error) return next
      await new Promise<void>((resolve) => {
        const signal = setup.abort.signal
        const done = () => {
          window.clearTimeout(timer)
          signal.removeEventListener("abort", done)
          resolve()
        }
        const timer = window.setTimeout(done, 2000)
        signal.addEventListener("abort", done, { once: true })
      })
      if (!isCurrent(setup)) return null
      next = await getTailscaleFunnelStatus()
    }
    return null
  }

  async function enablePublicAccess() {
    if (
      !status ||
      serviceBusy ||
      refreshInFlight.current ||
      operationInFlight.current
    )
      return
    const setup: SetupFlow = {
      request: ++revision.current,
      stopIntent: stopIntent.current,
      abort: new AbortController(),
      openedUrls: new Set(),
      mutated: false,
    }
    flow.current = setup
    operationInFlight.current = "enable"
    selectWhenEnabled.current = true
    setOperation("enable")
    setFailure(null)
    setOpenFailed(false)
    let loggedIn = false
    try {
      let next = status
      if (!serviceRunning) {
        setStage("startWebService")
        selfStartRefresh.current = true
        const started = await ensureWebServiceRunning()
        if (!isCurrent(setup) || !started) return
        // A stopped old backend cannot identify which service was stopped.
        // Re-read after our Web start before deciding whether to connect.
        next = await getTailscaleFunnelStatus()
        if (!isCurrent(setup)) return
        acceptStatus(next)
      }
      if (needsManualSetup(next)) {
        acceptStatus(next)
        return
      }
      if (isPending(next)) {
        const settled = await waitUntilSettled(setup, next, "publish")
        if (!settled || !isCurrent(setup)) return
        next = settled
        if (next.error || needsManualSetup(next)) return
      }
      const connect = async () => {
        loggedIn = true
        setup.mutated = true
        setStage("connect")
        return waitUntilSettled(setup, await startTailscaleLogin(), "connect")
      }
      if (needsConnection(next)) {
        const connected = await connect()
        if (!connected || !isCurrent(setup)) return
        next = connected
        if (next.error || needsManualSetup(next) || needsConnection(next))
          return
      }
      if (!isCurrent(setup) || next.enabled) return
      setStage("publish")
      setup.mutated = true
      let published = await waitUntilSettled(
        setup,
        await enableTailscaleFunnel(),
        "publish"
      )
      if (!published || !isCurrent(setup)) return
      // Legacy servers can discover login is needed only on enable. One login
      // and one post-login enable are allowed; no retry loop follows errors.
      if (
        !loggedIn &&
        !needsManualSetup(published) &&
        published.state === "needsLogin" &&
        (!published.error || published.error === "needsLogin")
      ) {
        const connected = await connect()
        if (
          !connected ||
          !isCurrent(setup) ||
          connected.error ||
          needsManualSetup(connected) ||
          needsConnection(connected)
        )
          return
        setStage("publish")
        published = await waitUntilSettled(
          setup,
          await enableTailscaleFunnel(),
          "publish"
        )
        if (!published || !isCurrent(setup)) return
      }
    } catch (error) {
      if (isCurrent(setup)) setFailure(errorCode(error) ?? "failed")
    } finally {
      if (isCurrent(setup)) {
        flow.current = null
        operationInFlight.current = null
        selectWhenEnabled.current = false
        setOperation(null)
        setStage(null)
      }
    }
  }

  async function runOperation(kind: "disable" | "cancel") {
    if (operationInFlight.current === "cancel") return
    if (
      kind !== "cancel" &&
      (serviceBusy || refreshInFlight.current || operationInFlight.current)
    )
      return
    flow.current?.abort.abort()
    flow.current = null
    operationInFlight.current = kind
    const request = ++revision.current
    const intent = stopIntent.current
    refreshInFlight.current = false
    selectWhenEnabled.current = false
    setRefreshing(false)
    setStage(null)
    setOperation(kind)
    setFailure(null)
    try {
      const next = await (kind === "cancel"
        ? cancelTailscaleFunnelOperation()
        : disableTailscaleFunnel())
      if (
        mounted.current &&
        request === revision.current &&
        intent === stopIntent.current
      )
        acceptStatus(next)
    } catch (error) {
      if (mounted.current && request === revision.current)
        setFailure(errorCode(error) ?? "failed")
    } finally {
      if (mounted.current && request === revision.current) {
        operationInFlight.current = null
        setOperation(null)
      }
    }
  }

  const busy = refreshing || operation !== null || serviceBusy
  useEffect(() => {
    if (refreshedVersion.current !== refreshVersion) {
      refreshedVersion.current = refreshVersion
      // Our sequence already reads status after starting Web. A second read
      // here could overwrite its confirmed URL with an older observation.
      refreshRequired.current = !selfStartRefresh.current
      selfStartRefresh.current = false
    }
    if (refreshRequired.current && !busy) {
      refreshRequired.current = false
      void refresh()
    }
  }, [busy, refresh, refreshVersion])

  const canCancel = pending || operation === "enable"
  const canDisable = status?.owned || status?.desiredEnabled
  const canEnable = status?.installed && !status.enabled
  const authUrl = authorizationAddress(status?.authUrl ?? null)
  const prerequisite =
    status?.prerequisite ??
    (status?.state === "notInstalled"
      ? "install"
      : status?.state === "needsLogin"
        ? "login"
        : status?.state === "stopped"
          ? serviceRunning
            ? "connect"
            : "startWebService"
          : null)

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
        {stage
          ? t(`stages.${stage}`)
          : operation
            ? t("processing")
            : status
              ? t(`states.${status.state}`)
              : t("loading")}
      </p>
      {prerequisite && !stage && (
        <p className="text-xs text-muted-foreground">
          {t(`hints.${prerequisite}`)}
        </p>
      )}
      {stage === "authorize" && (
        <p className="text-xs text-muted-foreground">
          {t("authorizationHint")}
        </p>
      )}
      {openFailed && (
        <p role="alert" className="text-sm text-destructive">
          {t("openAuthFailed")}
        </p>
      )}
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
        {canEnable && !canCancel && (
          <button
            type="button"
            className={BUTTON_CLASS}
            disabled={busy || (!!status && needsManualSetup(status))}
            onClick={() => void enablePublicAccess()}
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
            disabled={
              operation === "cancel" || (serviceBusy && operation !== "enable")
            }
            onClick={() => void runOperation("cancel")}
          >
            {t("cancel")}
          </button>
        )}
      </div>
      {authUrl && (
        <div className="space-y-2">
          <code className="block text-xs break-all select-all">{authUrl}</code>
          <div className="flex flex-wrap gap-2">
            <button
              type="button"
              className={BUTTON_CLASS}
              onClick={() => void openAuthorization(authUrl)}
            >
              <ExternalLink className="h-3.5 w-3.5" />
              {t("openAuth")}
            </button>
            <button
              type="button"
              className={BUTTON_CLASS}
              onClick={async () => {
                if (await copyTextToClipboard(authUrl)) markCopied()
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
