// MCode's connectionConfigCode v2: UTF-8 JSON encoded as unpadded base64url.
// This contains a bearer credential; do not log it or put it in a browser URL.

const FALLBACK_CONNECTION_NAME = "Codeg"
const MAX_CONNECTION_NAME_LENGTH = 64

// A name is only a label in MCode's connection list. Reject values that would
// break the payload: empty, absurdly long, or carrying control characters.
function usableConnectionName(value: string | null | undefined): string | null {
  const name = value?.trim() ?? ""
  const hasControlChar = Array.from(name).some((char) => {
    const code = char.codePointAt(0) ?? 0
    return code < 0x20 || code === 0x7f
  })
  if (!name || name.length > MAX_CONNECTION_NAME_LENGTH || hasControlChar) {
    return null
  }
  return name
}

export function buildMcodeConfigCode(
  address: string | null,
  token: string,
  hostName?: string | null
): string | null {
  const baseUrl = address?.trim().replace(/\/+$/, "")
  const accessToken = token.trim()
  if (!baseUrl || !accessToken) return null

  let endpointHostname = ""
  try {
    const url = new URL(baseUrl)
    if (
      !["http:", "https:"].includes(url.protocol) ||
      url.username ||
      url.password ||
      url.search ||
      url.hash
    )
      return null
    // IPv6 literals come back bracketed; the brackets are address syntax,
    // not part of a display name.
    endpointHostname = url.hostname.replace(/^\[|\]$/g, "")
  } catch {
    return null
  }

  // Prefer the OS hostname reported by the backend, so several machines stay
  // distinguishable in MCode. Fall back to the endpoint's hostname, then to
  // the product name.
  const name =
    usableConnectionName(hostName) ??
    usableConnectionName(endpointHostname) ??
    FALLBACK_CONNECTION_NAME

  const bytes = new TextEncoder().encode(
    JSON.stringify({
      version: 2,
      name,
      targetAgent: "codeg",
      routeMode: "direct",
      directBaseUrl: baseUrl,
      directToken: accessToken,
    })
  )
  let binary = ""
  for (const byte of bytes) binary += String.fromCharCode(byte)
  const code = btoa(binary)
    .replace(/\+/g, "-")
    .replace(/\//g, "_")
    .replace(/=+$/g, "")
  // Keep oversized credentials from overflowing the QR encoder/render tree.
  return code.length <= 2048 ? code : null
}
