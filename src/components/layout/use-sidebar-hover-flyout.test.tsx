import { act, fireEvent, render, screen } from "@testing-library/react"
import { useEffect } from "react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import {
  SIDEBAR_FLYOUT_CLOSE_DELAY_MS,
  SIDEBAR_FLYOUT_OPEN_DELAY_MS,
  useSidebarHoverFlyout,
} from "./use-sidebar-hover-flyout"

import type { SidebarHoverFlyout } from "./use-sidebar-hover-flyout"

// Latest controller the harness rendered with, so tests drive it directly.
const latestRef = { current: null as SidebarHoverFlyout | null }

function Harness({ isOpen }: { isOpen: boolean }) {
  const flyout = useSidebarHoverFlyout(isOpen)
  // Publish for assertions outside React. The effect phase is the lint-clean
  // channel for this — render-phase writes to outer scope are forbidden.
  useEffect(() => {
    latestRef.current = flyout
  })
  return (
    <div>
      {/* The keep-zone members the real layout marks: the fixed chrome strip
          and the flyout container, plus a stand-in Radix portal layer. */}
      <div data-sidebar-flyout-trigger="" data-testid="trigger" />
      <div data-sidebar-hover-flyout="" data-testid="flyout" />
      <div role="menu" data-testid="portal-layer" />
      <div data-testid="outside" />
    </div>
  )
}

function renderHarness(isOpen = false) {
  return render(<Harness isOpen={isOpen} />)
}

function openPreview() {
  act(() => latestRef.current?.onTriggerEnter())
  act(() => vi.advanceTimersByTime(SIDEBAR_FLYOUT_OPEN_DELAY_MS))
  expect(latestRef.current?.open).toBe(true)
}

beforeEach(() => {
  vi.useFakeTimers()
  latestRef.current = null
  document.body.style.pointerEvents = ""
})

afterEach(() => {
  vi.useRealTimers()
  document.body.style.pointerEvents = ""
})

describe("useSidebarHoverFlyout", () => {
  it("opens only after the hover-intent delay", () => {
    renderHarness()
    act(() => latestRef.current?.onTriggerEnter())

    act(() => vi.advanceTimersByTime(SIDEBAR_FLYOUT_OPEN_DELAY_MS - 1))
    expect(latestRef.current?.open).toBe(false)

    act(() => vi.advanceTimersByTime(1))
    expect(latestRef.current?.open).toBe(true)
  })

  it("never opens when the strip is left before the delay", () => {
    renderHarness()
    act(() => latestRef.current?.onTriggerEnter())
    act(() => latestRef.current?.onTriggerLeave())
    act(() => vi.advanceTimersByTime(SIDEBAR_FLYOUT_OPEN_DELAY_MS * 2))

    expect(latestRef.current?.open).toBe(false)
  })

  it("closes after the grace delay once the pointer moves outside the keep zone", () => {
    renderHarness()
    openPreview()

    act(() => fireEvent.mouseOver(screen.getByTestId("outside")))
    act(() => vi.advanceTimersByTime(SIDEBAR_FLYOUT_CLOSE_DELAY_MS - 1))
    expect(latestRef.current?.open).toBe(true)

    act(() => vi.advanceTimersByTime(1))
    expect(latestRef.current?.open).toBe(false)
  })

  it("stays open while the pointer is over the flyout, the strip or a portal layer", () => {
    renderHarness()
    openPreview()

    for (const zone of ["flyout", "trigger", "portal-layer"] as const) {
      act(() => fireEvent.mouseOver(screen.getByTestId(zone)))
      act(() => vi.advanceTimersByTime(SIDEBAR_FLYOUT_CLOSE_DELAY_MS * 2))
      expect(latestRef.current?.open).toBe(true)
    }
  })

  it("closes when the pointer moves from a portal layer out to the content", () => {
    renderHarness()
    openPreview()

    act(() => fireEvent.mouseOver(screen.getByTestId("portal-layer")))
    act(() => vi.advanceTimersByTime(SIDEBAR_FLYOUT_CLOSE_DELAY_MS))
    expect(latestRef.current?.open).toBe(true)

    act(() => fireEvent.mouseOver(screen.getByTestId("outside")))
    act(() => vi.advanceTimersByTime(SIDEBAR_FLYOUT_CLOSE_DELAY_MS))
    expect(latestRef.current?.open).toBe(false)
  })

  it("defers close decisions while a modal Radix layer holds body pointer-events", () => {
    renderHarness()
    openPreview()

    // A modal menu open: Radix sets body pointer-events none and Chrome
    // retargets hover to body with the pointer stationary.
    document.body.style.pointerEvents = "none"
    act(() => fireEvent.mouseOver(document.body))
    act(() => vi.advanceTimersByTime(SIDEBAR_FLYOUT_CLOSE_DELAY_MS * 3))
    expect(latestRef.current?.open).toBe(true)

    // The layer closed (click/Escape): normal judging resumes and the pointer
    // outside the keep zone closes the preview.
    document.body.style.pointerEvents = ""
    act(() => fireEvent.mouseOver(screen.getByTestId("outside")))
    act(() => vi.advanceTimersByTime(SIDEBAR_FLYOUT_CLOSE_DELAY_MS))
    expect(latestRef.current?.open).toBe(false)
  })

  it("closes immediately on Escape", () => {
    renderHarness()
    openPreview()

    act(() => {
      window.dispatchEvent(
        new KeyboardEvent("keydown", { key: "Escape", bubbles: true })
      )
    })
    expect(latestRef.current?.open).toBe(false)
  })

  it("ignores an Escape a Radix layer already consumed", () => {
    renderHarness()
    openPreview()

    const consumed = new KeyboardEvent("keydown", {
      key: "Escape",
      bubbles: true,
      cancelable: true,
    })
    consumed.preventDefault()
    act(() => {
      window.dispatchEvent(consumed)
    })
    expect(latestRef.current?.open).toBe(true)

    act(() => {
      window.dispatchEvent(
        new KeyboardEvent("keydown", { key: "Escape", bubbles: true })
      )
    })
    expect(latestRef.current?.open).toBe(false)
  })

  it("a formal expand closes the preview and hover-while-open never arms it", () => {
    const { rerender } = renderHarness(false)
    openPreview()

    rerender(<Harness isOpen={true} />)
    expect(latestRef.current?.open).toBe(false)

    act(() => latestRef.current?.onTriggerEnter())
    act(() => vi.advanceTimersByTime(SIDEBAR_FLYOUT_OPEN_DELAY_MS * 2))
    expect(latestRef.current?.open).toBe(false)

    // After collapsing again, hover works once more.
    rerender(<Harness isOpen={false} />)
    act(() => latestRef.current?.onTriggerEnter())
    act(() => vi.advanceTimersByTime(SIDEBAR_FLYOUT_OPEN_DELAY_MS))
    expect(latestRef.current?.open).toBe(true)
  })

  it("clears pending timers on unmount", () => {
    const { unmount } = renderHarness()
    act(() => latestRef.current?.onTriggerEnter())
    unmount()

    expect(() =>
      vi.advanceTimersByTime(SIDEBAR_FLYOUT_OPEN_DELAY_MS * 3)
    ).not.toThrow()
  })
})
