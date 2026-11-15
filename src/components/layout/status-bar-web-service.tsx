"use client"

/**
 * Web service (remote access), bottom-right of the workspace: whether the
 * desktop is serving Codeg over HTTP right now, on which address, and a way
 * to start or stop it without opening Settings.
 *
 * Desktop only — a browser is already talking to a web service, and a
 * remote-desktop window's calls go to the remote server — and always present
 * there, because the point is a passive signal: the glyph turns green while
 * the service is listening, and its tooltip says how far it reaches (every
 * interface, i.e. the local network, or this computer only).
 *
 * Starting a service that will listen on every interface asks first: it puts
 * the terminals and agents within reach of the local network, behind the
 * access token. A loopback-only start, and every stop, just happen.
 *
 * Starts and stops from any window — this one, the Settings page, auto-start
 * at launch — are broadcast, so the glyph never disagrees with Settings.
 *
 * PLUS: the same popover also glances at our two ways of reaching the service
 * from outside this computer — a Tailscale Funnel HTTPS endpoint and the MCode
 * phone connection code. Enabling Funnel is a multi-step flow (install, login,
 * browser authorization, publish), so it stays in Settings; here we only show
 * its state, copy the public endpoint, and hand off. The phone pairing is
 * self-contained: the QR code carries the chosen endpoint and the token.
 */

import { useCallback, useEffect, useState } from "react"
import { useTranslations } from "next-intl"
import {
  Check,
  Copy,
  Globe2,
  QrCode,
  RadioTower,
  Settings2,
  Smartphone,
} from "lucide-react"
import { QRCodeSVG } from "qrcode.react"

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover"
import { useCopiedFlag } from "@/hooks/use-copied-flag"
import {
  getTailscaleFunnelStatus,
  getWebServiceConfig,
  openSettingsWindow,
  startWebServer,
  stopWebServer,
  type TailscaleFunnelStatus,
  type WebServerInfo,
  type WebServiceConfig,
  type WebServiceHost,
} from "@/lib/api"
import { buildMcodeConfigCode } from "@/lib/mcode-config-code"
import { openUrl } from "@/lib/platform"
import { asWebServiceHost, isLoopbackHost } from "@/lib/web-service/hosts"
import { useLocalWebServerStatus } from "@/lib/web-service/use-local-web-server-status"
import { useWebServerStartErrorMessage } from "@/lib/web-service/start-error"
import { copyTextToClipboard, cn } from "@/lib/utils"

const DEFAULT_PORT = 3080
const MCODE_DOWNLOAD_URL = "https://getmcode.lingyun.net"
const RUNNING_TONE = "text-emerald-600 dark:text-emerald-400"
const ICON_BUTTON_CLASS =
  "inline-flex size-6 shrink-0 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-accent hover:text-foreground disabled:pointer-events-none disabled:opacity-40"

/** Where a start from here binds — decided once, from a fresh read of the
 *  saved settings, and then passed to the start verbatim, so what the
 *  confirmation names is exactly what gets bound. */
interface StartTarget {
  host: WebServiceHost
  port: number
}

// Only a confirmed HTTPS origin belongs in the address list. In particular,
// Tailscale authorization links and anything carrying credentials do not.
function funnelPublicAddress(
  funnel: TailscaleFunnelStatus | null | undefined
): string | null {
  if (!funnel?.enabled || !funnel.url) return null
  try {
    const url = new URL(funnel.url)
    return url.protocol === "https:" && !url.username && !url.password
      ? url.origin
      : null
  } catch {
    return null
  }
}

function isLoopbackAddress(address: string): boolean {
  try {
    const host = new URL(address).hostname.replace(/^\[|\]$/g, "")
    return host === "127.0.0.1" || host === "localhost" || host === "::1"
  } catch {
    return false
  }
}

/** A phone is often off the LAN, so prefer the public Funnel endpoint. With
 *  no public access, pick a LAN address over the loopback one. */
function mobileEndpoint(
  status: WebServerInfo | null,
  publicUrl: string | null
): string | null {
  if (publicUrl) return publicUrl
  const addresses = status?.addresses ?? []
  return (
    addresses.find((address) => !isLoopbackAddress(address)) ??
    addresses[0] ??
    null
  )
}

