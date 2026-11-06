import type { Transport } from "@/lib/transport/types"

export interface MessageQueueCapabilities {
  backend_drain: boolean
}

/** Missing-command is a legacy host; offline/auth errors are not permission
 *  for the client to become a second drainer. Callers retry them on ready. */
export async function readMessageQueueCapabilities(
  transport: Transport
): Promise<MessageQueueCapabilities> {
  try {
    const result = await transport.call<MessageQueueCapabilities>(
      "message_queue_capabilities",
      {},
      { timeoutMs: 5000 }
    )
    if (typeof result?.backend_drain !== "boolean") {
      throw new Error("Invalid message queue capabilities")
    }
    return result
  } catch (error) {
    const code =
      error && typeof error === "object" && "code" in error ? error.code : null
    const message = error instanceof Error ? error.message : String(error)
    if (
      code === "not_implemented" ||
      /command .*message_queue_capabilities.*not found/i.test(message)
    ) {
      return { backend_drain: false }
    }
    throw error
  }
}
