"use client"

import { useCallback, useEffect, useState } from "react"
import { Loader2, Pencil, Plus, Trash2 } from "lucide-react"
import { useTranslations } from "next-intl"
import { toast } from "sonner"
import { comicApi, type MediaProviderInfo } from "@/lib/comic"
import { toErrorMessage } from "@/lib/app-error"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Textarea } from "@/components/ui/textarea"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
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

export function MediaProviderSettings() {
  const t = useTranslations("MediaProviderSettings")
  const [providers, setProviders] = useState<MediaProviderInfo[]>([])
  const [editing, setEditing] = useState<MediaProviderInfo | "new" | null>(null)
  const [deleting, setDeleting] = useState<MediaProviderInfo | null>(null)
  const reload = useCallback(async () => {
    try {
      setProviders(await comicApi.listMediaProviders())
    } catch (error) {
      toast.error(toErrorMessage(error))
    }
  }, [])
  useEffect(() => {
    let disposed = false
    void comicApi.listMediaProviders().then(
      (rows) => {
        if (!disposed) setProviders(rows)
      },
      (error: unknown) => {
        if (!disposed) toast.error(toErrorMessage(error))
      }
    )
    return () => {
      disposed = true
    }
  }, [])

  return (
    <section className="h-full space-y-4 overflow-y-auto p-4">
      <h1 className="text-sm font-semibold">{t("title")}</h1>
      <p className="max-w-2xl text-sm text-muted-foreground">
        {t("description")}
      </p>
      <Button size="sm" onClick={() => setEditing("new")}>
        <Plus className="size-4" />
        {t("add")}
      </Button>
      {providers.length === 0 && (
        <p className="text-sm text-muted-foreground">{t("empty")}</p>
      )}
      {providers.map((provider) => (
        <div
          key={provider.id}
          className="flex items-center gap-3 rounded-md border p-3"
        >
          <div className="min-w-0 flex-1">
            <p className="text-sm font-medium">
              {provider.name}
              {provider.isDefault ? " ★" : ""}
            </p>
            <p className="truncate text-xs text-muted-foreground">
              {provider.kind} · {provider.model} · {provider.apiUrl}
              {provider.capabilities &&
                !provider.capabilities.available &&
                ` · ${t("unavailable")} (${provider.capabilities.protocol})`}
            </p>
          </div>
          <Button
            size="icon"
            variant="ghost"
            aria-label={t("edit")}
            onClick={() => setEditing(provider)}
          >
            <Pencil className="size-4" />
          </Button>
          <Button
            size="icon"
            variant="ghost"
            aria-label={t("delete")}
            onClick={() => setDeleting(provider)}
          >
            <Trash2 className="size-4" />
          </Button>
        </div>
      ))}
      {editing && (
        <ProviderEditor
          key={editing === "new" ? "new" : editing.id}
          provider={editing}
          onClose={() => setEditing(null)}
          onSaved={reload}
        />
      )}
      <AlertDialog
        open={deleting !== null}
        onOpenChange={(open) => {
          if (!open) setDeleting(null)
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t("deleteTitle")}</AlertDialogTitle>
            <AlertDialogDescription>{t("deleteHint")}</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>{t("cancel")}</AlertDialogCancel>
            <AlertDialogAction
              onClick={() => {
                if (!deleting) return
                void comicApi
                  .deleteMediaProvider(deleting.id)
                  .then(() => {
                    setDeleting(null)
                    void reload()
                  })
                  .catch((e) => toast.error(toErrorMessage(e)))
              }}
            >
              {t("delete")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </section>
  )
}

function ProviderEditor({
  provider,
  onClose,
  onSaved,
}: {
  provider: MediaProviderInfo | "new"
  onClose: () => void
  onSaved: () => Promise<void>
}) {
  const t = useTranslations("MediaProviderSettings")
  const original = provider === "new" ? null : provider
  const [name, setName] = useState(original?.name ?? "")
  const [kind, setKind] = useState(original?.kind ?? "image")
  const [url, setUrl] = useState(original?.apiUrl ?? "")
  const [key, setKey] = useState("")
  const [model, setModel] = useState(original?.model ?? "")
  // "{}" rather than a forced protocol: the backend resolves an omitted
  // protocol to the kind's implemented default (ark for video), and an
  // explicit unsupported protocol stays visibly unavailable.
  const [extra, setExtra] = useState(original?.extraJson || "{}")
  // The protocol picker owns the JSON `protocol` key as real state: hand
  // edits in the raw-JSON textarea can touch other keys (capabilities,
  // workflow, …) but never silently drop the chosen protocol.
  const [protocol, setProtocol] = useState(() => {
    try {
      const value: unknown = JSON.parse(original?.extraJson || "{}")
      const read =
        value && typeof value === "object" && !Array.isArray(value)
          ? (value as Record<string, unknown>).protocol
          : undefined
      return typeof read === "string" ? read : "default"
    } catch {
      return "default"
    }
  })
  const [modelOptions, setModelOptions] = useState<string[]>([])
  const [isDefault, setDefault] = useState(original?.isDefault ?? false)
  const [busy, setBusy] = useState(false)
  const setProtocolChoice = (next: string) => {
    setProtocol(next)
    let config: Record<string, unknown> = {}
    try {
      const value: unknown = JSON.parse(extra)
      if (value && typeof value === "object" && !Array.isArray(value))
        config = value as Record<string, unknown>
    } catch {
      // Invalid JSON surfaces through the save-time validation, not here.
    }
    if (next === "default") delete config.protocol
    else config.protocol = next
    setExtra(JSON.stringify(config, null, 2))
  }
  const fetchModels = async () => {
    setBusy(true)
    try {
      const { models } = await comicApi.listProviderModels({
        kind,
        apiUrl: url.trim(),
        apiKey: key,
        extraJson: extra,
      })
      setModelOptions(models)
      if (models.length === 0) toast.error(t("noModels"))
    } catch (error) {
      toast.error(toErrorMessage(error))
    } finally {
      setBusy(false)
    }
  }
  const save = async () => {
    let config: unknown
    try {
      config = JSON.parse(extra)
    } catch {
      toast.error(t("invalidJson"))
      return
    }
    if (!config || typeof config !== "object" || Array.isArray(config)) {
      toast.error(t("invalidJson"))
      return
    }
    // The protocol picker wins over anything hand-typed in the JSON area, so
    // editing capabilities there can never silently drop the chosen protocol.
    const merged: Record<string, unknown> = { ...config }
    if (protocol === "default") delete merged.protocol
    else merged.protocol = protocol
    setBusy(true)
    try {
      const fields = {
        name: name.trim(),
        apiUrl: url.trim(),
        model: model.trim(),
        extraJson: JSON.stringify(merged),
        isDefault,
      }
      if (original)
        await comicApi.updateMediaProvider(original.id, {
          ...fields,
          ...(key ? { apiKey: key } : {}),
        })
      else await comicApi.createMediaProvider({ ...fields, kind, apiKey: key })
      await onSaved()
      toast.success(t("saved"))
      onClose()
    } catch (error) {
      toast.error(toErrorMessage(error))
    } finally {
      setBusy(false)
    }
  }
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open && !busy) onClose()
      }}
    >
      <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{t(original ? "edit" : "add")}</DialogTitle>
          <DialogDescription>{t("description")}</DialogDescription>
        </DialogHeader>
        <div className="grid gap-3">
          <Label htmlFor="media-name">{t("name")}</Label>
          <Input
            id="media-name"
            value={name}
            onChange={(e) => setName(e.target.value)}
          />
          <Label htmlFor="media-kind">{t("kind")}</Label>
          <select
            id="media-kind"
            className="h-9 rounded-md border bg-background px-2 text-sm"
            value={kind}
            disabled={original !== null}
            onChange={(e) => setKind(e.target.value as typeof kind)}
          >
            <option value="image">{t("image")}</option>
            <option value="video">{t("video")}</option>
            <option value="tts">{t("tts")}</option>
          </select>
          <Label htmlFor="media-protocol">{t("protocol")}</Label>
          <select
            id="media-protocol"
            className="h-9 rounded-md border bg-background px-2 text-sm"
            value={protocol}
            onChange={(e) => setProtocolChoice(e.target.value)}
          >
            <option value="default">
              {kind === "video"
                ? t("protocolDefaultArk")
                : t("protocolDefaultOpenai")}
            </option>
            <option value="openai_compat">{t("openai")}</option>
            {kind === "image" && (
              <option value="comfyui">{t("comfyui")}</option>
            )}
            {kind === "video" && <option value="ark">{t("ark")}</option>}
            {kind === "video" && <option value="newapi">{t("newapi")}</option>}
          </select>
          <Label htmlFor="media-url">{t("url")}</Label>
          <Input
            id="media-url"
            type="url"
            value={url}
            onChange={(e) => setUrl(e.target.value)}
          />
          <Label htmlFor="media-key">{t("key")}</Label>
          <Input
            id="media-key"
            type="password"
            autoComplete="new-password"
            value={key}
            onChange={(e) => setKey(e.target.value)}
          />
          <p className="text-xs text-muted-foreground">{t("keyHint")}</p>
          <div className="flex items-center justify-between">
            <Label htmlFor="media-model">{t("model")}</Label>
            <Button
              variant="outline"
              size="sm"
              disabled={busy || !url.trim() || !key.trim()}
              onClick={() => void fetchModels()}
            >
              {t("fetchModels")}
            </Button>
          </div>
          <Input
            id="media-model"
            list="media-model-options"
            value={model}
            aria-describedby={
              kind === "video" ? "media-video-model-hint" : undefined
            }
            onChange={(e) => setModel(e.target.value)}
          />
          <datalist id="media-model-options">
            {modelOptions.map((model) => (
              <option key={model} value={model} />
            ))}
          </datalist>
          {kind === "video" && (
            <p
              id="media-video-model-hint"
              className="text-xs text-muted-foreground"
            >
              {t("videoModelHint")}
            </p>
          )}
          <Label htmlFor="media-extra">{t("extra")}</Label>
          <Textarea
            id="media-extra"
            className="min-h-24 font-mono text-xs"
            value={extra}
            onChange={(e) => setExtra(e.target.value)}
          />
          <p className="text-xs text-muted-foreground">{t("extraHint")}</p>
          {kind === "video" && (
            <div className="space-y-2 rounded-md border p-3 text-xs text-muted-foreground">
              {protocol === "newapi" && <p>{t("videoGatewayHint")}</p>}
              <p>{t("videoCapabilitiesHint")}</p>
            </div>
          )}
          <label className="flex items-center gap-2 text-sm">
            <input
              type="checkbox"
              checked={isDefault}
              onChange={(e) => setDefault(e.target.checked)}
            />
            {t("default")}
          </label>
        </div>
        <DialogFooter>
          <Button
            disabled={busy || !name.trim() || !url.trim()}
            onClick={() => void save()}
          >
            {busy && <Loader2 className="size-4 animate-spin" />}
            {t("save")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
