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
const desktop = workflow.jobs["build-tauri"]

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

test("desktop identity writes the full PLUS version by default so updaters see iterations", (t) => {
  const dir = identityFixture(t)
  const args = ["scripts/apply-plus-identity.mjs", "--check", "--iteration", "10"]
  // --check must not touch disk, so a later real injection still sees a pristine base.
  run(process.execPath, args, dir)
  assert.equal(
    JSON.parse(readFileSync(join(dir, "src-tauri/tauri.conf.json"))).version,
    "0.42.0"
  )
  run(process.execPath, ["scripts/apply-plus-identity.mjs", "--iteration", "10"], dir)
  assert.equal(
    JSON.parse(readFileSync(join(dir, "src-tauri/tauri.conf.json"))).version,
    "0.42.0-plus.10"
  )
  assert.equal(
    JSON.parse(readFileSync(join(dir, "package.json"))).version,
    "0.42.0-plus.10"
  )
  // Desktop injection leaves Cargo.toml alone; only --server writes it.
  assert.match(
    readFileSync(join(dir, "src-tauri/Cargo.toml"), "utf8"),
    /version = "0\.42\.0"/
  )
})

test("safe mode remains an explicit escape hatch back to a pure three-part version", (t) => {
  const dir = identityFixture(t)
  run(
    process.execPath,
    ["scripts/apply-plus-identity.mjs", "--version-mode", "safe", "--iteration", "10"],
    dir
  )
  assert.equal(
    JSON.parse(readFileSync(join(dir, "src-tauri/tauri.conf.json"))).version,
    "0.42.0"
  )
})

test("server identity uses the full PLUS version and is idempotent only via --check", (t) => {
  const dir = identityFixture(t)
  const args = ["scripts/apply-plus-identity.mjs", "--server", "--check", "--iteration", "10"]
  run(process.execPath, args, dir)
  assert.match(
    readFileSync(join(dir, "src-tauri/Cargo.toml"), "utf8"),
    /version = "0\.42\.0"/
  )
  run(process.execPath, ["scripts/apply-plus-identity.mjs", "--server", "--iteration", "10"], dir)
  const cargo = readFileSync(join(dir, "src-tauri/Cargo.toml"), "utf8")
  assert.match(cargo, /version = "0\.42\.0-plus\.10"/)
  assert.match(cargo, /serde = \{ version = "1" \}/)
})

test("desktop CI injects the PLUS version with semver mode", () => {
  const step = desktop.steps.find((entry) => entry.name === "Apply PLUS identity")
  assert.ok(step, "desktop build needs the identity injection step")
  assert.match(step.run, /--version-mode semver/)
  assert.match(step.run, /--iteration/)
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

test("CI packages one asset with a matching root and unchanged executables", (t) => {
  const dir = scratch(t)
  const matrix = server.strategy.matrix.include.find(
    (entry) => entry.target === "x86_64-unknown-linux-gnu"
  )
  assert.equal(matrix.artifact, "yoyocode-server-linux-x64")
  const bins = [
    "yoyocode-server",
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
  const name = matrix.artifact
  const entries = run("tar", ["tzf", `dist/${name}.tar.gz`], dir)
    .trim()
    .split(/\r?\n/)
  for (const bin of bins) assert.ok(entries.includes(`${name}/${bin}`))
  assert.ok(entries.includes(`${name}/web/index.html`))
  assert.ok(entries.every((entry) => entry.startsWith(`${name}/`)))
  const dockerDownloads = workflow.jobs["build-docker"].steps
    .filter((entry) => entry.uses === "actions/download-artifact@v4")
    .map((entry) => entry.with.name)
  assert.deepEqual(dockerDownloads, [
    "yoyocode-server-linux-x64",
    "yoyocode-server-linux-arm64",
  ])
})

test(
  "Windows CI packages one asset root",
  { skip: process.platform !== "win32" },
  (t) => {
    const dir = scratch(t)
    const matrix = server.strategy.matrix.include.find(
      (entry) => entry.target === "x86_64-pc-windows-msvc"
    )
    const binDir = join(dir, "src-tauri/target", matrix.target, "release")
    mkdirSync(binDir, { recursive: true })
    for (const bin of [
      "yoyocode-server",
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
    $name = 'yoyocode-server-windows-x64'
    Expand-Archive -Path "dist/$name.zip" -DestinationPath "check/$name"
    foreach ($leaf in @('yoyocode-server.exe', 'yoyocode-mcp.exe', 'codeg-computer-helper.exe', 'yoyo-cli.exe', 'web/index.html')) {
      if (-not (Test-Path "check/$name/$name/$leaf")) { throw "Missing $name/$leaf" }
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
