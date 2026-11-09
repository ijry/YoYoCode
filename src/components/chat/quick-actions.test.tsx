import { fireEvent, render, screen } from "@testing-library/react"
import { NextIntlClientProvider } from "next-intl"
import { beforeEach, describe, expect, it, vi } from "vitest"

import enMessages from "@/i18n/messages/en.json"
import { saveQuickActionsTab } from "@/lib/quick-actions-tab-storage"
import type { AgentType, ExpertListItem } from "@/lib/types"
import { QuickActions } from "./quick-actions"

const state = vi.hoisted(() => ({
  enabledIds: new Set<string>(),
  folderId: 7 as number | null,
  folderKind: "project" as "project" | "chat",
  openWorkbench: vi.fn(),
  openSettings: vi.fn().mockResolvedValue(undefined),
  warning: vi.fn(),
  info: vi.fn(),
}))
vi.mock("@/lib/api", () => ({
  openSettingsWindow: state.openSettings,
}))
vi.mock("@/contexts/active-folder-context", () => ({
  useActiveFolder: () => ({
    activeFolderId: state.folderId,
    activeFolder: { kind: state.folderKind },
  }),
}))
vi.mock("@/contexts/workspace-context", () => ({
  useOptionalWorkspaceActions: () => ({
    openComicWorkbench: state.openWorkbench,
  }),
}))
vi.mock("sonner", () => ({
  toast: { warning: state.warning, info: state.info },
}))
vi.mock("@/hooks/use-enabled-skill-ids", () => ({
  useEnabledSkillIds: () => ({
    enabledIds: state.enabledIds,
    ready: true,
    supported: true,
  }),
}))
vi.mock("@/hooks/use-appearance", () => ({
  useWelcomeQuickActions: () => ({ showWelcomeQuickActions: true }),
}))
vi.mock("@/hooks/use-built-in-experts", () => ({
  useBuiltInExperts: (): ExpertListItem[] => [
    {
      metadata: {
        id: "comic-production",
        category: "planning",
        icon: "PlayCircle",
        sort_order: 25,
        display_name: { en: "Comic production" },
        description: { en: "Create comics" },
        bundled_hash: "hash",
      },
      installed_centrally: true,
      user_modified: false,
      central_path: "/skills/comic-production",
    },
  ],
}))

function mount(agentType: AgentType = "codex") {
  const onSelect = vi.fn()
  const view = render(
    <NextIntlClientProvider locale="en" messages={enMessages}>
      <QuickActions agentType={agentType} onSelect={onSelect} />
    </NextIntlClientProvider>
  )
  return { ...view, onSelect }
}

beforeEach(() => {
  vi.clearAllMocks()
  state.enabledIds = new Set(["comic-production"])
  state.folderId = 7
  state.folderKind = "project"
  saveQuickActionsTab("office")
})

describe("comic production Office shortcut", () => {
  it("leads the Office rail and injects only a draft with the bundled skill", () => {
    const { container, onSelect } = mount()
    const first = container.querySelector(".qa-rail-viewport button")
    expect(first).toHaveTextContent("Comic production")
    fireEvent.click(first!)
    expect(onSelect).toHaveBeenCalledExactlyOnceWith({
      text: enMessages.Folder.chat.welcomePanel.quickActions.prompts
        .comicProduction,
      skill: { id: "comic-production", label: "Comic production" },
    })
    expect(state.openWorkbench).toHaveBeenCalledExactlyOnceWith(7)
    expect(state.info).toHaveBeenCalledWith(
      enMessages.Folder.chat.welcomePanel.quickActions.comicEntry.checkTools
    )
  })

  it("routes a locked comic skill to Experts without opening or injecting", () => {
    state.enabledIds.clear()
    const { onSelect } = mount()
    fireEvent.click(screen.getByRole("button", { name: "Comic production" }))
    expect(onSelect).not.toHaveBeenCalled()
    expect(state.openWorkbench).not.toHaveBeenCalled()
    state.warning.mock.calls[0][1].action.onClick()
    expect(state.openSettings).toHaveBeenCalledExactlyOnceWith("experts")
  })

  it("keeps existing Office skills routed to Office Tools", () => {
    mount()
    fireEvent.click(
      screen.getByRole("button", {
        name: new RegExp(
          enMessages.Folder.chat.welcomePanel.quickActions.excel
        ),
      })
    )
    state.warning.mock.calls[0][1].action.onClick()
    expect(state.openSettings).toHaveBeenCalledExactlyOnceWith("office-tools")
  })

  it.each<AgentType>(["pi", "cline", "open_claw"])(
    "opens manual workbench and explains unsupported %s without injecting",
    (agentType) => {
      state.enabledIds.clear()
      const { onSelect } = mount(agentType)
      fireEvent.click(screen.getByRole("button", { name: "Comic production" }))
      expect(state.openWorkbench).toHaveBeenCalledExactlyOnceWith(7)
      expect(onSelect).not.toHaveBeenCalled()
      expect(state.warning).toHaveBeenCalledWith(
        expect.stringContaining("cannot use the comic MCP tools")
      )
      expect(state.openSettings).not.toHaveBeenCalled()
    }
  )

  it("explains that a chat folder cannot host the workbench", () => {
    state.folderKind = "chat"
    mount()
    fireEvent.click(screen.getByRole("button", { name: "Comic production" }))
    expect(state.openWorkbench).not.toHaveBeenCalled()
    expect(state.warning).toHaveBeenCalledWith(
      enMessages.Folder.chat.welcomePanel.quickActions.comicEntry.folderRequired
    )
  })

  it("does not duplicate the featured Office expert in the Coding rail", () => {
    saveQuickActionsTab("coding")
    mount()
    expect(
      screen.queryByRole("button", { name: "Comic production" })
    ).not.toBeInTheDocument()
  })
})
