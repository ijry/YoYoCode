# YoYoCode 复刻计划 (Replication Plan)

- 上游：本地克隆见 `_upstream/ref`（基线 v0.34.0，2026-10 抓取）
- 原生 UI：https://github.com/egoist/mygo （见 `_upstream/mygo`）
- 迁移目标：mcode-app（uni-app 客户端，仓库根 `mcode-app` 符号链接）

本文档是复刻的**总契约**。
逐条勾选见 [ROUTE_INVENTORY.md](./ROUTE_INVENTORY.md)（全量 519 条路由），
阶段进度见 [PROGRESS.md](./PROGRESS.md)。

---

## 0. 目标与约束

1. **功能复刻**：除「模型供应商（model providers）」外完整复刻上游功能。
2. **接口 100% 兼容**：HTTP / WebSocket 与上游逐字段一致，mcode-app **零改动**直连。
3. **结构对齐上游**：目录一一对应，便于持续跟进上游新功能（diff 上游时路径可直接对上）。
4. **技术栈**：Go + mygo 原生 UI；HTTP 用标准库 `net/http`，WS 用 gorilla/websocket，DB 用 modernc.org/sqlite（纯 Go，无 cgo）。

### 0.1 范围排除

| 排除项 | 原因 |
|---|---|
| 模型供应商（`/model_provider*` 4 条） | 用户明确要求不复刻 |
| `browser-agent/`、`office_watch_proxy` 部分能力 | 依赖上游独立子进程，后置 |
| Tauri 插件生态 | 由 mygo 原生能力替代 |

> 注：mcode-app 会调用 `list_model_providers` 等 4 条 P0 路由。为保证"无缝迁移"，
> 这 4 条**保留空实现**（返回空列表 / no-op），不放 404，否则客户端列表页会报错。

---

## 1. 目录映射

`src-tauri` 已改名为 `src-go`，且把上游的 `src-tauri/src/*` 提升一层，使每个子目录与上游同名子目录 1:1 对应。

| 上游 | 复刻 YoYoCode | 说明 |
|---|---|---|
| `src-tauri/src/web/` | `src-go/web/` | HTTP/WS 服务器（**兼容核心**） |
| `src-tauri/src/web/handlers/` | `src-go/web/handlers/` | 每个上游 handler 文件一个 Go 文件 |
| `src-tauri/src/web/router.rs` | `src-go/web/server.go` | 路由分发 / CORS / 鉴权 |
| `src-tauri/src/web/auth.rs` | `src-go/web/auth.go` | Bearer + WS 子协议 |
| `src-tauri/src/web/ws.rs` / `ws_attach.rs` | `src-go/web/ws.go` / `ws_attach.go` | attach 协议 |
| `src-tauri/src/web/shutdown.rs` | `src-go/web/shutdown.go` | 关闭广播 |
| `src-tauri/src/web/event_bridge.rs` | `src-go/web/event_bridge.go` | 全局 firehose |
| `src-tauri/src/app_error.rs` | `src-go/app_error/error.go` | 错误信封（**兼容核心**） |
| `src-tauri/src/acp/` | `src-go/acp/` | ACP 引擎 |
| `src-tauri/src/acp/event_stream.rs` | `src-go/acp/event_stream.go` | 环形缓冲 + 广播 |
| `src-tauri/src/acp/session_state.rs` | `src-go/acp/session_state.go` | 会话快照 |
| `src-tauri/src/acp/internal_bus.rs` | `src-go/acp/internal_bus.go` | 事件总线指标 |
| `src-tauri/src/db/` | `src-go/db/` | SQLite |
| `src-tauri/src/commands/` | `src-go/commands/` | 命令实现 |
| `src-tauri/src/app_state.rs` | `src-go/app_state/` | 全局状态 |
| `src-tauri/src/bin/`（服务器二进制） | `cmd/yoyocode-server/` | 独立服务器二进制 |
| `src-tauri/src/bin/`（MCP 二进制） | `cmd/yoyocode-mcp/` | MCP 伴随进程 |
| `src/`（Next.js 前端） | `src/`（mygo 原生 UI） | 镜像上游页面树 |

