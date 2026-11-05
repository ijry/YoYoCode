"use client"

import { useEffect, useState } from "react"
import { AlertCircle, Loader2 } from "lucide-react"
import { useTranslations } from "next-intl"
import { getTransport } from "@/lib/transport"
import { toErrorMessage } from "@/lib/app-error"

/** A host-validated story image. Keep the folder id in the request rather
 *  than resolving against whichever workspace happens to be active. */
export function ComicImage({
  folderId,
  path,
  alt,
  className,
  onPreview,
}: {
  folderId: number
  path: string
  alt: string
  className?: string
  onPreview?: (src: string) => void
}) {
  const t = useTranslations("Folder.chat.contentParts")
  const key = `${folderId}\0${path}`
  const [result, setResult] = useState<{
    key: string
    src?: string
    error?: string
  } | null>(null)

  useEffect(() => {
    let disposed = false
    void getTransport()
      .call<{ mime: string; data: string }>("comic_read_image", {
        folderId,
        path,
      })
      .then(
        ({ mime, data }) => {
          if (!disposed) {
            setResult({ key, src: `data:${mime};base64,${data}` })
          }
        },
        (error: unknown) => {
          if (!disposed) setResult({ key, error: toErrorMessage(error) })
        }
      )
    return () => {
      disposed = true
    }
  }, [folderId, path, key])

  const current = result?.key === key ? result : null
  if (!current) {
    return (
      <span
        role="status"
        className="flex min-h-24 items-center justify-center gap-2 text-xs text-muted-foreground"
      >
        <Loader2 aria-hidden="true" className="size-4 animate-spin" />
        {t("readImageLoading")}
      </span>
    )
  }
  if (!current.src) {
    return (
      <span
        className="flex min-h-24 items-center gap-2 text-xs text-muted-foreground"
        title={current.error}
      >
        <AlertCircle aria-hidden="true" className="size-4" />
        {t("readImageError")}
      </span>
    )
  }
  const image = (
    // Local media loaded through the transport; Next's image optimizer is not
    // available in the static export.
    // eslint-disable-next-line @next/next/no-img-element
    <img
      src={current.src}
      alt={alt}
      className={
        className ?? "mx-auto max-h-64 w-full rounded-md object-contain"
      }
      onError={() => setResult({ key, error: t("readImageError") })}
    />
  )
  if (!onPreview) return image
  return (
    <button
      type="button"
      aria-label={alt}
      className="block w-full cursor-zoom-in"
      onClick={(event) => {
        event.preventDefault()
        event.stopPropagation()
        onPreview(current.src!)
      }}
    >
      {image}
    </button>
  )
}
