import { cleanup, render, waitFor } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { AboutLogoAnimation } from "./about-logo-animation"

const reducedMotion = vi.fn<() => boolean>()
vi.mock("@/hooks/use-chat-motion", () => ({
  useReducedMotion: () => reducedMotion(),
}))

const RUNTIME_URL = "/logo/animation/lottie-web-5.12.2.min.js"
const POSTER_URL = "/logo/animation/jellyfish-breathe-alpha-poster.png"

type LottieWindow = Window & {
  lottie?: { loadAnimation: (options: unknown) => { destroy: () => void } }
}

beforeEach(() => {
  reducedMotion.mockReturnValue(false)
})
afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
  delete (window as LottieWindow).lottie
  document.head.querySelectorAll("script").forEach((s) => s.remove())
})

describe("AboutLogoAnimation", () => {
  it("shows the transparent poster without loading anything when motion is reduced", () => {
    reducedMotion.mockReturnValue(true)
    const { container } = render(
      <AboutLogoAnimation className="size-20" label="Animated logo" />
    )

    const poster = container.querySelector("img")
    expect(poster).toHaveAttribute("src", POSTER_URL)
    expect(
      document.head.querySelector(`script[src="${RUNTIME_URL}"]`)
    ).toBeNull()
    expect(container.querySelector('[role="img"]')).toHaveAttribute(
      "aria-label",
      "Animated logo"
    )
  })

  it("loads the vendored runtime and vector data, then mounts the animation", async () => {
    const destroy = vi.fn()
    const loadAnimation = vi.fn<(options: unknown) => { destroy: () => void }>(
      () => ({ destroy })
    )
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      json: async () => ({ v: "5.9.6", layers: [] }),
    })
    vi.stubGlobal("fetch", fetchMock)

    render(<AboutLogoAnimation className="size-20" label="Animated logo" />)

    const script = await waitFor(() => {
      const found = document.head.querySelector<HTMLScriptElement>(
        `script[src="${RUNTIME_URL}"]`
      )
      expect(found).not.toBeNull()
      return found as HTMLScriptElement
    })
    const windowWithLottie = window as LottieWindow
    windowWithLottie.lottie = { loadAnimation }
    script.onload?.(new Event("load"))

    await waitFor(() => expect(loadAnimation).toHaveBeenCalledTimes(1))
    expect(fetchMock).toHaveBeenCalledWith(
      "/logo/animation/jellyfish-breathe.json"
    )
    expect(loadAnimation.mock.calls[0][0]).toMatchObject({
      renderer: "svg",
      loop: true,
      autoplay: true,
      animationData: { v: "5.9.6", layers: [] },
    })

    cleanup()
    expect(destroy).toHaveBeenCalled()
  })
})
