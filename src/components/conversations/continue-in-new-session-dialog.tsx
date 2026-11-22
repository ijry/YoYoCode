"use client"

import { useEffect, useState } from "react"
import { useTranslations } from "next-intl"
import { Check } from "lucide-react"

import { AgentIcon } from "@/components/agent-icon"
import { useAgentOptions } from "@/components/automations/use-agent-options"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Textarea } from "@/components/ui/textarea"
import {
  MODEL_CONFIG_OPTION_ID,
  isModelConfigOption,
  modelListGroups,
} from "@/lib/model-config-groups"
import { useAcpAgents } from "@/hooks/use-acp-agents"
import type { AcpAgentInfo } from "@/lib/types"
import type { DbConversationSummary } from "@/lib/types"
import { cn } from "@/lib/utils"

/** What a confirmed dialog hands to the continue flow. */
export interface ContinueSessionChoice {
  agentType: string
  /** null = the agent's own default model (the "default" row, or a probe
   *  that never came back). */
  preferredConfigValues: Record<string, string> | null
  promptText: string
}

interface ContinueInNewSessionDialogProps {
  /** The conversation being continued; null = closed. */
  conversation: DbConversationSummary | null
  folderPath: string | null
  /** Open-time snapshot: the old session still holds a stoppable connection. */
  running: boolean
  onOpenChange: (open: boolean) => void
  onConfirm: (choice: ContinueSessionChoice) => void
}

/**
 * The "continue in a new session" chooser. Replaces the old running-only
 * confirm: the entry now ALWAYS opens this dialog, so the user can hand the
 * work to a different agent and model than the one the old session ran on.
 * Defaults keep the old behavior — the same agent, the agent's default model,
 * the standard continuation prompt.
 *
 * The model list comes from the agent-options probe (a transient CLI session,
 * cached 30s by the hook), so an agent that has never connected in this app
 * still lists its models; while the probe runs or when it fails, the "default
 * model" row alone stays — a probe that never lands must not block the
 * handoff.
 */
export function ContinueInNewSessionDialog({
  conversation,
  folderPath,
  running,
  onOpenChange,
  onConfirm,
}: ContinueInNewSessionDialogProps) {
  const tCard = useTranslations("Folder.conversationCard")
  const open = conversation != null
  const { agents } = useAcpAgents()
  const availableAgents = agents.filter(
    (agent) => agent.enabled && agent.available
  )

  const [selectedAgent, setSelectedAgent] = useState<string | null>(null)
  const [modelPick, setModelPick] = useState<string | null>(null)
  const [promptText, setPromptText] = useState("")

  // Reset per continuation: the previous dialog's choices must not leak into
  // the next one's defaults.
  useEffect(() => {
    if (conversation == null) return
    setSelectedAgent(conversation.agent_type)
    setModelPick(null)
    setPromptText(tCard("continuePromptText"))
    // `tCard` is stable per locale; the reset is keyed on the conversation.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [conversation?.id])

  // The requested agent may not be installed/enabled; fall back to the first
  // available one rather than confirming a draft nothing can connect to.
  useEffect(() => {
    if (availableAgents.length === 0) return
    if (
      selectedAgent &&
      availableAgents.some((agent) => agent.agent_type === selectedAgent)
    ) {
      return
    }
    setSelectedAgent(availableAgents[0].agent_type)
  }, [availableAgents, selectedAgent])

  // A one-shot pick belongs to the agent it was probed for: switching agents
  // discards it.
  useEffect(() => {
    setModelPick(null)
  }, [selectedAgent])

  const { snapshot } = useAgentOptions(
    (selectedAgent ?? "claude_code") as AcpAgentInfo["agent_type"],
    folderPath,
    open
  )
  const modelOption = snapshot?.config_options.find(isModelConfigOption)
  const modelGroups = modelOption ? modelListGroups(modelOption) : []

  if (conversation == null) return null

  const confirm = () => {
    if (selectedAgent == null) return
    onConfirm({
      agentType: selectedAgent,
      preferredConfigValues:
        modelPick != null ? { [MODEL_CONFIG_OPTION_ID]: modelPick } : null,
      promptText,
    })
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="flex max-h-[85vh] flex-col sm:max-w-[28rem]">
        <DialogHeader>
          <DialogTitle>{tCard("continueInNewSession")}</DialogTitle>
          <DialogDescription>
            {running ? tCard("continueRunningDescription") : null}
          </DialogDescription>
        </DialogHeader>
        <div className="flex min-h-0 flex-1 flex-col gap-3 overflow-y-auto">
          <section className="flex flex-col gap-1">
            <p className="text-sm font-medium">
              {tCard("continueDialogAgentLabel")}
            </p>
            <div className="flex flex-col gap-1">
              {availableAgents.map((agent) => (
                <Button
                  key={agent.agent_type}
                  variant="ghost"
                  size="sm"
                  className={cn(
                    "justify-start gap-2",
                    agent.agent_type === selectedAgent && "bg-accent"
                  )}
                  onClick={() => setSelectedAgent(agent.agent_type)}
                >
                  <AgentIcon agentType={agent.agent_type} />
                  {agent.name}
                  {agent.agent_type === selectedAgent && (
                    <Check className="ml-auto size-4" />
                  )}
                </Button>
              ))}
            </div>
          </section>
          <section className="flex flex-col gap-1">
            <p className="text-sm font-medium">
              {tCard("continueDialogModelLabel")}
            </p>
            <ScrollArea className="max-h-40">
              <div className="flex flex-col gap-1 pr-2">
                <Button
                  variant="ghost"
                  size="sm"
                  className={cn(
                    "justify-start",
                    modelPick == null && "bg-accent"
                  )}
                  onClick={() => setModelPick(null)}
                >
                  {modelPick == null && <Check className="size-4" />}
                  {tCard("continueDialogDefaultModel")}
                </Button>
                {modelGroups.map((group) => (
                  <div
                    key={group.key ?? "models"}
                    className="flex flex-col gap-1"
                  >
                    {group.name != null && (
                      <p className="px-2 pt-1 text-xs text-muted-foreground">
                        {group.name}
                      </p>
                    )}
                    {group.options.map((option) => (
                      <Button
                        key={option.value}
                        variant="ghost"
                        size="sm"
                        className={cn(
                          "justify-start",
                          modelPick === option.value && "bg-accent"
                        )}
                        onClick={() => setModelPick(option.value)}
                      >
                        {modelPick === option.value && (
                          <Check className="size-4" />
                        )}
                        {option.name}
                      </Button>
                    ))}
                  </div>
                ))}
              </div>
            </ScrollArea>
          </section>
          <section className="flex flex-col gap-1">
            <p className="text-sm font-medium">
              {tCard("continueDialogPromptLabel")}
            </p>
            <Textarea
              value={promptText}
              onChange={(event) => setPromptText(event.target.value)}
              rows={3}
            />
          </section>
        </div>
        <DialogFooter>
          <Button variant="ghost" onClick={() => onOpenChange(false)}>
            {tCard("cancel")}
          </Button>
          <Button onClick={confirm} disabled={selectedAgent == null}>
            {running
              ? tCard("continueStopAndContinue")
              : tCard("continueDialogConfirm")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
