# PLUS 本地变更清单与上游同步

PLUS 指本仓库相对上游 [spacering-net/codeg](https://github.com/spacering-net/codeg)
的全部自有改动。本文档回答两个问题：上游更新了怎么跟进；本地改了什么、为什么存在。

没有这份清单，半年后没人敢动任何一条补丁。

## 同步机制：merge 流

- `main` = `upstream/main` + 本地 commit（含合并节点），不另设集成分支
- `plugins` 为封存分支，不参与日常同步；将来启用时 `git merge main`（其 40 个
  commit 落后上游较多，也可按需改走 rebase）
- 同步一条命令：`node scripts/sync-upstream.mjs`（fetch → merge → 冲突指引）
- `rerere.enabled = true`：相同冲突自动复用历史解法
- `remote.pushDefault = origin`：fetch/pull 面向上游，push 面向 fork（ijry/YoYoCode）

### 方案取舍

| 方案 | 适合 | 不适合 |
| --- | --- | --- |
| rebase | 本地 ≤ ~10 个小 commit | commit 多了每次逐个解冲突、SHA 全改写、必须 force push |
| **merge（采用）** | 本地功能成体系、长期演进 | 历史带合并节点（可接受） |
| patch 文件 | 无 git 历史的构建环境（如 Docker 构建期注入品牌） | 冲突无三方上下文、一断全断，只作补充不当主力 |

### 降低冲突面的规则（比 git 机制更重要）

1. **增量式落位**：自有功能放新文件/新组件，与上游的接线点压缩到一行
   import/注册，不改上游函数内部。
2. **品牌字段构建期注入**：包名、软件名、图标、identifier、updater 这类高频
   冲突字段不直接 patch 文件内容，改为构建期注入，见
   `scripts/apply-plus-identity.mjs` 与下文「PLUS 产品身份」。
3. **每个本地 commit 登记到下表**：同步冲突时逐条核对"这个差异还要不要"。

## 补丁台账

| commit | 主题 | 冲突面 | 何时可删 | 说明 |
| --- | --- | --- | --- | --- |
| `c937df5f7` | 上游同步机制 + 本文档 | `.gitignore`、`docs/`、`scripts/` | 永不 | 基础设施，预期不冲突 |
| （身份注入 commit） | PLUS 产品身份构建期注入 | 仅新增 `scripts/` 文件 | 永不 —— fork 存在就需要 | 见下文 |
| 本 commit | CI 发布流水线接入身份注入 | `.github/workflows/release.yml`（上游文件，上游改动时需手动合并，低频） | 永不 —— fork 发布就需要 | 见下文「CI 发布」 |
| （品牌 commit） | 品牌 → YoYoCode：logo / 显示名 / README | `public/icon*.svg`、`src-tauri/icons/`、`src/app/favicon.ico`、`README.md`、`docs/readme/`×9 | 永不 —— fork 品牌就是需要 | 赞助段已删；显示名与仓库 slug 统一为 YoYoCode（原 Go 项目让出名字后改名 GoCode）；上游动这些文件时需手动合并 |
| （CLI/端口 commit） | 命令行改 `yoyo-cli` + Web 端口默认 18080 | `Cargo.toml`、`src/bin/yoyo_cli.rs`、`cli_run/`（shim/command_tool/hello 等）、`bin/codeg_server.rs`、`tauri.conf.json`、`prepare-sidecars.mjs`、`release.yml`、install 脚本、`docs/cli.md`、README×10 | 永不 —— fork 品牌就是需要 | 握手域串 `codeg-cli-hello` 保留（CLI↔运行时协议，两端同版才可改）；DB `source=cli` 与 `CODEG_CLI` env 名不变；上游动这些文件时需手动合并 |

### CI 发布

- **tag 规则（强制）**：`v<app version>-plus.N`，如 `v0.41.3-plus.1`。不带
  `-plus.N` 后缀的 tag 会在 CI 直接失败 —— 那意味着产物要带着官方身份出仓库。
  `-plus.N` 传给身份注入脚本；`-plus` 前缀部分必须精确等于 `tauri.conf.json`
  的 `version`（safe 模式下与上游一致）。
- **产物**：Windows NSIS 安装包 + Linux deb/appimage + macOS app（ad-hoc）+
  server 二进制 + updater 工件与 `latest.json`（PLUS 私钥签名）+
  `ghcr.io/ijry/YoYoCode:<version>` 镜像。不推 Docker Hub（无 secret）。
- **macOS 走 ad-hoc**：无 Developer ID 证书 —— 上游的信任锚编译 / 证书导入 /
  helper 重签 / hardened-runtime 校验步骤以 `if: false` 门禁（原文保留，有证书
  后恢复），helper 由 prepare-sidecars 以 `codesign --sign -` 密封并以
  development 模式运行（`computer/helper/mod.rs` 的 `PEER_REQUIREMENT` 分支，
  功能可用、无 peer 校验）。主程序靠身份注入设置
  `bundle.macOS.signingIdentity = "-"` 让 bundler ad-hoc 密封 —— plus.3 实测：
  不设的话 bundler 完全不签名，`codesign --verify` 直接失败。server 二进制不
  发布 macOS 版：未签名二进制每次更新丢 TCC 授权（上游 #1035），macOS 用户走
  桌面版。

## PLUS 产品身份（release-identity）

**新增** `scripts/apply-plus-identity.mjs` —— 构建期改写产品身份，**不提交字段
改动本身**：

| 字段 | 上游 | PLUS |
| --- | --- | --- |
| `productName` | `codeg` | `YoYoCode` |
| `identifier` | `app.codeg` | `net.lingyun.yoyocode` |
| `plugins.updater.endpoints` | 上游 releases | PLUS releases 的 `latest.json` |
| `plugins.updater.pubkey` | 上游的 minisign 公钥 | PLUS 自己的 minisign 公钥 |
| `bundle.createUpdaterArtifacts` | `true` | `true`（用 PLUS 私钥签名） |
| `bundle.macOS.signingIdentity` | 未设置（上游走 env） | `"-"`（ad-hoc） |
| `version` | `x.y.z` | 默认不变（见「版本模式」） |

为什么用构建期注入而不是 patch 提交：`version` 上游每次发版都会动，一旦在分支里
patch 它，每次同步必冲突且 rerere 帮不上（值每次都不同）。注入之后 git 历史里这些
字段和上游逐位相同 —— 永久冲突面归零。

### 为什么必须换 identifier

Tauri 的 `app_data_dir()` 直接从 `identifier` 推导，换掉它就等于换掉整个数据目录：
`…/net.lingyun.yoyocode/` vs `…/app.codeg/`。

因此 `db/mod.rs` 的 `database_file_name()` 不需要改 —— 两边各自的 `codeg.db` 天然
隔离。这也顺带避开了 `commands/backup/` 里归档路径硬编码 `db/codeg.db` 而落地名走
`database_file_name()` 的不对称。

### 为什么必须换掉 updater 的源与公钥

不换的话，PLUS 装到用户机器上会自动更新成官方版：plus 功能全丢，且 PLUS 写入的库
官方版读不了（`backup/core.rs` 的 `evaluate_compat` 会因未知 migration 拒绝）。
沿用上游公钥也不行 —— 我们没有对应私钥，签不出能通过验证的更新包。

现在源指向 `ijry/YoYoCode` 的 release（`latest.json`），公钥换成 PLUS 自己的
minisign 密钥对：

- 私钥：`D:\Repos\xyito\config\codeg-plus\codeg-plus.key`（base64 单行，口令为空），
  同时配在 fork 的 `TAURI_SIGNING_PRIVATE_KEY` / `TAURI_SIGNING_PRIVATE_KEY_PASSWORD`
  两个 secret 上
- 8 月的旧密钥已归档为 `codeg-plus.key.legacy-2D14EC03`（公钥从未发布过，直接
  轮换无损失）
- 私钥丢失 = updater 报废；换钥必须同步改 `scripts/apply-plus-identity.mjs` 并发新版

### 版本模式

- **`safe`（默认）**：`tauri.conf.json` 的 `version` 保持纯三段 `x.y.z`。
  用户看到的 `x.y.z-plus.N` 由 git tag、GitHub release 标题与产物文件名承载；
  安装后靠 `productName` = `YoYoCode` 区分。
- **`semver`**：把 `version` 写成 `x.y.z-plus.N`。

默认走 `safe` 的原因：Windows MSI 的 ProductVersion 不接受 semver 的 prerelease
段，而上游从未发过任何 prerelease tag（`release.yml` 的 channel 判定只认 `-rc`），
所以这条流水线上没有先例可依。在 Windows 上实测通过后可把默认切成 `semver`。

### 禁止修改（跨版本互操作契约）

以下三处一旦在 PLUS 侧改动，官方版与 PLUS 之间的备份导入导出就会失效：

- `commands/backup/manifest.rs` 的 `BACKUP_KIND`、`BACKUP_FORMAT_VERSION`
- 归档内的 `db/codeg.db` 路径（`backup/core.rs` / `restore.rs` 必须一致）
- `core.rs` 的 `is_excluded_upload()` 里 `.codeg` 前缀判定 —— 改前缀会让 restore
  的安全快照被打进备份，套娃

### 数据迁移：官方版 → PLUS

manifest 里没有任何 identifier / app 身份字段，兼容门禁只有两条
（`commands/backup/core.rs` 的 `evaluate_compat`）：

- `format_version > BACKUP_FORMAT_VERSION || kind != BACKUP_KIND` → 拒
- `!known_migration(&manifest.latest_migration)` → 拒（"备份比本程序新"）

判据是"这个 migration 名我认不认识"，不是版本比大小。于是：

| 方向 | 结果 |
| --- | --- |
| 官方 → PLUS | ✅ 通。PLUS 建立在上游之上，migration 是超集 |
| PLUS → 官方 | ❌ 干净拒绝（"备份比本程序新"），不会写坏数据 |

单向可导，符合数据隔离的预期。

## 向上游贡献（参考 PR #1062 的路径）

1. `git switch -c feat/xxx upstream/main`——从纯上游起点开分支，绝不能基于 main
   （否则 PR 会把其他本地补丁一起拖进去）
2. `git cherry-pick <main 上的本地 commit>`（或按 PR 范围重写）
3. push 到自己的 fork、向上游发 PR；上游合并后该内容与本地一致，merge 不再
   冲突，在上表把它标记为"已上游"
