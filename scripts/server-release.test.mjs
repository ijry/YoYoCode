import assert from "node:assert/strict"
import { spawnSync } from "node:child_process"
import {
  chmodSync,
  cpSync,
  existsSync,
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
const rustTests = parse(
  readFileSync(join(root, ".github/workflows/test.yml"), "utf8")
).jobs.rust

const sherpaPlatforms = [
  ["x86_64-unknown-linux-gnu", "Linux", "X64", "linux-x64-static-lib"],
  ["aarch64-unknown-linux-gnu", "Linux", "ARM64", "linux-aarch64-static-lib"],
  ["x86_64-apple-darwin", "macOS", "X64", "osx-x64-static-lib"],
  ["aarch64-apple-darwin", "macOS", "ARM64", "osx-arm64-static-lib"],
  ["x86_64-pc-windows-msvc", "Windows", "X64", "win-x64-static-MT-Release-lib"],
  [
    "aarch64-pc-windows-msvc",
    "Windows",
    "ARM64",
    "win-arm64-static-MT-Release-lib",
  ],
]

for (const [label, job] of [
  ["desktop release", desktop],
  ["server release", server],
  ["Rust tests", rustTests],
]) {
  test(`${label} purges stale sherpa extraction caches before building`, () => {
    const purgeStep = job.steps.find(
      (entry) => entry.name === "Purge stale sherpa-onnx extraction caches"
    )
    assert.ok(
      purgeStep,
      "rust-cache restores can be size-pruned; the sys crate must re-extract"
    )
    assert.match(purgeStep.run, /sherpa-onnx-prebuilt/)
    assert.match(purgeStep.run, /sherpa-onnx-sys-\*/)
  })

  test(`${label} exports the cached native archive to the sherpa build script`, (t) => {
    const resolveStep = job.steps.find((entry) =>
      entry.name?.startsWith("Resolve sherpa-onnx native archive")
    )
    const cacheStep = job.steps.find(
      (entry) => entry.name === "Cache sherpa-onnx native archive"
    )
    assert.ok(resolveStep)
    assert.ok(cacheStep)

    for (const [target, os, arch, archive] of sherpaPlatforms) {
      if (
        job.strategy.matrix.include?.some((entry) => entry.target) &&
        !job.strategy.matrix.include.some((entry) => entry.target === target)
      )
        continue

      const dir = scratch(t)
      const runnerTemp = join(dir, "runner temp").replaceAll("\\", "/")
      const envFile = join(dir, "github-env").replaceAll("\\", "/")
      const archiveDir = `${runnerTemp}/sherpa-onnx`
      const archiveName = `sherpa-onnx-v2.0.1-${archive}.tar.bz2`
      mkdirSync(archiveDir, { recursive: true })
      writeFileSync(join(archiveDir, archiveName), "cached native library")
      writeFileSync(envFile, "")
      run(
        "bash",
        [
          "-e",
          "-o",
          "pipefail",
          "-c",
          resolveStep.run.replaceAll("${{ matrix.target }}", target),
        ],
        dir,
        {
          RUNNER_OS: os,
          RUNNER_ARCH: arch,
          RUNNER_TEMP: runnerTemp,
          GITHUB_ENV: envFile,
          SHERPA_ONNX_ARCHIVE_DIR: "",
        }
      )
      const exported = Object.fromEntries(
        readFileSync(envFile, "utf8")
          .trim()
          .split(/\r?\n/)
          .map((line) => {
            const equals = line.indexOf("=")
            return [line.slice(0, equals), line.slice(equals + 1)]
          })
      )
      assert.ok(
        exported.SHERPA_ONNX_ARCHIVE_DIR,
        `${target}: sherpa cannot see the cached archive`
      )
      assert.equal(
        readFileSync(
          join(exported.SHERPA_ONNX_ARCHIVE_DIR, archiveName),
          "utf8"
        ),
        "cached native library"
      )
      assert.equal(exported.SHERPA_ARCHIVE_NAME, archiveName)
      const cachePath = cacheStep.with.path
        .replaceAll("${{ runner.temp }}", runnerTemp)
        .replace(/\$\{\{ env\.(\w+) \}\}/g, (_, key) => exported[key] ?? "")
      assert.equal(
        resolve(cachePath),
        resolve(exported.SHERPA_ONNX_ARCHIVE_DIR)
      )

      // The merged macOS release job cross-builds the other architecture on
      // the same runner, so its cache must carry BOTH archives and the
      // download must fetch any that are missing. The test-rust job only
      // builds for its host, so the extra archive is release-desktop-only.
      if (target === "aarch64-apple-darwin" && label === "desktop release") {
        const extraName = "sherpa-onnx-v2.0.1-osx-x64-static-lib.tar.bz2"
        assert.equal(exported.SHERPA_EXTRA_ARCHIVE_NAME, extraName)
        assert.match(exported.SHERPA_CACHE_KEY, new RegExp(extraName))
        writeFileSync(join(archiveDir, extraName), "cached native library")
        const downloadStep = job.steps.find(
          (entry) =>
            entry.name === "Download sherpa-onnx native archive on cache miss"
        )
        assert.ok(downloadStep)
        const downloads = join(dir, "downloads")
        mkdirSync(join(dir, "curl-bin"), { recursive: true })
        writeFileSync(
          join(dir, "curl-bin/curl"),
          [
            "#!/bin/bash",
            "for ((i=1; i<=$#; i++)); do",
            '  if [[ "${!i}" == "-o" ]]; then',
            '    dest="${!((i+1))}"',
            '    url="${!((i+2))}"',
            '    name="$(basename "$url")"',
            '    mkdir -p "$(dirname "$dest")"',
            '    cp "' + downloads.replaceAll("\\", "/") + '/$name" "$dest"',
            "    exit 0",
            "  fi",
            "done",
            "exit 1",
          ].join("\n")
        )
        chmodSync(join(dir, "curl-bin/curl"), 0o755)
        mkdirSync(downloads, { recursive: true })
        for (const name of [archiveName, extraName]) {
          writeFileSync(join(downloads, name), `downloaded ${name}`)
        }
        const downloadScript = downloadStep.run.replaceAll(
          "${{ matrix.target }}",
          target
        )
        const env = { ...exported }
        delete env.__proto__
        run("bash", ["-e", "-o", "pipefail", "-c", downloadScript], dir, {
          ...env,
          PATH: `${join(dir, "curl-bin")}:${process.env.PATH}`,
        })
        for (const name of [archiveName, extraName]) {
          const expected =
            existsSync(join(archiveDir, name)) &&
            readFileSync(join(archiveDir, name), "utf8") !==
              `downloaded ${name}`
              ? "cached native library"
              : `downloaded ${name}`
          assert.equal(
            readFileSync(join(exported.SHERPA_ONNX_ARCHIVE_DIR, name), "utf8"),
            expected
          )
        }
      }
    }
  })
}

test("Rust media tests provision a subtitle-capable FFmpeg on every test OS", () => {
  const macosTools = rustTests.steps.find(
    (entry) => entry.name === "Setup macOS FFmpeg media environment"
  )
  assert.ok(macosTools, "macOS media tests need a libass-enabled FFmpeg")
  assert.equal(
    macosTools.uses,
    "mamba-org/setup-micromamba@v3",
    "macOS media tests must use the conda-forge build"
  )
  assert.match(macosTools.with?.["create-args"] ?? "", /ffmpeg/)

  const mediaChecks = rustTests.steps.filter(
    (entry) => entry.name?.includes("FFmpeg") && entry.run
  )
  assert.ok(mediaChecks.length >= 2, "each media runner needs a tool check")
  for (const step of mediaChecks) {
    assert.match(
      step.run ?? "",
      /(?:ffmpeg -version|& \$ffmpeg\.FullName -version)/
    )
    assert.match(
      step.run ?? "",
      /(?:ffprobe -version|& \$ffprobe\.FullName -version)/
    )

    assert.match(step.run ?? "", /(?:-filters|subtitles)/)
    assert.match(step.run ?? "", /subtitles/)
  }
})

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
  const args = [
    "scripts/apply-plus-identity.mjs",
    "--check",
    "--iteration",
    "10",
  ]
  // --check must not touch disk, so a later real injection still sees a pristine base.
  run(process.execPath, args, dir)
  assert.equal(
    JSON.parse(readFileSync(join(dir, "src-tauri/tauri.conf.json"))).version,
    "0.42.0"
  )
  run(
    process.execPath,
    ["scripts/apply-plus-identity.mjs", "--iteration", "10"],
    dir
  )
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
    [
      "scripts/apply-plus-identity.mjs",
      "--version-mode",
      "safe",
      "--iteration",
      "10",
    ],
    dir
  )
  assert.equal(
    JSON.parse(readFileSync(join(dir, "src-tauri/tauri.conf.json"))).version,
    "0.42.0"
  )
})

