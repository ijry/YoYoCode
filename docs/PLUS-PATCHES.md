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
| （CLI/端口 commit） | 命令行改 `yoyo-cli` + Web 端口默认 18080 | `Cargo.toml`、`src/bin/yoyo_cli.rs`、`cli_run/`（shim/command_tool/hello 等）、`bin/codeg_server.rs`、`tauri.conf.json`、`prepare-sidecars.mjs`、`release.yml`、install 脚本、`Dockerfile`、`src-tauri/windows/installer-hooks.nsh`、`src/components/settings/cli-access-settings.tsx`（示例命令 + 测试）、`src/i18n/messages/`×10（cliAccess* 文案）、`docs/cli.md`、README×10 | 永不 —— fork 品牌就是需要 | 握手域串 `codeg-cli-hello` 保留（CLI↔运行时协议，两端同版才可改）；DB `source=cli` 与 `CODEG_CLI` env 名不变；2026-11-15 补漏：前端硬编码文案/示例命令、Dockerfile 的 `--bin`/`COPY`、NSIS 的 exe 名曾仍写 `codeg-cli` / `codeg-mcp.exe`，已统一为 `yoyo-cli` / `yoyocode-mcp`；上游动这些文件时需手动合并 |
| （MCP commit） | `codeg-mcp` → `yoyocode-mcp`：二进制/注入的 MCP server 名/工具名匹配/前端解析 | `src-tauri/`（companion、注入、mention 解析、测试夹具）、`src/` 前端 65 文件（normalization 表、卡片、settings、i18n×10） | 永不 —— fork 品牌就是需要 | 前端 normalizeToolName 的后缀规则 server 名无关，旧会话的 `mcp__codeg-mcp__*` 天然兼容；上游动这些文件时需手动合并 |
| （体验 commit） | 「关于」设置页 + Web 服务移动端区块 | `settings-shell.tsx`（导航）、`about-settings.tsx`、`about-logo-animation.tsx`、`app/settings/about/`、`web-service-settings.tsx`、i18n×10、README×11、`public/logo/animation/` | 永不 —— fork 品牌就是需要 | 关于页含对上游 Codeg 的致谢与仓库/文档链接；Web 服务页新增移动端远程管理入口（getmcode.lingyun.net）；2026-11-15 补：关于页把「本仓库 / App 端连接 / 上游 Codeg」拆成三组链接，顶部换成动态 Lottie 水母徽标，README×11 里仍指向上游文档的 2 处移动端链接统一为 `getmcode.lingyun.net` 并在顶部加 App 入口；上游动这些文件时需手动合并 |

### 队列扣留、端口迁移与品牌图标（v0.44.0-plus.1）

