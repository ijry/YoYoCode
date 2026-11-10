import { describe, expect, it } from "vitest"
import { buildMcodeConfigCode } from "./mcode-config-code"

function decode(code: string | null) {
  expect(code).not.toBeNull()
  expect(code).toMatch(/^[A-Za-z0-9_-]+$/)
  return JSON.parse(Buffer.from(code!, "base64url").toString("utf8"))
}

describe("MCode connection configuration", () => {
  it.each([
    "http://192.168.1.8:3080",
    "https://workstation.example.ts.net",
    "http://[2001:db8::8]:3080",
  ])(
    "encodes the selected endpoint %s using the v2 direct protocol",
    (address) => {
      expect(decode(buildMcodeConfigCode(address, "example-token"))).toEqual({
        version: 2,
        name: "Codeg",
        targetAgent: "codeg",
        routeMode: "direct",
        directBaseUrl: address,
        directToken: "example-token",
      })
    }
  )

  it("uses UTF-8 and normalizes whitespace and trailing slashes like MCode", () => {
    expect(
      decode(buildMcodeConfigCode(" https://example.test/codeg/ ", " 令牌-🔑 "))
    ).toMatchObject({
      directBaseUrl: "https://example.test/codeg",
      directToken: "令牌-🔑",
    })
  })

  it.each([
    [null, "token"],
    ["", "token"],
    ["http://localhost:3080", "  "],
    ["javascript:alert(1)", "token"],
    ["not a URL", "token"],
    ["https://user:password@example.test", "token"],
    ["https://example.test/?token=other", "token"],
    ["https://example.test/#token", "token"],
  ])("does not export invalid credentials or endpoints", (address, token) => {
    expect(buildMcodeConfigCode(address, token)).toBeNull()
  })

  it("declines payloads too large for a readable configuration QR", () => {
    expect(
      buildMcodeConfigCode("https://example.test", "x".repeat(3000))
    ).toBeNull()
  })
})