test("server identity uses the full PLUS version and is idempotent only via --check", (t) => {
  const dir = identityFixture(t)
  const args = [
    "scripts/apply-plus-identity.mjs",
    "--server",
    "--check",
    "--iteration",
    "10",
  ]
  run(process.execPath, args, dir)
  assert.match(
    readFileSync(join(dir, "src-tauri/Cargo.toml"), "utf8"),
    /version = "0\.42\.0"/
  )
  run(
    process.execPath,
    ["scripts/apply-plus-identity.mjs", "--server", "--iteration", "10"],
    dir
  )
  const cargo = readFileSync(join(dir, "src-tauri/Cargo.toml"), "utf8")
  assert.match(cargo, /version = "0\.42\.0-plus\.10"/)
  assert.match(cargo, /serde = \{ version = "1" \}/)
})

test("macOS release declares microphone use and signs the main app for audio input", () => {
  const { infoPlist } = JSON.parse(
    readFileSync(join(root, "src-tauri/tauri.macos.conf.json"), "utf8")
  ).bundle.macOS
  assert.ok(infoPlist, "macOS bundle needs a microphone usage description")
  assert.match(
    readFileSync(join(root, "src-tauri", infoPlist), "utf8"),
    /<key>NSMicrophoneUsageDescription<\/key>\s*<string>\s*[^<\s][^<]*<\/string>/
  )

  const macosBuilds = desktop.steps.filter((entry) =>
    entry.name?.startsWith("Build, sign, and upload to draft release (macOS")
  )
  assert.ok(macosBuilds.length >= 1, "macOS release needs its build passes")
  for (const macosBuild of macosBuilds) {
    const configPath = macosBuild.with.args.match(/--config\s+(\S+)/)?.[1]
    assert.ok(configPath, "macOS release needs a signing configuration")
    const { bundle } = JSON.parse(readFileSync(join(root, configPath), "utf8"))
    assert.deepEqual(bundle.externalBin, [], "sidecars are signed separately")
    for (const name of ["yoyocode-mcp", "yoyo-cli"]) {
      assert.ok(
        bundle.macOS.files[`MacOS/${name}`],
        `${name} must remain beside the main executable`
      )
    }
    const entitlementPlist = readFileSync(
      join(root, "src-tauri", bundle.macOS.entitlements),
      "utf8"
    )
    assert.match(
      entitlementPlist,
      /<key>com\.apple\.security\.device\.audio-input<\/key>\s*<true\s*\/>/
    )
    assert.equal(entitlementPlist.match(/<key>/g)?.length, 1)
  }
})