- `f0645f2cc`：手动停止回合时扣住消息队列。取消来源按 `by_user` 归因（自动化 / 工作任务 / CLI 运行不触发）；`message_queue_state` 新增 `hold` 列承载扣留状态，`can_drain` 增加 `hold IS NULL`；新增 `message_queue_release_hold` 与 `message_queue_send_now` 两个命令（Tauri + HTTP + router）；扣留期间输入框直发绕过队列尾随，下一回合成功完成后自动解除。冲突面：`db/migration/mod.rs`、`db/service/message_queue_control_service.rs`、`acp/{manager,connection,session_state,message_queue_drain}.rs`、`commands/message_queue.rs`、`web/{router,handlers/message_queue}.rs`、`chat/message-queue-display.tsx`、`conversation-detail-panel.tsx`、i18n×10。
- `fbec51004`：Web 服务默认端口 3080/18080 → **18989**；MCode 连接配置码携带宿主机主机名。冲突面：`Dockerfile*`、`docker-compose.yml`、`install.{sh,ps1}`、`bin/codeg_server.rs`、`cli/args.rs`、`web/mod.rs`（上游同期新增绑定地址选择器，需与其 `bind_host` 字段缝合）、`web-service-settings.tsx`、`mcode-config-code.ts`、README×10、docs。
- `e371b4874`：`feedback.enabled` 默认改为开启，与 `question.enabled` 一致。
- `297863f12`：续跑回合被受理（`resuming`）后隐藏中断横幅；`resume_requested` 期间保留，因为此时禁用按钮是点击的唯一反馈。
- 2026-11-17：`paused`（上一任务因错误中断）下的**直接发送**不再入队。此前 `handleSend` 对任何非 `open` 状态一律 `mqEnqueue`，草稿排在只有横幅按钮才能解除的闸门后面，用户不打字就没法继续；现在暂停只拦队列不拦对话，`begin_attempt` 同事务受理直发并广播 `message-queue://state` 让横幅立即消失，失败则把这条用户消息记为新的 failed attempt 重新暂停，成功则放开队列。`resume_requested`/`resuming` 仍入队（会话会拒绝第二个回合），暂停期间直发也跳过 FIFO 尾随。冲突面：`db/service/message_queue_control_service.rs`（`QueueTurnAttempt.pause_lifted`）、`acp/manager.rs`（受理后广播）、`conversation-detail-panel.tsx`。见 [message-queue-error-recovery.md](message-queue-error-recovery.md)。同批：中断横幅右上角新增手动关闭 ×，仅本地隐藏提示（按 failed attempt id 记忆，新失败会重弹），不改暂停状态也不动队列行；冲突面 `chat/message-queue-display.tsx` + i18n×10（`dismissNotice`）。
- `79a600219`：定时发送并入发送按钮右侧下拉，空闲时右侧分体默认隐藏、悬停浮现（触屏常显）；无 steer 通道的回合补上排队发送按钮。冲突面：`message-input.tsx`。
- `be3004562`：水母图标替换悠悠球标记，覆盖全部平台资源；轮廓由源图追踪生成（轮廓 IoU 0.993，颜色平均误差 <1/255）。删除无引用的 `app-icon.tsx`。定时发送弹窗改用轻遮罩，`DialogContent` 新增可选 `overlayClassName`，其余弹窗不受影响。冲突面：`src-tauri/icons/`（上游改动时整体冲突，重跑追踪脚本重新生成即可）、`public/icon*`、`ui/dialog.tsx`。 2026-11-15 补漏：该 commit 漏了 `src-tauri/icons/tray-icon-template.png`，macOS 菜单栏一直显示上游的括号/圆点 mark；现改由 `tray-icon-template.gen.py` 从 `icon.svg` 的水母轮廓派生单色 template（黑色 RGB + alpha，系统按菜单栏明暗自动染色，深色下即白色），不再保留独立的旧图形常量。改 `icon.svg` 后必须重跑该脚本，否则 Dock / favicon 与菜单栏会再次分叉。
- 2026-11-15 复核「升级后 macOS Dock 图标没变」：`icon.icns`（119194 B，sha256 `33e92720…`）在工作区、`be3004562`、`v0.44.0-plus.1` 三处逐字节一致；解包 `ic10` 确认是水母 + 白色高光 + 824/1024 安全区。`tauri.conf.json` 的 `bundle.icon` 含 `icons/icon.icns`，`apply-plus-identity.mjs` 不改写图标，macOS 更新走 `download_and_install` 整包替换，`relaunch.rs` 只负责重启、不设运行时 Dock 图标。结论：资源侧没有漏改，是 macOS IconServices / Dock 缓存，或 Dock 仍固定着改名前 `codeg.app`（构建期注入 `productName = YoYoCode`，产物是 `YoYoCode.app`）；用户侧处理见 `docs/releasing/v0.44.0-plus.1.md` 使用说明。为防以后真出现漏带，`.github/workflows/release.yml` 的 macOS ad-hoc 校验新增：把成品 `Contents/Resources/*.icns` 与 `src-tauri/icons/icon.icns` 做 `cmp`，不一致即失败，并打印 `CFBundleIconFile`。

### 发版校验与品牌资源（v0.44.0-plus.2）

- `.github/workflows/release.yml`：macOS ad-hoc 校验新增成品 `.app` 内 `Contents/Resources/*.icns` 与 `src-tauri/icons/icon.icns` 的逐字节比对，防止「升级后 Dock 仍是旧图标」这类漏带在 CI 里静默通过。
- `src-tauri/icons/tray-icon-template.{gen.py,png}`：菜单栏 template 改由 `icon.svg` 派生水母单色蒙版，补齐 `be3004562` 漏掉的资源；改 `icon.svg` 后需重跑该脚本。
- `public/logo/`、`scripts/jellyfish-*.gen.py`、`public/logo/animation/lottie-web-5.12.2.min.js`：高清静态 logo、两套透明呼吸动画与可复现生成脚本入库，均为 fork 自有资产，上游同步时不产生冲突面。2026-11-15 起 Lottie runtime 从 `scripts/` 移到 `public/logo/animation/`，由「关于」页按需加载（不进应用 bundle），生成自包含预览页与页面运行时共用同一份文件；`jellyfish-lottie.gen.py` 的 `LOTTIE_JS` 已同步改路径，重跑产物逐字节一致。
- 命令行改名残留（`yoyo-cli` / `yoyocode-mcp`）已收尾，但协议兼容项 `codeg-cli-hello`、`CODEG_CLI` 与历史会话中的旧 MCP server 名必须保留，属于上游同步时的固定冲突面。

