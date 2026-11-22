// PLUS 上游同步（merge 流）。
//
// main = upstream/main + 本地 commit。本脚本 fetch 上游后把 upstream/main
// merge 进 main：本地还没有自有 commit 时是 fast-forward，有则产生一个合并
// 提交，冲突一次性集中解决。机制说明与变更登记见 docs/PLUS-PATCHES.md。
//
// 用法: node scripts/sync-upstream.mjs

import { execFileSync } from "node:child_process"

const UPSTREAM = "upstream"
const UPSTREAM_URL = "https://github.com/spacering-net/codeg.git"
const BRANCH = "main"

const git = (args, opts = {}) => {
  // stdio: "inherit" 时返回 null（输出直接进终端），此时无需 trim
  const out = execFileSync("git", args, { encoding: "utf8", ...opts })
  return typeof out === "string" ? out.trim() : out
}

const fail = (msg) => {
  console.error(`✗ ${msg}`)
  process.exit(1)
}

// ── 前置检查 ───────────────────────────────────────────────────────────────
if (git(["rev-parse", "--abbrev-ref", "HEAD"]) !== BRANCH)
  fail(`请在 ${BRANCH} 分支上执行`)

const dirty = git(["status", "--porcelain"])
if (dirty) fail(`工作区不干净，先提交或 stash：\n${dirty}`)

if (!git(["remote"]).split("\n").includes(UPSTREAM))
  fail(
    `缺少 ${UPSTREAM} remote：\n  git remote add ${UPSTREAM} ${UPSTREAM_URL}`
  )

// ── 拉取上游 ───────────────────────────────────────────────────────────────
console.log(`▶ git fetch ${UPSTREAM} --prune`)
git(["fetch", UPSTREAM, "--prune"], { stdio: "inherit" })

const tip = git(["rev-parse", "--short", `${UPSTREAM}/${BRANCH}`])
const behind = Number(
  git(["rev-list", "--count", `HEAD..${UPSTREAM}/${BRANCH}`])
)
const ahead = Number(
  git(["rev-list", "--count", `${UPSTREAM}/${BRANCH}..HEAD`])
)

if (behind === 0) {
  console.log(
    `✓ 已是最新（upstream/main = ${tip}，本地 ${ahead} 个自有 commit）`
  )
  process.exit(0)
}

console.log(
  `▶ 上游新增 ${behind} 个 commit（→ ${tip}），` +
    `本地 ${ahead} 个自有 commit，开始 merge`
)

// ── 合并 ───────────────────────────────────────────────────────────────────
try {
  git(["merge", `${UPSTREAM}/${BRANCH}`, "--no-edit"])
} catch {
  const conflicts = git(["diff", "--name-only", "--diff-filter=U"])
    .split("\n")
    .filter(Boolean)
  console.error(`\n✗ ${conflicts.length} 个文件冲突，逐个解决后结束合并：`)
  for (const f of conflicts) console.error(`    ${f}`)
  console.error("\n  git add <文件> && git commit")
  console.error("  rerere 已启用，与历史相同的冲突会自动套用当时的解法")
  console.error("  对照 docs/PLUS-PATCHES.md 核对每处差异是否仍需保留")
  process.exit(1)
}

// ── 完成提示 ───────────────────────────────────────────────────────────────
const changed = git(["diff", "--name-only", "ORIG_HEAD", "HEAD"])
console.log(`✓ 同步完成，main = ${git(["rev-parse", "--short", "HEAD"])}`)
if (changed.includes("pnpm-lock.yaml"))
  console.log("▶ pnpm-lock.yaml 有变化，请执行 pnpm install")
if (changed.includes("Cargo.lock"))
  console.log("▶ Cargo.lock 有变化，下次 cargo build 自动更新")
console.log(`▶ 推送：git push origin ${BRANCH}`)
