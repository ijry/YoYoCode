import type { ComponentProps } from "react"
import { cleanup, render, screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { NextIntlClientProvider } from "next-intl"
import { afterEach, expect, it, vi } from "vitest"
import enMessages from "@/i18n/messages/en.json"
import type { MessageQueueState } from "@/lib/types"

// Keep the actual shell, ChatInput and queue display. The editor and unrelated
// status strips do not participate in queue pause/resume.
vi.mock("@/components/chat/message-input", () => ({ MessageInput: () => null }))
vi.mock("@/components/chat/composer-status-strips", () => ({
  ComposerStatusStrips: () => null,
}))

import { ConversationShell } from "./conversation-shell"

afterEach(cleanup)

it("exposes pause and single-row send through the conversation shell, even while a turn is running", async () => {
  const onQueuePause = vi.fn(async () => {})
  const onQueueReleaseHold = vi.fn(async () => {})
  const onQueueSendNow = vi.fn(async () => {})
  const control: MessageQueueState = {
    owner_id: "42",
    status: "open",
    active_attempt_id: null,
    failed_attempt_id: null,
    error: null,
    hold: null,
    revision: 1,
  }
  const props = {
    status: "prompting",
    promptCapabilities: { image: false, audio: false, embedded_context: false },
    claudeApiRetry: null,
    pendingPermission: null,
    pendingQuestion: null,
    pendingAskQuestion: null,
    pendingPlanApproval: null,
    onFocus: vi.fn(),
    onSend: vi.fn(),
    onCancel: vi.fn(),
    onRespondPermission: vi.fn(),
    onAnswerQuestion: vi.fn(),
    onAnswerAskQuestion: vi.fn(),
    onAnswerPlanApproval: vi.fn(),
    queue: [
      {
        id: "q1",
        draft: {
          blocks: [{ type: "text", text: "later" }],
          displayText: "later",
        },
        modeId: null,
      },
    ],
    manualQueuePause: true,
    onQueuePause,
    onQueueReleaseHold,
    onQueueSendNow,
    onQueueReorder: vi.fn(),
    onQueueEdit: vi.fn(),
    onQueueDelete: vi.fn(),
  } satisfies Omit<ComponentProps<typeof ConversationShell>, "children">
  const display = (state: MessageQueueState, empty = false) => (
    <NextIntlClientProvider locale="en" messages={enMessages}>
      <ConversationShell
        {...props}
        queue={empty ? [] : props.queue}
        queueControl={state}
      >
        <div>Transcript</div>
      </ConversationShell>
    </NextIntlClientProvider>
  )
  const view = render(display(control))
  const t = enMessages.Folder.chat.messageQueue
  await userEvent.click(screen.getByRole("button", { name: t.pauseQueue }))
  expect(onQueuePause).toHaveBeenCalledTimes(1)

  const held = { ...control, hold: "manual_pause", revision: 2 }
  view.rerender(display(held))
  await userEvent.click(screen.getByTitle(t.sendNow))
  expect(onQueueSendNow).toHaveBeenCalledWith("q1")
  expect(onQueueReleaseHold).not.toHaveBeenCalled()
  view.rerender(display(held, true))
  await userEvent.click(screen.getByRole("button", { name: t.resumeQueue }))
  expect(onQueueReleaseHold).toHaveBeenCalledTimes(1)
})