### 自有文档站与上游文档链接替换（2026-11-15）

- 上游 `docs.codeg.app` 的源码不在 `spacering-net/official-website`（那是 spacering.net 公司官网，Astro + Cloudflare Worker + D1），而在 `xintaofei/codeg-docs`（VitePress，Pages CNAME 到 `xintaofei.github.io`）。已 fork 为 `ijry/yoyocode-docs`，品牌化为 YoYoCode 并发布到 `https://ijry.github.io/yoyocode-docs/`；该仓库为 fork 自有资产，不与上游同步产生冲突面。
- 本仓库内全部 `https://docs.codeg.app` 链接（14 文件 / 415 处匹配，含 10 个 README 徽章）统一替换为 `https://ijry.github.io/yoyocode-docs`，路径与锚点原样保留。冲突面：README×11、`src-tauri/src/acp/registry.rs`、`src-tauri/src/web/handlers/files.rs`、`about-settings.tsx`、`acp-agent-settings.test.tsx`；上游动这些文件时需手动合并。
- 「关于」页把「文档」按钮从上游分组移回本仓库分组（`UPSTREAM_DOCUMENTATION` → `DOCUMENTATION`），上游分组只留 `spacering-net/codeg` 仓库链接与致谢；10 语言 `openDocumentation` 恢复中性文案。
- 页面部署在 Pages 子路径，VitePress 需要 `base: '/yoyocode-docs/'`：`sitemap.hostname` 必须含 base（VitePress 不会二次补），raw HTML 里的根路径 **链接** 要手工加前缀、**public 图片** 必须保留 `/images/` 交给 Vite 补前缀（手写前缀会被当模块导入而构建失败）。
- 品牌替换会让英文自动标题 slug 从 `codeg` 变 `yoyocode`，中文文档用 `{#slug}` 固定旧名 → 必须同步 8 个 slug 及全部站内锚点，否则 `check-anchors.py` / `check-parity.py` 会失败。`codeg-server`、`CODEG_*`、`.codeg`、`codeg://`、`codeg-computer-helper` 等技术标识保留。

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
| `version` | `x.y.z` | `x.y.z-plus.N`（见「版本模式」） |

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

- **`semver`（默认）**：`tauri.conf.json` 的 `version` 写成 `x.y.z-plus.N`，桌面端
  编译进去的当前版本与 `latest.json` 都带迭代号，`plus.4 → plus.5` 才能被 updater
  识别为更新。
- **`safe`（逃生舱）**：`version` 保持纯三段 `x.y.z`（上游原样），`--version-mode
  safe` 启用；仅在 semver 打包真出问题时临时回退，代价是桌面端重新失去迭代号比较能力。

为什么从 safe 切到 semver（2026-11-17）：桌面端 `tauri-plugin-updater` 的默认比较是
`release.version > current_version`，而 semver 里 `0.44.0-plus.N < 0.44.0`。safe 模式
下迭代号只活在 tag 里、从不进 version，于是 latest.json 与本机版本恒为同一个纯三段号，
桌面端永远报告「已是最新」—— plus.1 到 plus.4 的桌面用户一次都没收到过更新提示。
把迭代号写进 version 后，两端同带 plus.N，排序由 semver 正确处理（`update/version.rs`
的 `plus_iterations_are_ordered_numerically` 测试覆盖）。

原先把 semver 留作可选、默认 safe 的理由是 Windows MSI 的 ProductVersion 不接受
prerelease 段；但 MSI 早因 tauri#14681 在 `release.yml` 跳过（Windows 只打 NSIS +
updater），这条顾虑随之消失。

**存量用户的一次性断裂**：plus.1 ~ plus.4 装到机器上的都是纯三段 `0.44.0`。按 semver，
新版 `0.44.0-plus.N` 反而比它「小」，这些用户仍收不到自动更新，需手动装一次首个
semver 版（从那一版起后续迭代恢复自动检测）。与 plus.1 / plus.2 因 server 改名需
手动重装是同类的一次性迁移成本。

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

### server 二进制与 Release 资产全量改名（2026-11-15）