test("macOS release builds arm64 natively and x64 by cross-compiling on one runner", () => {
  const appleEntries = desktop.strategy.matrix.include.filter((entry) =>
    entry.target.includes("apple-darwin")
  )
  assert.equal(
    appleEntries.length,
    1,
    "one macOS job: a second macOS runner is what GitHub kept cancelling"
  )
  assert.equal(appleEntries[0].target, "aarch64-apple-darwin")
  // macos-latest (arm64) sat starved for hours on 2026-11-19/20 while the
  // Intel pool stayed available (otools ships macos-15-intel natively): run
  // on Intel, where x64 is native and arm64 becomes the cross build. Both
  // cross envs ride every rust step so the job is host-agnostic.
  assert.equal(appleEntries[0].runner, "macos-15-intel")

  const toolchain = desktop.steps.find(
    (entry) => entry.name === "Install Rust stable"
  )
  assert.match(
    toolchain.with.targets,
    /aarch64-apple-darwin,x86_64-apple-darwin/,
    "the cross target must be installed alongside the native one"
  )

  const buildPasses = desktop.steps.filter(
    (entry) =>
      entry.uses === "tauri-apps/tauri-action@v0.6.1" &&
      entry.with?.args?.includes(
        "--config src-tauri/macos/tauri.release.conf.json"
      )
  )
  assert.deepEqual(
    buildPasses.map((entry) => entry.with.args.match(/--target\s+(\S+)/)?.[1]),
    ["aarch64-apple-darwin", "x86_64-apple-darwin"],
    "arm64 builds first, then x64, both on the same runner"
  )
  for (const pass of buildPasses) {
    assert.equal(pass.env.CODEG_SKIP_SIDECAR, "1")
    assert.equal(pass.env.CC_aarch64_apple_darwin, "clang")
    assert.equal(pass.env.CXX_aarch64_apple_darwin, "clang++")
    assert.equal(pass.env.CC_x86_64_apple_darwin, "clang")
    assert.equal(pass.env.CXX_x86_64_apple_darwin, "clang++")
  }
})

