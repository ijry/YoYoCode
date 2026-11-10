// MCode's connectionConfigCode v2: UTF-8 JSON encoded as unpadded base64url.
// This contains a bearer credential; do not log it or put it in a browser URL.
export function buildMcodeConfigCode(
  address: string | null,
  token: string
): string | null {
  const baseUrl = address?.trim().replace(/\/+$/, "")
  const accessToken = token.trim()
  if (!baseUrl || !accessToken) return null

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
  } catch {
    return null
  }

  const bytes = new TextEncoder().encode(
    JSON.stringify({
      version: 2,
      name: "Codeg",
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
