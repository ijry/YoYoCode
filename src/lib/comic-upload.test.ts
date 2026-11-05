import { describe, expect, it } from "vitest"
import { readComicUpload } from "./comic-upload"

describe("comic novel uploads", () => {
  it("keeps original GBK bytes so the backend can decode them", async () => {
    const file = new File(
      [new Uint8Array([0xb5, 0xda, 0xd2, 0xbb])],
      "novel.txt"
    )
    expect(await readComicUpload(file)).toBe("tdrSuw==")
  })

  it("handles a full-size novel without spreading bytes as function arguments", async () => {
    const bytes = new Uint8Array(250_000).fill(65)
    const encoded = await readComicUpload(new File([bytes], "novel.txt"))
    expect(atob(encoded).length).toBe(250_000)
    expect(atob(encoded).slice(-4)).toBe("AAAA")
  })
})
