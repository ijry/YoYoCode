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
      expect(
        decode(buildMcodeConfigCode(address, "example-token", "Workstation-01"))
      ).toEqual({
        version: 2,
        name: "Workstation-01",
        targetAgent: "codeg",
        routeMode: "direct",
        directBaseUrl: address,
        directToken: "example-token",
      })
    }
  )

  it("prefers and trims the machine name over the endpoint hostname", () => {
    expect(
      decode(
        buildMcodeConfigCode(
          "https://node.example.ts.net",
          "token",
          "  My-PC  "
        )
      ).name
    ).toBe("My-PC")
  })

  it.each([
    ["http://192.168.1.8:3080", "192.168.1.8"],
    ["https://workstation.example.ts.net", "workstation.example.ts.net"],
    ["http://[2001:db8::8]:3080", "2001:db8::8"],
  ])(
    "falls back to the endpoint hostname %s without a machine name",
    (address, expectedName) => {
      expect(decode(buildMcodeConfigCode(address, "token")).name).toBe(
        expectedName
      )
    }
  )

  it.each([null, "   ", "bad\nname", "x".repeat(300)])(
    "ignores an unusable machine name %s",
    (hostName) => {
      expect(
        decode(buildMcodeConfigCode("https://fallback.test", "token", hostName))
          .name
      ).toBe("fallback.test")
    }
  )

  it("uses UTF-8 and normalizes whitespace and trailing slashes like MCode", () => {
    expect(
      decode(
        buildMcodeConfigCode(
          " https://example.test/codeg/ ",
          " 令牌-🔑 ",
          " 主机🔑 "
        )
      )
    ).toMatchObject({
      name: "主机🔑",
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
