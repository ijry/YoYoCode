"use client"

import { useEffect, useRef, useState } from "react"

import { useReducedMotion } from "@/hooks/use-chat-motion"
import { cn } from "@/lib/utils"

const RUNTIME_URL = "/logo/animation/lottie-web-5.12.2.min.js"
const DATA_URL = "/logo/animation/jellyfish-breathe.json"
const POSTER_URL = "/logo/animation/jellyfish-breathe-alpha-poster.png"

interface LottieAnimationHandle {
  destroy(): void
}

interface LottiePlayer {
  loadAnimation(options: {
    container: HTMLElement
    renderer: "svg"
    loop: boolean
    autoplay: boolean
    animationData: unknown
  }): LottieAnimationHandle
}

type LottieWindow = Window & { lottie?: LottiePlayer }

let runtimePromise: Promise<LottiePlayer> | null = null

/** lottie-web is vendored under public/ rather than bundled: the About page is
 * the only consumer, and a 305 KiB renderer should not sit in the app chunk.
 * A single in-flight promise keeps repeated mounts from stacking scripts. */
function loadLottieRuntime(): Promise<LottiePlayer> {
  if (runtimePromise) return runtimePromise
  runtimePromise = new Promise<LottiePlayer>((resolve, reject) => {
    const windowWithLottie = window as LottieWindow
    if (windowWithLottie.lottie) {
      resolve(windowWithLottie.lottie)
      return
    }
    const script = document.createElement("script")
    script.src = RUNTIME_URL
    script.async = true
    script.onload = () => {
      const player = windowWithLottie.lottie
      if (player) {
        resolve(player)
      } else {
        runtimePromise = null
        reject(new Error("lottie-web did not register a player"))
      }
    }
    script.onerror = () => {
      runtimePromise = null
      reject(new Error("failed to load lottie-web"))
    }
    document.head.appendChild(script)
  })
  return runtimePromise
}

/** The breathing jellyfish mark, rendered as the Lottie/vector version. */
export function AboutLogoAnimation({
  className,
  label,
}: {
  className?: string
  label: string
}) {
  const hostRef = useRef<HTMLDivElement>(null)
  const reducedMotion = useReducedMotion()
  const [failed, setFailed] = useState(false)
  const showPoster = reducedMotion || failed

  useEffect(() => {
    if (showPoster) return
    const host = hostRef.current
    if (!host) return

    let cancelled = false
    let animation: LottieAnimationHandle | null = null

    void (async () => {
      try {
        const [player, response] = await Promise.all([
          loadLottieRuntime(),
          fetch(DATA_URL),
        ])
        if (!response.ok) {
          throw new Error(`Lottie data HTTP ${response.status}`)
        }
        const animationData: unknown = await response.json()
        if (cancelled) return
        animation = player.loadAnimation({
          container: host,
          renderer: "svg",
          loop: true,
          autoplay: true,
          animationData,
        })
      } catch {
        if (!cancelled) setFailed(true)
      }
    })()

    return () => {
      cancelled = true
      animation?.destroy()
      // lottie-web owns the SVG it mounts. Clear only that private host so
      // React's poster <img> sibling is never touched by imperative DOM work.
      host.replaceChildren()
    }
  }, [showPoster])

  return (
    <div
      className={cn("relative shrink-0 overflow-hidden", className)}
      role="img"
      aria-label={label}
    >
      <div ref={hostRef} className="absolute inset-0" aria-hidden="true" />
      {showPoster && (
        // eslint-disable-next-line @next/next/no-img-element
        <img
          src={POSTER_URL}
          alt=""
          className="absolute inset-0 h-full w-full object-contain"
        />
      )}
    </div>
  )
}
