#!/usr/bin/env node
/**
 * PLUS 身份注入 —— 构建期改写，**不提交进 git**。
 *
 * 为什么是构建期而不是一个 patch 提交：`version` / `productName` 这些字段上游
 * 每次发版都会动，一旦在分支里 patch 它们，每次 rebase 必冲突，而且 rerere 也帮不上
 * （值每次都不一样）。放到构建期注入，git 历史里这些字段和上游逐位相同 —— 永久冲突面归零。
 *
 * 用法：
 *   node scripts/apply-plus-identity.mjs --iteration 1
 *   node scripts/apply-plus-identity.mjs --iteration 1 --version-mode semver
 *   node scripts/apply-plus-identity.mjs --check     # 只打印将要改成什么，不落盘
 */

import { execFileSync } from "node:child_process"
import { readFileSync, writeFileSync } from "node:fs"
import { resolve, dirname } from "node:path"
import { fileURLToPath } from "node:url"
import { createRequire } from "node:module"

const ROOT = resolve(dirname(fileURLToPath(import.meta.url)), "..")
const TAURI_CONF = resolve(ROOT, "src-tauri/tauri.conf.json")
const PKG_JSON = resolve(ROOT, "package.json")
const CARGO_TOML = resolve(ROOT, "src-tauri/Cargo.toml")

const PLUS_IDENTIFIER = "net.lingyun.yoyocode"
const PLUS_PRODUCT_NAME = "YoYoCode"

/**
 * 更新源必须换成 PLUS 自己的 release：否则 PLUS 会把自己自动更新成官方版 ——
 * plus 功能全丢，且 PLUS 写入的库官方版读不了（migration 门禁会拒，见
 * backup/core.rs 的 evaluate_compat）。
 *
 * `pubkey` 是 PLUS 自己的 minisign 公钥（私钥在
 * ~/Repos/xyito/config/codeg-plus/codeg-plus.key，口令为空，同时以
 * TAURI_SIGNING_PRIVATE_KEY / TAURI_SIGNING_PRIVATE_KEY_PASSWORD 两个 secret
 * 配在 ijry/YoYoCode）。更新包由 release.yml 用它签名，客户端用这把公钥
 * 验签 —— 私钥丢了 updater 就废了，换 key 必须同步改这里并重新发版。
 */
const PLUS_UPDATER_ENDPOINTS = [
  "https://github.com/ijry/YoYoCode/releases/latest/download/latest.json",
]
const PLUS_UPDATER_PUBKEY =
  "dW50cnVzdGVkIGNvbW1lbnQ6IG1pbmlzaWduIHB1YmxpYyBrZXk6IEY5OTQ5NzFCOUM0RjA1NjIKUldSaUJVK2NHNWVVK1RSVnNpK2Q4am0zS1B3cnR1Sy91Mzd4UVl3OTJzTzhlSVM0VElaSk0wL04K"

function parseArgs(argv) {
  const args = {
    iteration: process.env.PLUS_ITERATION || "1",
    versionMode: "safe",
    check: false,
  }
  for (let i = 0; i < argv.length; i += 1) {
    const a = argv[i]
    if (a === "--iteration") args.iteration = argv[++i]
    else if (a === "--version-mode") args.versionMode = argv[++i]
    else if (a === "--check") args.check = true
    else if (a === "--server") args.server = true
    else if (a === "--help" || a === "-h") args.help = true
    else throw new Error(`未知参数：${a}`)
  }
  if (!/^(0|[1-9]\d*)$/.test(String(args.iteration))) {
    throw new Error(`--iteration 必须是正整数，收到：${args.iteration}`)
  }
  if (!["safe", "semver"].includes(args.versionMode)) {
    throw new Error(
      `--version-mode 只能是 safe | semver，收到：${args.versionMode}`
    )
  }
  return args
}

function readJson(path) {
  return JSON.parse(readFileSync(path, "utf8"))
}

function writeJson(path, value) {
  // 尾随换行，跟上游的 prettier 产物保持一致，避免无意义的 diff 噪音
  writeFileSync(path, `${JSON.stringify(value, null, 2)}\n`, "utf8")
}

/**
 * JSON.stringify 的短数组总是展开成多行，而上游的 tauri.conf.json 是 prettier
 * 格式（短数组内联）。写入后用仓库自带的 prettier 归一，让工作区 diff 只剩
 * 身份字段本身、不带格式噪声。prettier 不可用时保留 stringify 输出（仅多几行
 * 格式噪声，不影响正确性）。
 */
function prettierize(file) {
  try {
    const require = createRequire(import.meta.url)
    const bin = resolve(
      dirname(require.resolve("prettier/package.json")),
      "bin/prettier.cjs"
    )
    execFileSync(process.execPath, [bin, "--write", file], { stdio: "pipe" })
  } catch {
    console.log(
      "  （prettier 不可用，保留 stringify 输出，diff 会带几行格式噪声）"
    )
  }
}

/**
 * 上游版本号必须是纯 semver 三段。
 *
 * safe 模式不动 version，所以重复注入是幂等的、无害的。semver 模式会把 version 写成
 * `x.y.z-plus.N`，此时再跑一次就会拿它当基线算出 `x.y.z-plus.N-plus.N` —— 这道守卫
 * 专门挡这种情况。
 */
function assertPristineBaseVersion(version) {
  if (!/^\d+\.\d+\.\d+$/.test(version)) {
    throw new Error(
      `tauri.conf.json 的 version 不是纯三段 semver（读到 "${version}"）。` +
        `这通常意味着 semver 模式的注入已经跑过一次，请先 git checkout 还原再重跑。`
    )
  }
}