**依赖方向**：`acp` **不得** import `web`（会成环，因为 web 的 attach 协议需要 acp）。
二者通过 `acp.EventSink` 接口解耦，由 `app_state` 装配。

---

## 2. 兼容性契约（不可协商部分）

这四条是 mcode-app 无缝迁移的硬门槛，任何改动都必须先跑 `go test ./src-go/...`。

### 2.1 HTTP 命令

```
POST /api/{command}
Authorization: Bearer {token}
Content-Type: application/json
```

- 未匹配路由 → **纯文本** `404 Not Found`（axum 默认，不是 JSON）
- 鉴权失败 → **纯文本** `401 Invalid or missing token`（不是 JSON）
- 空 token 配置 → 一律拒绝（fail closed）

### 2.2 错误信封 —— `AppCommandError`

**这是最容易踩的坑。** 上游 `app_error.rs` 的 `IntoResponse` 输出的是结构化对象，
**不是** `{error: "..."}`：

```json
{
  "code": "not_found",
  "message": "conversation not found",
  "detail": "…",          // 可选，未设置时不出现该键
  "i18n_key": "…",        // 可选
  "i18n_params": {…}      // 可选，空 map 时不出现
}
```

mcode-app 的 `parseAppCommandError`（`src/services/gateway/commandError.ts`）要求
`code` **和** `message` 同时为非空字符串才认作结构化错误；且恢复动作**只按 `i18n_key` 分支**
（forge 的 `noAccount` / `unsupportedHost` / `wrongForge` 三者共用 `configuration_missing`，
靠 code 无法区分）。

状态码映射（`web/handlers/error.rs::status_for_app_error_code`）：

| code | HTTP |
|---|---|
| `invalid_input` | 400 |
| `not_found` | 404 |
| `already_exists` / `turn_in_progress` | 409 |
| `permission_denied` | 403 |
| `configuration_missing` / `configuration_invalid` / `dependency_missing` / `not_a_git_repository` / `authentication_failed` | 422 |
| `network_error` / `database_error` / `io_error` / `external_command_failed` / `window_operation_failed` / `task_execution_failed` | 500 |

### 2.3 WebSocket `/ws/events`

- 鉴权：`Sec-WebSocket-Protocol: codeg-events, codeg-token.<base64url(token)>`
- 连接后**立即**发送：`{"channel":"__ready__","payload":null}`
  —— **payload 必须是 `null`**，不能是对象，否则客户端握手超时告警。
- 客户端 → 服务端：`{action: "attach"|"detach"|"ping", subscription_id?, connection_id?, since_seq?}`
- 服务端 → 客户端（serde 标签联合，**只输出当前变体的字段**）：

| type | 字段 |
|---|---|
| `snapshot` | `subscription_id`, `connection_id`, `snapshot`, `event_seq` |
| `replay` | `subscription_id`, `connection_id`, `events`, `high_water_seq` |
| `event` | `subscription_id`, `envelope` |
| `detached` | `subscription_id`, `reason`（`connection_gone` / `lagged` / `server_shutdown`） |
| `pong` | （仅 `type`） |

`event_seq` / `high_water_seq` **不能**用 `omitempty` 省略 —— 0 是有效游标，
省略会让客户端失去续传起点。

attach 决策顺序（与上游 `ws_attach::handle_attach` 逐条对齐）：

1. `since_seq` 为空 → `snapshot`
2. `since_seq >= head` → `snapshot`（防御客户端游标越界）
3. 环形缓冲覆盖且事件数 ≤ `REPLAY_BATCH_THRESHOLD(32)` → `replay`
4. 否则 → `snapshot`

关键常量：`REPLAY_BATCH_THRESHOLD=32`、`OUTBOUND_CAPACITY=64`、
`BROADCAST_CAPACITY=4096`、`RECENT_BUFFER_MAX_BYTES=128KB`、
`RECENT_BUFFER_MAX_COUNT=128`、`RECENT_EVENT_MAX_BYTES=64KB`。

