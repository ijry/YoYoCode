import { act, fireEvent, render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { NextIntlClientProvider } from "next-intl"
import { createRef, useEffect } from "react"
import { beforeEach, describe, expect, it, vi } from "vitest"

import enMessages from "@/i18n/messages/en.json"
import type { AgentType } from "@/lib/types"
import { ComposerAddMenu } from "./composer-add-menu"
import { RichComposer, type RichComposerHandle } from "./rich-composer"
import {
  useComposerShortcuts,
  type ComposerShortcuts,
} from "./use-composer-shortcuts"

const state = vi.hoisted(() => ({
  enabledIds: new Set<string>(),
  folderId: 7 as number | null,
  openWorkbench: vi.fn(),
  openSettings: vi.fn().mockResolvedValue(undefined),
  warning: vi.fn(),
  info: vi.fn(),
  call: vi.fn(),
}))
vi.mock("@/lib/api", () => ({
  openSettingsWindow: state.openSettings,
  quickMessagesList: async () => [],
}))
vi.mock("@/lib/transport", () => ({
  getTransport: () => ({ call: state.call }),
}))
vi.mock("@/contexts/active-folder-context", () => ({
  useActiveFolder: () => ({
    activeFolderId: state.folderId,
    activeFolder: state.folderId == null ? null : { kind: "project" },
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
vi.mock("@/hooks/use-built-in-experts", () => ({ useBuiltInExperts: () => [] }))
vi.mock("@/hooks/use-built-in-science", () => ({ useBuiltInScience: () => [] }))

async function mount(agentType: AgentType = "codex", defaultText = "") {
  const editorRef = createRef<RichComposerHandle>()
  const onSubmit = vi.fn()
  let shortcuts!: ComposerShortcuts
  function Harness() {
    const currentShortcuts = useComposerShortcuts({ editorRef, agentType })
    useEffect(() => {
      shortcuts = currentShortcuts
    }, [currentShortcuts])
    return (
      <>
        <RichComposer
          ref={editorRef}
          defaultText={defaultText}
          onSubmit={onSubmit}
        />
        <ComposerAddMenu shortcuts={currentShortcuts} slashCommands={[]} />
      </>
    )
  }
  render(
    <NextIntlClientProvider locale="en" messages={enMessages}>
      <Harness />
    </NextIntlClientProvider>
  )
  await waitFor(() => expect(editorRef.current?.getEditor()).toBeTruthy())
  const comic = shortcuts.officeActions.find(
    (action) => action.id === "comicProduction"
  )
  expect(comic).toBeDefined()
  return { editorRef, onSubmit, shortcuts, comic: comic! }
}

beforeEach(() => {
  vi.clearAllMocks()
  state.enabledIds = new Set(["comic-production"])
  state.folderId = 7
})

describe("composer comic shortcut", () => {
  it("offers the comic entry inside + Office and prepares a draft without sending or API calls", async () => {
    const { editorRef, onSubmit } = await mount()
    const user = userEvent.setup()
    await user.click(
      screen.getByRole("button", {
        name: enMessages.Folder.chat.messageInput.addActions,
      })
    )
    await user.hover(
      screen.getByRole("menuitem", {
        name: enMessages.Folder.chat.messageInput.office,
      })
    )
    // jsdom has no submenu geometry; a synthetic pointer move can close the
    // hovered submenu before its click. Dispatch the item click directly.
    fireEvent.click(
      await screen.findByRole("menuitem", { name: "Comic production" })
    )
    await waitFor(() =>
      expect(editorRef.current?.getText()).toContain("$comic-production")
    )
    expect(editorRef.current?.getText()).toContain("Help me create or continue")
    expect(state.openWorkbench).toHaveBeenCalledExactlyOnceWith(7)
    expect(onSubmit).not.toHaveBeenCalled()
    expect(state.call).not.toHaveBeenCalled()
  })

  it("preserves an existing draft while prepending the comic skill", async () => {
    const { editorRef, shortcuts, comic, onSubmit } = await mount(
      "codex",
      "Keep my handwritten ending"
    )
    act(() => shortcuts.insertOffice(comic))
    await waitFor(() =>
      expect(editorRef.current?.getText()).toContain("$comic-production")
    )
    expect(editorRef.current?.getText()).toContain("Keep my handwritten ending")
    expect(editorRef.current?.getText()).not.toContain(
      "Help me create or continue"
    )
    expect(onSubmit).not.toHaveBeenCalled()
  })

  it("uses expert settings when the bundled comic skill is not linked", async () => {
    state.enabledIds.clear()
    const { shortcuts, comic, editorRef } = await mount()
    act(() => shortcuts.insertOffice(comic))
    expect(editorRef.current?.getText()).toBe("")
    state.warning.mock.calls[0][1].action.onClick()
    expect(state.openSettings).toHaveBeenCalledExactlyOnceWith("experts")
    expect(state.openWorkbench).not.toHaveBeenCalled()
  })

  it("allows manual workbench access for Cline and does not inject unavailable tools", async () => {
    const { shortcuts, comic, editorRef, onSubmit } = await mount("cline")
    act(() => shortcuts.insertOffice(comic))
    expect(editorRef.current?.getText()).toBe("")
    expect(state.openWorkbench).toHaveBeenCalledExactlyOnceWith(7)
    expect(state.warning).toHaveBeenCalledWith(
      expect.stringContaining("cannot use the comic MCP tools")
    )
    expect(onSubmit).not.toHaveBeenCalled()
    expect(state.call).not.toHaveBeenCalled()
  })

  it("asks for a folder without creating a project when none is active", async () => {
    state.folderId = null
    const { shortcuts, comic } = await mount()
    act(() => shortcuts.insertOffice(comic))
    expect(state.openWorkbench).not.toHaveBeenCalled()
    expect(state.warning).toHaveBeenCalledWith(
      enMessages.Folder.chat.welcomePanel.quickActions.comicEntry.folderRequired
    )
    expect(state.call).not.toHaveBeenCalled()
  })
})