- 承接「连二进制一起改名」：`codeg-server` 全量统一为 `yoyocode-server`。Cargo `[[bin]]` 名与 `src/bin/codeg_server.rs` → `yoyocode_server.rs`、压缩包内可执行文件名、Release 资产名、安装目录名、Docker / PATH 命令名、更新器查找名、i18n 文案、文档与测试夹具全部同步；下划线形式 `codeg_server` 的注释引用一并更新。
- `.github/workflows/release.yml` 删除 matrix `legacy_artifact` 与双份复制 / 签名 / 上传循环，每个平台只产一套 `yoyocode-server-*` 资产；`install.sh` / `install.ps1` 删除「新名 404 回退旧名」逻辑；`scripts/server-release.test.mjs`、`scripts/server-install-download.test.mjs` 断言同步改为单资产 / 不重试。
- **保留**：`CODEG_*` 环境变量、`CODEG_STATIC_DIR`、`.codeg` 数据目录、`codeg://` URL scheme、桌面端安装包文件名前缀等协议与兼容标识；`.workbuddy/memory/`、`.zcode/plans/`、`docs/releasing/v*.md`、`docs/model-switch-history.md` 等历史归档保留原文，避免篡改历史记录。
- **升级风险**：plus.1 / plus.2 已发布的内置更新器会在新包内查找旧名 `codeg-server`，从这些版本直接走「软件更新」到下一版会失败，需手动重装或由发版时额外提供兼容副本。旧 Release 中已存在的 `codeg-server-*` 资产是否删除、是否发 plus.3 另行决定。
- 上游同步冲突面进一步扩大：Cargo.toml、release.yml、两份 install 脚本、Dockerfile*、AGENTS.md / CLAUDE.md、README×11、docs/cli.md 等均属 fork 自有改动，上游动这些文件时需手动合并。

### 发版 v0.44.0-plus.3（2026-11-15）

- 第三个 PLUS 迭代，覆盖自 `v0.44.0-plus.2` 以来的 10 个提交。Release 说明见 `docs/releasing/v0.44.0-plus.3.md`；`create-draft-release` 直接取 tag 指向 commit 的 message 当 Release body，所以 `chore(release)` 提交正文必须承载完整说明。
- 本版把前面几节的改动正式交付：关于页三分组与动态 Lottie 水母徽标、自有文档站与 415 处链接替换、文档站 MCode 自有化，以及 `codeg-server` → `yoyocode-server` 全量改名与单套 Release 资产。
- **升级断裂（已知限制，已写入 Release 说明）**：plus.1 / plus.2 的内置更新器会在下载包内查找旧名 `codeg-server`，本版包内只有新名，从旧版直接走应用内更新会失败，需手动重装；本版之后恢复正常。
- 已发布 Release 里的旧 `codeg-server-*` 资产本版未删除，保留历史安装兼容；是否清理另行决定。
- 版本仍为 safe 模式：`tauri.conf.json` / `package.json` 保持 `0.44.0`，迭代号由 tag 与 Release 标题承载，server Cargo 版本由 `--iteration 3` 注入为 `0.44.0-plus.3`。

### ZCode 供应商切换（v0.44.0-plus.4）

- `src-tauri/src/commands/zcode_provider.rs`（fork 自有新文件）：`acp_zcode_list_providers` 双模式命令，镜像上游 zcode-acp 桥接的供应商扫描（`~/.zcode/v2/config.json` 供应商表 + `acp-accounts.json` 叠加 + `providerSelectable` 可用性判定），只出 id / 名称 / 可用性、永不携带密钥；`ZCODE_HOME`（agent env 优先）跟随。接线：`commands/mod.rs`、`lib.rs` invoke_handler、`web/handlers/acp.rs`、`web/router.rs`。
- `commands/acp.rs`：`agent_env_keys` 为 ZCode 加显式条目（占位符 `ZCODE_ACP_BASE_URL` / `ZCODE_ACP_API_KEY` 特意避开 `ZCODE_BASE_URL` —— 它是 app-server 的 service origin，桥接器会剥离继承值；模型槽 `ZCODE_MODEL` 是真实变量）；`keeps_own_providers` 收编 ZCode，绑定模型供应商直接拒绝。
- 前端：`acp-agent-settings.tsx` 的 ZCode 面板新增供应商下拉（写 `env_json` 的 `ZCODE_PROVIDER`，与 env 文本框同一 Save 提交；手输自定义 id 透传，未启用项带提示）；`lib/{api,types}.ts`、i18n×10、`acp-agent-settings.zcode.test.tsx`（4 用例）。
- 上游跟进：供应商元数据 / 会话级切换 / 账号套餐可见性已提 william0wang/zcode-acp#400；上游落地后可把「下拉 + 新会话生效」升级为会话内切换。上游动 `commands/{acp,mod}.rs`、`lib.rs`、`web/{router,handlers/acp}.rs`、`acp-agent-settings.tsx`、i18n 时需手动合并。
