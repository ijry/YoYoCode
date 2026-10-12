/**
 * Regenerates docs/ROUTE_INVENTORY.md from the upstream sources.
 *
 * Three inputs, all read-only:
 *   1. _upstream/ref/src-tauri/src/web/router.rs  → the authoritative route
 *      list plus each route's owning handler module.
 *   2. src-go/ (递归 *.go)                          → which commands we have
 *      actually registered (`reg.Register("cmd", ...)`).
 *   3. mcode-app/src/ (递归 *.ts / *.vue)           → which commands the mobile
 *      client really calls (the P0 compatibility set).
 *
 * Run after rebasing onto a newer upstream so the checklist picks up new
 * commands automatically:
 *   node scripts/gen_inventory.js
 */
const fs = require("fs");
const path = require("path");

const ROOT = path.resolve(__dirname, "..");
const UPSTREAM_ROUTER = path.join(
  ROOT,
  "_upstream/ref/src-tauri/src/web/router.rs"
);
const MCODE_SRC = path.join(ROOT, "mcode-app/src");
const OUT = path.join(ROOT, "docs/ROUTE_INVENTORY.md");

if (!fs.existsSync(UPSTREAM_ROUTER)) {
  console.error("missing upstream router.rs — is _upstream/ checked out?");
  process.exit(1);
}

