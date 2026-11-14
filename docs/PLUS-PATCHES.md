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
| （MCP commit） | `codeg-mcp` → `yoyocode-mcp`：二进制/注入的 MCP server 名/工具名匹配/前端解析 | `src-tauri/`（companion、注入、mention 解析、测试夹具）、`src/` 前端 65 文件（normalization 表、卡片、settings、i18n×10） | 永不 —— fork 品牌就是需要 | 前端 normalizeToolName 的后缀规则 server 名无关，旧会话的 `mcp__codeg-mcp__*` 天然兼容；上游动这些文件时需手动合并 |
| （体验 commit） | 「关于」设置页 + Web 服务移动端区块 | `settings-shell.tsx`（导航）、`about-settings.tsx`、`app/settings/about/`、`web-service-settings.tsx`、i18n×10 | 永不 —— fork 品牌就是需要 | 关于页含对上游 Codeg 的致谢与仓库/文档链接；Web 服务页新增移动端远程管理入口（getmcode.lingyun.net）；上游动这些文件时需手动合并 |

### 队列扣留、端口迁移与品牌图标（v0.44.0-plus.1）

- `f0645f2cc`：手动停止回合时扣住消息队列。取消来源按 `by_user` 归因（自动化 / 工作任务 / CLI 运行不触发）；`message_queue_state` 新增 `hold` 列承载扣留状态，`can_drain` 增加 `hold IS NULL`；新增 `message_queue_release_hold` 与 `message_queue_send_now` 两个命令（Tauri + HTTP + router）；扣留期间输入框直发绕过队列尾随，下一回合成功完成后自动解除。冲突面：`db/migration/mod.rs`、`db/service/message_queue_control_service.rs`、`acp/{manager,connection,session_state,message_queue_drain}.rs`、`commands/message_queue.rs`、`web/{router,handlers/message_queue}.rs`、`chat/message-queue-display.tsx`、`conversation-detail-panel.tsx`、i18n×10。
- `fbec51004`：Web 服务默认端口 3080/18080 → **18989**；MCode 连接配置码携带宿主机主机名。冲突面：`Dockerfile*`、`docker-compose.yml`、`install.{sh,ps1}`、`bin/codeg_server.rs`、`cli/args.rs`、`web/mod.rs`（上游同期新增绑定地址选择器，需与其 `bind_host` 字段缝合）、`web-service-settings.tsx`、`mcode-config-code.ts`、README×10、docs。
- `e371b4874`：`feedback.enabled` 默认改为开启，与 `question.enabled` 一致。
- `297863f12`：续跑回合被受理（`resuming`）后隐藏中断横幅；`resume_requested` 期间保留，因为此时禁用按钮是点击的唯一反馈。
- `79a600219`：定时发送并入发送按钮右侧下拉，空闲时右侧分体默认隐藏、悬停浮现（触屏常显）；无 steer 通道的回合补上排队发送按钮。冲突面：`message-input.tsx`。
- `be3004562`：章鱼图标替换悠悠球标记，覆盖全部平台资源；轮廓由源图追踪生成（轮廓 IoU 0.993，颜色平均误差 <1/255）。删除无引用的 `app-icon.tsx`。定时发送弹窗改用轻遮罩，`DialogContent` 新增可选 `overlayClassName`，其余弹窗不受影响。冲突面：`src-tauri/icons/`（上游改动时整体冲突，重跑追踪脚本重新生成即可）、`public/icon*`、`ui/dialog.tsx`。

### Web 公网访问与 MCode 配置（v0.42.0-plus.4）

- `5f70cc35c`：设置 → Web 服务集成 Tailscale Funnel。主机 CLI 检测、登录/授权、配置确认、启停恢复及自有映射清理；成功后公网 HTTPS 地址优先显示。
- `746d04dfe`：一键衔接 Web 启动、Tailscale 连接/登录和 Funnel 发布；区分设备审批、DNS 与服务未启动，取消/停止阻止后续步骤。新增 MCode v2 配置二维码，跟随当前地址并携带运行中访问令牌，供手机扫码或粘贴导入。
- 冲突面：`web/tailscale_funnel*`、`web/mod.rs` 状态共享及启停、Tauri/HTTP 注册、Web 服务设置组件、`api.ts`、国际化文件。上游同步需保留身份/映射归属、取消和地址选择逻辑。
- 验证和真实网络限制见 [tailscale-funnel-verification.md](tailscale-funnel-verification.md)，使用方式见 [tailscale-funnel.md](tailscale-funnel.md)。可在上游具备同等固定公网入口和 MCode 协议支持后评估移除。