function AddressRow({
  address,
  copyLabel,
  dense = false,
}: {
  address: string
  copyLabel: string
  dense?: boolean
}) {
  const [copied, markCopied] = useCopiedFlag()
  return (
    <div
      className={cn(
        "flex items-center gap-2",
        dense ? "min-w-0 py-0.5" : "px-2 py-1"
      )}
    >
      <code className="min-w-0 flex-1 truncate text-2xs select-all">
        {address}
      </code>
      <button
        type="button"
        onClick={async () => {
          if (await copyTextToClipboard(address)) markCopied()
        }}
        aria-label={copyLabel}
        title={copyLabel}
        className="text-muted-foreground transition-colors hover:text-foreground"
      >
        {copied ? (
          <Check className="size-3 text-green-500" />
        ) : (
          <Copy className="size-3" />
        )}
      </button>
    </div>
  )
}

export function StatusBarWebService() {
  const t = useTranslations("Folder.statusBar.webService")
  const ts = useTranslations("WebServiceSettings")
  const startErrorMessage = useWebServerStartErrorMessage()
  const { status, enabled, refresh, mark, applySince } =
    useLocalWebServerStatus()
  const [open, setOpen] = useState(false)
  const [config, setConfig] = useState<WebServiceConfig | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  /** The network-wide start awaiting confirmation. */
  const [pending, setPending] = useState<StartTarget | null>(null)
  /** `undefined` until the first Funnel read; `null` when the read failed. */
  const [funnel, setFunnel] = useState<
    TailscaleFunnelStatus | null | undefined
  >(undefined)
  const [qrOpen, setQrOpen] = useState(false)
  const [codeCopied, markCodeCopied] = useCopiedFlag()

  const refreshFunnel = useCallback(async () => {
    try {
      setFunnel(await getTailscaleFunnelStatus())
    } catch {
      setFunnel(null)
    }
  }, [])

  // Funnel can be enabled in Settings (or still be publishing) while this
  // popover is open. Keep the public endpoint fresh, faster while settling.
  // Hooks stay above the `enabled` early return so their order never changes.
  useEffect(() => {
    if (!open || !enabled || status === undefined) return
    const settling =
      funnel?.state === "starting" ||
      funnel?.state === "authorizing" ||
      !!funnel?.desiredEnabled
    const timer = window.setInterval(
      () => {
        if (document.visibilityState === "visible") void refreshFunnel()
      },
      settling ? 2000 : 10000
    )
    return () => window.clearInterval(timer)
  }, [open, enabled, status, funnel, refreshFunnel])

  if (!enabled || status === undefined) return null

  const running = status !== null
  const loopback = running && isLoopbackHost(status.bindHost)
  // What a start would bind, for the stopped hint: the saved choice, or the
  // default when none was ever made. Re-read at the moment of starting.
  const nextHost = asWebServiceHost(config?.host)
  const nextPort = config?.port ?? DEFAULT_PORT
  const nextLoopback = config !== null && isLoopbackHost(nextHost)

  const publicUrl = funnelPublicAddress(funnel)
  const endpoint = mobileEndpoint(status, publicUrl)
  // Contains the bearer token: never logged, never put in a URL or tooltip.
  const mobileCode =
    running && endpoint
      ? buildMcodeConfigCode(endpoint, status.token, status.hostName)
      : null
  const funnelSettling =
    funnel?.state === "starting" ||
    funnel?.state === "authorizing" ||
    !!funnel?.desiredEnabled
  const funnelLabel =
    funnel === undefined
      ? ts("processing")
      : publicUrl
        ? t("tailscaleOn")
        : funnelSettling
          ? t("tailscalePending")
          : funnel === null
            ? t("tailscaleUnavailable")
            : funnel.installed
              ? t("tailscaleOff")
              : t("tailscaleSetup")

  const openSettings = () => {
    openSettingsWindow("web-service").catch((err) => {
      console.error("[StatusBarWebService] failed to open settings:", err)
    })
  }

  const copyMobileCode = async () => {
    if (!mobileCode) return
    if (await copyTextToClipboard(mobileCode)) markCodeCopied()
  }

  const handleOpenChange = (next: boolean) => {
    setOpen(next)
    if (!next) return
    setError(null)
    setQrOpen(false)
    void refresh()
    getWebServiceConfig()
      .then(setConfig)
      .catch(() => setConfig(null))
    void refreshFunnel()
  }

  const start = async (target: StartTarget) => {
    setBusy(true)
    setError(null)
    const since = mark()
    try {
      // Host and port explicit (the ones just confirmed, or loopback); the
      // token is left to the backend, which uses the saved one.
      applySince(
        await startWebServer({ host: target.host, port: target.port }),
        since
      )
    } catch (e) {
      setError(startErrorMessage(e, target.port))
    } finally {
      setBusy(false)
    }
  }

  const stop = async () => {
    setBusy(true)
    setError(null)
    const since = mark()
    try {
      await stopWebServer()
      applySince(null, since)
      // The pairing code embeds a listener that is going away.
      setQrOpen(false)
    } catch {
      setError(ts("stopFailed"))
    } finally {
      setBusy(false)
    }
  }

  const handleStartClick = async () => {
    setBusy(true)
    setError(null)
    // Fresh, not what the popover read on opening: Settings may have
    // switched the host since, and a network-wide start must never slip
    // through without its confirmation.
    let saved: WebServiceConfig
    try {
      saved = await getWebServiceConfig()
    } catch (e) {
      setError(startErrorMessage(e, nextPort))
      setBusy(false)
      return
    }
    setConfig(saved)
    setBusy(false)
    const target: StartTarget = {
      host: asWebServiceHost(saved.host),
      port: saved.port ?? DEFAULT_PORT,
    }
    if (isLoopbackHost(target.host)) void start(target)
    else setPending(target)
  }

  const listening = running ? `${status.bindHost}:${status.port}` : ""
  const tooltip = running
    ? t(loopback ? "tooltipLocal" : "tooltipNetwork", { address: listening })
    : t("tooltipStopped")

  return (
    <>
      <Popover open={open} onOpenChange={handleOpenChange}>
        <PopoverTrigger asChild>
          <button
            type="button"
            aria-label={t("title")}
            title={tooltip}
            data-state-running={running ? "true" : "false"}
            className={cn(
              "flex items-center transition-colors",
              running ? RUNNING_TONE : "hover:text-foreground"
            )}
          >
            <RadioTower className="size-3.5" />
          </button>
        </PopoverTrigger>
        <PopoverContent
          side="top"
          align="end"
          className="w-80 gap-2 p-2.5"
          onInteractOutside={(event) => {
            // The QR dialog portals to body; opening it must not dismiss the
            // popover that owns it.
            if (qrOpen) event.preventDefault()
          }}
        >
          <div className="flex items-center justify-between gap-2">
            <span className="truncate text-xs font-medium">{t("title")}</span>
            <span
              className={cn(
                "flex shrink-0 items-center gap-1 rounded-full px-1.5 py-0.5 text-3xs font-medium",
                running
                  ? "bg-emerald-500/10 text-emerald-600 dark:text-emerald-400"
                  : "bg-muted text-muted-foreground"
              )}
            >
              <span
                aria-hidden="true"
                className={cn(
                  "size-1.5 rounded-full",
                  running ? "bg-emerald-500" : "bg-muted-foreground/50"
                )}
              />
              {running ? ts("running") : ts("stopped")}
            </span>
          </div>

          <p className="text-2xs text-muted-foreground">
            {running
              ? t(loopback ? "hintLocal" : "hintNetwork", {
                  address: listening,
                })
              : t(nextLoopback ? "hintStoppedLocal" : "hintStoppedNetwork", {
                  address: `${nextHost}:${nextPort}`,
                })}
          </p>

          {running && status.addresses.length > 0 && (
            <div className="divide-y overflow-hidden rounded-lg border">
              {status.addresses.map((address) => (
                <AddressRow
                  key={address}
                  address={address}
                  copyLabel={t("copyAddress")}
                />
              ))}
            </div>
          )}

          {/* Our two off-machine routes. Funnel's setup state machine stays in
              Settings; this is a glance, a copy target and a phone pairing. */}
          <div className="space-y-2 rounded-lg border bg-muted/30 p-2">
            <div className="text-3xs font-medium tracking-wide text-muted-foreground uppercase">
              {t("remoteTitle")}
            </div>

            <div className="flex items-start gap-2">
              <Globe2 className="mt-0.5 size-3.5 shrink-0 text-muted-foreground" />
              <div className="min-w-0 flex-1">
                <div className="flex items-center gap-1.5">
                  <span className="truncate text-2xs font-medium">
                    {t("tailscaleTitle")}
                  </span>
                  <span
                    className={cn(
                      "shrink-0 rounded-full px-1 py-0.5 text-3xs font-medium",
                      publicUrl
                        ? "bg-emerald-500/10 text-emerald-600 dark:text-emerald-400"
                        : "bg-muted text-muted-foreground"
                    )}
                  >
                    {funnelLabel}
                  </span>
                </div>
                {publicUrl ? (
                  <AddressRow
                    address={publicUrl}
                    copyLabel={t("copyPublicAddress")}
                    dense
                  />
                ) : (
                  <p className="text-3xs text-muted-foreground">
                    {t("tailscaleHint")}
                  </p>
                )}
              </div>
              <button
                type="button"
                onClick={openSettings}
                className={ICON_BUTTON_CLASS}
                aria-label={t("tailscaleConfigure")}
                title={t("tailscaleConfigure")}
              >
                <Settings2 className="size-3" />
              </button>
            </div>

            <div className="border-t" />

            <div className="flex items-center gap-2">
              <Smartphone className="size-3.5 shrink-0 text-muted-foreground" />
              <div className="min-w-0 flex-1">
                <div className="truncate text-2xs font-medium">
                  {t("mobileTitle")}
                </div>
                <p className="text-3xs text-muted-foreground">
                  {mobileCode ? t("mobileHint") : t("mobileStoppedHint")}
                </p>
              </div>
              {mobileCode && (
                <>
                  <button
                    type="button"
                    onClick={() => setQrOpen(true)}
                    className={ICON_BUTTON_CLASS}
                    aria-label={t("showMobileQr")}
                    title={t("showMobileQr")}
                  >
                    <QrCode className="size-3.5" />
                  </button>
                  <button
                    type="button"
                    onClick={() => void copyMobileCode()}
                    className={ICON_BUTTON_CLASS}
                    aria-label={ts("mobileConfigCopy")}
                    title={ts("mobileConfigCopy")}
                  >
                    {codeCopied ? (
                      <Check className="size-3.5 text-green-500" />
                    ) : (
                      <Copy className="size-3.5" />
                    )}
                  </button>
                </>
              )}
            </div>
          </div>

          {error && (
            <div
              role="alert"
              className="rounded-md border border-red-500/30 bg-red-500/5 px-2 py-1.5 text-2xs break-words text-red-400"
            >
              {error}
            </div>
          )}

          <Button
            size="sm"
            variant={running ? "outline" : "default"}
            className="w-full"
            disabled={busy}
            onClick={() => void (running ? stop() : handleStartClick())}
          >
            {busy ? ts("processing") : running ? t("stop") : t("start")}
          </Button>
          <Button
            size="sm"
            variant="outline"
            className="w-full"
            onClick={openSettings}
          >
            <Settings2 className="h-3.5 w-3.5" />
            {t("openSettings")}
          </Button>
        </PopoverContent>
      </Popover>

      {/* Outside the Popover: its dismissable layer would otherwise close on
          the dialog's portal, taking the QR code with it. */}
      <Dialog open={qrOpen && !!mobileCode} onOpenChange={setQrOpen}>
        <DialogContent className="sm:max-w-xs">
          <DialogHeader>
            <DialogTitle>{t("mobileQrTitle")}</DialogTitle>
          </DialogHeader>
          <div className="flex flex-col items-center gap-3 py-2">
            <div className="rounded-lg bg-white p-3">
              <QRCodeSVG
                value={mobileCode ?? ""}
                size={192}
                marginSize={0}
                role="img"
                aria-label={ts("mobileConfigQrLabel")}
              />
            </div>
            {endpoint && (
              <code className="text-center text-2xs break-all text-muted-foreground select-all">
                {endpoint}
              </code>
            )}
            <p className="text-center text-2xs text-muted-foreground">
              {ts("mobileConfigSecretHint")}
            </p>
            <div className="flex flex-wrap justify-center gap-2">
              <Button
                size="sm"
                variant="outline"
                className="h-7 text-xs"
                onClick={() => void copyMobileCode()}
              >
                {codeCopied ? (
                  <Check className="size-3.5" />
                ) : (
                  <Copy className="size-3.5" />
                )}
                {codeCopied ? ts("mobileConfigCopied") : ts("mobileConfigCopy")}
              </Button>
              <Button
                size="sm"
                variant="outline"
                className="h-7 text-xs"
                onClick={() => void openUrl(MCODE_DOWNLOAD_URL)}
              >
                <Smartphone className="size-3.5" />
                {ts("mobileRemoteDownload")}
              </Button>
            </div>
          </div>
        </DialogContent>
      </Dialog>

      <AlertDialog
        open={pending !== null}
        onOpenChange={(next) => {
          if (!next) setPending(null)
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t("confirmTitle")}</AlertDialogTitle>
            <AlertDialogDescription>
              {t("confirmDescription", {
                address: pending ? `${pending.host}:${pending.port}` : "",
              })}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t("cancel")}</AlertDialogCancel>
            <AlertDialogAction
              onClick={() => {
                const target = pending
                setPending(null)
                if (target) void start(target)
              }}
            >
              {t("confirmStart")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  )
}
