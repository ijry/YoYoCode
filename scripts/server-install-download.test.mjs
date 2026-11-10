import assert from "node:assert/strict"
import { spawnSync } from "node:child_process"
import { existsSync, mkdtempSync, readFileSync, rmSync } from "node:fs"
import { tmpdir } from "node:os"
import { dirname, join, resolve } from "node:path"
import { fileURLToPath } from "node:url"
import test from "node:test"

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..")
const powershell = ["pwsh", "powershell"].find(
  (command) =>
    spawnSync(command, ["-NoProfile", "-Command", "exit 0"]).status === 0
)

// Execute only configuration assignments and the download/extract section.
// Never source an installer: its stop, cleanup and installation code is unsafe
// to run against the developer's machine, even with a temporary install path.
function section(source, end) {
  const start = source.indexOf("# ── Download and extract ──")
  const stop = source.indexOf(end, start)
  assert.ok(start >= 0 && stop > start)
  return source.slice(start, stop)
}

const sh = readFileSync(join(root, "install.sh"), "utf8")
const ps = readFileSync(join(root, "install.ps1"), "utf8")
const shScript = String.raw`
set -euo pipefail
if command -v cygpath >/dev/null; then
  FIXTURE=$(cygpath -u "$FIXTURE")
  export TMPDIR="$FIXTURE"
fi
PLATFORM=linux
ARCH_SUFFIX=x64
VERSION=v0.42.0-plus.10
${sh.match(/^REPO=.*$/m)[0]}
${sh.match(/^ARTIFACT=.*$/m)[0]}
for prefix in yoyocode codeg; do
  name="$prefix-server-linux-x64"
  mkdir -p "$FIXTURE/$name/web"
  printf server > "$FIXTURE/$name/codeg-server"
  printf companion > "$FIXTURE/$name/yoyocode-mcp"
  printf frontend > "$FIXTURE/$name/web/index.html"
  tar czf "$FIXTURE/$name.tar.gz" -C "$FIXTURE" "$name"
done
curl() {
  local out='' url='' write_out=0 status
  while [ "$#" -gt 0 ]; do
    case "$1" in
      -o|--output) out="$2"; shift 2 ;;
      -w|--write-out) write_out=1; shift 2 ;;
      https://*) url="$1"; shift ;;
      *) shift ;;
    esac
  done
  printf '%s\n' "$url" >> "$FIXTURE/requests"
  status="$FIRST_STATUS"
  if [ "$(wc -l < "$FIXTURE/requests")" -gt 1 ]; then status="$SECOND_STATUS"; fi
  if [ "$write_out" = 1 ]; then
    if [ "$status" = network ]; then printf 000; else printf '%s' "$status"; fi
  fi
  case "$status" in
    200) cp "$FIXTURE/${"${url##*/}"}" "$out" ;;
    network) return 7 ;;
    *) return 22 ;;
  esac
}
${section(sh, "# ── Install binaries ──")}
[ "$(< "$TMP_DIR/$ARTIFACT/codeg-server")" = server ]
[ "$(< "$TMP_DIR/$ARTIFACT/yoyocode-mcp")" = companion ]
[ "$(< "$TMP_DIR/$ARTIFACT/web/index.html")" = frontend ]
printf '%s' "$ARTIFACT" > "$FIXTURE/extracted"
`