test("macOS release stages sidecars at their configured bundle source paths", (t) => {
  const armStage = desktop.steps.find(
    (entry) => entry.name === "Stage signed macOS sidecars"
  )
  const crossStage = desktop.steps.find(
    (entry) => entry.name === "Stage signed macOS x64 sidecars"
  )
  assert.ok(armStage, "native arm64 sidecars are staged and signed")
  assert.ok(crossStage, "cross x64 sidecars are staged and signed")
  const macosBuilds = desktop.steps.filter((entry) =>
    entry.name?.startsWith("Build, sign, and upload to draft release (macOS")
  )
  const configPath = macosBuilds[0].with.args.match(/--config\s+(\S+)/)[1]
  const { bundle } = JSON.parse(readFileSync(join(root, configPath), "utf8"))

  // arm64: the shared stage step copies the matrix-target binaries; x64: the
  // cross stage step first cross-builds them, then copies and signs.
  const stages = [
    { step: armStage, target: "aarch64-apple-darwin", prepare: null },
    {
      step: crossStage,
      target: "x86_64-apple-darwin",
      prepare: "pnpm tauri:prepare-sidecars --target x86_64-apple-darwin",
    },
  ]
  for (const { step, target, prepare } of stages) {
    const dir = scratch(t)
    const bins = join(dir, "src-tauri/binaries")
    mkdirSync(bins, { recursive: true })
    for (const name of ["yoyocode-mcp", "codeg-computer-helper", "yoyo-cli"]) {
      writeFileSync(join(bins, `${name}-${target}`), `${target}:${name}`)
    }
    if (prepare) {
      assert.match(step.run, /cross_target="x86_64-apple-darwin"/)
      assert.match(
        step.run,
        /pnpm tauri:prepare-sidecars --target "\$cross_target"/
      )
      // The cross compiler env rides the step env, not the run script.
      assert.equal(step.env.CC_x86_64_apple_darwin, "clang")
      assert.equal(step.env.CXX_x86_64_apple_darwin, "clang++")
    }
    // Only replace the unavailable macOS signing/inspection binaries and the
    // pnpm runner (the scratch dir has no manifest). Run the actual workflow
    // copy commands, then inspect the files the bundler will consume.
    const script =
      [
        'codesign() { printf "%s\\n" "$*" >> signatures.log; }',
        'pnpm() { printf "%s\\n" "$*" >> pnpm.log; }',
        "lipo() { echo x86_64; }",
        step.run,
      ].join("\n") + "\n"
    run("bash", ["-e", "-c", script], dir, { BUILD_TARGET: target })
    for (const name of ["yoyocode-mcp", "yoyo-cli"]) {
      const source = bundle.macOS.files[`MacOS/${name}`]
      assert.equal(
        readFileSync(join(dir, "src-tauri", source), "utf8"),
        `${target}:${name}`
      )
    }
    const signatures = readFileSync(join(dir, "signatures.log"), "utf8")
      .trim()
      .split(/\r?\n/)
    assert.deepEqual(signatures, [
      "--force --options runtime --sign - src-tauri/binaries/macos-release/yoyocode-mcp",
      "--force --options runtime --sign - src-tauri/binaries/macos-release/yoyo-cli",
    ])
    if (prepare) {
      assert.deepEqual(
        readFileSync(join(dir, "pnpm.log"), "utf8").trim().split(/\r?\n/),
        ["tauri:prepare-sidecars --target x86_64-apple-darwin"]
      )
    }
  }
})

test("macOS bundle gate verifies every architecture, including x64 purity", () => {
  const gate = desktop.steps.find(
    (entry) => entry.name === "Verify macOS bundle (ad-hoc)"
  )
  assert.ok(gate)
  // Both bundles are checked, each against its own target triple.
  assert.match(
    gate.run,
    /for gate_target in aarch64-apple-darwin x86_64-apple-darwin/
  )
  // Cross-compiled builds prove the executable carries only x86_64 code.
  assert.match(gate.run, /lipo -archs "\$app\/Contents\/MacOS\/\$BIN"/)
  assert.match(gate.run, /x86_64-apple-darwin\)\s+want_arch="x86_64"/)
})

test("desktop CI injects the PLUS version with semver mode", () => {
  const step = desktop.steps.find(
    (entry) => entry.name === "Apply PLUS identity"
  )
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