### 2.4 全局 firehose

`{"channel": "...", "payload": ...}`，与 attach 协议并行存在（上游 Phase 4 才下线）。
`acp://event`、`conversation://changed` 等走这里。

---

## 3. 前期工作评估（接手时已存在的代码）

结论：**骨架可用，协议层有 3 处必须修正的偏差。**

| 项 | 状态 | 说明 |
|---|---|---|
| 模块结构 / 目录镜像 | ✅ 可用 | 已建 `web`/`db`/`acp`/`app_state`/`cmd` |
| 鉴权（Bearer + WS 子协议） | ✅ 正确 | 与 `auth.rs` 逐行对齐，含 fail-closed |
| 路由器 / CORS / 分发 | ✅ 正确 | |
| DB schema（10 张核心表） | ⚠️ 部分 | 上游 40+ 张表，待补 |
| **错误信封** | ❌ 已修正 | 原返回 `{error:"cmd: msg"}`，与 mcode-app 契约不符 → 已改为 `AppCommandError` |
| **WS attach** | ❌ 已修正 | 原 attach 只登记订阅、不回任何帧 → 已实现 snapshot/replay/event/detached 全链路 |
| **`__ready__` payload** | ❌ 已修正 | 原发 `{"ready":true}` → 改为 `null` |
| handler 覆盖 | ⚠️ 41/519 | 见 ROUTE_INVENTORY.md |
| ACP 引擎 | ⚠️ 骨架 | 事件流/会话状态已建；agent spawn 待做 |
| mygo 原生 UI | ❌ 未开始 | 依赖已验证可构建 |

### 修正过程中发现的两个真实缺陷（已修 + 已加测试）

1. **`event_seq` 被 `omitempty` 吞掉**：冷启动 attach 时 seq=0，键直接消失，
   客户端拿不到续传游标。改为按变体定制 `MarshalJSON`。
2. **detach 竞态**：订阅在 goroutine 启动**之后**才登记，客户端若把 `detach`
   紧跟 `attach` 流水线发出，会打到空 map，forwarder 永远收不到取消 →
   泄漏广播槽并持续推送已退订的事件。改为**先登记再启动** + sticky `stopped` 标志。

---

## 4. 模块清单（47 个，按优先级排序）

完整 519 条逐条清单见 [ROUTE_INVENTORY.md](./ROUTE_INVENTORY.md)。
下列为模块级视图，**P0** 表示 mcode-app 实际调用。

### 第一梯队 —— P0，mcode-app 直接依赖

| 模块 | 路由数 | P0 | 说明 |
|---|---|---|---|
| `acp` | 75 | 16 | ACP 引擎：连接/会话/权限/模型目录 |
| `git` | 52 | 13 | 分支/提交/diff/stash/worktree |
| `folders` | 38 | 6 | 项目（folder）CRUD + 文件树 |
| `work_task` | 31 | 26 | 任务看板 |
| `files` / `workspace_files` | 14 | 6 | 文件读写、上传 |
| `conversations` | 21 | 6 | 会话 CRUD |
| `forge` | 17 | 15 | GitHub/Gitea/GitLab issue/PR |
| `terminal` | 6 | 6 ✅ | 终端 spawn/write/resize/kill/list/snapshot |
| `quick_messages` | 5 | 4 | 快捷消息 |
| `system_settings` | 8 | 2 | 语言等设置 |
| `web_server` | 8 | 1 | 服务状态/配置 |
| `conversation_tags` | 8 | 0 | 会话标签 |
| `project_boot` | 5 | 0 | 项目初始化 |
| `pet` | 19 | 1 | 桌宠 |
| `model_provider` | 4 | 4 | **空实现占位**（用户要求不复刻） |

### 第二梯队 —— 桌面端功能