function main() {
  const args = parseArgs(process.argv.slice(2))
  if (args.help) {
    console.log(
      "用法: node scripts/apply-plus-identity.mjs [--iteration N] [--version-mode safe|semver] [--server] [--check]"
    )
    return
  }

  const conf = readJson(TAURI_CONF)
  const pkg = readJson(PKG_JSON)

  const baseVersion = conf.version
  assertPristineBaseVersion(baseVersion)

  const plusVersion = `${baseVersion}-plus.${args.iteration}`
  // safe 模式：tauri.conf 的 version 保持纯三段。
  //
  // 原因：Windows MSI 的 ProductVersion 不接受 semver 的 prerelease 段，而上游从未发过
  // 任何 prerelease tag（`git tag -l` 里一条都没有，release.yml 的 channel 判定也只认
  // `-rc`），所以「带 prerelease 的版本号能不能过 Windows 打包」在这条流水线上没有先例。
  //
  // 用户看到的 `0.27.0-plus.N` 由 git tag、GitHub release 标题和产物文件名承载；
  // 安装后的应用靠 productName「YoYoCode」区分，不会和官方版混淆。
  //
  // 等你在 Windows 上实测过 semver 模式能出包，把默认切成 semver 即可。
  const injectedVersion =
    args.versionMode === "semver" ? plusVersion : baseVersion

  const changes = [
    [
      "src-tauri/tauri.conf.json  productName",
      conf.productName,
      PLUS_PRODUCT_NAME,
    ],
    [
      "src-tauri/tauri.conf.json  identifier ",
      conf.identifier,
      PLUS_IDENTIFIER,
    ],
    ["src-tauri/tauri.conf.json  version    ", conf.version, injectedVersion],
    [
      "src-tauri/tauri.conf.json  updater    ",
      JSON.stringify(conf.plugins?.updater?.endpoints ?? null),
      JSON.stringify(PLUS_UPDATER_ENDPOINTS),
    ],
  ]

  console.log(`PLUS 身份注入（--version-mode ${args.versionMode}）`)
  console.log(`  上游基线版本 : ${baseVersion}`)
  console.log(`  PLUS 版本标识: ${plusVersion}`)
  for (const [field, from, to] of changes) {
    console.log(`  ${field}: ${from} -> ${to}`)
  }

  // Server 不受 MSI 版本格式限制；Cargo 的运行版本必须与清单及签名的 PLUS tag 一致。
  let serverCargo
  if (args.server) {
    const cargo = readFileSync(CARGO_TOML, "utf8")
    const packageVersion = /(^\[package\][\s\S]*?^version\s*=\s*")[^"]+("\s*$)/m
    if (!packageVersion.test(cargo)) {
      throw new Error("src-tauri/Cargo.toml 中未找到 package.version")
    }
    serverCargo = cargo.replace(
      packageVersion,
      (_, before, after) => `${before}${plusVersion}${after}`
    )
    console.log(`  src-tauri/Cargo.toml  version: -> ${plusVersion}`)
  }

  if (args.check) {
    console.log("\n--check 模式：未写入任何文件。")
    // 供 CI 取用（即使 check 模式也输出，方便 tag 校验步骤复用）
    emitOutputs({ baseVersion, plusVersion, injectedVersion })
    return
  }

  conf.productName = PLUS_PRODUCT_NAME
  conf.identifier = PLUS_IDENTIFIER
  conf.version = injectedVersion
  conf.plugins = conf.plugins || {}
  conf.plugins.updater = {
    ...(conf.plugins.updater || {}),
    endpoints: PLUS_UPDATER_ENDPOINTS,
    pubkey: PLUS_UPDATER_PUBKEY,
  }

  // 没有自有更新源时不产出 updater 产物 —— 否则 CI 会为了签名去找不存在的私钥而失败。
  conf.bundle = conf.bundle || {}
  if (PLUS_UPDATER_ENDPOINTS.length === 0) {
    conf.bundle.createUpdaterArtifacts = false
    console.log(
      "  src-tauri/tauri.conf.json  createUpdaterArtifacts: true -> false（无自有更新源）"
    )
  }

  // macOS ad-hoc 签名：无 Developer ID 证书，signingIdentity 用 "-"（codesign
  // 的 ad-hoc 身份）。不设的话 bundler 完全不签名，交付的 .app 无签名。
  // 备好证书后删除此行，走 release.yml 上游的 APPLE_SIGNING_IDENTITY 流程。
  conf.bundle.macOS = { ...(conf.bundle.macOS || {}), signingIdentity: "-" }
  console.log(
    '  src-tauri/tauri.conf.json  macOS.signingIdentity: -> "-"（ad-hoc）'
  )

  writeJson(TAURI_CONF, conf)
  prettierize(TAURI_CONF)

  // package.json 的 version 与 tauri.conf 保持一致：release.yml 那类
  // 「tag 是否等于 app version」的校验会读它。
  pkg.version = injectedVersion
  writeJson(PKG_JSON, pkg)
  prettierize(PKG_JSON)

  if (serverCargo !== undefined) writeFileSync(CARGO_TOML, serverCargo, "utf8")

  console.log("\n已写入 src-tauri/tauri.conf.json 与 package.json。")
  emitOutputs({ baseVersion, plusVersion, injectedVersion })
}

/** 把算出来的版本号透给 GitHub Actions 后续步骤。 */
function emitOutputs({ baseVersion, plusVersion, injectedVersion }) {
  const out = process.env.GITHUB_OUTPUT
  if (!out) return
  writeFileSync(
    out,
    `base_version=${baseVersion}\nplus_version=${plusVersion}\ninjected_version=${injectedVersion}\n`,
    { flag: "a" }
  )
}

main()