### 会话模型切换与漫画工作台（v0.42.0-plus.4）

- `2d6f8b05a`：持久化并展示会话内模型切换；冲突面为会话事件、数据模型和前端展示，上游提供同等能力后评估合并替代。
- `6d1a591e8`：漫画工作台创作流程改进；冲突面为 `comic/` 后端与对应前端工作台，上游覆盖相同流程后评估替代。
- `86671550e`：桌面运行时窗口与托盘显示名统一，详见下节。

### 桌面窗口与托盘显示名（2026-11-10）

- 主窗口、远程工作台窗口标题、托盘悬停提示和 10 种语言的退出菜单统一使用 `YoYoCode`；保留连接名和现有菜单行为。
- 冲突面：`src-tauri/src/lib.rs`、`commands/windows.rs`、`commands/remote_workspace.rs`。这些运行时硬编码不受产品身份构建期注入覆盖，上游同步时需保留品牌差异。

### 截图窗口自动选区（2026-11-08）

- Windows 原生枚举可见顶层窗口并读取 DWM 边界，在冻结屏幕时生成按叠放顺序排列的候选选区；隐藏、最小化、cloaked、桌面与任务栏窗口被排除。
- 选区覆盖窗在尚未选定时预览悬停窗口，单击选整窗，拖拽仍为自由框选；选定后保留移动、缩放、标注、钉图、保存和复制行为。
- 冲突面：`commands/screenshot.rs` 的帧元数据与 Windows 子模块、`Cargo.toml` 的 Windows DWM feature、`lib/screenshot/` 与截图覆盖窗。该功能只支持 Windows，不放宽 macOS 捕获 API 门禁。

### macOS 截图 helper 通道（2026-11-09）

- helper 协议升级到 15，新增用户截图的 Begin/Chunk 消息；使用独立、按现有规则鉴权的短生命周期连接，不要求 Computer Use 开关或 driver 下载，也不新增 agent MCP 工具。
- 原生捕获实现仅在 macOS + computer-helper 下编译：macOS 14+ 动态加载 ScreenCaptureKit，旧系统使用 CoreGraphics 兼容路径。主程序只收图像/几何数据，保留发布脚本的捕获符号门禁。
- 显示器逻辑点和图像像素分别记录；悬停候选按实际像素换算，钉图按逻辑点定位。WebKit 复制在点击时同步发起，异步合成由 ClipboardItem 的 Promise 承接。
- 冲突面：`computer/protocol.rs`、`local.rs`、helper 分派/新原生模块、截图命令与权限入口、`copy-image.ts` 和截图覆盖窗。Mac 实机验收前不得宣称原生功能验证完成，见 `screenshot-macos-validation.md`。

### 待发送队列后端自治（2026-11-06）

- `acp/message_queue_drain.rs` + `message_queue_service.rs`：Rust 完全控制持久化队列的认领与发送，startup/30s scan + save/turn-status 触发；无页面/无连接时自行续会话建连，不再依赖前端 effect。
- 新 migration `m20261106_000001_message_queue_delivery`，原子 claim/发送确认、陈旧保存防复活；sending 交付不确定时保留 uncertain 并要求确认后 retry，不宣称 exactly-once。
- desktop/web 与 mcode-app 用 `message_queue_capabilities` fail-closed 门控；失败显示和确认重试，保留旧宿主兼容。
- 冲突面：ACP manager/lifecycle、双 runtime bootstrap、队列 handlers/types、前端 queue hook/panel/display。详细协议与原生复制见 [backend-message-queue-drain.md](backend-message-queue-drain.md)。

- 会话执行错误暂停/原上下文继续：`message_queue_state/attempt` 持久化恢复gate，实际轮次success才释放后续，UI空队列也有固定继续提示，CAS防多端重复点击。见 [message-queue-error-recovery.md](message-queue-error-recovery.md)。

- 一次性定时发送：`scheduled_at` 列 + `message_queue_enqueue/set_schedule/remove` 原子 patch API + capabilities `scheduled_send`；claim 头跳过未到期项（未来定时不挡普通消息），drainer 按最近 deadline 唤醒；旧 full-save 不触碰时间、漏列不删定时行、显式删除走 REMOVED 墓碑。desktop/Web/App 弹窗（30 分钟/2 小时预设）与行内转换。

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