`automation`(11) · `backup`(11) · `canvas`(12) · `chat_channel`(22) · `computer`(18) ·
`config_sync`(12) · `custom_skills`(10) · `experts`(8) · `science`(8) · `app_update`(4) ·
`logging`(5) · `mcp`(8) · `mcp_service`(3) · `office_tools`(14) · `token_usage`(4) ·
`version_control`(12) · `folder_commands`(6) · `folder_links`(6) · `background`(5) ·
`browser_bridge`(3) · `browser_tools`(2) · `chat_authoring`(2) · `computer_tools`(4) ·
`delegation`(2) · `feedback`(3) · `question`(2) · `session_info`(2) · `event_metrics`(1) ·
`workspace_state`(3) · `import-sessions` 等

### 第三梯队 —— mygo 原生 UI（镜像上游 `src/app`）

`workspace`（主窗口）· `settings/*`（21 个子页）· `commit` / `push` / `merge` / `stash` ·
`pet` / `pet-panel` · `login` · `project-boot` · `import-sessions` ·
`computer-indicator` / `computer-marker`

---

## 5. 里程碑

| 里程碑 | 内容 | 验收标准 |
|---|---|---|
| **M0** | 协议兼容层（错误信封 + WS attach 全链路 + 环形缓冲） | `go test ./src-go/...` 全绿 |
| **M1** | DB 全量 schema + P0 handler 骨架 | mcode-app 能列出项目/会话、打开详情 |
| **M2** | ACP 引擎（spawn / stdio / JSON-RPC / 事件发布） | mcode-app 能发 prompt 并流式收到事件 |
| **M3** | git / forge / work_task / terminal 完整实现 | mcode-app 主要页面可用 |
| **M4** | 桌面端模块（automation/backup/canvas/chat_channel/…） | 功能对齐上游 |
| **M5** | mygo 原生 UI | 单二进制桌面应用 |

当前位置：**M0 已完成**，M1 进行中。

---

## 5.1 执行顺序（已锁定，自主推进）

依赖关系决定顺序：先铺"无外部依赖、线契约确定"的 P0，再建 ACP 引擎（全局骨架），
最后做依赖 ACP/worktree 的重模块与原生 UI。每批编译/vet/test 全绿 + 更新 PROGRESS/ROUTE_INVENTORY。