// ── 1. Parse upstream routes ───────────────────────────────────────────────
const src = fs.readFileSync(UPSTREAM_ROUTER, "utf8");
// Strip // line comments first: upstream documents a few routes (e.g.
// /background_set's body-size rationale) with a comment *between* the path
// string and the `post(...)` handler, which would otherwise break a
// single-regex match.
const srcNoComments = src.replace(/\/\/[^\n]*/g, "");
// Match the route path + method, then read the handler that immediately
// follows `post(`. Tolerant of trailing `.layer(...)` / middleware chains
// (which carry nested parens the old `([^)]*)` capture choked on) and of
// inline closures (handler → "(inline)").
const routeRe = /\.route\(\s*"([^"]+)"\s*,\s*(post|get|any)\(/g;
const routes = [];
let m;
while ((m = routeRe.exec(srcNoComments))) {
  const rest = srcNoComments.slice(routeRe.lastIndex, routeRe.lastIndex + 300);
  const hmod = rest.match(/^\s*(handlers::([a-z_]+)::[a-z0-9_]+)/);
  routes.push({
    path: m[1],
    method: m[2],
    module: hmod ? hmod[2] : "(inline)",
    handler: hmod ? hmod[1] : "(inline)",
    cmd: m[1].replace(/^\//, "").replace(/\{.*/, ""),
  });
}

// ── 2. Commands registered in our Go code ──────────────────────────────────
function scanGo(dir) {
  const out = new Set();
  if (!fs.existsSync(dir)) return out;
  const walk = (d) => {
    for (const e of fs.readdirSync(d, { withFileTypes: true })) {
      const p = path.join(d, e.name);
      if (e.isDirectory()) walk(p);
      else if (e.name.endsWith(".go")) {
        const text = fs.readFileSync(p, "utf8");
        for (const mm of text.matchAll(/Register(?:Public)?\(\s*"([a-z0-9_/]+)"/g)) {
          out.add(mm[1]);
        }
        // Also commands registered through a helper that takes the command
        // name as a string arg, e.g. registerWorkTaskTransition(reg, "cmd", ...).
        for (const mm of text.matchAll(/register[A-Za-z]*\(\s*reg,\s*"([a-z0-9_]+)"/g)) {
          out.add(mm[1]);
        }
      }
    }
  };
  walk(dir);
  return out;
}
const implemented = scanGo(path.join(ROOT, "src-go"));

// ── 3. Commands mcode-app actually calls ───────────────────────────────────
function scanMcode() {
  const tokens = new Set();
  if (!fs.existsSync(MCODE_SRC)) {
    console.warn("mcode-app not present at " + MCODE_SRC + " — P0 set will be empty");
    return tokens;
  }
  // Pure-JS walk: shelling out to grep is unreliable here (the mcode-app entry
  // is a symlink and the sandboxed shell intermittently returns EBUSY).
  const tokenRe = /['"]([a-z][a-z0-9_]{3,})['"]/g;
  const walk = (d) => {
    let entries;
    try {
      entries = fs.readdirSync(d, { withFileTypes: true });
    } catch {
      return;
    }
    for (const e of entries) {
      if (e.name === "node_modules" || e.name === "dist") continue;
      const p = path.join(d, e.name);
      if (e.isDirectory()) {
        walk(p);
      } else if (/\.(ts|vue)$/.test(e.name)) {
        let text;
        try {
          text = fs.readFileSync(p, "utf8");
        } catch {
          continue;
        }
        for (const mm of text.matchAll(tokenRe)) tokens.add(mm[1]);
      }
    }
  };
  walk(MCODE_SRC);
  return tokens;
}
const mcodeTokens = scanMcode();
const routeCmds = new Set(routes.map((r) => r.cmd));
const p0 = new Set([...mcodeTokens].filter((c) => routeCmds.has(c)));

// ── 4. Emit markdown ───────────────────────────────────────────────────────
const byModule = {};
for (const r of routes) (byModule[r.module] = byModule[r.module] || []).push(r);

const lines = [
  "# 上游路由全量清单 (Route Inventory)",
  "",
  `自动解析自 \`_upstream/ref/src-tauri/src/web/router.rs\`，共 **${routes.length}** 条路由、${Object.keys(byModule).length} 个模块。`,
  "",
  "> 注：个别路由名里含上游品牌词（如 `get_codeg_mcp_service_status`），那是**协议契约的字面量**，",
  "> 客户端按原样调用，不能改名。",
  "",
  "图例：",
  "",
  "- `[x]` 已注册 · `[ ]` 未实现",
  `- **[P0]** mcode-app 实际调用的路由 —— 无缝迁移的硬门槛，共 **${p0.size}** 条`,
  "- 无标记 = 桌面端/其他端使用，mcode-app 不调用，可后置",
  "",
  "> 重新生成：`node scripts/gen_inventory.js`（跟进上游新功能后重跑即可）",
  "",
];

const summary = [];
for (const mod of Object.keys(byModule).sort()) {
  const list = byModule[mod];
  const done = list.filter((r) => implemented.has(r.cmd)).length;
  const p0Count = list.filter((r) => p0.has(r.cmd)).length;
  const p0Done = list.filter((r) => p0.has(r.cmd) && implemented.has(r.cmd)).length;
  summary.push({ mod, done, total: list.length, p0Count, p0Done });

  lines.push(
    `## ${mod} — ${done}/${list.length}` +
      (p0Count ? ` · P0 ${p0Done}/${p0Count}` : "") +
      "",
    ""
  );
  for (const r of list) {
    const mark = implemented.has(r.cmd) ? "x" : " ";
    const tag = p0.has(r.cmd) ? " **[P0]**" : "";
    const method = r.method === "post" ? "" : ` \`${r.method}\``;
    lines.push(`- [${mark}] \`/${r.cmd}\`${method}${tag}`);
  }
  lines.push("");
}

// Roll-up table at the top for at-a-glance status.
const totalDone = routes.filter((r) => implemented.has(r.cmd)).length;
const p0Done = routes.filter((r) => p0.has(r.cmd) && implemented.has(r.cmd)).length;
lines.push("## 汇总", "");
lines.push(`- 全部路由：${totalDone}/${routes.length}`);
lines.push(`- P0（mcode-app 依赖）：${p0Done}/${p0.size}`);
lines.push("");
lines.push("| 模块 | 已实现 | P0 进度 |");
lines.push("|---|---|---|");
for (const s of summary) {
  lines.push(
    `| ${s.mod} | ${s.done}/${s.total} | ${s.p0Count ? `${s.p0Done}/${s.p0Count}` : "—"} |`
  );
}
lines.push("");

fs.writeFileSync(OUT, lines.join("\n"));
console.log(
  `wrote ${path.relative(ROOT, OUT)}: ${routes.length} routes, ` +
    `${totalDone} implemented, P0 ${p0Done}/${p0.size}`
);