const psScript = String.raw`
$ErrorActionPreference = "Stop"
$Version = "v0.42.0-plus.10"
${ps.match(/^\$Repo =.*$/m)[0]}
${ps.match(/^\$Artifact =.*$/m)[0]}
foreach ($prefix in @("yoyocode", "codeg")) {
    $name = "$prefix-server-windows-x64"
    $dir = Join-Path $env:FIXTURE $name
    New-Item -ItemType Directory -Path (Join-Path $dir "web") -Force | Out-Null
    Set-Content (Join-Path $dir "codeg-server.exe") "server"
    Set-Content (Join-Path $dir "yoyocode-mcp.exe") "companion"
    Set-Content (Join-Path $dir "web/index.html") "frontend"
    Compress-Archive -Path $dir -DestinationPath (Join-Path $env:FIXTURE "$name.zip")
}
$script:Requests = 0
function Invoke-WebRequest {
    param($Uri, $OutFile, [switch]$UseBasicParsing)
    Add-Content (Join-Path $env:FIXTURE "requests") $Uri
    $script:Requests++
    $status = $env:FIRST_STATUS
    if ($script:Requests -gt 1) { $status = $env:SECOND_STATUS }
    if ($status -eq "200") {
        Copy-Item (Join-Path $env:FIXTURE ($Uri.Split('/')[-1])) $OutFile
        return
    }
    if ($status -eq "network") { throw [System.Net.WebException]::new("Connection failed") }
    $exception = [System.Exception]::new("HTTP $status")
    $exception | Add-Member -NotePropertyName Response -NotePropertyValue ([pscustomobject]@{
        StatusCode = [System.Net.HttpStatusCode][int]$status
    })
    throw $exception
}
${section(ps, "# ── Install ──")}
foreach ($entry in @(@("codeg-server.exe", "server"), @("yoyocode-mcp.exe", "companion"), @("web/index.html", "frontend"))) {
    if ((Get-Content (Join-Path $ArtifactDir $entry[0])) -ne $entry[1]) { throw "Wrong extracted content" }
}
Set-Content (Join-Path $env:FIXTURE "extracted") $Artifact
`

for (const [shell, suffix, ext, script] of [
  ["bash", "linux-x64", "tar.gz", shScript],
  [powershell, "windows-x64", "zip", psScript],
]) {
  for (const [label, first, second, expectedPrefix] of [
    ["new asset", "200", "500", "yoyocode"],
    ["404 falls back to legacy asset", "404", "200", "codeg"],
    ["500 does not fall back", "500", "200", null],
    ["connection failure does not fall back", "network", "200", null],
    ["legacy 404 fails without another fallback", "404", "404", null],
  ]) {
    test(`${shell || "PowerShell"}: ${label}`, (t) => {
      assert.ok(shell, "PowerShell (pwsh or powershell) must be available")
      const dir = mkdtempSync(join(tmpdir(), "server-download-test-"))
      t.after(() => rmSync(dir, { recursive: true, force: true }))
      const args =
        shell === "bash"
          ? ["-c", script]
          : [
              "-NoProfile",
              "-NonInteractive",
              "-EncodedCommand",
              Buffer.from(script, "utf16le").toString("base64"),
            ]
      const result = spawnSync(shell, args, {
        cwd: root,
        encoding: "utf8",
        timeout: 30_000,
        env: {
          ...process.env,
          FIXTURE: dir.replaceAll("\\", "/"),
          TMPDIR: dir.replaceAll("\\", "/"),
          TEMP: dir,
          FIRST_STATUS: first,
          SECOND_STATUS: second,
        },
      })
      assert.ifError(result.error)
      assert.ok(
        existsSync(join(dir, "requests")),
        result.stdout + result.stderr
      )
      const base =
        "https://github.com/ijry/YoYoCode/releases/download/v0.42.0-plus.10/"
      const expected = [`${base}yoyocode-server-${suffix}.${ext}`]
      if (first === "404") expected.push(`${base}codeg-server-${suffix}.${ext}`)
      assert.deepEqual(
        readFileSync(join(dir, "requests"), "utf8").trim().split(/\r?\n/),
        expected
      )
      if (expectedPrefix) {
        assert.equal(result.status, 0, result.stdout + result.stderr)
        assert.equal(
          readFileSync(join(dir, "extracted"), "utf8").trim(),
          `${expectedPrefix}-server-${suffix}`
        )
      } else {
        assert.notEqual(result.status, 0, result.stdout + result.stderr)
        assert.equal(existsSync(join(dir, "extracted")), false)
      }
    })
  }
}
