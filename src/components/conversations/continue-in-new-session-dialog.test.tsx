import { render, screen, fireEvent } from "@testing-library/react"
import { beforeEach, describe, expect, it, vi } from "vitest"

import type { DbConversationSummary } from "@/lib/types"
import type { AgentOptionsSnapshot } from "@/lib/types"

import {
  ContinueInNewSessionDialog,
  type ContinueSessionChoice,
} from "./continue-in-new-session-dialog"

vi.mock("next-intl", () => ({
  useTranslations: () => (key: string, params?: Record<string, unknown>) =>
    params ? `${key}:${JSON.stringify(params)}` : key,
}))

vi.mock("@/components/agent-icon", () => ({
  AgentIcon: ({ agentType }: { agentType: string }) => (
    <span data-testid={`agent-icon-${agentType}`} />
  ),
}))

const agents = vi.hoisted(() => ({
  list: [
    {
      agent_type: "claude_code",
      name: "Claude Code",
      enabled: true,
      available: true,
      sort_order: 0,
    },
    {
      agent_type: "codex",
      name: "Codex",
      enabled: true,
      available: true,
      sort_order: 1,
    },
    {
      agent_type: "disabled_one",
      name: "Off",
      enabled: false,
      available: true,
      sort_order: 2,
    },
  ],
}))

vi.mock("@/hooks/use-acp-agents", () => ({
  useAcpAgents: () => ({ agents: agents.list, fresh: true, refresh: vi.fn() }),
}))

const agentOptions = vi.hoisted(() => ({
  useAgentOptions: vi.fn(),
}))

vi.mock("@/components/automations/use-agent-options", () => ({
  useAgentOptions: (...args: unknown[]) =>
    agentOptions.useAgentOptions(...args),
}))

function snapshotWithModel(): AgentOptionsSnapshot {
  return {
    modes: null,
    config_options: [
      {
        id: "model",
        name: "Model",
        description: null,
        category: "model",
        recommended_value: "model-a",
        kind: {
          type: "select",
          current_value: "model-a",
          groups: [],
          options: [
            { value: "model-a", name: "Model A", description: null },
            { value: "model-b", name: "Model B", description: null },
          ],
        },
      },
    ],
    available_commands: [],
  } as unknown as AgentOptionsSnapshot
}

function agentOptionsReady(snapshot: AgentOptionsSnapshot | null) {
  return {
    snapshot,
    snapshotAgentType: "claude_code",
    snapshotModel: null,
    current: true,
    loading: false,
    error: null,
    reload: vi.fn(),
    ensure: vi.fn(async () => snapshot),
  }
}

const conversation = {
  id: 42,
  folder_id: 1,
  title: "Fix auth",
  agent_type: "claude_code",
} as DbConversationSummary

function openDialog(overrides?: {
  running?: boolean
  folderPath?: string | null
  onConfirm?: (choice: ContinueSessionChoice) => void
}) {
  const onConfirm =
    overrides?.onConfirm ?? vi.fn<(choice: ContinueSessionChoice) => void>()
  render(
    <ContinueInNewSessionDialog
      conversation={conversation}
      folderPath={overrides?.folderPath ?? "/p/1"}
      running={overrides?.running ?? false}
      onOpenChange={vi.fn()}
      onConfirm={onConfirm}
    />
  )
  return { onConfirm }
}

describe("ContinueInNewSessionDialog", () => {
  beforeEach(() => {
    agentOptions.useAgentOptions.mockReset()
    agentOptions.useAgentOptions.mockReturnValue(agentOptionsReady(null))
  })

  it("defaults to the old session's agent and the agent's default model", () => {
    const { onConfirm } = openDialog()

    fireEvent.click(
      screen.getByRole("button", { name: /continueDialogConfirm/ })
    )
    expect(onConfirm).toHaveBeenCalledWith({
      agentType: "claude_code",
      preferredConfigValues: null,
      promptText: "continuePromptText",
    })
  })

  it("offers the probed models and pins the picked one", () => {
    agentOptions.useAgentOptions.mockReturnValue(
      agentOptionsReady(snapshotWithModel())
    )
    const { onConfirm } = openDialog()

    // The probe ran for the folder the conversation lives in.
    expect(agentOptions.useAgentOptions).toHaveBeenCalledWith(
      "claude_code",
      "/p/1",
      true
    )

    fireEvent.click(screen.getByRole("button", { name: /Model B/ }))
    fireEvent.click(
      screen.getByRole("button", { name: /continueDialogConfirm/ })
    )
    expect(onConfirm).toHaveBeenCalledWith({
      agentType: "claude_code",
      preferredConfigValues: { model: "model-b" },
      promptText: "continuePromptText",
    })
  })

  it("switches agents and re-probes the new one's options", () => {
    agentOptions.useAgentOptions.mockReturnValue(
      agentOptionsReady(snapshotWithModel())
    )
    openDialog()

    fireEvent.click(screen.getByRole("button", { name: /agent-codex|Codex/ }))
    expect(agentOptions.useAgentOptions).toHaveBeenCalledWith(
      "codex",
      "/p/1",
      true
    )
  })

  it("stays confirmable — on the default model — when the probe fails", () => {
    agentOptions.useAgentOptions.mockReturnValue({
      snapshot: null,
      snapshotAgentType: null,
      snapshotModel: null,
      current: false,
      loading: false,
      error: "boom",
      reload: vi.fn(),
      ensure: vi.fn(async () => null),
    })
    const { onConfirm } = openDialog()

    expect(screen.queryByRole("button", { name: /Model A/ })).toBeNull()
    fireEvent.click(
      screen.getByRole("button", { name: /continueDialogConfirm/ })
    )
    expect(onConfirm).toHaveBeenCalledWith({
      agentType: "claude_code",
      preferredConfigValues: null,
      promptText: "continuePromptText",
    })
  })

  it("warns and relabels the confirm button while the old session runs", () => {
    openDialog({ running: true })

    expect(screen.getByText("continueRunningDescription")).toBeTruthy()
    expect(
      screen.getByRole("button", { name: /continueStopAndContinue/ })
    ).toBeTruthy()
  })

  it("carries the edited prompt text in the payload", () => {
    const { onConfirm } = openDialog()

    fireEvent.change(screen.getByRole("textbox"), {
      target: { value: "接着做迁移部分" },
    })
    fireEvent.click(
      screen.getByRole("button", { name: /continueDialogConfirm/ })
    )
    expect(onConfirm).toHaveBeenCalledWith({
      agentType: "claude_code",
      preferredConfigValues: null,
      promptText: "接着做迁移部分",
    })
  })
})
