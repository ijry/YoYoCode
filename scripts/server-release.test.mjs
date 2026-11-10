import assert from "node:assert/strict"
import { spawnSync } from "node:child_process"
import {
  cpSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  rmSync,
  writeFileSync,
} from "node:fs"
import { tmpdir } from "node:os"
import { dirname, join, resolve } from "node:path"
import { fileURLToPath } from "node:url"
import test from "node:test"
import { parse } from "yaml"

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..")
const workflow = parse(
  readFileSync(join(root, ".github/workflows/release.yml"), "utf8")
)
const server = workflow.jobs["build-server"]

function scratch(t) {
  const dir = mkdtempSync(join(tmpdir(), "yoyocode-server-test-"))
  t.after(() => rmSync(dir, { recursive: true, force: true }))
  return dir
}

function run(command, args, cwd, env = {}) {
  const result = spawnSync(command, args, {
    cwd,
    encoding: "utf8",
    env: { ...process.env, GITHUB_OUTPUT: "", ...env },
  })
  assert.equal(result.error, undefined)
  assert.equal(result.status, 0, result.stdout + result.stderr)
  return result.stdout
}

function identityFixture(t) {
  const dir = scratch(t)
  mkdirSync(join(dir, "scripts"))
  mkdirSync(join(dir, "src-tauri"))
  cpSync(
    join(root, "scripts/apply-plus-identity.mjs"),
    join(dir, "scripts/apply-plus-identity.mjs")
  )
  for (const file of ["package.json", "src-tauri/tauri.conf.json"]) {
    const value = JSON.parse(readFileSync(join(root, file), "utf8"))
    value.version = "0.42.0"
    writeFileSync(join(dir, file), JSON.stringify(value))
  }
  writeFileSync(
    join(dir, "src-tauri/Cargo.toml"),
    '[package]\nname = "codeg"\nversion = "0.42.0"\n\n[dependencies]\nserde = { version = "1" }\n'
  )
  return dir
}

test("server identity uses the full PLUS version without changing desktop safe mode", (t) => {
  const dir = identityFixture(t)
  const args = ["scripts/apply-plus-identity.mjs", "--iteration", "10"]
  run(process.execPath, args, dir)
  assert.match(
    readFileSync(join(dir, "src-tauri/Cargo.toml"), "utf8"),
    /version = "0\.42\.0"/
  )
  run(process.execPath, [...args, "--server", "--check"], dir)
  assert.match(
    readFileSync(join(dir, "src-tauri/Cargo.toml"), "utf8"),
    /version = "0\.42\.0"/
  )
  run(process.execPath, [...args, "--server"], dir)
  run(process.execPath, [...args, "--server"], dir)
  const cargo = readFileSync(join(dir, "src-tauri/Cargo.toml"), "utf8")
  assert.match(cargo, /version = "0\.42\.0-plus\.10"/)
  assert.match(cargo, /serde = \{ version = "1" \}/)
  assert.equal(
    JSON.parse(readFileSync(join(dir, "src-tauri/tauri.conf.json"))).version,
    "0.42.0"
  )
})

test("server identity rejects a noncanonical PLUS iteration", (t) => {
  const dir = identityFixture(t)
  const result = spawnSync(
    process.execPath,
    ["scripts/apply-plus-identity.mjs", "--server", "--iteration", "01"],
    { cwd: dir, encoding: "utf8" }
  )
  assert.notEqual(result.status, 0)
  assert.match(result.stderr, /iteration/)
})