- [x] **B1** 纯 DB P0：conversations 变更 / quick_messages / conversation_tags / opened_tabs / model_provider（存储 CRUD）
- [x] **B2** git 读 + 分支：status/diff/show_diff/log(含 pushed)/branches/push_info/new_branch/checkout/reset …
- [x] **B3** version_control 全量：detect_git / git_settings / github_accounts / token 校验(GH/GL/Gitea) / account token 存取
- [x] **B4** files/workspace FS：create_file_tree_entry / create_folder_directory / rename / move / delete / save_file_content / read_file_for_edit / upload_* / read_workspace_file_base64（纯文件系统 + upload_jail 校验）· list_directory_with_files / list_workspace_files（含自带 gitignore 匹配器，2026-10-11）✅
- [x] **B5** 杂项设置 P0 + folder 管理：delegation / feedback / question / session_info 设置项 · get_home_directory / probe_terminal_shell_path · pet_list_active_sessions · folder 打开/别名/颜色/默认 agent · conversation_tags 已完 · list_child_conversations / create_chat_conversation / create_chat_dir
- [x] **B6** terminal（6/6 ✅ 2026-10-11）：pty spawn/write/resize/kill/list/snapshot（go-pty 统一 Windows ConPTY + unix pty，128KB scrollback + terminal://output|exit firehose）
- [x] **B7** ACP 引擎（M2，骨架级可跑）：JSON-RPC 2.0 over stdio 传输 + 单测 → agent registry（内置 claude/codex/opencode/pi…元数据）→ 进程 spawn + initialize/session 协商 → prompt → 事件发布进环形缓冲 → 权限/提问/plan-approval → session 快照/解析器 → 其余 acp_* P0（get_session_snapshot / set_config_option / update_agent_env|config / download_agent_binary / install_uv_tool / prepare_npx_agent / uninstall_agent / reorder_agents）
- [~] **B8** git 写：commit / push / pull / fetch / add_files / stash 系列已完 ✅；本地无凭据批次新增 current_user / search_authors / add_remote / remove_remote / set_remote_url / merge / rebase / delete_branch ✅（2026-10-11）；冲突工作区族已完 ✅（start_pull_merge/has_merge_head/list_conflicts/conflict_file_versions/resolve/abort/continue，2026-10-11）；commit_files/show_file_base64 已完 ✅；剩余 worktree_add / remove_worktree / delete_remote_branch / update_branch（凭据+DB）+ clone_repository
- [~] **B9** forge（15 P0）：GitHub 全量（含 PR 细节/merge）完成；Gitea/GitLab REST 待做：GitHub/Gitea/GitLab 的 issue/PR/comment（REST，复用 B3 的 token 存储）
- [x] **B10** work_task（26 P0 ✅ 2026-10-11）：全量 schema（work_task 扩列 + work_task_event / _settings / _template 表）+ 数据面 CRUD/排序/归档/事件/设置/模板 + 生命周期状态机（start/retry/requeue/schedule/return/cancel/merge/deliver_pr/complete，接 B7 ACP + B8 worktree + B9 forge）
- [~] **B11** 其余后端模块（M4）—— 已完成 folder_commands(6✅)·folder_links(6✅)·automation(11✅)·system_settings 代理/终端(5✅)·canvas(12✅)·computer_tools(4✅)·browser_tools(2✅)·app_update(4✅)·browser_bridge(3✅)·token_usage(4✅)·project_boot(5✅)·background(6✅)·mcp(8/8✅ 15/15 agent)·logging(5✅)·web_server(8✅)·workspace_state(3✅)·event_metrics(1✅)·feedback(submit✅)·mcp_service(3✅)·conversations侧栏聚合(get_sidebar_data✅ + 修正get_stats/list_folders契约)·conversations chat模式(create_chat_dir✅ create_chat_conversation✅)；剩余：automation / backup / canvas / chat_channel / computer / config_sync / custom_skills / experts / science / app_update / office_tools(_watch_proxy) / token_usage / folder_commands / folder_links / browser_bridge / browser_tools / computer_tools / chat_authoring / pet 全量 / project_boot
- [~] **B12** mygo 原生 UI（M5，骨架启动 ✅ 2026-10-11：cmd/yoyocode 进程内起后端 + mygo 原生窗口 Sidebar/Workspace，go tool mygo dev 实时预览可用；src/app 结构化：Workspace(folders+conversations 实数据)/Settings(19页二级导航,general/web-service/quick-messages 实数据)/Pet 占位 ✅ 2026-10-11；工作区核心主循环（folders→conversations→会话详情+composer 驱动 acp_connect/acp_prompt）✅ 2026-10-11，按用户定调优先；git 窗口(commit/merge/push/stash)降后；transcript JSONL 解析 + WS 事件流渲染为下一步）：workspace 主窗口 → settings/* 子页 → commit/push/merge/stash → pet/pet-panel → login → project-boot → import-sessions → computer-indicator/marker。镜像上游 `src/app` 页面树，走已实现的 HTTP/WS。

> 原则：客户端会读返回值的命令必须逐字段对齐；只被 `gateway.call<void>` 忽略的返回可宽松。
> 每批补回归测试（真实 DB / 真实 git / httptest），其中若发现上游契约与现实现冲突，以上游为准并记日志。

## 6. 工作流

### 新增一个命令

1. 在上游 `_upstream/ref/src-tauri/src/web/handlers/<mod>.rs` 找到对应 handler，
   抄清**请求字段**与**响应结构**（字段名务必逐字对齐，上游大量 camelCase / snake_case 混用）。
2. 在 `src-go/web/handlers/<mod>.go` 实现，用 `reg.Register("cmd", fn)` 注册。
3. 错误一律用 `web.Err*` 构造器（返回 `*apperror.AppCommandError`），**不要**自己拼 `{error:...}`。
4. 在 [ROUTE_INVENTORY.md](./ROUTE_INVENTORY.md) 勾选，并在 [PROGRESS.md](./PROGRESS.md) 记日志。

### 跟进上游新功能

```bash
cd _upstream/ref && git pull
node scripts/gen_inventory.js     # 重新生成路由清单，新增命令会自动出现为未勾选
```

### 校验

```bash
gofmt -l src-go cmd      # 必须为空
go vet ./...
go test ./...
```

### 契约审计（改 P0 路由前后都该跑）

```bash
python scripts/audit_contract.py
```

从上游 handler 源解析每个 P0 路由的参数结构与响应类型，和我们的 handler 实际读取的 body key
比对，列出「上游发但我们不读」（会静默丢值）与「我们读但上游不发」（多半是无害别名）。
**写一个 P0 handler 时，先去 `_upstream/ref` 读它的 `struct XxxParams` 与响应 struct** ——
尤其注意有没有 `#[serde(rename_all = "camelCase")]`；只测行为会漏掉线格式错误
（2026-10-12 就是这么发现 `get_file_tree`、`get_folder_conversation` 等一批的）。

### mygo 依赖

mygo 是**普通 Go 模块依赖**（`require github.com/egoist/mygo v0.3.5`），
哈希写在 go.sum，构建时经 git 拉取 —— 仓库**不需要**携带上游源码。

```bash
# 模块代理不通时走 git 直连：
GOPROXY=direct go mod download github.com/egoist/mygo
# 或只对该模块绕过代理（其余仍走默认）：
GOPRIVATE=github.com/egoist/mygo go build ./...
```

> 历史：曾用 `replace github.com/egoist/mygo => ./_upstream/mygo` 把源码内联进仓库，
> 起因是「模块代理拉不动」。实测 `proxy.golang.org` 确实被挡（502），
> 但 `GOPROXY=direct` 走 git 完全可用 —— 于是改回标准模块依赖，
> 仓库不再携带 22MB 上游源码，`.gitignore` 也恢复为整目录忽略 `_upstream/*`。
> `_upstream/mygo` 现在只是本地参考，可随时删除。

引入 UI 代码后需执行 `go mod tidy` 补 `require`。

---

## 7. 命名与品牌约定

本仓库**不在任何提交内容里出现上游项目的品牌名**。指代它一律用「上游」
（英文注释里用 `upstream`）。唯一说明上游是谁的地方是本地忽略目录里的
`_upstream/README.md`，它不随仓库分发。

替换时踩过的坑，记下来免得重犯：

- **注释用词分语言**：中文注释用「上游」，英文注释用 `upstream` —— 把中文词塞进
  英文句子（`上游's bottom chrome`）读不通。
- **本地命名空间**已一并改掉，不再是上游的：数据目录 `~/.yoyocode`（原为上游桌面版的
  目录，共用会互相干扰）、数据库 `yoyocode.db`、环境变量 `YOYOCODE_*`、
  二进制 `cmd/yoyocode-server` / `cmd/yoyocode-mcp`、config-sync 默认远端目录、
  出网 User-Agent、`folder_links` 写进 `.git/info/exclude` 的标记。

### 7.1 例外：这些字符串**不能**改

它们是协议契约的字面量，客户端按原样匹配；改名即断线：

| 类别 | 字面量 |
|---|---|
| WS 子协议 | `codeg-events`、`codeg-token.<base64url(token)>` |
| 路由名 | `get_codeg_mcp_service_status`、`start_codeg_mcp_service`、`set_codeg_mcp_tool_group` |
| JSON 字段 | `codegConfigExport`、`exists_in_codeg`、`linked_to_codeg` |

上表之外，仓库里不应再出现上游品牌名；上游自身的文件名、目录对应关系等细节
放在本地 `_upstream/README.md`（不随仓库分发），那里也附了复核用的 grep 命令。

> 自查方式：以「上表字面量是唯一允许项」为准，逐条确认新增/改动的文件里
> 没有别的写法出现。
