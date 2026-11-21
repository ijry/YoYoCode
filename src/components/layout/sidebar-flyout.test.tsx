import { fireEvent, render, screen } from "@testing-library/react"
import { beforeEach, describe, expect, it, vi } from "vitest"

import { SidebarFlyout } from "./sidebar-flyout"

// Mutable sidebar state; the flyout container reads isOpen + width from it.
const mockState = vi.hoisted(() => ({ isOpen: false, width: 320 }))
vi.mock("@/contexts/sidebar-context", () => ({
  useSidebarContext: () => mockState,
}))

// The sidebar content itself is covered by sidebar.test.tsx; the stub here
// carries a conversation row so the click-to-dismiss contract is exercised.
vi.mock("./sidebar", () => ({
  Sidebar: () => (
    <div>
      <button type="button" data-conversation-id="c1">
        Conversation row
      </button>
    </div>
  ),
}))

function makeController(open: boolean) {
  return {
    open,
    onTriggerEnter: vi.fn(),
    onTriggerLeave: vi.fn(),
    close: vi.fn(),
  }
}

beforeEach(() => {
  mockState.isOpen = false
  mockState.width = 320
})

describe("SidebarFlyout", () => {
  it("mounts the floating sidebar when collapsed and the preview is open", () => {
    render(<SidebarFlyout flyout={makeController(true)} />)

    const container = screen.getByTestId("sidebar-flyout")
    // The preview floats OVER the content: a fixed overlay, not a layout column.
    expect(container.className).toContain("fixed")
    expect(container.className).toContain("z-40")
    expect(container.style.width).toBe("320px")
    expect(screen.getByText("Conversation row")).toBeTruthy()
  })

  it("renders nothing while the hover preview is closed", () => {
    render(<SidebarFlyout flyout={makeController(false)} />)

    expect(screen.queryByTestId("sidebar-flyout")).toBeNull()
  })

  it("renders nothing while the sidebar is formally open", () => {
    mockState.isOpen = true
    render(<SidebarFlyout flyout={makeController(true)} />)

    expect(screen.queryByTestId("sidebar-flyout")).toBeNull()
  })

  it("dismisses the preview when a conversation row is clicked", () => {
    const controller = makeController(true)
    render(<SidebarFlyout flyout={controller} />)

    fireEvent.click(screen.getByText("Conversation row"))

    expect(controller.close).toHaveBeenCalledTimes(1)
  })

  it("keeps the preview open for clicks outside conversation rows", () => {
    const controller = makeController(true)
    render(<SidebarFlyout flyout={controller} />)

    fireEvent.click(screen.getByTestId("sidebar-flyout"))

    expect(controller.close).not.toHaveBeenCalled()
  })
})