test("CI packages both asset names with matching roots and unchanged executables", (t) => {
  const dir = scratch(t)
  const matrix = server.strategy.matrix.include.find(
    (entry) => entry.target === "x86_64-unknown-linux-gnu"
  )
  assert.equal(matrix.artifact, "yoyocode-server-linux-x64")
  assert.equal(matrix.legacy_artifact, "codeg-server-linux-x64")
  const bins = [
    "codeg-server",
    "yoyocode-mcp",
    "codeg-computer-helper",
    "yoyo-cli",
  ]
  const binDir = join(dir, "src-tauri/target", matrix.target, "release")
  mkdirSync(binDir, { recursive: true })
  for (const bin of bins) writeFileSync(join(binDir, bin), "fixture")
  mkdirSync(join(dir, "out"))
  writeFileSync(join(dir, "out/index.html"), "fixture frontend")
  const step = server.steps.find((entry) => entry.name === "Package (Unix)")
  const script = step.run.replace(
    /\$\{\{ matrix\.(\w+) \}\}/g,
    (_, key) => matrix[key]
  )
  run("bash", ["-e", "-c", script], dir)
  for (const name of [matrix.artifact, matrix.legacy_artifact]) {
    const entries = run("tar", ["tzf", `dist/${name}.tar.gz`], dir)
      .trim()
      .split(/\r?\n/)
    for (const bin of bins) assert.ok(entries.includes(`${name}/${bin}`))
    assert.ok(entries.includes(`${name}/web/index.html`))
    assert.ok(entries.every((entry) => entry.startsWith(`${name}/`)))
  }
  const dockerDownloads = workflow.jobs["build-docker"].steps
    .filter((entry) => entry.uses === "actions/download-artifact@v4")
    .map((entry) => entry.with.name)
  assert.deepEqual(dockerDownloads, [
    "yoyocode-server-linux-x64",
    "yoyocode-server-linux-arm64",
  ])
})

test(
  "Windows CI packages both asset roots",
  { skip: process.platform !== "win32" },
  (t) => {
    const dir = scratch(t)
    const matrix = server.strategy.matrix.include.find(
      (entry) => entry.target === "x86_64-pc-windows-msvc"
    )
    const binDir = join(dir, "src-tauri/target", matrix.target, "release")
    mkdirSync(binDir, { recursive: true })
    for (const bin of [
      "codeg-server",
      "yoyocode-mcp",
      "codeg-computer-helper",
      "yoyo-cli",
    ]) {
      writeFileSync(join(binDir, `${bin}.exe`), "fixture")
    }
    mkdirSync(join(dir, "out"))
    writeFileSync(join(dir, "out/index.html"), "fixture frontend")
    const step = server.steps.find(
      (entry) => entry.name === "Package (Windows)"
    )
    const script = step.run.replace(
      /\$\{\{ matrix\.(\w+) \}\}/g,
      (_, key) => matrix[key]
    )
    writeFileSync(
      join(dir, "package.ps1"),
      `$ErrorActionPreference = 'Stop'\n${script}\n
    foreach ($name in @('yoyocode-server-windows-x64', 'codeg-server-windows-x64')) {
      Expand-Archive -Path "dist/$name.zip" -DestinationPath "check/$name"
      foreach ($leaf in @('codeg-server.exe', 'yoyocode-mcp.exe', 'codeg-computer-helper.exe', 'yoyo-cli.exe', 'web/index.html')) {
        if (-not (Test-Path "check/$name/$name/$leaf")) { throw "Missing $name/$leaf" }
      }
    }
  `
    )
    run(
      "powershell.exe",
      ["-NoProfile", "-ExecutionPolicy", "Bypass", "-File", "package.ps1"],
      dir
    )
  }
)

test("server manifest announces the same PLUS tag used for signatures", (t) => {
  const dir = scratch(t)
  const step = server.steps.find(
    (entry) => entry.name === "Generate server update manifest"
  )
  assert.ok(step, "server needs its own full-version manifest")
  const script = step.run.replace(/gh release upload[^\n]*/g, ":")
  mkdirSync(join(dir, "dist"))
  run("bash", ["-e", "-c", script], dir, {
    GITHUB_REF_NAME: "v0.42.0-plus.10",
  })
  const manifest = JSON.parse(
    readFileSync(join(dir, "dist/server-latest.json"), "utf8")
  )
  assert.equal(manifest.version, "0.42.0-plus.10")
  assert.ok(Number.isFinite(Date.parse(manifest.pub_date)))
})
