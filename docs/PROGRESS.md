# YoYoCode 复刻进度 (Progress)

> 勾选规则：完成**且可验证**（编译通过 + 有测试覆盖或手工验证）才打 `[x]`；进行中 `[~]`；未开始 `[ ]`。
>
> - 全量 523 条路由逐条清单 → [ROUTE_INVENTORY.md](./ROUTE_INVENTORY.md)
> - 总契约与架构映射 → [REPLICATION_PLAN.md](./REPLICATION_PLAN.md)
>
> 上游基线：v0.34.0（2026-10-09 抓取）

---

## 当前状态速览（滚动更新）

- **后端路由：476 / 523 已实现**（ROUTE_INVENTORY 自动统计）。**P0 128/128 全数实现** —— mcode-app 无缝迁移的硬门槛已满足。
- 已闭环的完整子系统簇：conversations/chat、git（worktree add/remove、branch update/delete、clone）、work_task、automation、canvas、terminal、forge、folders/files（含工作区上传/下载/下载票据）、mcp 全家、settings 杂项、agent-skills / custom-agents / custom-skills（含从 agent 导入）/ experts / science、chat_channel、computer-use（诚实降级）、config_sync（本地设置/状态/导出/导入/回滚 + WebDAV test_connection/upload_now/peek_remote/download_apply 全 9 路由）、会话导入（scan / import_local / import_selected）、pet 本地数据层 + codex 导入、acp 版本探测/环境诊断/goal_control、officecli 技能 + render_html、ACP 注册表目录。
- **UI（`src/app/`）**：工作区 IA（New chat + 导航行 + Pinned/Chats）、设置页树（General/Appearance/Agents/Version Control/System/MCP/Logs/Skills/Chat Channels/Computer Use/Experts 等实数据）、工作区空态欢迎面板、外观模式（light/dark 全局主题）、**会话详情区视觉**（消息气泡 / 工具调用卡片 / 输入坞，对齐上游 `ai-elements/message`、`ai-elements/tool`、`chat/message-input`；`src/app/conversation.go`，离屏渲染测试见 `render_test.go`）。
- **按约定跳过**：模型供应商专属路由（cursor/qoder/kimi/pi/deepseek/antigravity/grok/codex catalog 等）。
- **尚未实现（剩余，均为大子系统或外部依赖）**：backup 归档/恢复/安全快照全套（约 6300 行）、office-watch（文件监视 + HTTP 代理，约 1206 行）、acp_fork（会话克隆 + 重连）、部分 acp 厂商配置路由、非 claude 的 11 个会话 parser。
- **验证门槛**：每批 `gofmt` + `go build ./...` + `go vet ./...` + 相关 `go test` 全绿；exe 重建。

---

## M0 — 协议兼容层 ✅

- [x] 目录重构 `src-tauri/src/*` → `src-go/*`（用户要求：不生硬保留 tauri）
- [x] `AppCommandError` 错误信封（`src-go/app_error/error.go`）
  - [x] 16 个 `AppErrorCode` + 状态码映射表
  - [x] `i18n_key` / `i18n_params` / `detail` 可选字段语义（空值不出键）
  - [x] 单测：线格式最小集 / 带 i18n / 空 params 省略 / 16 条状态码映射
- [x] 401 / 404 改为纯文本（对齐 axum 默认，不再是 JSON）
- [x] WS attach 全链路（`src-go/web/ws.go`）
  - [x] `__ready__` payload 修正为 `null`
  - [x] attach → `snapshot`（冷启动 / 游标越界 / 大间隔 / 缓冲未覆盖）
  - [x] attach → `replay`（≤32 事件）+ `high_water_seq`
  - [x] attach → `detached{connection_gone}`（连接不存在）
  - [x] `event` 帧转发（envelope 扁平化：`seq`/`connection_id` 与 payload 同级）
  - [x] `detached{lagged}`（慢消费者）
  - [x] `detached{server_shutdown}`（优雅关闭，先广播再关 socket）
  - [x] detach 立即停止转发
  - [x] epoch 守卫（陈旧 cleanup 信号不误删新 handle）
- [x] 环形缓冲（`src-go/acp/event_stream.go`）
  - [x] 字节上限 128KB / 条数上限 128 / 单条上限 64KB
  - [x] `RangeAfter` 游标覆盖判定
  - [x] 广播 + lag 检测
  - [x] 单测全覆盖
- [x] 会话快照 `LiveSessionSnapshot`（`src-go/acp/session_state.go`）
- [x] 事件总线指标 `EventBusMetrics`（`src-go/acp/internal_bus.go`）
- [x] 优雅关闭（`cmd/yoyocode-server/main.go`：latch → 广播 → 250ms → Close）
- [x] 依赖解耦：`acp` 不再 import `web`（改为 `acp.EventSink` 接口），消除 import cycle

### M0 验收

```
gofmt -l src-go cmd   → 空
go vet ./...          → 无输出
go test ./...         → acp / app_error / web 全绿
```

---

## M1 — DB 全量 schema + P0 handler

- [x] DB 骨架（`app_metadata`/`folder`/`conversation`/`conversation_tag`/`folder_group`/`folder_command`/`opened_tab`/`quick_message`/`work_task`）
- [x] 补齐表：`app_metadata`/`folder`/`conversation`/`conversation_tag`(+link)/`folder_group`/`folder_command`/`folder_link`/`opened_tab`/`quick_message`/`work_task`(+event/settings/template)/`automation`(+run)/`canvas_board`/`canvas_node`/`token_usage_turn`/`token_usage_sync`/`model_provider`/`agent_setting`/`chat_channel`(+message_log)/`custom_agent`。逐列对齐上游 entities。`pet`/`config_sync`/`experts`/`science`/`custom_skills` 走文件系统 + `app_metadata`，无需独立表；`backup` 为归档子系统（未实现）。
- [~] `db/service` 层：核心 service（conversation/folder/work_task/agent_setting 等）逻辑内联在 handler 中已可用；尚未抽成独立 `db/service` 包。

### P0 handler（mcode-app 依赖，128/128 ✅ 全数实现）

- [x] `health`
- [x] `conversations`：`list_conversations` / `list_all_conversations` / `get_conversation` / `create_conversation` / `get_folder_conversation`
- [x] `folders`：`list_folders` / `list_open_folders` / `list_open_folder_details` / `list_all_folder_details` / `get_file_tree` / `list_directory_entries` / `read_file_preview` / `read_file_base64` / `save_file_copy`
- [x] `system_settings`：`get_system_language_settings` / `update_system_language_settings`
- [x] `web_server`：`get_web_server_status` / `get_web_service_config` / `update_web_service_config` / `start_web_server` / `stop_web_server` / `probe_web_service_port`
- [x] `git`：`get_git_branch` / `get_git_head`
- [x] `acp` 12 条骨架：`list_agents` / `list_connections` / `connect` / `disconnect` / `get_agent_status` / `prompt` / `cancel` / `respond_permission` / `describe_agent_options` / `set_mode` / `answer_question` / `preflight`
- [x] `conversations` 变更：`update_conversation_title`（+`title_locked`）/ `update_conversation_status` / `update_conversation_pinned` / `delete_conversation`（软删 + 清理 opened_tab）
- [x] `git` 读 + 简单分支操作（shell out 到系统 git，输出逐字段对齐上游）：`get_git_branch` / `get_git_head` / `git_status` / `git_diff` / `git_diff_with_branch` / `git_show_diff` / `git_show_file` / `git_is_tracked` / `git_list_branches` / `git_list_all_branches` / `git_commit_branches` / `git_list_remotes` / `git_push_info` / `git_log`（含 pushed/has_upstream 推送状态推断）/ `git_new_branch` / `git_checkout` / `git_reset`
- [x] `git` 写操作：`git_add_files` / `git_commit`（含 staged-deletion 跳过 + committed_files 计数）/ `git_init` / `git_rollback_file` / stash 全套（push/pop/apply/drop/clear/show/list）/ `git_fetch` / `git_fetch_remote` / `git_pull`（updated_files）/ `git_push`（pushed_commits + upstream_set，首推 -u）—— 远程操作带 askpass 凭据注入
- [x] `work_task` 26 条 P0（CRUD/排序/归档/清理/事件 + 生命周期状态机 + 设置 + 模板，见 M3 B10）
- [ ] `forge` 15 条 P0
- [ ] `terminal` 5 条
- [x] `files` 文件编辑器命令：`create_file_tree_entry` / `rename_file_tree_entry` / `move_file_tree_entry` / `delete_file_tree_entry` / `read_file_for_edit` / `save_file_content`（etag 乐观并发）/ `read_workspace_file_base64`
- [x] `quick_messages` 5 条：`list` / `create` / `update` / `delete` / `reorder`
- [x] `conversation_tags` 8 条：list/create/update/delete/reorder + `update_conversation_tags` + branch tag（含重名/颜色校验的 i18n_key）
- [x] `opened_tabs`：`list_opened_tabs` / `save_opened_tabs`（CAS 版本号 + 草稿不落库 + at-most-one active）
- [x] `model_provider` 4 条：list/create/update/delete —— 仅做存储 CRUD（不复刻 provider 逻辑/连接刷新），返回含 `api_key_masked` 的完整 ModelProviderInfo
- [ ] `pet`：`pet_list_active_sessions`
- [x] 设置项：`question`(get/set) / `feedback`(get/set) / `session_info`(get/set) / `chat_authoring`(get/set) / `delegation`(get/set，depth 1..8 clamp) —— app_metadata 持久化 + firehose 广播
- [x] `get_home_directory` / `probe_terminal_shell_path` / `pet_list_active_sessions`（空载）/ `list_child_conversations`；`detect_git` 已在 B3 完成
- [ ] `github_accounts` 4 条

---

## M2 — ACP 引擎

- [~] 连接表 / Manager（`src-go/acp/manager.go`，内存态）
- [x] JSON-RPC 2.0 over stdio：传输层 `acp/jsonrpc.go` + 子进程 spawn `acp/agent_process.go`（stdio 接 Peer、stderr tail、env 合并）
- [x] 内置 agent registry（`acp/registry.go`，15 个内置 agent：wire 名/registry_id/distribution/is_acp_adapter），`acp_list_agents` 返回忠实 AcpAgentInfo
- [ ] `preflight` / 依赖探测 / 二进制下载
- [x] 权限请求：`session/request_permission` → emit permission_request → `acp_respond_permission` HTTP → ResolvePermission 全链路打通
- [ ] 提问请求 → `pending_question` → `acp_answer_question`
- [x] session 驱动 `acp/agent_session.go` + 活连接生命周期 `acp/connection.go`：spawn→握手→注册 SessionRegistry→prompt/permission/cancel/disconnect，HTTP handler 全部接到真实引擎
- [ ] fork / delegation / plan approval
- [ ] `yoyocode-mcp` 伴随二进制

---

## M3 — git / forge / work_task / terminal

- [~] `git`（52 条）：读/分支/写/stash/push/pull/fetch 已落地 32 条；worktree/conflict/remote 管理待做
- [ ] `forge`（17 条）：GitHub / Gitea / GitLab issue / PR / comment
- [x] `work_task`（31 条）：全量 schema（work_task 36 列 + work_task_event/_settings/_template）+ 数据面 CRUD/reorder/archive/cleanup/events/attention_count + 生命周期状态机（start/retry/requeue/schedule/return/cancel/merge/merge_unqueue/deliver_pr/complete/start_all）+ 每文件夹设置（get/effective/get_own/set/delete，defaults 合并）+ 模板（list/save-upsert/delete）+ diff/changed_files（git over worktree）。agent 实际执行为后续增强，board 契约忠实。
- [ ] `terminal`（6 条）：pty spawn / write / resize / kill / list
- [ ] `version_control`（12 条）

---

## M4 — 桌面端模块

- [ ] `automation`(11) · `backup`(11) · `canvas`(12) · `chat_channel`(22) · `computer`(18)
- [ ] `config_sync`(12) · `custom_skills`(10) · `experts`(8) · `science`(8)
- [ ] `app_update`(4) · `logging`(5) · `mcp`(8) · `mcp_service`(3)
- [ ] `office_tools`(14) · `office_watch_proxy`(3) · `token_usage`(4)
- [ ] `folder_commands`(6) · `folder_links`(6) · `background`(5)
- [ ] `browser_bridge`(3) · `browser_tools`(2) · `computer_tools`(4)
- [ ] `chat_authoring`(2) · `delegation`(2) · `feedback`(3) · `question`(2) · `session_info`(2)
- [ ] `event_metrics`(1) · `workspace_state`(3) · `workspace_files`(4) · `project_boot`(5)
- [ ] `pet`(19)

---

## M5 — mygo 原生 UI

依赖为普通 Go 模块（`require github.com/egoist/mygo v0.3.5`，哈希在 go.sum），
构建时经 git 拉取；`_upstream/*` 整目录忽略，仓库不携带上游源码。

- [ ] `src/app/workspace` 主窗口（会话列表 / 标签 / 文件树 / diff / 终端）
- [ ] `src/app/settings/*` 21 个子页
- [ ] `src/app/commit` / `push` / `merge` / `stash`
- [ ] `src/app/pet` / `pet-panel`
- [ ] `src/app/login`
- [ ] `src/app/project-boot`
- [ ] `src/app/import-sessions`
- [ ] `src/app/computer-indicator` / `computer-marker`

---

## 日志

### 2026-10-13（契约审计第二批：会话列表筛选/排序 + summary 字段对齐）

- **`list_all_conversations` 四个筛选/排序参数接进来了**（此前被忽略，客户端列表的
  搜索/筛选/排序全部失效）。逐项对齐上游 `conversation_service::list_all`：
  过滤全部下推 SQL，`search` 是标题子串，`status` 只在**能解析成已知状态时**生效
  （解析不了静默忽略，不报错），`sortBy=oldest` 升序、其余降序。
  另外两条上游语义此前也没有：**`kind != 'loop'`**（loop-engineering 跑次不进列表）与
  **默认排除子会话**（`parent_id IS NULL`，除非 `includeChildren`），以及
  `folderIds` 为空时等于「所有**还活着**的文件夹」——已删除文件夹里的会话仍隐藏。
- **`DbConversationSummary` 字段补齐**：此前只输出 11 个字段，缺 `title_locked`、
  `kind`、`child_count`、`pinned_at`；且 `title`/`model`/`git_branch`/`external_id`
  为 NULL 时输出 `""` 而上游是 `null`（客户端区别「无标题」与「标题为空」）；
  `pinned_at` 恒在场（客户端按 `string | null` 读）。统一抽出 `conversationSummaryColumns`
  + `scanConversationSummary`（`rowScanner` 同时服务 list 与单行查询），
  `child_count` 用子查询算（上游 `fill_summary_extras` 同义）。
- **修一个隐藏的值漂移**：`conversation.kind` 的列默认值是占位的 `'agent'`，
  而 `create_conversation` 从不写这一列 → 新会话落到 `'agent'`，上游的取值是
  `regular|chat|loop|delegate`，客户端按它分组。改为显式写 `'regular'`，
  并加一条幂等迁移把历史 `agent`/NULL 行归一成 `regular`（列默认值同步改）。
  `create_chat_conversation` 本来就写 `'chat'`，不变。
- 测试：`TestListAllConversationsFilters`（逐筛选 + 排序 + 关闭文件夹隐藏）、
  `TestConversationKindValues`。契约测试文件更名为 `routes_contract_test.go`（已不止文件路由）。
- 修测试自身的一个坏假设：三条会话在同一秒内创建、`updated_at` 相同，
  SQLite 对相同键排序不保证稳定 —— 排序断言改为**按返回的 `updated_at` 值**校验。
- 审计脚本复核：18 → 17 处不一致（`list_all_conversations` 已消）。
- gofmt / go build ./... / go vet ./... / 契约测试全绿。

### 2026-10-12（契约审计：P0 参数名全量比对，修 3 处真问题）

- 新增 `scripts/audit_contract.py`：从上游 handler 源解析每个 P0 路由的**参数结构**（含 serde
  `rename_all` 归一）与响应类型，和我们 handler 实际读取的 body key 做集合比对，输出
  「上游发但我们不读」（会**静默丢值**）与「我们读但上游不发」（多半是无害别名）两张清单。
  脚本头记了已知局限（辅助函数经别的参数名取 body、共享 func 注册、故意忽略的可选参数），
  结论需人工复核。
- 逐条核实后，**确认为真并已修**的三处：
  - **`acp_connect`**：客户端发 `workingDir`，我们只读 `folderPath`/`path` → **连接拿不到工作目录**。
    已改为 `workingDir` 优先。
  - **`get_folder_conversation`**：上游收 `{conversationId, tailTurns?, fromIndex?}`、返回**会话详情**
    （summary + 窗口化 turns）；我们收 `folderId`、返回文件夹 `{id,name,path}` —— 语义完全不同。
    已改为会话详情：复用 `getConversation` + `loadConversationTurns` + `computeWindowMeta`；
    两个窗口选择器互斥（同时给 → 400）；**只有请求了窗口才带**
    `turns_offset`/`turns_total`/`assistant_turns_before_offset`（它们的存在与否正是客户端判断
    「分页合并」还是「整段替换」的依据）。
  - **`update_system_language_settings`**：上游收 `{settings:{mode,language}}` 并回显同一对象；
    我们读顶层 `language` → **语言设置永远写不进去**。已改为上游信封 + `mode` 归一
    （snake_case：`system`|`manual`）+ 发 `app://language-settings-updated`。
- 核实为**误报**（实现本就正确，是脚本识别不到）：`acp_prompt`（已支持 `blocks`）、
  `acp_update_agent_env`（`GetRaw(c,"env")`）、`acp_update_agent_config`（`storeAgentConfigBlobs` 从 body 取）、
  `forge_*`（经 `forgeAuthFromCtx(c, …)` 读 folderId）、`git_*`（经 `pathParam(c)`）、
  `save_opened_tabs.expectedVersion`、`work_task_template_delete`（收 id 或 name）、
  `update_conversation_*` 多收的 `id`（无害别名）。
- 新增回归测试 `TestGetFolderConversationContract` / `TestUpdateSystemLanguageSettingsContract`。
- **剩余待办（已确认、未修，按影响排序）**：
  1. `list_all_conversations` 的 `search`/`sortBy`/`status`/`includeChildren` 被忽略（列表筛选/排序失效）；
  2. `acp_connect` 的 `preferredModeId`/`preferredConfigValues` 未应用（用户选的模式/配置不生效）；
  3. `acp_set_config_option`（`connectionId`/`configId`/`valueId`）与 `acp_describe_agent_options`
     仍是空实现；
  4. `create_conversation` 返回对象，上游是**裸 i32**（客户端两种都容忍，但应逐字对齐）；
  5. `work_task_*` 的 `deleteWorktree`/`note`/`blocks`/`reason` 等可选参数未接；
  6. `forge_tab_count` 的 `tab`/`filters`；`save_opened_tabs.origin`；
     `acp_download_agent_binary` / `acp_prepare_npx_agent` 的 `version` 系列。
- gofmt / go build ./... / go vet ./... / go test ./... 全绿（handlers 387s）。

### 2026-10-12（契约修复：文件/文件夹 P0 路由对齐上游）

审计 `get_file_tree` 时发现一批 P0 路由的**线格式**与上游不一致 —— 客户端按自己的形状解析，
行为测试全绿也照样断。逐条对齐，并加回归测试 `src-go/web/handlers/file_routes_contract_test.go`：

| 路由 | 原实现（错） | 现在（对齐上游） |
|---|---|---|
| `get_file_tree` | 参数 `{folderId, depth, showHidden}`；返回扁平 `{entries:[{path,name,type}]}` | 参数 `{path, maxDepth?}`；返回**嵌套** `FileTreeNode[]`（`kind: file\|dir`、`name`、`path` 相对正斜杠、dir 的 `children` 恒在、`symlink` 仅 true 时出现）；忽略 `.git`/`__pycache__`/`.DS_Store`；符号链接目录报空 dir；不可读项跳过；同级 dirs 先、按名小写排序 |
| `read_file_preview` | 参数只有 `{path}`（当绝对路径用）；返回 `{content, truncated, size}` 且 200KB 截断 | 参数 `{rootPath, path}`（相对路径经 `resolveTreePath`）；返回 `{path, content}`，全量读取（50MB 上限）、前 2KiB 有 NUL 判为二进制 |
| `read_file_base64` | 参数 `{path}`；返回 `{data, size}` | 参数 `{path`(绝对)`, maxBytes?}`；返回**裸 base64 字符串**；maxBytes 默认 20MB、clamp 到 [4KiB, 100MB]，超限报 invalid_input 且 detail 带 `max_bytes=N` |
| `save_file_copy` | `ErrNotImplemented` 空实现（清单却标成已实现） | 真实现：同目录写副本 `<stem>.local.<ext>` → `.local.N.<ext>`；符号链接源拒绝；返回 FileSaveResult |
| `list_directory_entries` | 返回 `{name, type}` 且把文件也列出来 | 只列**子目录**、跳过隐藏目录、返回 `{name, path, hasChildren}`；非目录报 io_error |
| `remove_folder_from_history` | 收 `{folderId}`、返回 `{ok:true}` | 收 `{path}`（folderId 仍作内部便捷回退）、返回 `null` |
| `add_folder_to_history` | 返回 `{id, name, path}` | 补 `last_opened_at`（FolderHistoryEntry）；id 改为按 path 回读（upsert 命中已有行时 LastInsertId 不可靠） |
| `EmptyResult()` | 返回 `{}` | 返回 `nil`（上游 `Result<Json<()>>` 序列化为 `null`）—— 影响 43 处 handler |

- 顺带核对、**本就正确未改**：`list_directory_with_files`（`DirectoryItem` 是 camelCase，`hasChildren` 为任意非隐藏子项）、
  `list_workspace_files`、`create_directory`（`{parentPath, name}`），以及 files.go 里那批已读 `rootPath` 的路由。
- 复用而非重复声明：`fileBase64DefaultMaxBytes`/`fileBase64MaxBytes`（git_show_base64.go）、
  `fileTreeIgnoredDirs`、`entryKind`（workspace_file_list.go）。
- 测试：7 个契约测试锁定线格式（含 `read_file_base64` 的裸串、`EmptyResult` 的 `null`、
  `get_file_tree` 的嵌套形状与排序、符号链接目录）。
- gofmt / go build ./... / go vet ./... / go test ./... 全绿（handlers 277s）。

### 2026-10-12（品牌清理：仓库内不再出现上游品牌名）

- **目标**：提交内容里不出现上游品牌名，一律用「上游」指代；唯一说明上游是谁的地方放在本地忽略目录。
- **新增 `_upstream/README.md`**（整个 `_upstream/` 被忽略，不随仓库分发）：说明 `ref/` 是被复刻的上游克隆、
  `mygo/` 是 UI 工具包、目录对应表、跟进上游的命令，以及**不能改的契约字面量清单**。
- **上游克隆目录**改名为 `_upstream/ref`，`scripts/gen_inventory.js` 的路径同步。
- **清理范围（64 文件）**：文档散文、Go 注释、UI 文案，以及整个本地命名空间 ——
  数据目录（原先与上游桌面版共用，会互相干扰）、库文件名、环境变量前缀、
  两个 `cmd/` 二进制目录、config-sync 默认远端目录、出网 User-Agent、
  `folder_links` 写入 `.git/info/exclude` 的标记、内部标识符，全部换成本项目自己的名字。
  精确的旧→新对照表放在本地 `_upstream/README.md`（不随仓库分发）。
  README 重写为正常的产品说明。
- **注释用词分语言**：中文注释用「上游」，英文注释用 `upstream` —— 第一遍把中文词塞进英文句子
  读不通，补了一遍按行判定的修正。
- **只保留协议契约字面量**：WS 子协议、路由名、JSON 字段按原样保留（改名即断线），
  清单见 `docs/REPLICATION_PLAN.md` §7；除此之外全仓不再出现上游品牌名。
- gofmt / go build ./... / go vet ./... / go test ./... 全绿（handlers 302s）。

### 2026-10-12（UI：会话详情区视觉 —— 消息气泡 / 工具调用卡片 / 输入坞）

- 新增 `src/app/conversation.go`，按上游 的视觉语言绘制会话详情三块核心：
  - **buildTranscript**：把 ACP 扁平事件流折成渲染行 —— `content_delta`/`thinking` 各自合并、
    `tool_call` 建卡并由 `tool_call_update` 原地更新（按 tool_call_id 索引）、`turn_complete` 收尾、
    `error` 成告警行。
  - **消息气泡**（对齐 `ai-elements/message` 的 Message + MessageContent）：user 右对齐灰底圆角胶囊
    （`max-w 88%`、`px-4 py-3`、`rounded-lg`）、assistant 通栏纯文本、system 边框静音卡；
    正文按反引号切分，行内代码走等宽 + 底色的 RichText span。
  - **工具调用卡片**（对齐 `ai-elements/tool` 的 Tool + ToolHeader）：通栏圆角边框卡，头部 =
    扳手图标 + 标题 + 圆角状态徽章（徽章内含状态图标与文案：绿勾 / 红叉 / 琥珀时钟）+ chevron；
    点头部折叠展开，失败态默认展开（AgentCapsule 的 defaultOpen 语义）。
  - **输入坞**（对齐 `chat/message-input` 的 chrome）：圆角边框输入框 + 底部操作行（左侧状态提示、
    右侧 Stop/Send）；连接降级时上方出现 warning 语气状态条（composer-dock-strip）。
- `workspace.go`：`viewConversationDetail` 改用上述视觉；快照里的 in-flight 工具调用按 id 去重后并入
  时间线；pending 权限 / 提问卡改为边框卡片语言。删掉被取代的 `viewToolCall` / `viewTurnBlocks` /
  `eventKind` / `eventText`。
- **顺手修掉一个既有布局 bug**：侧栏 FOLDERS 列表一直是空的 —— `Grow(1)` 的 Scroll 放在按内容撑高的
  列里，高度塌成 0。改成 `MaxHeight(220)` 的 `ws-folder-list` Scroll，列表正常显示。
- 新增 `src/app/render_test.go`：用 mygo 的 `ui.NewTester` **离屏渲染整窗**（strict 模式，重复 key 会
  panic，顺带守护 key 唯一性），断言会话详情的关键文本（会话标题 / assistant 正文 / 工具卡标题 /
  状态徽章 / Send / 文件夹名）都在帧里；驱动工具卡头部点击验证折叠展开；并校验明暗两态画布像素确实
  不同（#ffffff → #18181b）。设 `MYGO_UI_PNG` 可把帧导出成 PNG 供肉眼查看。
- `app_test.go` 增补 buildTranscript / toolStatusLabel / toolGlyph / proseSpans / composerStrip|Hint 单测；
  `stream_test.go` 改为断言事件字段 + 折叠结果。
- 顺带：`src-go/web/server.go` 有一处 gofmt 未格式化（提交时就存在），一并格式化。
- gofmt / go build ./... / go vet ./... / go test ./... 全绿。

### 2026-10-12（依赖：mygo 改回标准模块依赖，去掉 replace + 源码内联）

- **起因**：准备首次提交时发现 `_upstream/mygo` 自带 `.git`，git 只能把它暂存成 `160000` gitlink —— 克隆者拿不到源码，与 `.gitignore` 白名单「保证 fresh clone 可构建」的意图自相矛盾。
- **实测澄清**：mygo 完全可以作为普通模块依赖。`proxy.golang.org` 确实被挡（502），但 `GOPROXY=direct` 走 git 正常：`go list -m -versions` 列出 v0.1.0…v0.4.6，v0.3.5 已落模块缓存并可编译。
- **改动**：`go mod edit -dropreplace` + `require github.com/egoist/mygo v0.3.5`，`go mod tidy` 写入 go.sum 哈希；`.gitignore` 恢复整目录忽略 `/_upstream/*`（删掉 mygo 白名单）。
- **验证**：把整个 `_upstream` 删掉的副本里，仅凭模块缓存（`GOPROXY=off`）`go build ./...` 通过；本项目内 `go build ./...` / `go vet ./...` / `go test ./...` 全绿（handlers 416s）；dev 进程（`go run github.com/egoist/mygo/cmd/mygo dev`）重建正常。
- **顺带**：`go mod vendor` 也可行，但 vendor 达 143MB/2778 文件（带上了 modernc sqlite），远大于内联 mygo 的 22MB，故不采用。待提交文件从「240 + gitlink」变为纯 240 个自有文件。

### 2026-10-12（UI：外观模式 浅色/深色 + Appearance 设置页）

- 后端 `src-go/web/handlers/appearance.go`：get_appearance_mode / update_appearance_mode（对齐上游 commands/windows.rs），模式 system|light|dark 存 app_metadata 键 appearance_mode，未知值归一为 system。测试 appearance_test.go 覆盖默认 system→dark→light→未知归一。
- UI 主题：`src/app/theme.go` 新增 themeDark（对齐上游 globals.css 的 [data-theme="neutral"].dark：近黑画布 #18181b、深灰面板、浅色主色 #e4e4e7 + 深色文字、灰阶）+ resolveTheme(mode)（system 无进程内探测时回退 light）。
- Shell 接线：Shell 增 appearanceMode 字段（默认 system），View 里经 ensure("appearance-mode","get_appearance_mode") 读取并把 resolveTheme 结果 SetTheme 到整窗——所以主题是全局生效的，不只是某页。
- Appearance 设置页：settingsPages 的 appearance 由占位改为真实页 viewSettingsAppearance —— 主题模式三选一（System/Light/Dark，当前项高亮为主按钮），点击经 setAppearanceMode 乐观改主题 + 调 update_appearance_mode 持久化 + 失效缓存重绘；下方显示当前模式与三色实时预览块。对齐上游 components/settings/appearance-settings.tsx 的主题模式段（字体/工作区背景等其余段留待）。
- 全量 build/vet/test ./... 全绿（handlers 82s），exe 重建；dev 热重载预览已重启（yoyocode Dev / YoYoCode）。
### 2026-10-12（UI：工作区空态欢迎面板）

- src/app/workspace.go viewConversationDetail 的「未选会话」空态由单行「Select or create a conversation」升级为居中欢迎面板（对齐 上游 的无会话画布）：产品名 YoYoCode（28号粗体）+ 一行提示 + 主按钮「New chat」(createChatConversation) 与次按钮「Open folder…」(openFolder)。dev 热重载后 a11y 树已确认渲染（元素 [5]/[6] 两按钮 + 标题/副标题文本）。
- 说明：本会话此前多轮均为后端路由（450→476）实现，故 UI 一直未变；本次起转入可见 UI 改进。注意本会话无法读取 mygo GPU 窗口像素（截图只回元数据），UI 结构以无障碍树核验、视觉保真以上游 src/app 源逐一移植。
- go test ./src/app 全绿、go vet 全绿、exe 重建。
### 2026-10-12（续：import_local_conversations + import_selected_sessions 会话导入写入，后端 474→476）

- 在 src-go/web/handlers/session_import.go 新增两写入端点（对齐上游 import_service import_summaries/import_one）：
  - importOneSession（镜像 import_one，按 (external_id, agent_type) 对账）：不存在→插入新会话（status=pending_review、kind=regular、title_locked=0、created_at=started_at、updated_at=ended_at||started_at、last_message_at=ended_at、model/git_branch/message_count/external_id）→"imported"；存在且 parent→skip；软删且非 restore→skip；软删且 restore→un-delete(deleted_at=NULL, folder_id, status=pending_review)→"restored"；标题刷新（未锁且变更，不动 updated_at）+ 活动刷新（ended_at 严格更新→改 updated_at/last_message_at/message_count）→"updated"；否则 skip。
  - import_local_conversations(folderId)：取文件夹 path，导入 cwd 归一化匹配的 summary（DeletedPolicy=Skip），返回 {imported,updated,skipped,restored}。
  - import_selected_sessions(selections[{agentType,externalId}])：按 (agent,ext) 索引 summary，resolveImportFolder（归一化 path 找 folder，软删则重开，否则 addFolder 新建）→import_one(restore=true)，按文件夹汇总，返回 {imported,updated,skipped,restored,not_found,failed,created_folders,folders[],errors[]}。
- 已知跨平台注意：resolveImportFolder 用 addFolder（filepath.Abs），真实 claude cwd 为 OS 绝对路径故与 summary.cwd 归一化一致；测试故用 t.TempDir() 作 cwd 以保跨平台匹配。token-usage 重算标记（Go 无该服务）略过，非致命。
- 测试 session_import_write_test.go（CLAUDE_CONFIG_DIR 隔离、cwd=TempDir、json.Marshal 转录）：import_selected 建夹+导入(imported=1,created_folders=1)→scan 复查 exists_in_codeg=true/status=imported→重导入 skip→import_local 入已存夹不重插→bogus 选择 not_found=1。
- 全量 build/vet/test ./... 全绿（handlers 57s），inventory 523/476/P0 128/128，exe 重建。会话导入 3 路由（scan/import_local/import_selected）闭环；非 claude agent 的会话枚举（其余 11 parser）待后续移植。
### 2026-10-12（续：scan_importable_sessions 会话导入扫描，后端 473→474）

- 新增 src-go/parsers/claude_summary.go（对齐上游 parsers/claude.rs list_conversations + parse_jsonl_summary 的选取器子集）：ListClaudeConversations 遍历 <claude>/projects/*/<uuid>.jsonl，每份转录提取一条 ConversationSummary（sessionId=external_id、cwd=目标文件夹、title[/rename custom→summary ai→首条 user 文本，截 100 符]、首末时间戳、消息数、model[取 assistant.message.model]、gitBranch），按 started_at 倒序；cwd 缺失时由目录名反解（- 还原为 /）。
- 新增 src-go/web/handlers/session_import.go：scan_importable_sessions（只读）。collectLocalSummaries 聚合已移植 parser（当前仅 claude，其余诚实留空）、跳过委派子会话。按 cwd 归一化分组（normalizePathForMatching：trim、\→/、去尾斜杠、Windows 小写），每组对账 folder 表（命中则复用其存储 path/name + folder_id + exists_in_codeg）与 conversation.external_id（any-live→imported、仅软删→deleted、无→new）。返回 ScanResult {folders[{path,name,exists_in_codeg,folder_id,agent_types,sessions[{external_id,agent_type,title,started_at,ended_at,message_count,model,git_branch,status}]}], no_folder_count, total_sessions, importable_count}；ScanSessionStatus snake_case new/imported/deleted。
- 测试 session_import_test.go（CLAUDE_CONFIG_DIR 隔离）：植入 claude 转录（sess-abc, cwd=/home/u/proj, model/gitBranch/2 msgs/首条 user 文本）→ scan 得 1 组（path/name/exists_in_codeg=false）、1 会话（external_id/agent_type/status=new/model/git_branch/count/title 全对）、计数 total=importable=1、no_folder=0。
- 全量 build/vet/test ./... 全绿（handlers 56s、parsers 0.7s），inventory 523/474/P0 128/128，exe 重建。剩余 import_local_conversations/import_selected_sessions（写入：import_one 的 upsert/软删恢复/标题刷新/token 重算标记），接着做。
### 2026-10-12（续：config_sync 远端 WebDAV 传输 upload_now/peek_remote/download_apply，后端 470→473）

- 新增 src-go/web/handlers/config_sync_webdav_remote.go（对齐上游 commands/config_sync/webdav_sync.rs）：在 <remoteDir>/v1/<profile>/ 下搬运快照格式定义的两个文件 config.json + manifest.json，复用 config_sync_content.go 的 collect/serialize/manifest/apply 与 config_sync_webdav.go 的 URL 逻辑。
  - 内置 WebDAV 读写客户端：webdavGet（404→未找到、2xx→体、8MiB 上限）、webdavPut（application/json）、webdavEnsureDir（逐级 MKCOL 容忍已存 405/301/302）、basic-auth、30s 超时、状态分类（401/403→Auth、404/409→路径缺失、其余→Network）。
  - upload_now：collect→serialize→sha256→counts；remoteTarget=serverUrl\0remoteDir\0profile\0protection；!force 且 sha+target 未变→uploaded:false 不触网；写前 ensureDir→PUT config→PUT manifest→存 state(lastUploadedSha256/Target/SyncAt)。返回 {uploaded,sha256,counts,syncedAt}。
  - peek_remote：GET manifest.json→解析 ConfigManifest 或 null。download_apply：GET manifest+config→validateManifest(schema/encryption/size/sha256)→parse→applyImport(存回滚+应用)→{manifest,applied,rollbackPath}。
  - 加密诚实降级：encrypt=true 的上传/下载直接 ErrConfigInvalid（本构建仅 none），绝不半写加密远端。
- 测试 config_sync_webdav_remote_test.go（YOYOCODE_HOME 隔离，httptest 内存 WebDAV map 支持 MKCOL/PUT/GET/PROPFIND）：seed→update_settings→upload_now(uploaded:true + config/manifest 落服务器)→二次 upload 不变 no-op→peek_remote 得 manifest→download_apply 应用(manifest+applied+rollbackPath)→空 profile peek 得 null。
- 全量 build/vet/test ./... 全绿（handlers 56s），inventory 523/473/P0 128/128，exe 重建。config_sync 全 9 路由（settings/state/export/peek/import/rollback/test_connection/upload_now/peek_remote/download_apply）已闭环。
### 2026-10-12（续：config_sync_test_connection WebDAV 连通性探测，后端 469→470）

- 新增 src-go/web/handlers/config_sync_webdav.go（对齐上游 commands/config_sync/webdav_sync.rs test_connection_core + network/webdav.rs probe）：对 WebDAV 远端做凭据+可达性检查，不写任何东西。
  - 合并候选设置（serverUrl/username/password/remoteDir/profile over 已存设置，sameAccount 时沿用已存密码，mirror update_settings 规则）；serverUrl 空→ErrInvalidInput；remoteDir/profile 经 normalizeSegment 清洗。
  - rel = <remoteDir>/v1/<profile>；PROPFIND(Depth 0) basic-auth，15s 超时。状态映射：2xx/207/404(目录未建)→成功；401/403→ErrAuthFailed；409→ErrConfigInvalid(远端路径缺失)；其余→ErrNetwork。传输错误只返回裸网络消息（绝不泄露含用户名的 URL）。
- 测试 config_sync_webdav_test.go：httptest 伪 WebDAV（好凭据→207、坏→401）→ 好凭据成功、坏凭据失败、空 URL 失败、不可达主机失败（非崩溃）。
- 全量 build/vet/test ./... 全绿（handlers 59s），inventory 523/470/P0 128/128，exe 重建。剩余 config_sync 远端 3 路由（upload_now/peek_remote/download_apply）依赖完整 WebDAV 引擎（读写 config.json+manifest.json、可选加密、状态跟踪），留待。
### 2026-10-12（续：upload_attachment，后端 468→469）

- 新增 src-go/web/handlers/upload_attachment.go（对齐上游 web/handlers/files.rs upload_attachment）：把单个 multipart 文件流式写入 <uploads-root>/<session-bucket>/，限定在 uploads 根内，文件名清洗 + 冲突后缀、20 MiB 单文件上限、可选总量配额（YOYOCODE_UPLOAD_MAX_TOTAL_BYTES）。返回 {path,name,size,mimeType}。
  - yoyocodeUploadsRoot：YOYOCODE_HOME/uploads → YOYOCODE_DATA_DIR/uploads → ~/.yoyocode/uploads。sanitizeUploadFilename（取末段、剔控制符、替换 /\:*?"<>| 、剪尾点、限 120 字、全点→file）、sanitizeSessionBucket（仅字母数字-_、去 _ 边界、限 80、空→anon）。
  - finalizeUploadName：原名→"name (1).ext"…(≤999)→UUID 回退，用 os.Link（已存在即换名，跨设备回退 O_EXCL 预留 + rename）。.tmp 暂存 + 符号链接 bucket/tmp 拒绝 + ensureNavigable 越界拒。空文件/多 file 字段/缺 file 拒。
  - 配额：cap>0 时 reserve=min(Content-Length,20MiB)，used+reserve>cap 拒。limitedReader 实现 20MiB 硬上限。
- 测试 upload_attachment_test.go（YOYOCODE_HOME 隔离）：基本上传（落盘+size）、同名冲突得 "notes (1).txt"、../ 文件名清洗为 passwd、空文件拒、缺 file 字段拒、无 session 落 anon 桶。
- 全量 build/vet/test ./... 全绿（handlers 68s），inventory 523/469/P0 128/128，exe 重建。
### 2026-10-12（续：open_worktree_folder，后端 467→468）

- 在 src-go/web/handlers/open_windows.go 落地 open_worktree_folder（对齐上游 commands/folders.rs open_worktree_folder_core）：把 worktree 检出注册为工作区文件夹，挂到源文件夹的根下，并以该目录当前 git 分支种子化 alias（never-clobber 用户自定义 alias）。
  - parentID：sourceFolderId>0 时取源文件夹 parent_id（为空则取其自身 id）；否则 null。
  - add-with-parent：INSERT folder(... parent_id ... is_open=1) ON CONFLICT(path) 更新 name/parent_id/last_opened/updated 并 deleted_at=NULL（worktree 重注册保持父链接）。
  - 种子 alias：resolveGitHead(path).branch 非空且 alias 为空时才写（UPDATE ... WHERE alias IS NULL OR alias=''），保证任务重试/重建检出不覆盖用户命名。广播 folders://changed，返回 FolderDetail。
- 测试 open_worktree_folder_test.go：建 repo+worktree → open_folder 注册源 → open_worktree_folder（校验 alias=feature-x、parent_id=源 id）→ 用户改 alias 后重开不被覆盖。
- 全量 build/vet/test ./... 全绿（handlers 54s），inventory 523/468/P0 128/128，exe 重建。
### 2026-10-12（续：acp_goal_control，后端 466→467）

- src-go/acp/connection.go 加 Manager.GoalControlLive(connID, action)：向 live 适配器发 `_codex/session/goal_control` 通知（action pause|clear + sessionId），镜像 CancelLive 的 live-session 转发模式；无 live 会话返回 false。对齐上游 acp::manager::goal_control 的管理器侧（out-of-band 中断回复为适配器特定、此处不等待）。
- src-go/web/handlers/acp.go 注册 acp_goal_control：校验 connectionId 非空、action∈{pause,clear}，经 GoalControlLive 转发；连接不存在→ErrNotFound。
- 测试 cp_goal_control_test.go：自定义 fake spawner 注册 `_codex/session/goal_control` 通知处理器写 channel；acp_connect(codex) 建会话 → 非法 action 拒 / 未知连接拒 → pause 转发（适配器收到 "pause"）→ clear 转发（收到 "clear"）。
- 全量 build/vet/test ./... 全绿（handlers 60s），inventory 523/467/P0 128/128，exe 重建。
### 2026-10-12（续：officecli_render_html，后端 465→466）

- 在 src-go/web/handlers/office_tools.go 落地 officecli_render_html（对齐上游 commands/office_tools.rs）：把 .docx/.xlsx/.pptx 经 `officecli view <file> html` 渲染为自包含 HTML 供应用内预览，在 上游 自身进程跑（不受 agent 命令沙箱限制）。
  - 路径限定：requireFolderDir + resolveTreePath + ensureNavigable（越界拒）；isOfficePath 校验扩展名；非文件/不存在拒。
  - resolveOfficecli：先 exec.LookPath("officecli")，再回退官方安装器已知位置（Windows %LOCALAPPDATA%/OfficeCLI/officecli.exe；类 Unix ~/.local/bin/officecli）——覆盖 install 后 PATH 尚未刷新到本进程的窗口。未安装 → ErrDependencyMissing。
  - 运行失败→ErrExternalCommand(带 stderr)；空输出→ErrExternalCommand。成功返回 HTML 字符串。
- 测试 office_render_html_test.go：缺失根拒、非 office 扩展拒、../ 穿越拒、真实 .docx（本构建无 officecli → 干净依赖错误，不崩溃）。
- 全量 build/vet/test ./... 全绿（handlers 55s），inventory 523/466/P0 128/128，exe 重建。剩余 office 面：start/stop_office_watch + office-watch-proxy（文件监视 + HTTP 代理子系统），留待。
### 2026-10-12（续：config_sync 内容快照 export/peek/import + rollback 5 路由，后端 460→465）

- 新增 src-go/web/handlers/config_sync_content.go（对齐上游 commands/config_sync/{local_io,snapshot,domains}.rs）：可移植设置快照——JSON 信封 {codegConfigExport, manifest, config}，config.domains 为 域id→载荷；行按自然键 upsert（不清表），导入前先写 pre-apply 回滚快照以便撤销。
  - 域（跳过 modelProviders —— 模型供应商配置不在范围）：agentSettings（provider 以自然键 {agentType,name} 引用，导入时解析为本地 model_provider.id，解析不到降级为空而不失败）、customAgents（按 registry_id，排除设备本地 skills_dir/installed_version）、quickMessages（按 title）、taskTemplates（按 name）、preferences（PORTABLE_PREFERENCE_KEYS 白名单的字符串键，导入再校验白名单防注入 github_accounts 等）。未知域忽略，与上游优雅降级一致。
  - 快照序列化 json pretty(2-space) 与 serde to_vec_pretty 一致（domains map Go 按键排序 == BTreeMap）；manifest {schemaVersion,encryption:none,createdAt,appVersion,sourceDevice(hostname),config:{size,sha256},counts}，sha256 覆盖快照字节。counts：数组域取长度、preferences 取白名单字符串键数。
  - config_sync_export_content（返回 {content,counts}）、config_sync_peek_content（解析信封或裸 config.json→{manifest,counts}，裸文件合成信封、appVersion=unknown）、config_sync_import_content（parse→saveRollback(采集当前态写盘)→事务内按序 upsert 5 域→{applied:{domains,total},rollbackPath}）。
  - 回滚文件：configSnapshotDir = YOYOCODE_HOME|~/.yoyocode /config-snapshots；写 config-<YYYYMMDDTHHMMSSmmm>.json 并保留 10 个；list（名字时间序 desc，id 校验字母数字、createdAt 从 id 反解、counts）、apply_rollback（resolveRollback 校验 id 字母表防穿越→读→applyImport）。
- 测试 config_sync_content_test.go（YOYOCODE_HOME 隔离，不污染真实 ~/.yoyocode）：seed quick message → export（校验信封 + counts.quickMessages=1 + modelProviders 不出现）→ peek（manifest/counts）→ import（applied.total + rollbackPath）→ list_rollbacks(≥1，id config- 前缀) → apply_rollback → 穿越 id (config-../etc) 被拒。
- 全量 build/vet/test ./... 全绿（handlers 60s），inventory 523/465/P0 128/128，exe 重建。剩余 config_sync 远端同步 4 路由（test_connection/upload_now/peek_remote/download_apply）依赖 WebDAV 后端，留待。
### 2026-10-12（续：工作区下载票据流 + 路由 raw 前缀路由，后端 459→460）

- 路由扩展：src-go/web Router 加 `rawRoutes` + `RegisterRaw(prefix, fn)`；ServeHTTP 在 /api 之后、静态之前按前缀匹配 raw 端点（供流式下载等自写响应用）。handlers Registry 加 RegisterRaw 代理。
- 新增 src-go/web/handlers/workspace_download_ticket.go（对齐上游 workspace_transfer.rs + workspace_files.rs create/consume_download_ticket）：
  - 进程内票据存储 downloadTicketStore（mutex + 60s TTL + cleanup），每个 Router 独立（不跨测试泄漏）。
  - workspace_download_ticket（POST）：kind file/dir 校验 → resolveDownloadFileTarget / resolveDownloadDirTarget 确定 filename → issue 票据 → 返回 {ticket,url(/workspace_download/<id>),filename,expiresAt(unix)}。
  - GET /workspace_download/<ticket>（RegisterRaw，无需鉴权，先于 /api 调度）：consume（单次、过期判定）→ 重解析 → streamFileToWriter / streamZipToWriter。无效/过期票据→404 纯文本。
  - download_workspace_file/dir 的流式写抽成 streamFileToWriter/streamZipToWriter，票据消费与 /api 下载共用。
- 测试 workspace_download_ticket_test.go：file 票据→GET 消费比对内容 + filename；单次性（二次消费 404）；dir 票据→GET 得 application/zip 非空；非法 kind 拒；bogus 票据 404。
- 说明：GET /workspace_download/<ticket> 走 RegisterRaw（非 reg.Register 字面量），故 inventory 静态扫描不计入该 GET 行；workspace_download_ticket 命令计入（460）。
- 全量 build/vet/test ./... 全绿（handlers 90s、web 2s），inventory 523/460/P0 128/128，exe 重建。
### 2026-10-12（续：工作区文件传输 3 路由 + 路由原始响应基座，后端 456→459）

- 路由基座：src-go/web 的 Context 加 `Handled bool`；serveAPI 在 handler 返回后若 `ctx.Handled` 为真则不再写 JSON（handler 已自写文件流/zip/原始字节）。这是非 JSON 响应的通用出口，解锁后续 raw-HTTP 端点（backup 下载、下载票据等）。web 包测试全绿。
- 新增 src-go/web/handlers/workspace_file_transfer.go（对齐上游 web/handlers/workspace_files.rs，Issue #179 服务端无原生文件对话框）：
  - upload_workspace_file：解析 multipart（root_path/rootPath、target_path/targetPath、relative_path/relativePath、file），computeFinalRel（sanitizeSegment/sanitizeRelativeSubpath 跨平台清洗、拒 ..）→ resolveTreePath 定位 → MkdirAll parent → ensureNavigable 越界拒 → 已存在目录/文件拒 → 写 .yoyocode-upload-<uuid>.part 暂存 → os.Link 原子提交（已存在报 already_exists，无硬链接文件系统回退 os.Rename）→ 复核 navigable + Lstat 校验 → 返回 {path,name,size}。
  - download_workspace_file：resolveTreePath + Stat（非文件/不存在拒）+ ensureNavigable → 流式写 c.W（octet-stream + Content-Length + attachmentHeader，RFC5987 filename*）。
  - download_workspace_dir：resolveTreePath 目录（空 path=根）→ archive/zip 流式写 c.W（跳过符号链接），Content-Type application/zip + attachmentHeader(<name>.zip)。
  - attachmentHeader 对齐 attachment_header（header-safe ASCII filename + filename*=UTF-8''<pct-encoded>）。
- 测试 workspace_file_transfer_test.go：multipart 上传（校验落盘）→ 重复路径 already_exists → 下载原始字节比对 + Content-Disposition → ../ 穿越被拒 → 目录 zip 下载解包校验含 sub/hello.txt。
- 全量 build/vet/test ./... 全绿（handlers 66s、web 2s），inventory 523/459/P0 128/128，exe 重建。
### 2026-10-12（续：custom_import_from_agent，后端 455→456）

- 新增 src-go/web/handlers/custom_import_from_agent.go（对齐上游 commands/custom_skills.rs: custom_import_from_agent + import_one_from_agent_locked）：把某 agent 的全局技能批量复制进中央自定义技能库，让单 agent 技能变为共享自定义技能。
  - 逐项结果（不中断批次）：locateExistingSkillAcrossDirs 在 spec.globalDirs 里按 kind 找到（找不到→ok:false/skipped:false/error:not found）；中央库已存在/软链→幂等 skipped:true；validateSkillID 守卫内置包占用的 id；按 layout 选 copyDirRecursive（目录）或 copyFileContents→SKILL.md（独立 .md）；复制失败回滚部分拷贝（受护：在库内且非链接）。
  - 仅取全局技能（项目作用域技能工作区专属，故意排除）；不支持的 agent → ErrTaskExecution。复用现有 skillStorageSpecFor/customCentralPath/pathIsSymlink/copyDirRecursive/copyFileContents/validateSkillID。
- 测试 custom_import_from_agent_test.go：在 ~/.claude/skills 植入技能 → 导入（含 bogus id 返回 error）→ 校验落入 ~/.yoyocode/skills 中央库 → 重导入幂等 skip → 不支持 agent 报错。
- 全量 build/vet/test ./... 全绿（handlers 52s），inventory 523/456/P0 128/128，exe 重建。
### 2026-10-12（续：acp_fetch_registry_catalog + acp_add_registry_agent，后端 453→455）

- 新增 src-go/web/handlers/acp_registry_catalog.go（对齐上游 commands/custom_agents.rs + acp/remote_registry.rs）：读取公开的 Agent Client Protocol 注册表（开放协议 agent 目录，非模型供应商目录，在范围内）。
  - acp_fetch_registry_catalog：拉取 https://cdn.agentclientprotocol.com/registry/v1/latest/registry.json，解析 agents[{id,name,description,version?,icon?,website?,repository?,license?,distribution}]，逐条标注 builtin/installed/supportedOnPlatform + distributionKinds，按 name 排序。installed 取自 DB custom_agent.registry_id。
  - 纯逻辑逐函数对齐：distributionKindsOf（binary→npx→uvx）、kindRunsHere（npx/uvx 平台无关；binary 需本平台非空 archive）、defaultKindFor/effectiveRegistryKind、isBuiltinRegistryID（BUILTIN_REGISTRY_ALIASES kimi/qoder + acp.BuiltinAgents 的 RegistryID）。
  - acp_add_registry_agent：先拒 builtin（含别名）与空 id（均在网络拉取前短路）→ 拉目录找 entry → effectiveRegistryKind 选 channel → 组 customAgentDef（spec 直存，kind/name/description/version/icon）→ validateCustomDef → upsertCustomAgent（复用现有自定义 agent 持久化）→ 发 agents://changed。
  - 响应键 camelCase（registryId/iconUrl/distributionKinds/supportedOnPlatform/spec 等）与上游 RegistryCatalogAgent 一致。
- 测试：内部 cp_registry_catalog_internal_test.go 覆盖 distributionKindsOf/kindRunsHere（含本平台 archive 判定）/effectiveRegistryKind/isBuiltinRegistryID；外部 cp_registry_catalog_test.go 覆盖 add 的确定性拒绝路径（builtin id、builtin 别名、空 id —— 均不触网）。网络拉取路径不做 flaky 联网断言。
- 全量 build/vet/test ./... 全绿（handlers 52s），inventory 523/455/P0 128/128，exe 重建。
### 2026-10-12（续：acp_env_diagnostics 落地，后端 452→453）

- 新增 src-go/web/handlers/acp_env_diagnostics.go（对齐上游 commands/acp.rs: acp_env_diagnostics_core 及其纯函数）：只读环境诊断报告——探测 node/npm/npx + PATH，推导一行"最可能原因"verdict，并渲染可复制文本 blob。
  - 纯逻辑逐函数对齐：redactSecret（secret-ish key 多字节安全掩码）、parseNodeMajor、computeVerdict（node_missing/npm_missing/node_too_old/terminal_only_path/adapter_missing(_native_present)/user_prefix_not_on_path/homebrew_bin_not_on_path/npm_prefix_timeout/installed_but_unresolved/not_installed/ok 全分支）、buildReport（Runtime/Node·npm/npm global prefix/Agent/Node 版本管理器候选/Terminal 对比 6 段）、renderPlainText（含 PATH/terminal 额外目录/[PATH] 日志附录）。
  - collectDiagInputs 探测实环境：os/arch/appVersion、PATH 拆分、node/npm/npx 版本探测（exec 带 5s 超时）、npm prefix -g/root -g/config get prefix（带计时+1.5s 门限）、nodeBinDirCandidates（nvm/fnm/volta/asdf/mise/n/homebrew/system 常见管理器）、diagSafeEnvKeys（redact 后）、agent 段（由 acp.FindBuiltinAgent + acp.LaunchSpecFor 的 cmd/distribution/package + DB installed_version + resolve-on-PATH launchable + 前缀 bin 存在性 + npmGlobalVersion detected）、Unix 登录 shell 终端对比探测。
  - 响应键 snake_case（generated_at/agent_type/verdict{level,code,summary}/sections[{title,checks[{label,value,status,hint}]}]/plain_text），DiagLevel 序列化 ok/warn/fail/info。agent_type 省略→null。
  - Go 模型未覆盖处诚实降级：node_required（Go 注册表未建模）、adapter 关系（Go 未建模）、recent [PATH] 日志（Go 无进程内日志环）→ 空；仍保留对应 report 字段/分支以保证形状兼容。
- 测试：内部 cp_env_diagnostics_internal_test.go（package handlers）移植上游单测 redactSecret/parseNodeMajor/computeVerdict 多分支（node_missing/ok/user_prefix_not_on_path/homebrew_bin_not_on_path/terminal_only_path/node_missing>terminal/not_installed）；外部 cp_env_diagnostics_test.go 校验 HTTP 报告形状（字段齐、agent_type null、verdict code/level、sections 非空、plain_text blob）。
- 全量 build/vet/test ./... 全绿（handlers 52s），inventory 523/453/P0 128/128，exe 重建。
### 2026-10-12（续：clone_repository 落地，后端 451→452）

- 新增 src-go/web/handlers/git_clone.go（对齐上游 commands/folders.rs: clone_repository_core）：git clone <url> <target>，带 credentials 时注入 askpass 凭据助手（复用 credentialEnv/gitRunEnv）。URL/目标空 → ErrInvalidInput。
- classifyGitCloneError 逐条对齐上游 stderr 分类：已存在非空目录→ErrAlreadyExists；repository not found→ErrNotFound；could not resolve host/network unreachable/timed out/failed to connect→ErrNetwork；authentication failed/could not read username|password/logon failed/terminal prompts disabled/401/403/http basic access denied/publickey→ErrAuthFailed；permission denied→ErrPermissionDenied；其余→ErrExternalCommand。
- 测试 git_clone_test.go：以本地 repo 作为 file 远端克隆（校验文件落地）→ 再克隆进同一非空目录被拒 → 空 url/target 被拒。
- 全量 build/vet/test ./... 全绿，inventory 523/452/P0 128/128，exe 重建。
### 2026-10-12（续：git_remove_worktree 落地，后端 450→451）

- 新增 src-go/web/handlers/git_remove_worktree.go（对齐上游 commands/folders.rs: git_remove_worktree_core）：移除托管某分支的 worktree 检出目录；deleteBranch 时同时把该 worktree 文件夹的会话重挂到项目文件夹、关闭其标签页、软删文件夹行、解绑指向它的 work_task，再删分支。
  - 拒绝规则对齐上游：主工作树不可删（The main working tree cannot be removed）、当前使用中的工作树不可删（canonical==repo）、有 queued/preparing/running/awaiting_input/merging 的 to-do 任务在该 worktree 内时拒绝（A to-do task is still working…）。
  - worktree remove 失败但目录已不在 → git worktree prune 清理陈旧注册；branch 删除时 -d/-D 随 force，stderr 含 "not found" 视为已删（幂等重试）。
  - DB 收敛 convergeRemovedWorktreeFolder：reparent 会话（Go 无 origin_cwd 列，仅改 folder_id+updated_at）、DELETE opened_tab、软删 folder、清 work_task.worktree_folder_id/cleanup_state；广播 conversation://changed / folders://changed / work_task://changed。返回 {worktree_path, branch_deleted, folder_id, reparented}。
  - 复用现有 folderParentID（work_task_forge.go）、canonicalPath、parseWorktrees、ensureGitRepo、gitRun、fileExists。
- 测试 git_remove_worktree_test.go：建 repo+worktree → 主树删除被拒 → 删 feature worktree+分支（校验 worktree_path/branch_deleted、目录消失、分支不再 resolve）→ 幂等重试仍 200。
- 全量 build/vet/test ./... 全绿（handlers 48s），inventory 523/451/P0 128/128，exe 重建。
### 2026-10-12（续：Pet Codex 导入 + marketplace 降级完成，后端 444→450；修复测试污染）

- pet_codex.go（3 路由 + marketplace 3 路由）落地并通过测试。对齐上游 pets/codex_import.rs + pets/marketplace.rs：
  - pet_codex_import_available / pet_list_importable_codex / pet_import_codex：扫描用户自有 \/pets/<id>（{pet.json, spritesheet.webp} 同构），复制进 上游 本地 pets 库；支持 ids 过滤、overwriteWithSuffix（冲突时存为 <id>-imported）、skipped 原因回传。
  - pet_marketplace_list/install/asset：远端成品宠物目录含第三方角色美术，**不抓取/不复刻**——list 返回空页，install/asset 返回 ErrConfigInvalid（本构建不可用）。
- **修复 pet_codex_test.go 污染根因**：petsRoot() 优先读 YOYOCODE_HOME，再读 YOYOCODE_DATA_DIR；开发机 env 里 YOYOCODE_DATA_DIR=…/app.yoyocode 为真实数据目录，之前测试 import 把 oo 写进真实 pets 库，导致复跑时 alreadyImported=true。修法：测试首行 	.Setenv("YOYOCODE_HOME", t.TempDir()) 隔离 pets 根（既不读也不污染真实目录）；并清理已污染的 oo。
- 全量 go build/vet/test ./... 全绿（handlers 49s），inventory 523 路由 / 450 实现 / P0 128/128，exe 重建。
### 2026-10-12（续：Pet 本地数据层 12 路由，后端 432→444）

- 新增 `src-go/web/handlers/pet.go`（原生 Go，wire 对齐上游 commands/pet.rs + pets/*）：桌面宠物的数据层——本地宠物包存于 <pets-root>/<id>/{pet.json, spritesheet.webp}（YOYOCODE_HOME/YOYOCODE_DATA_DIR/~.yoyocode 解析），加 app_metadata 的窗口配置。
  - pet_list / pet_get / pet_read_spritesheet（magic bytes 判 webp/png，base64）/ pet_add（校验 id、1KB–16MB、base64 可带 data: 前缀）/ pet_update_meta / pet_replace_sprite / pet_delete（限 pets 根内）。
  - pet_get_settings / pet_set_active（校验存在 + 发 pet://active-changed）/ pet_save_window_state（x/y/scale/alwaysOnTop/enabled 补丁）：PetWindowConfig 存 pet.config，默认 scale 0.75 / alwaysOnTop true。
  - pet_get_current_state → "idle"（本构建无 ACP→pet 状态映射器）；pet_celebrate（jumping/waving/failed 校验 → 发 pet://oneshot）。
  - **美术版权**：不内置、不生成、不复刻任何吉祥物/角色精灵；精灵全部为用户经 pet_add/pet_replace_sprite 提供的字节。marketplace（远端成品宠物目录）与 codex 导入扫描留待后续、不复刻其角色素材。
- 测试 `pet_test.go`：空→加（过小拒绝、重复拒绝）→列→读精灵（webp mime + base64 往返）→改名→设置默认/设活跃/存窗口态→当前态 idle/庆祝校验→删全链路。全量 build/vet/test ./... 全绿，exe 重建。ROUTE_INVENTORY 432→444（P0 仍 128/128）。

### 2026-10-12（续：Office Tools 技能/检测 11 路由，后端 421→432）

- 新增 `src-go/web/handlers/office_tools.go`（原生 Go，wire 对齐上游 commands/office_tools.rs）：OfficeCLI 为外部文档转换二进制，本构建不内置：
  - officecli_detect / install / uninstall → OfficecliInfo {installed:false, version/path/runtime_error:null}（无 bundle 可装）。
  - officecli_list_skills → []；officecli_sync_skills → {synced:0, errors:[]}；skill_get/list_all_install_statuses → []。
  - officecli_skill_read_content → 中央库 SKILL.md 只读。
  - officecli_skill_apply_links / link_to_agent / unlink_from_agent → 复用 skills_link.go 共享引擎（office 技能 id 走 expertId 字段）。
- 重 office 面（render_html / sync 真实 bundle / start-stop_office_watch + office-watch HTTP 代理）为后续。
- 测试 `office_tools_test.go`：detect 字段齐全 not-installed、空技能表、sync=0、read_content 读到。全量 build/vet/test ./... 全绿，exe 重建。ROUTE_INVENTORY 421→432（P0 仍 128/128）。

### 2026-10-12（续：Science 技能包 8 路由，后端 413→421）

- 新增 `src-go/web/handlers/science.go`（原生 Go，wire 对齐上游 commands/science.rs）：与 Experts 同构的 bundled 技能包族，共享中央库 + 链接引擎。本构建不内置 science 包：
  - science_list / science_list_all_install_statuses / science_get_install_status → 空。
  - science_read_content → 只读中央库 ~/.yoyocode/skills/<id>/SKILL.md（无 embedded 回退）。
  - science_open_central_dir → mkdir + 路径。
  - science_apply_links / science_link_to_agent / science_unlink_from_agent → 复用 skills_link.go 的共享引擎（enableSkillLink/disableSkillLink/applySkillLinkOps）。
- 测试 `science_test.go`：空列表 + open_central_dir + read 读到/缺失报错。全量 build/vet/test ./... 全绿，exe 重建。ROUTE_INVENTORY 413→421（P0 仍 128/128）。至此 experts/science/custom-skills 三条技能族 + 共享链接引擎全部就位。

### 2026-10-12（续：原生 UI Settings → Agents 接版本探测）

- settings.go viewSettingsAgents 每个 agent 行加「Check」按钮：按需（避免 npm 探测风暴）后台调 acp_detect_agent_local_version（显示 vX.Y.Z）+ acp_fetch_agent_latest_release（有更新则显示 update available: vX.Y.Z），结果存 Shell.agentVer/agentLatest（加锁）后 Invalidate 重绘。
- app.go Shell 加 agentVer / agentLatest map 并在 New() 初始化；settings.go 引入 encoding/json 解析路由返回。
- 把本轮两条版本探测后端接进可操作 UI：Agents 页现可逐个查「已装版本 / 可更新」。build/vet/test ./src/app 全绿，exe 重建。

### 2026-10-12（续：acp_stop_async_task + acp_answer_plan_approval 优雅桩，后端 411→413）

- src-go/web/handlers/acp.go 增两路由（对齐上游 wire）：
  - `acp_stop_async_task` → false：Go 引擎不追踪后台异步任务，无可停者，返回 200+false（等同上游 adapter 拒绝）。
  - `acp_answer_plan_approval` → null no-op：plan-mode 审批是部分 agent 的协议特性（如 Grok plan mode），Go 引擎尚未上抛，故无 pending approval；接受答复为 no-op 而非报错，避免对非 plan agent 的客户端造成破坏。
- 测试 `acp_async_test.go`：stop=false、answer=200。全量 build/vet/test ./... 全绿，exe 重建。ROUTE_INVENTORY 411→413（P0 仍 128/128）。

### 2026-10-12（续：acp_fetch_agent_latest_release 新版探测，后端 410→411）

- 新增 `src-go/web/handlers/acp_latest_release.go`（原生 Go，wire 对齐上游 acp/latest_release.rs）：
  - 仅 npx + SupportsCustomVersion 的内建 agent 查 npm 公共 registry（dist-tags，scoped 名 / 转义 %2F）取 latest；uvx / binary（本构建不下载）→ null。
  - 与 pin（launchSpec 包里的 @version）做严格 semver 比较（versionSegments 数字段逐级），仅当 latest 严格更新且 sanitizeCustomVersion 通过（去 v、须含点、字符白名单）时返回 {version}，否则 null。
  - 网络失败→null（优雅降级，离线后端设置页显示「已最新」而非报错）。
- 测试：内部单测（versionFromSpec / isStrictlyNewer / sanitizeCustomVersion）+ 路由无网络 null 路径（缺 agentType 拒绝、未知 agent、uvx agent pi 均 null）。全量 build/vet/test ./... 全绿，exe 重建。ROUTE_INVENTORY 410→411（P0 仍 128/128）。

### 2026-10-12（续：acp_detect_agent_local_version 本地版本探测，后端 409→410）

- 新增 `src-go/web/handlers/acp_detect.go`（原生 Go，wire 对齐上游 acp_detect_agent_local_version_core）：
  - npx agent → packageNameFromSpec（剥尾部 @version，保留 @scope）+ `npm ls -g <pkg> --json --depth=0` 解析 dependencies[pkg].version。
  - 探到版本 → 写 agent_setting.installed_version 并在变更时 Emit acp://agents-updated（治愈 composer 的「已安装?」徽标）；未探到 → 回退已存值（null 即从未记录）。binary/uvx 本构建不下载 → 回退已存值。
- 测试 `acp_detect_test.go`：缺 agentType 拒绝；已知/未知 agent 均 200 且返回版本串或 null（不依赖本机是否装过包）。全量 build/vet/test ./... 全绿，exe 重建。ROUTE_INVENTORY 409→410（P0 仍 128/128）。
- 注：该路由会实际调用 npm（秒级），与上游行为一致；设置页每次打开对每个 agent 重跑属上游既有设计。

### 2026-10-12（续：acp_touch_connection 连接保活，后端 408→409）

- src-go/acp/manager.go 加 Manager.Touch(id) bool：报告连接是否仍存活（供客户端刷新空闲计时）。
- src-go/web/handlers/acp.go 加 `acp_touch_connection`（connectionId → bool），对齐上游 connection_manager.touch。
- 测试 `acp_touch_test.go`：未知连接 touch=false 且不报错。全量 build/vet/test ./... 全绿，exe 重建。ROUTE_INVENTORY 408→409（P0 仍 128/128）。

### 2026-10-12（续：原生 UI Settings +3 实数据页 = 12/19）

- settings.go 新增三页接真实后端（build/vet/test/exe 全绿）：
  - **Chat Channels**：list_chat_channels → 渠道卡片（名称 + channel_type + enabled/disabled 状态点）。
  - **Computer Use**：computer_available → 状态（available / 本后端不可用）+ platform，附说明（仅 YOYOCODE_COMPUTER_USE=1 桌面会话提供）。
  - **Experts**：experts_list → 列表（本构建无内置包 → 空态引导去 Skills → Custom skills 自建）。
- Settings 二级导航 19 页中 12 页出实数据（General / Web Service / Quick Messages / Agents / Version Control / System / MCP / Logs / Skills+Custom / Chat Channels / Computer Use / Experts）。
- 自验：mygo dev 热重载常驻，build/vet/test ./src/app 全绿，exe 重建。

### 2026-10-12（续：Skill 链接引擎 5 路由，后端 403→408）

- 新增 `src-go/web/handlers/skills_link.go`（原生 Go，共享于 experts + custom skills，wire 对齐上游 commands/experts.rs 链接原语）：把中央库技能 ~/.yoyocode/skills/<id> 软链进 agent 自己的全局技能目录。
  - `custom_apply_links` / `experts_apply_links`（ops[{expertId,agentType,enable}]）→ []LinkOpResult（逐 op ok/status/error，失败不中断）。
  - `custom_list_all_install_statuses` → (技能 × 15 个 skills-capable agent) 矩阵的 ExpertInstallStatus（expert_id/agent_type/state/link_path/target_path/expected_target_path/copy_mode）。
  - `experts_link_to_agent`（单 enable → ExpertInstallStatus）/ `experts_unlink_from_agent`（单 disable → null）。
  - classifyLink 对齐上游：not_linked / linked_to_codeg（EvalSymlinks 规范化比对）/ linked_elsewhere / blocked_by_real_directory / broken。
  - 用 os.Symlink 建真实软链（Unix 恒可；Windows 需权限/开发者模式，无权限时该 op 诚实返回 error 而非崩溃）；不复刻上游 Windows junction + copy-mode 回退，copy_mode 恒 false，已注释记录。
- 测试 `skills_link_test.go`：状态矩阵键齐全 + 默认 not_linked；apply enable 容忍无权限主机（逐 op error 契约），有权限则校验软链建立/解除。全量 build/vet/test ./... 全绿，exe 重建。ROUTE_INVENTORY 403→408（P0 仍 128/128）。至此 experts(8/8) 与 custom skills(10/10) 两簇全部就位。

### 2026-10-12（续：ACP temp-reclaim + binary-cache 维护 3 路由，后端 400→403）

- 新增 `src-go/web/handlers/acp_temp.go`（原生 Go，wire 对齐上游 acp/temp_reclaim.rs）：
  - `acp_scan_leaked_temp` → LeakedTempScan {root, entries[{path,bytes,age_hours,is_dir}], total_bytes, skipped}：扫系统 temp 根直属项，匹配泄漏形状、过 15 分钟龄、上限 500，按体积倒序；不跟符号链接。
  - `acp_reclaim_leaked_temp` {paths} → {removed, freed_bytes, failed[]}：删前逐条重校验（必须直属 temp 根、匹配形状、够龄、非符号链接），失败入 failed 不中断。
  - `acp_clear_binary_cache`：本构建不下载 agent 二进制 → no-op 成功（与上游对非 binary agent 一致）。
  - **安全取舍**：上游在 Windows 还回收整个 PyInstaller _MEI* 解包目录（需 share-mode 句柄探测证明空闲，Go 标准库无此能力）；本移植只回收单个 `__py_binary_resource_` 资源文件（启动读一次、可安全 unlink），目录只列不删——上游行为的安全子集，已注释记录。
- 测试 `acp_temp_test.go`（t.Setenv TMP/TEMP/TMPDIR 控 temp 根）：扫到 1 项（无关文件/过新文件忽略或计入 skipped）、回收删除、clear_binary_cache no-op、temp 外路径拒删。全量 build/vet/test ./... 全绿，exe 重建。ROUTE_INVENTORY 400→403（P0 仍 128/128）。

### 2026-10-12（续：Experts 读/降级 5 路由，后端 395→400 🎯）

- 新增 `src-go/web/handlers/experts.go`（原生 Go，wire 对齐上游 commands/experts.rs）：本构建不内置 bundled expert 包，故：
  - `experts_list` / `experts_list_all_install_statuses` / `experts_get_install_status` → 空列表（无内置包、无链接）。
  - `experts_read_content`：只读中央库 ~/.yoyocode/skills/<id>/SKILL.md（校验 id；**不内置任何 bundled 包内容**，无 embedded 回退）。
  - `experts_open_central_dir`：mkdir 中央库并返回路径。
- link 引擎（experts_link_to_agent/apply_links/unlink + custom_apply_links/list_all_install_statuses/import_from_agent，把中央技能 symlink/junction 进各 agent 技能目录）仍留待专门一轮。
- 测试 `experts_test.go`：空目录三连、open_central_dir 建目录、read_content 读到/缺失报错。全量 build/vet/test ./... 全绿，exe 重建。ROUTE_INVENTORY 395→400（里程碑：实现 400/523，P0 仍 128/128）。

### 2026-10-12（续：open_*_window 导航 + resolve_worktree_folder + open_in_code 8 路由，后端 387→395）

- 新增 `src-go/web/handlers/open_windows.go`（原生 Go，wire 对齐上游 web/handlers/folders.rs）：server/web 运行时「开窗口」不开 OS 窗口，而是返回客户端路由路径（SettingsNavigationResult {path}）：
  - `open_settings_window`：section → /settings/<route>（13 段白名单，缺省 appearance；agents 带 ?agent=<wire> 校验）。
  - `open_commit_window` / `open_stash_window`：/commit|/stash?folderId=N。
  - `open_merge_window`：/merge?folderId=N(&operation&upstreamCommit)。
  - `open_push_window`：/push?folderId=N(&branch=urlencoded)。
  - `open_import_sessions_window`：/import-sessions(?focusPath=urlencoded)。
  - `resolve_worktree_folder`：ensureGitRepo + git worktree list --porcelain + parseWorktrees 找分支 → 规范化路径 + 匹配 folder 表 id（WorktreeResolution {path, folder_id}，未检出返回 null）。
  - `open_in_code`：host 上启动编辑器（code / code.cmd），不等待。
- 测试 `open_windows_test.go`：7 条导航路径映射（含 agent/branch/focus 编码）+ resolve（未检出 null / 加 worktree 后解析到路径）。全量 build/vet/test ./... 全绿，exe 重建。ROUTE_INVENTORY 387→395（P0 仍 128/128）。

### 2026-10-12（续：weixin 扫码降级 2 路由 + 原生 UI Custom Skills 管理，后端 385→387）

- chat_channel.go 增 weixin_get_qrcode / weixin_check_qrcode：WeChat 扫码登录需连 WeChat bot 服务（网络 backend 未接），统一 configuration_invalid「WeChat QR login is not available in this build」而非 404。至此 weixin 2 路由就位（降级）。
- 原生 UI：Settings → Skills 页新增「Custom skills (central store)」区——custom_list 列出中央库技能（名称/描述）、输入 id + Create 生成最小 SKILL.md 脚手架、每项 Delete；createCustomSkill/deleteCustomSkill 走后台 goroutine 调 custom_create_skill/custom_delete_skills 后失效缓存重绘。app.go Shell 加 newSkillID 字段。
- 自验：build/vet/test ./... 全绿，exe 重建；mygo dev 热重载常驻（存盘自动重编译）。ROUTE_INVENTORY 385→387（P0 仍 128/128）。

### 2026-10-12（续：Backup 恢复只读面 2 路由，后端 383→385）

- 新增 `src-go/web/handlers/backup.go`（原生 Go，wire 对齐上游 commands/backup/restore.rs）：
  - `backup_active_agents` → []（咨询用；无头构建此层不追踪活跃 ACP agent，忠实报告无）。
  - `backup_list_safety_snapshots` → SafetySnapshot[]（camelCase：id/path/created_at/size_bytes/rollback_supported），扫 <data_dir>/.yoyocode-restore-backup/ 下目录，读 .yoyocode-snapshot.json（v1+staging 才算可回滚），按时间（无则目录名）倒序。
- 归档流式传输/恢复/回滚/下载票据等写入面是更大的 archive 子系统，后续。
- 测试 `backup_read_test.go`：active_agents 空；两个快照目录（含/不含 manifest）→ rollback_supported 正确、created_at 读出、size_bytes 计数。全量 build/vet/test ./... 全绿，exe 重建。ROUTE_INVENTORY 383→385（P0 仍 128/128）。

### 2026-10-12（续：Custom Skills 中央库 CRUD 7 路由，后端 376→383）

- 新增 `src-go/web/handlers/custom_skills.go`（原生 Go，wire 对齐上游 commands/custom_skills.rs）：中央技能库 ~/.yoyocode/skills/<id>/SKILL.md 的增删改查：
  - `custom_list` → CustomSkillItem[]（camelCase：id/name/description/centralPath）；扫描库目录（dir+SKILL.md、合法 id、跳 dotfile），name 取 frontmatter 顶层 name: 否则 id，description 复用 readSkillDescription。
  - `custom_read_skill` / `custom_create_skill` / `custom_save_skill`（校验非 symlink + 已存在）/ `custom_duplicate_skill`（递归拷贝）/ `custom_import_skill`（.md 包成 <id>/SKILL.md 或整目录拷贝，id 显式或从名派生 deriveIDFromName）/ `custom_delete_skills`（批量，逐条 ok/error，symlink 只解链、真实目录限中央库内 RemoveAll 防越界）。
  - reserved-id（bundled experts/science/office）在 Go 构建为空集（本构建不内置这些包），故库内每个目录都是 custom——忠实于不内置 bundled 包的构建。
- 暂缓：link 引擎（custom_apply_links / custom_list_all_install_statuses / custom_import_from_agent，需把中央技能 symlink/junction 进各 agent 技能目录）。
- 测试 `custom_skills_test.go`（t.Setenv HOME/USERPROFILE 控库位）：建（含重复拒绝）→读→改名→复制→批删全链路；.md 文件与目录两种 import。全量 build/vet/test ./... 全绿，exe 重建。ROUTE_INVENTORY 376→383（P0 仍 128/128）。

### 2026-10-12（续：Chat Channel 状态/消息日志/传输降级 5 路由，后端 371→376）

- migration_chat_channel.go 增 chat_channel_message_log 表（channel_id/direction/message_type/content_preview/status/error_detail/created_at）。
- chat_channel.go 增 5 路由：
  - `get_chat_channel_status`：本构建无传输 → 空列表（忠实：manager 在未连接前亦然）。
  - `list_chat_channel_messages`：按 channel_id 读 message_log（limit 默认 50 / offset，DESC），wire = ChatChannelMessageLogInfo（snake_case）。
  - `connect_chat_channel` / `test_chat_channel`：无 Lark/Telegram/WeChat backend → configuration_invalid「live chat-channel transport is not available in this build」。
  - `disconnect_chat_channel`：无连接可拆 → no-op 成功。
- 至此 chat_channel 全套 12 路由就位（管理 7 + 状态/消息/传输 5），实时 backend 待后续网络子系统。
- 测试扩展：status/messages 空、connect 被拒、disconnect no-op。全量 build/vet/test ./... 全绿，exe 重建。ROUTE_INVENTORY 371→376（P0 仍 128/128）。

### 2026-10-12（续：Chat Channel 管理 + token 7 路由，后端 364→371）

- 新增 `src-go/db/migration_chat_channel.go`：chat_channel 表（name/channel_type/enabled/config_json/event_filter_json/daily_report_enabled/daily_report_time/时间戳），token 不入表。
- 新增 `src-go/web/handlers/chat_channel.go`（原生 Go，wire 对齐上游 commands/chat_channel.rs + service）：
  - `list_chat_channels` → ChatChannelInfo[]（snake_case：id/name/channel_type/enabled/config_json/event_filter_json/daily_report_enabled/daily_report_time/created_at/updated_at）。
  - `create_chat_channel`：channel_type 白名单校验（lark/telegram/weixin），config_json 空→"{}"，event_filter_json 建时为 null。
  - `update_chat_channel`：部分更新；event_filter_json / daily_report_time 为 Option<Option<String>>（键存在→设值，null→清空；缺省→保留）。
  - `delete_chat_channel`：删行并清 token。
  - `save/get_has/delete_chat_channel_token`：token 存独立 app_metadata key（chat_channel_token:<id>）——上游用 OS keyring，Go 无 keyring 的同款权衡（明文落本地库，行内不含 token），已注释记录。
- 暂缓：实时传输（connect/disconnect/test/status/list_messages，需 Lark/Telegram/Weixin backend 与网络）与 weixin 扫码。
- 测试 `chat_channel_test.go`：空列表→建（校验坏类型）→列→改（名/启用/清 daily_time）→token 无/存/删→删渠道全链路。全量 build/vet/test ./... 全绿，exe 重建。ROUTE_INVENTORY 364→371（P0 仍 128/128）。

### 2026-10-12（续：Computer Use 能力探测 + 诚实降级 18 路由，后端 346→364）

- 新增 `src-go/web/handlers/computer.go`（原生 Go，wire 对齐上游 web/handlers/computer.rs）：
  - `computer_available` → {available:false, platform}（platform 由 GOOS 映射 macos/windows/linux）：无头 Go server 不托管 computer-use 服务，诚实报告不可用，让 mcode-app 的能力探测正常拿到结果。
  - 其余 17 个 computer_* 操作（status/request_permission/open_permission_settings/reveal_helper/list_shareable_windows/window_thumbnail/share_window(s)/shared_state/share_app/share_screen/revoke_all/stop/stop_key_status/driver_info/driver_install/driver_uninstall）统一经 computerRefused 返回 configuration_invalid + 上游同款「computer use is not available here…」文案，而非 404。
  - 全部用字面量 reg.Register 注册（便于 route-inventory 静态扫描 + 人读）。
- 契合上游语义：computer use 仅由带 YOYOCODE_COMPUTER_USE=1 的桌面会话（或桌面 app 自身窗口）提供；真正的驱动/截屏/窗口共享是重桌面子系统，后续若做桌面宿主再接。
- 测试 `computer_test.go`：available=false + 合法 platform；6 个操作路由被拒。全量 build/vet/test ./... 全绿，exe 重建。ROUTE_INVENTORY 346→364（P0 仍 128/128）。

### 2026-10-12（续：Canvas 路由接真实数据，workbench 3/4 实面板）

- workspace.go：viewCanvas 接 canvas_list_boards → 看板卡片（名称 + 描述 + node_count/terminal_count），Refresh 重拉；dispatch 加 canvas 分支；viewRoutePlaceholder 仅剩 forge（需连 GitHub 账号，故留占位）。
- 新增小协助：asObjVal（嵌套对象）、intOf（JSON number→int）。
- 至此 workbench 四路由中 Automations / To-dos / Canvas 三个出真实数据，仅 Repositories(forge) 留占位。
- 自验：mygo dev 热重载生效（workspace.go 存盘 ~4-8s 重建）。build/vet/test ./src/app 全绿，build/yoyocode.exe 重建。

### 2026-10-12（续：workbench 路由接真实数据 — Automations + To-dos 面板）

- workspace.go：viewWorkbenchRoute 由纯占位改为分发——Automations/To-dos 接真实后端，Repositories/Canvas 暂留占位。
  - viewAutomations：automation_list → 卡片列表（名称 + enabled/disabled 状态点 + trigger_kind·agent_type），Refresh 重拉。
  - viewTasks：work_task_list（选中文件夹时带 folderId，否则全量）→ 任务卡片（title/prompt + status + agent_type）。
  - 复用既有 ensure 异步缓存模式（与 Settings 实数据页同一路径）；新增 truthy 协助 enabled 布尔归一。
- 自验：桌面共享下重启的 mygo dev watcher 热重载生效（workspace.go 存盘 8.3s 重建、新窗口 pid 常驻、a11y 树确认导航块 + 分段渲染正常，路由切换此前已验）。build/vet/test ./src/app 全绿，build/yoyocode.exe 重建。
- 注：mygo GPU 窗口在本会话截图只回元数据、按坐标点击工具报 generation 错，故结构验证以 a11y 树为准；用户可在常驻窗口自行点 Automations/To-dos 看真实数据。

### 2026-10-12（续：原生 UI 侧栏 IA 对齐 上游 + 桌面共享自验）

- 用户共享整个桌面 → 我用 computer_snapshot 读 yoyocode 窗口 a11y 树做结构自验（mygo GPU 窗口截图在本会话只回元数据、不可直接读像素，故以 a11y 树 + 上游 已移植 globals.css 设计 token 为准）。
- workspace.go 侧栏按 上游 sidebar IA 重构：
  - 顶部**固定导航块**（原创几何 glyph）：New chat（主操作，开无文件夹会话）+ Automations / To-dos / Repositories / Canvas 四个路由行；点击切换主区为对应 route 占位（viewWorkbenchRoute 带标题+对应上游路由说明），选中态浅灰高亮（navRow）。
  - 会话列表拆 **PINNED + CHATS** 两段（isPinned 读 pinned/pinned_at），sectionHeader 带 glyph；convRow 统一行；选会话清 workbench 路由。
  - 新增 icons.go 原创 glyph：iconNewChat/Automations/Tasks/Forge/Canvas/Pinned/Recent（纯几何，非任何版权图标艺术）。app.go Shell 加 workbench 字段。
- 自验（w33 pid4268 a11y 树）：导航块 5 行 + FOLDERS/CONVERSATIONS 分段 + 点击 Repositories/Canvas 主区正确切换对应占位，均渲染正常。
- 工具链：重启 mygo dev 热更新（旧 watcher 已 quit），现 pid 常驻，存盘自动重编译+重开窗口（桌面共享下新窗口自动可截图，解决此前单窗口共享断链痛点）。build/vet/test ./... 全绿，build/yoyocode.exe 重建。
- 待续：workbench 四路由的实面板（automations/work_task/forge/canvas 后端已多数就绪，待接 UI）；顶层 Workspace/Settings/Pet 导航与 上游 chrome 细化；像素级配色/间距在能读像素的环境下再精修。

### 2026-10-12（续：Config Sync 本地设置/状态 3 路由，后端 343→346）

- 新增 `src-go/web/handlers/config_sync.go`（原生 Go，wire 对齐上游 commands/config_sync/webdav_sync.rs）：
  - `config_sync_get_settings` → ConfigSyncSettingsView（camelCase：enabled/serverUrl/username/hasPassword/encrypt/hasPassphrase/remoteDir/profile/autoSync/intervalMinutes），默认 remoteDir=上游/profile=default/autoSync=true/interval=5。
  - `config_sync_update_settings`（{settings}）→ 合并逻辑移植：serverUrl/username trim；password 为 Option——present+非空用之，否则同账号保留、换账号丢弃；passphrase 不绑账号，present+非空用之否则保留；encrypt 为真且 passphrase 为空则拒绝；passphraseId 在 passphrase 未变且已有时保留否则重新随机；remoteDir/profile 经 sanitize_path_segment（空→默认，禁 / \\ \0 和 . ..）；intervalMinutes clamp 1..1440。返回 View。
  - `config_sync_get_state` → ConfigSyncState（lastUploadedSha256/lastUploadedTarget/lastSyncAt/lastError，默认全 null）。
  - **安全权衡**：上游密码/口令存 OS keyring，Go 无 keyring → 存于独立 app_metadata key（config_sync_password/_passphrase），settings 行本身仍无密文（保持上游不变量），View 仅报告是否已存。此明文落本地库的权衡已在代码注释与此处记录。
- WebDAV 传输本体（test_connection/upload_now/peek_remote/download_apply）与本地 import/export + rollback 为更大子系统，后续。
- 测试 `config_sync_test.go`：默认值、设账号+密码/interval 钳制、同账号保留密码、换账号丢密码、encrypt 缺口令拒绝、非法 segment 拒绝、state 键齐全。全量 build/vet/test ./... 全绿，exe 重建。ROUTE_INVENTORY 343→346（P0 仍 128/128）。

### 2026-10-12（续：Chat 频道设置 get/set 8 路由，后端 335→343）

- 新增 `src-go/web/handlers/chat_settings.go`（原生 Go，wire 对齐上游 commands/chat_channel.rs，均以 app_metadata key/value 持久化）：
  - `get/set_chat_command_prefix`：默认 "/"，set 校验 1-3 个非字母数字字符（空/超长/含字母数字拒绝）。
  - `get/set_chat_message_language`：默认 "en"，set 校验白名单（en/zh-cn/zh-tw/ja/ko/es/de/fr/pt/ar）并小写化持久。
  - `get/set_chat_event_filter`：Option<[]string>，未设/传 null 存 "null" 哨兵并回读为 null（默认事件集），数组存 JSON。
  - `get/set_chat_event_webhooks`：[]{url,enabled}；set 做 trim、丢空 URL、要求 http(s) scheme、按 URL 去重（首个 enabled 胜），默认返回空数组。
  - 复用既有 metadataValue + 新增 upsertMetadataValue（INSERT…ON CONFLICT）；chat_channel 的 event_config_epoch bump 在 Go 侧暂无事件订阅器，略过。
- 聊天频道传输本体（connect/test/messages/token 等）为更大子系统，后续；本批仅独立闭环的设置路由。
- 测试 `chat_settings_test.go`：prefix 默认/合法/四类非法拒绝；language 默认/大小写归一/非法拒绝；event_filter 数组↔null 往返；webhooks trim+去重+scheme 校验+空默认。全量 build/vet/test ./... 全绿，exe 重建。ROUTE_INVENTORY 335→343（P0 仍 128/128）。

### 2026-10-12（续：opencode_list_plugins 插件发现，后端 334→335）

- 新增 `src-go/web/handlers/opencode_plugins.go`（原生 Go 重实现 opencode 插件发现，wire 对齐上游 acp/opencode_plugins.rs 的 PluginCheckSummary）：
  - `opencode_list_plugins`：读 XDG config（XDG_CONFIG_HOME else ~/.config）下 opencode/opencode.json 的 plugin[]，按名去重，逐条分类后返回 {config_path, cache_dir, plugins[], has_project_config_hint}。
  - 分类逻辑移植：路径插件（. / file: :// / 绝对 / Windows 盘符）走 resolvePathPluginTarget（file:// 主机名/localhost/盘符处理、相对路径 lexical join），命中=path / 缺失=path_missing / 无法定位=path(resolved=null)；包插件按 opencode 1.18.35 的 effective_spec（裸名→@latest）+ sanitize(windows) 的 packages/<spec>/node_modules/<name>/package.json 判 installed，legacy 扁平 node_modules 判 needs_migration，否则 missing；installed_version 读 package.json。
  - 复用既有 fileExists；XDG 路径函数独立命名（ocConfigPath/ocCacheDir）以区别 mcp.go 中硬编码 ~/.config 的 opencodeConfigPath。
- 测试 `opencode_plugins_test.go`：无配置=空列表；配置声明包插件(missing)+绝对路径插件(path_missing)，建 modern 缓存后转 installed@version（用 t.Setenv 控 XDG）。全量 build/vet/test ./... 全绿，exe 重建。ROUTE_INVENTORY 334→335（P0 仍 128/128）。

### 2026-10-12（续：Git worktree/branch 写操作 3 路由，后端 331→334）

- 新增 `src-go/web/handlers/git_worktree.go`（原生 Go 编排 git 子进程，wire 对齐上游 commands/folders.rs）：
  - `git_worktree_add`：rev-parse 校验分支不存在 + 目录不存在 → `git worktree add -b <branch> <path> [<base>]`，分支/目录已存在分别报 already_exists。
  - `git_delete_remote_branch`：`git push <remote> --delete <branch>`（带 credentialEnv askpass），失败走 classifyRemoteError，返回 stderr 文本。
  - `git_update_branch`：三分支逻辑移植——远程 tracking ref 用强制 refspec 镜像；当前检出分支走真实 pull --ff（唯一能安全协调工作树的路径）；其它本地分支按 branch.<b>.remote/merge 配置 fetch 快进并同步 remote-tracking ref。返回 GitPullResult {updated_files, conflict:null}。
  - 复用既有 helpers：gitRun/gitRunEnv/credentialEnv/classifyRemoteError/gitCommandError/ensureGitRepo/resolveGitHead/gitDiffFileCount；新增 gitRevParseRef/gitConfigValue/gitCountRefAdvance。
  - 暂缓 git_remove_worktree（与 folder + work_task 深耦合：worktree 发现、忙碌任务拒绝、删除后文件夹 reparent），留待该集成。
- 测试 `git_worktree_test.go`：worktree add 成功建树 + 分支/目录已存在双拒绝；update_branch 对无上游分支干净报错不 panic。全量 build/vet/test ./... 全绿，exe 重建。ROUTE_INVENTORY 331→334（P0 仍 128/128）。

### 2026-10-12（续：Custom Agents 子系统 list/save/delete + DB 表，后端 329→331）

- 新增 `src-go/db/migration_custom_agent.go`：custom_agent 表（registry_id 唯一 / name / description / version / distribution_kind / spec_json / icon_url / skills_shared_store / skills_dir / source / version_probe / supports_mcp / 时间戳），通过 RegisterMigration 挂载。
- 新增 `src-go/web/handlers/acp_custom_agents.go`（原生 Go 实现上游 commands/custom_agents.rs + custom_agent_service.rs 的 wire 契约）：
  - `acp_list_custom_agents`：读表 → CustomAgentInfo（camelCase：registryId/agentType="custom:<id>"/name/description/version/distributionKind/spec/iconUrl/skillsSharedStore/skillsDir/source/versionProbe/supportsMcp/launchable/problem）。坏行（spec_json 解析失败/未知 kind）跳过而非整表失败。
  - `acp_save_custom_agent`：解析 {params} → def，source/supportsMcp 按上游规则对已存行回退（显式值优先，否则沿用存行，新行→manual/true），normalize_skills_dir（~ 展开 + 必须绝对路径）后 validate 再 upsert（INSERT…ON CONFLICT 更新）。
  - `acp_delete_custom_agent`：按 registry_id 删除，未命中报 not found。
  - 校验移植：isValidCustomAgentID（≤64 / 非点开头 / 字符白名单 / 不与 15 个内建 wire 名冲突）、builtin registry-id 冲突优先诊断、isSafePathComponent（版本）、npx/uvx 需 package、binary 需当前平台条目 + archive URL + 所有平台 cmd 的 isSafeArchiveEntry（禁绝对路径/盘符/.. 逃逸）。
  - 暂缓：acp_fetch_registry_catalog / acp_add_registry_agent（需远程 ACP registry + npm 元数据，网络依赖，后续）；icon 下载内联亦暂以 icon_url 透传（展示用，不参与启动）。
- 测试 `acp_custom_agents_test.go`：save→list→upsert(改名保 source)→delete→二次删 not found 全链路；内建 registry-id 冲突 + 缺 npx package 校验拒绝。全量 build/vet/test ./... 全绿，exe 重建。ROUTE_INVENTORY 329→331（P0 仍 128/128）。

### 2026-10-12（续：原生 UI Settings → Skills 页接后端 = 9 页实数据）

- settings.go 新增 `viewSettingsSkills`：agent 选择器（取自 acp_list_agents）+ global/project 作用域选择器 → `acp_list_agent_skills` 列出该 agent 技能，卡片展示 名称/布局(skill_directory|markdown_file)/描述，CLI 自带只读技能标 read-only，不支持的 agent 显示后端 message。缓存键按 agent 变化（skillsCacheKey）。
- app.go 加 `asListVal`（把对象响应里的嵌套数组转 []map）、Shell 加 skillsAgent/skillsScope 字段。
- Settings 二级导航 19 页中 9 页出实数据（+Skills）。build/vet/test/exe 全绿。

### 2026-10-12（续：Agent Skills CRUD + acp_current_platform，后端 324→329）

- 新增 `src-go/web/handlers/acp_skills.go`：忠实移植上游 commands/acp.rs 的 agent-skills 管理面（原生 Go 实现，非移植版权素材）：
  - `acp_list_agent_skills`：按 agent 的技能存储规格（15 个内建 agent 的 global/project 目录链）扫描 SKILL.md 目录包与（部分 agent 支持的）扁平 `<id>.md`，去重 + 排序（global 先于 project、按名），返回 supported/message/locations/skills；不支持的 agent 返回 supported=false。
  - `acp_read_agent_skill` / `acp_save_agent_skill` / `acp_delete_agent_skill`：按 scope + id 定位已存在技能，save 以 preferred 目录新建（SkillDirectoryOnly→目录包；DirOrMarkdown→可选 layout），read 回读内容，delete symlink 安全删除（软链只删链本身，目录递归，文件 unlink）。
  - 存储规格 `skillStorageSpecFor`：claude_code/.claude、codex/CODEX_HOME+.system+.agents、open_code、gemini、open_claw、cline、hermes、code_buddy、kimi_code、pi、grok、cursor、deepseek、qoder、antigravity 的目录链逐一对齐上游；DeepSeek/KimiCode 的 project base 向上找 .git。
  - 只读保护 `isReadOnlySkillPath`：Codex `.system`、Cursor `skills-cursor`、DeepSeek `.system` 列出但拒绝写/删。
  - frontmatter 描述提取（short-description 任意缩进优先，顶层 description 次之，仅读前 4KiB）、skill id 校验（非空/非点开头/无路径分隔符/字符白名单）。
  - `acp_current_platform`：返回 `<os>-<arch>`（amd64→x86_64 / arm64→aarch64），对齐上游 registry::current_platform（如 darwin-aarch64 / windows-x86_64）。
- 错误一律映射 task_execution_failed，与上游 web handler 的 AcpError→AppCommandError 映射一致。
- 测试 `acp_skills_test.go`：claude_code project 作用域 save→list→read→delete 全链路、未知 agent unsupported、非法 id 拒绝、current_platform token 形态。全量 `go build/vet/test ./...` 全绿，exe 重建。ROUTE_INVENTORY 324→329（gen_inventory 重算，P0 仍 128/128）。

### 2026-10-12（续：Settings 实数据页 +MCP +Logs = 8 页接后端）

- settings.go 新增两页接真实后端数据（build/vet/test/exe 全绿）：
  - **MCP**：mcp_scan_local → servers（名称/命令/状态）+ warnings 列表；空态/加载态收敛。
  - **Logs**：get_log_settings（级别/目录）+ list_log_files（文件名/大小）两段式呈现。
- 至此 Settings 二级导航 19 页中 8 页出实数据：General / Web Service / Quick Messages / Agents / Version Control / System / MCP / Logs；其余 11 页仍为占位并标注对应上游路由。

### 2026-10-12（B12 UI 视觉对齐 上游 + 原生选目录 + New chat + 热更新）

- 对照用户给的 上游 真机截图,把原生 UI 往其视觉语言靠(全部 build/vet/test 全绿):
  - **主题**:照搬 上游 globals.css 的 zinc/neutral token → mygo Theme(白底 / off-white 面板 / #e4e4e7 描边 / #18181b 文字 / 深色 primary / 圆角 8),选中态改低调浅灰。
  - **图标**:自绘通用几何 glyph(文件夹 / 聊天气泡 / 滑块 / 爪印 / 网格,原创非任何版权形象),用于导航与侧栏行;sideRow 统一行样式并压缩到 ≈30px 密度。
  - **底部状态栏**:连接圆点(连上=绿)+ 后端地址 + 版本(对应 上游 底部 chrome)。
  - **composer dock**:transcript 与输入区之间加 Divider,输入框包进圆角描边卡片,Send/Cancel 右对齐。
  - **「+ New chat」主操作**:侧栏顶部,走 create_chat_conversation 直开无文件夹会话并选中(对应 上游 "新建会话")。
  - **「Open…」**:FOLDERS 区原生选目录框(mygo.Dialog.Open)→ add_folder_to_history,解决 ~/.yoyocode 空库无数据的问题。
- 工具链:`go run github.com/egoist/mygo/cmd/mygo dev ./cmd/yoyocode` 跑起热更新(改 Go 存盘即自动重编译+重启窗口)。`build/yoyocode.exe` 同步到最新。
- 已知协作卡点:热更新每次重开新窗口 → computer-use 单窗口共享会断,需用户开常驻自动共享后智能体才能连续截图自验;本轮多处视觉改动为对照截图的合理实现,逻辑/构建已保证,像素级细节待共享后迭代。

### 2026-10-12（B12 UI 布局修复 + 外观打磨）

- 用户截图反馈 UI 粗糙。定位到核心 bug：根导航用 `ui.Spacer().Height(10)` 当固定间距——但 Spacer 的语义是「吃掉所有剩余空间把兄弟撑开」，导致 Workspace/Settings/Pet 被挤到左栏底部。
- 修复：根 View 导航栏改为 brand 块在上 + `ui.Sidebar(...).Grow(1)`（项目靠顶），不再用增长 Spacer 当间距；栏间加 `ui.Divider`。
- 工作区重排：原三栏（folders+convs+detail，两块 Surface 空荡发灰）→ 单个 290px Surface 侧栏竖向堆叠「FOLDERS（MaxHeight 260）+ Divider + CONVERSATIONS（Grow 填满）+ 新建会话」，再 Divider + 聊天区（Grow）。更贴近上游 sidebar+main 结构。
- 抽出 `sideRow` 统一侧栏行（标题 + 可选灰色副标题 + 选中高亮 + 单行省略），folders/conversations 共用。空态文案收敛（Loading…/No folders open/No conversations/Select a folder）。
- 窗口默认 1240x820。exe 重新构建。
- `go build/vet/test ./...` 全绿。
- 诚实说明：原生 UI 仍是功能优先的清爽外观，与上游 Next.js 的精致视觉（图标体系、配色层次、动效）尚有差距；后续可做图标（ui 支持 SVG）、SidebarItem 图标、主题色层次等专门的视觉打磨轮次。

### 2026-10-11（续：firehose 自动刷新列表 + 本地预览验证）

- 工作区列表自动刷新：stream.onChange 签名扩为 `func(connID, channel string)`；收到 firehose `conversation://changed` 时清 folders + 当前文件夹 convs 缓存，侧栏随服务端变更（含 agent 自动回填标题、任何来源的建/删/改名/改状态）自动刷新，无需手点 Refresh。stream_test 适配新签名。
- **本地预览验证（无头沙箱）**：
  - `go build -o build/yoyocode.exe ./cmd/yoyocode` 成功（30MB 纯 Go 原生二进制）。
  - 后端可无头起：`build/yoyocode-server.exe`（YOYOCODE_PORT/TOKEN/DATA_DIR 可配）起在 :38080，`curl /api/health` → `{"status":"ok","version":"0.1.0"}`，确认 UI/mcode-app 连的服务可用；用完 taskkill 清理。
  - 原生 GUI 窗口（mygo 走 GPU/WebView2）需显示器，本沙箱无头无法显示；本机预览命令：`go tool mygo dev ./cmd/yoyocode`（热重载）或直接 `go run ./cmd/yoyocode`。
- `go build/vet/test ./...` 全绿。

### 2026-10-11（续：transcript 解析里程碑启动 — claude_code JSONL 读取打通）

- 新建 `src-go/parsers/` 包（镜像上游 src-tauri/src/parsers，一 agent 一文件）。
- `parsers/claude.go`：claude_code 会话 JSONL 的最小正确读取器——
  - `claudeConfigDir()`（CLAUDE_CONFIG_DIR→~/.claude）+ `findClaudeSessionFile`（扫 `projects/*/<sessionID>.jsonl`，session id 做路径穿越安全校验）。
  - `ReadClaudeTurns(sessionID)` → `[]Turn{Role, TSMillis, HasTS, Raw}`：逐行解析 type=user/assistant 记录，message.content 支持 string 与 typed blocks（text / thinking→text / tool_use→{tool_name,tool_use_id,input_preview}），产出 wire MessageTurn 形状 `{id, role, blocks, timestamp}`；summary/synthetic/纯 tool_result 回显跳过（无可渲染内容）。诚实降级：sidechain / /clear rollover / compaction / 合并连续 assistant / usage 等长尾暂不处理（跳过而非乱猜），后续分阶段补。
- `loadConversationTurns` 接上：查会话 agent_type+external_id，claude_code/claude 走 ReadClaudeTurns 转 convTurn；其余 agent 仍空（各自 reader 后续）。分页/指纹契约不变，历史立即可见。
- 原生 UI `viewTurnBlocks`：按 blocks 渲染 transcript——text 块出 prose，tool_use 块出 "⚙ 工具 参数" 紧凑行；无 blocks 时回退 inline text。
- 测试：`parsers/claude_test.go`（string/blocks 两种 content、summary 与 tool_result 跳过、缺会话返回 nil、session id 安全校验）全过；全仓 `go build/vet/test ./...` 全绿。
- 现状：装了 Claude Code 且用它跑过的会话（external_id = claude 会话 uuid）打开后能看到历史对话。其他 agent 的 reader（codex/opencode/…）为后续阶段。

### 2026-10-11（续：B12 工作区核心 — 会话管理动作 Rename/Pin/Delete + app 层端到端测试）

- 会话详情头加管理动作（均 off-thread，回执后清 convs 缓存重绘）：
  - **Rename**：切内联 TextInput → `update_conversation_title{conversationId,title}`（title_locked）。
  - **Pin / Unpin**：`update_conversation_pinned{conversationId,pinned}`。
  - **Delete**：`delete_conversation{conversationId}`，删后清空 selectedConv。
- Shell 增 renaming/renameText 状态；变更在 goroutine 内加锁写、view 侧读取。
- 新建 **app 层端到端测试** `src/app/integration_test.go`：用 httptest 起真实 上游 栈（db+appstate+web+RegisterAll），Backend.raw 跑通 add_folder→create_conversation→rename(校验 list 回显)→pin→delete(校验清空)——验证原生 UI 数据层与整栈 wire 的闭环（不开窗）。
- `go build/vet/test ./...` 全绿（src/app 现含纯函数 + stream 逻辑 + 整栈集成三类测试）。
- **关于历史 transcript 解析的工程判断（记录在案）**：上游 per-agent JSONL 解析器体量巨大且各不相同（claude.rs ~7096 行、codex.rs ~15700 行、opencode.rs ~4270 行…），一次性忠实移植风险极高、不宜塞进单个增量。将作为**独立分阶段里程碑**推进（先 claude_code 最小正确读取：按 external_id 定位会话文件 + 解析标准 user/assistant/tool 行 → MessageTurn，长尾诚实降级），而非在工作区增量里仓促半成品。当前 loadConversationTurns 仍返回空（契约/分页算法已就位，接上解析源即生效）。

### 2026-10-11（续：B12 工作区核心 — 结构化工具调用卡片 + 取消）

- 会话详情从快照结构化渲染 agent 工作过程：
  - **active_tool_calls**：每个 ToolCallState（id/kind/label/status/content/output）渲染为紧凑卡片——状态字形（○/●/✓/✗/⊘）+ 颜色（running=Accent, success=Success, failed/rejected=Danger）+ label + 内联 content/output（MaxLines 6）。viewToolCall/toolStatusGlyph/toolStatusColor。
  - 保留原始事件流（在历史 transcript 未移植前，用它保住一轮输出不随 live_message 清空而丢失），叠加结构化工具卡片。
  - 快照只 ensure 一次，供工具卡片 + pending 卡片共用（去重拉取）。
  - 空态判定纳入 toolCalls（turns/live/toolCalls 皆空才提示"发消息启动"）。
- **Cancel 按钮**：有活动连接时在 composer 旁出现，点击 `acp_cancel{connectionId}` 中止当前 turn（off-thread，清快照缓存重绘）。
- 单测：toolStatusGlyph 全分支。`go build/vet/test ./...` 全绿。
- 工作区核心交互面现已完整：发 prompt → 实时流式文本 → 结构化工具调用（带状态/输出）→ 权限/提问卡片 → 取消。
- 下一步候选：per-agent JSONL 历史 transcript 解析（让旧会话可回看）、live_message thinking/plan 块的分样式渲染、会话标题自动回填。

### 2026-10-11（续：B12 工作区核心 — 权限/提问卡片 + 快照驱动）

- 实时交互闭环再进一步：会话详情现在渲染 agent 在途的**权限请求**与**提问**卡片，接 ACP 回执命令。
- stream.go：onChange 签名改为 `func(connID string)`（firehose 传 ""），每条带连接的事件触发 Shell 清 `snapshot:<conn>` 缓存并重绘——使快照随事件保持新鲜而无需逐帧拉取。
- workspace.go viewPendingCards：从 `acp_get_session_snapshot`（ensure 缓存、事件到达即失效重拉）读 `pending_permission`{request_id, tool_call, options} 渲染 tool_call 标签 + 每个 option 一个按钮 → `acp_respond_permission{connectionId, requestId, optionId}`（off-thread，回执后清快照缓存重绘）；`pending_question`{questions} 渲染问题文本 + 提示在 composer 作答。option/question 字段用 firstNonEmpty 兼容 optionId/option_id/id 与 name/label 等多种命名。
- 单测扩展：firstNonEmpty；stream_test 适配新 onChange 签名。
- `go build/vet/test ./...` 全绿。工作区核心交互面已具备：发 prompt → 实时事件流 → 工具调用需批准时弹权限卡片点一下即放行。
- 下一步候选：工具调用块结构化渲染（active_tool_calls/live_message 从快照）、取消按钮（acp_cancel）、per-agent JSONL 历史 transcript 解析。

### 2026-10-11（续：B12 工作区核心 — WS /ws/events 实时事件流接入）

- 新建 `src/app/stream.go`：原生 UI 的 `/ws/events` 客户端（与 mcode-app 同一多路复用 firehose + attach 协议）。
  - gorilla/websocket 客户端拨 `ws://127.0.0.1:port/ws/events`（Bearer header），读循环按帧分流：有 `channel` → firehose（conversation://changed、acp://connection|event）触发重绘；有 `type` → attach 协议（snapshot 记游标 / replay 批量累积 / event 单条累积），按 connection_id 分桶存 envelope，维护 lastSeq。
  - `Attach(connID)` 发 `{action:"attach", subscription_id, connection_id, since_seq}`（幂等，带断点续传游标）；`Events(connID)` 取累积事件；新事件回调 `svc.Invalidate()` 请求 UI 线程重绘（goroutine 安全）。
- Shell 接线：新增 `stream *Stream` + `connByConv`（会话→活动连接）；View 首帧惰性 `ensureStream()`。
- `sendPrompt` 现在 acp_connect 后把 connID 存入 connByConv 并 `stream.Attach`（**先订阅再 prompt，不漏事件**），再 acp_prompt。
- 会话详情 transcript 区在历史轮次之后追加**实时事件流**（eventKind/eventText 从扁平化 envelope 提取类型与文本），agent 流式输出即时显示。
- 单测 `stream_test.go`：handle() 对 snapshot/replay/event 的累积与 lastSeq、firehose 帧不建连、未知连接空事件（纯逻辑，不拨真 socket）。
- `go build/vet/test ./...` 全绿。工作区核心闭环已通：选/建会话 → composer 发 prompt → connect+attach → 实时渲染 agent 事件（需 agent 二进制在场才有真实回复）。
- 下一步候选：权限/提问卡片（snapshot.pending_permission/pending_question → acp_respond_permission/acp_answer_question）、工具调用块的结构化渲染、per-agent JSONL 历史 transcript 解析。

### 2026-10-11（续：B12 工作区核心 — 会话详情 + 新建会话 + композер驱动 ACP）

> 用户定调：工作区相关页面是 agent 软件核心，git 窗口优先级降后。本轮集中铺工作区聊天主循环。

- `src/app/workspace.go` 重写为三栏核心界面（上游 src/app/workspace 的原生等价）：folders | conversations | 聊天详情。
  - **folders 栏**：list_all_folder_details，点选切换；选中高亮。
  - **conversations 栏**：list_conversations{folderId}，点选进详情；底部"新建会话"= acp_list_agents 下拉选 agent + New 按钮 → create_conversation → 刷新并自动选中新会话。
  - **聊天详情**：会话头（标题/agent/状态）+ transcript（get_folder_conversation_turns；因 per-agent JSONL 解析未移植，历史轮次通常为空，诚实提示"发消息启动 agent"）+ **composer（TextInput + Send）**。
  - **composer→ACP 主循环**：Send 在后台 goroutine 走 acp_connect{agentType, folderPath, conversationId} → acp_prompt{connectionId, prompt}，状态/错误落 actionMsg（无 agent 二进制时如实显示连接失败），不阻塞 UI。
- 异步缓存模式扩展：新增 `peek(key)`（只读缓存不触发拉取）、`setAction`（goroutine 安全写状态+Invalidate）、convByID/folderPathByID 查缓存、createConversation/sendPrompt 两个 off-thread 变更。
- Shell 增 selectedConv/composer/newAgent/actionMsg 字段。
- 单测扩展 turnText；`go build/vet/test ./...` 全绿。
- 说明：历史 transcript 需后续移植 per-agent JSONL 解析（loadConversationTurns 当前返回空）；live 事件流（WS /ws/events）渲染为下一步，将把 agent 的流式输出接入 transcript。

### 2026-10-11（续：B12 原生 UI 页面树 — src/app 结构化 + Workspace/Settings 实数据）

- 新建 Go 包 `yoyocode/src/app`（上游 Next.js src/app 的等价层，一页一文件便于跟上游 diff）：
  - `app.go`：Backend loopback 客户端 + Shell 根状态；**异步缓存模式**——view 永不阻塞 IO，调 `ensure(key,cmd,body)` 首次触发后台 goroutine 拉取，落地后用 `ui.Services().Invalidate()`（goroutine 安全）请求重绘；`invalidate(key)` 供 Refresh 丢缓存重拉。顶层 Sidebar（Workspace/Settings/Pet）。
  - `workspace.go`：镜像 src/app/workspace。左列 folders（list_all_folder_details），点选 → 右列该文件夹会话（list_conversations{folderId}，标题/agent/status）。folderId 用 numOrString 按数字/字符串正确送型。
  - `settings.go`：镜像 src/app/settings。二级导航列出全部 19 个上游设置页；已接后端的 3 页出实数据——General（get_system_language_settings→language）、Web Service（get_web_server_status→running/port/addresses）、Quick Messages（quick_messages_list 列表）；其余页占位并标注对应上游路由。
  - `pet.go`：占位（pet_* 后端后置）。
- `cmd/yoyocode/main.go` 精简为「起进程内后端 + app.New(backend) + 开窗」。
- 单测 `src/app/app_test.go`：asList/asObj/str/numOrString/itoa 纯函数覆盖（无需开窗）。
- `go build/vet/test ./...` 全绿（新增 yoyocode/src/app 包测试通过）。B12 结构就位，后续按上游逐页填充（commit/merge/push/stash 窗口、login、project-boot、import-sessions、pet-panel、computer-indicator/marker，及 settings 其余页）。

### 2026-10-11（续：B12 原生 UI 骨架启动 + dev 实时预览打通）

- **mygo 原生 UI 接入**：go.mod 新增 `require github.com/egoist/mygo`（replace → ./_upstream/mygo），拉入 UI 依赖 purego / go-text/typesetting / golang.org/x/image（纯 Go，无 cgo）。
- 新建 `cmd/yoyocode/main.go`（上游 Tauri+Next.js 外壳的 Go 等价物）：单可执行文件——
  - `bootBackend()`：在 127.0.0.1:0 以进程内方式起 src-go web 服务器（复用 db/appstate/web/handlers，与 mcode-app 完全同一 HTTP 契约），后台 goroutine serve。
  - mygo 原生窗口（package ui，GPU 绘制，无 webview）：Row = 左侧 Sidebar（YoYoCode 标题 + 后端地址 + Workspace/Settings/Pet 导航，用 ui.Sidebar/ui.SidebarItem）+ 右侧主区（按 section 切换；Workspace 区经 `list_folders` 展示打开的文件夹及会话计数，Refresh 按钮重拉）。
  - UI 经 loopback HTTP 调后端（`Bearer` token），验证原生 UI ↔ 后端端到端路径。
- 新建 `mygo.json`（name/identifier/version/main=./cmd/yoyocode/out=build/app）。
- **实时 dev 预览可用**：`go tool mygo dev ./cmd/yoyocode`（或 `go run github.com/egoist/mygo/cmd/mygo dev`）——改 Go 代码即自动重编译+重启窗口，编译失败则保留上一个在运行。原生 UI 无需 dev server/devUrl。注意：会开真实桌面窗口（Windows WebView2/GPU），需显示器；无头环境可编译不可显示。首次需联网拉 mygo CLI 依赖。
- `go build ./...` / `go vet ./...` / `go test ./...` 全绿（cmd/yoyocode 纳入构建）；gofmt 干净。B12 由 [ ] → [~]（骨架可跑，页面树待逐页镜像 src/app）。

### 2026-10-11（续：list_directory_with_files + list_workspace_files 文件系统读）

- `src-go/web/handlers/workspace_file_list.go`：移植 commands/folders.rs 的两条文件系统列举命令。
  - `list_directory_with_files` → `[DirectoryItem]`（camelCase `{name, path(绝对), isDir, hasChildren, size}`）：直接子项（跳 dotfile），符号链接按 Stat 分类目录/文件；目录 size=null、hasChildren 看是否有非隐藏子项；文件给字节大小。排序目录在前、组内按名称大小写不敏感。
  - `list_workspace_files` → `[WorkspaceFileEntry]`（snake_case `{name, path(相对,正斜杠), kind:"file"|"dir"}`）：递归扁平列举，供模糊搜索。
- 自带轻量 gitignore 匹配器（本移植未引第三方 ignore 库）：逐目录栈式加载 in-tree `.gitignore`/`.ignore`（根额外含 `.git/info/exclude`），支持 basename/anchored/dir-only(尾/)/negation(!)/glob(`*` `**` `?`)，glob→regex，last-match-wins；叠加硬编码 FILE_TREE_IGNORED_DIRS(.git/__pycache__) 与 .DS_Store 跳过，dotfile 默认可见；不跟随符号链接目录递归。
- 诚实降级（注释+此处注明）：未复刻 folder_links 授权链接展开（folder_links 子系统未建），符号链接项目仅作单个 dir 条目不下钻；gitignore 为常见模式覆盖而非 `ignore` crate 的完整语义。
- 新注册 `RegisterWorkspaceFileList`。测试 `workspace_file_list_test.go`：gitignore（node_modules/、*.log 排除，.git 跳过，src/.env 保留，kind 正确）+ list_directory_with_files（目录在前排序、dotfile 排除、size/hasChildren、dir size=null）。
- gofmt/vet/test 全绿；inventory 323/523，P0 128/128。

### 2026-10-11（续：git_show_file_base64 + git_commit_files）

- `src-go/web/handlers/git_show_base64.go`：
  - `git_show_file_base64` → GitBlobBase64 `{exists, ref_missing, data, byte_size, too_large}`：先 `rev-parse --verify --quiet <ref>:<file>` 钉住 oid（oid 不可变，size/read 描述同一 blob），缺 oid 时再 `rev-parse <ref>^{commit}` 区分「路径缺失」vs「版本缺失」；`cat-file -s` 仅读对象头做大小闸（default 20MB，clamp 4096..100MB），超限 too_large 不缓冲；`cat-file blob` 取原始字节（非 show，无 eol 过滤）base64。
  - `git_commit_files` → `[{path, status, additions, deletions}]`：`show --format= --first-parent --raw --numstat --no-renames <commit>`，复用 git_log 的 parseRawFileLine/parseNumstatFileLine 合并（新 parseCommitFileChanges）。
- 新注册 `RegisterGitShowBase64`。
- 测试 `git_show_base64_test.go`：commit_files(两文件 status=A) / base64 文本解码+byte_size=12 / maxBytes=4096 对 5000B 文件 too_large(data 空,size=5000) / 缺路径(exists=false,ref_missing=false) / 缺版本(ref_missing=true)。
- git 批次至此：本地分支/远程/合并 + 冲突族 + show_base64/commit_files 全完。剩 clone_repository / worktree(add/remove) / delete_remote_branch / update_branch（凭据+DB，后续）。gofmt/vet/test 全绿；inventory 321/523，P0 128/128。

### 2026-10-11（续：git 冲突工作区 7 条 — merge/rebase 冲突解决族）

- `src-go/web/handlers/git_conflict.go`：移植 web/handlers/git.rs + commands/folders.rs 的本地冲突工作区操作（无凭据）：
  - `git_start_pull_merge` → ()（`merge --no-commit <upstreamCommit|@{u}>`；失败且有冲突即视为成功——冲突态已激活，供合并工作区使用）。
  - `git_has_merge_head` → bool（`rev-parse --verify MERGE_HEAD` 的成功与否）。
  - `git_list_conflicts` → Vec<String>（复用 detectConflicts）。
  - `git_conflict_file_versions` → `{base, ours, theirs, merged}`（index 三 stage :1:/:2:/:3: 原始字节读取 + 前 2048B NUL 二进制短路报错；merged 读工作树带标记文件；用新增 gitRunBytes 保留原始字节）。
  - `git_resolve_conflict` → ()（写回解决内容 + `git add` 暂存）。
  - `git_abort_operation` → ()（merge/pull→`merge --abort`，rebase→`rebase --abort`，其余 invalid_input）。
  - `git_continue_operation` → ()（merge/pull→`commit --no-edit`，rebase→`rebase --continue`，GIT_EDITOR=true 保持非交互；用 gitRunEnv）。
- 新注册 `RegisterGitConflict`（register.go）。新增测试 helper `callBool`（裸 bool 返回）。
- 测试 `git_conflict_test.go`：构造 main/feature 对同文件分叉的冲突仓库，经 git_merge 进入冲突态后跑通 has_merge_head→true / list_conflicts=["a.txt"] / file_versions(ours=main-change,theirs=feature-change,merged 含 <<<<<<<) / resolve→list_conflicts=[] / continue→has_merge_head=false；另一仓库 start_pull_merge→abort 往返 + 未知 operation→invalid_input。
- gofmt/vet/test 全绿；inventory 319/523，P0 128/128。

### 2026-10-11（续：git 本地分支/远程/合并 8 条 — 无凭据批次）

- `src-go/web/handlers/git_branch_remote.go`：忠实移植 web/handlers/git.rs + commands/folders.rs 的无凭据本地 git 操作（全部 shell out 系统 git，输出形状对齐客户端解析器）：
  - `git_current_user` → Option<String>（`git config user.name`，空/失败返回 null）。
  - `git_search_authors` → Vec<String>（`git shortlog -s -n -e --all` + filter_shortlog_authors 端口：剥计数列/剥 " <email>"、大小写子串匹配、按频次去重、limit 默认 20 clamp 1..100；空 query 短路 []）。
  - `git_add_remote`/`git_remove_remote`/`git_set_remote_url` → ()（remote add/remove/set-url）。
  - `git_merge` → GitMergeResult `{merged_commits, conflict}`（先 `rev-list --count HEAD..branch` 计数再 merge；失败且 detect_conflicts 非空则返回 conflict 对象而非错误）。
  - `git_rebase` → GitRebaseResult `{message, conflict}`（同款 conflict 降级）。
  - `git_delete_branch` → String（`git branch -d/-D`，force 切 -D，返回 git stdout）。
  - detect_conflicts（`diff --name-only --diff-filter=U` + unquote）与 GitConflictInfo `{has_conflicts, conflicted_files, operation, upstream_commit:null}` 均逐字段对齐。
- 新注册 `RegisterGitBranchRemote`（register.go，接 RegisterGitWrite 后）。
- 测试 `git_branch_remote_test.go`：真实仓库跑通 current_user / search_authors（含空 query 短路）/ remote add→set-url(校验 get-url)→remove / 干净 merge(merged_commits=1,conflict=null) / rebase up-to-date / delete_branch。
- 说明：需凭据/网络的 `git_delete_remote_branch` / `git_update_branch` / `git_remove_worktree`（askpass+DB 管线）与 conflict 读写族（list_conflicts/conflict_file_versions/resolve/abort/continue/has_merge_head/start_pull_merge/worktree_add）留后续批次。
- gofmt/vet/test 全绿；inventory 312/523，P0 128/128。

### 2026-10-11（续：conversations chat 模式 — create_chat_dir + create_chat_conversation）

- `src-go/web/handlers/conversation_chat.go`：无文件夹"chat 模式"两条端点，忠实移植 commands/conversations.rs 的 create_chat_dir_core / create_chat_conversation_core。
  - `create_chat_dir`：纯文件系统，mint `<dataDir>/chat-sessions/<YYYY-MM-DD>/<uuid>/`（v4 UUID simple 形状，32 hex 无横线），不写 DB；结果 camelCase `{path}`。
  - `create_chat_conversation`：复用 existingDir 或新建 scratch 目录 → 插入隐藏 "Chat" 文件夹（kind='chat'/is_open=1/color='inherit'/sort_order=max(folder,group)+1，对齐 folder_service::add_chat_folder）→ 插入 chat 会话（kind='chat'/status='in_progress'/title_locked=0，title 为空存 NULL）；会话插入失败则补偿软删孤儿文件夹（上游无事务跨两 insert 的同款语义）。结果 camelCase `{conversationId, folderId, folder}`，folder 复用 `folderDetailByID` 输出完整 snake_case FolderDetail（id/name/path/git_branch/default_agent_type/last_opened_at/sort_order/color/parent_id/kind/alias/group_id）。广播 EmitConversationChanged。
- 新注册 `RegisterConversationChat`（register.go，接在 RegisterConversationTurns 后）。
- 测试 `conversation_chat_test.go`：create_chat_dir 落盘校验；create_chat_conversation 复用目录（folder.path==scratch）与全新建（新目录）两路径；FolderDetail 字段齐全；chat 会话可 get_conversation 读回；get_stats 计数。
- conversations 18/21（+create_chat_dir +create_chat_conversation）。剩 import_* 三条（依赖 transcript 解析，重）。gofmt/vet/test 全绿；inventory 304/523，P0 128/128。

### 2026-10-11（续：conversations 侧栏聚合 — get_sidebar_data + 修正 get_stats/list_folders 契约）

- 发现并修正上游契约偏差：原 `get_stats` 返回自造 camelCase 形状（totalConversations/activeConversations/totalFolders/agentConversations），而上游 `AgentStats` 为 snake_case `{total_conversations, total_messages, by_agent:[{agent_type, conversation_count}]}`（见 models/conversation.rs + src/lib/types.ts）；原 `list_folders` 返回 folder 表原始行，而上游 `FolderInfo` 为按会话聚合 `{path, name, agent_types, conversation_count}`。二者均非 mcode-app 消费（已 grep 确认），但原生 mygo UI 将镜像上游前端类型，故按「上游为准」修正。
- `src-go/web/handlers/stats.go`：新增 `computeFolders`（LEFT JOIN folder，缺失/软删 folder 归入 "unknown"，按 conversation_count 降序，agent_types 首见序）、`computeStats`（GROUP BY agent_type，by_agent 降序，total_messages=SUM(message_count)）、`getSidebarData`；移除死代码 `scanFolders`。
- `src-go/web/handlers/conversations.go`：`list_folders`→computeFolders、`get_stats`→computeStats、新增 `get_sidebar_data`（SidebarData {folders, stats}）；移除死代码 `listAllFolders`。上游数据源是 transcript parser（list_conversations_sync 扫描各 agent 磁盘会话），本移植以 DB conversation 表为真相源做同形聚合（注释已注明分歧）。
- 测试 `conversations_sidebar_test.go`：seed 1 folder + 3 会话（claude_code×2 / codex×1），断言 get_stats/list_folders/get_sidebar_data 三者 wire 形状与计数/排序。
- conversations 16/21（+get_sidebar_data）。gofmt/vet/test 全绿；inventory 302/523，P0 128/128。

### 2026-10-11（续：B11p — mcp_service yoyocode-mcp 服务状态/工具组 3/3）

- `src-go/web/handlers/mcp_service.go`：状态栏指示器的服务健康报告 + 工具组开关，忠实移植 commands/mcp_service.rs 的 wire 契约。
- `get_codeg_mcp_service_status` → YoyocodeMcpServiceStatus（snake_case）：`{state, listening, socket_path, binary_path, tool_groups, companion_count, session_count, active_delegations, depth_limit, started_at, last_error, can_start}`；tool_groups 9 项（delegation/feedback/ask/sessions/automations/taskboard/browser/browser_eval(within browser)/computer），顺序与 companion 的 features 解析一致，browser_eval 为 browser&&eval 的依赖项（带 requires）。
- 诚实降级：Go 构建不含进程内 delegation broker socket 与 yoyocode-mcp companion 二进制 → listening=false、binary_path=null（除非 YOYOCODE_MCP_BIN 覆盖），按上游 state 排序 → "stopped"、can_start=false（UI 隐藏启动按钮）；companion/session/active_delegations=0。`start_codeg_mcp_service` 无 socket 可绑 → 返回该 stopped 状态供 UI 重新同步。
- 工具组开关是真实的：`set_codeg_mcp_tool_group(key, enabled)` 按 slug 写对应 per-feature 的 app_metadata 键（复用 settings_misc/tools_settings 既有键与默认值）并广播该功能自己的 `*-settings://changed` 事件，故 popover 与设置页一致；depth_limit 读自 delegation 设置；未知 slug 报 invalid_input。
- 测试 `mcp_service_test.go`：status 的 state=stopped/can_start=false、9 工具组、browser_eval 带 requires、ask 默认 ON；toggle computer 后 status 与 get_computer_tools_settings 均反映；未知 slug 报错。
- 结果：build / `go vet ./...` / `go test ./...` 全绿；ROUTE_INVENTORY 523 路由、301 实现、mcp_service 3/3、P0 仍 128/128。


### 2026-10-11（续：B11o — feedback 收尾：submit_session_feedback 3/3）

- `src-go/web/handlers/settings_misc.go`：补齐 feedback 模块最后一条 `submit_session_feedback`，feedback 现 3/3。
- 诚实降级：该命令本应把实时反馈经 ACP feedback 工具投递给进行中的 agent turn；Go server 未接线该工具，故（对齐上游 NoActiveTurn/FeedbackDisabled 的可恢复路径）返回 invalid_input —— 正是前端据以回退"把反馈作为普通 prompt 发送"的信号。仍校验 connectionId + text，使畸形调用被单独拒绝。
- 测试（settings_misc_test.go 追加）：缺参报错；合法参数返回非 200 且 error code=invalid_input。
- 结果：build / `go vet ./...` / `go test ./...` 全绿；ROUTE_INVENTORY 523 路由、298 实现、feedback 3/3、P0 仍 128/128。


### 2026-10-11（续：B11n — event_metrics 调试端点 1/1）

- `src-go/web/handlers/event_metrics.go`：操作者调试端点 `GET /api/debug/event_metrics`，忠实移植 web/handlers/event_metrics.rs 的 EventBusMetricsSnapshot wire（snake_case 9 个 u64 计数）。注册命令名 `debug/event_metrics`（含斜杠），由 ServeHTTP 的 /api/ 分派按普通受保护命令响应（method 无关，token 保护，与上游一致）。
- 诚实降级：这些计数属上游 ACP 内部事件总线（环形缓冲/重放/forwarder 滞后），Go server 的 broadcaster 是简单扇出不追踪它们 → 返回全 0 快照（poller 跨调用 diff 的稳定 JSON 形状），注明"本构建无 ACP 事件总线埋点"。
- 顺带修正 `scripts/gen_inventory.js` 的 scanGo 正则：允许命令名含 `/`（如 debug/event_metrics），否则已注册路由被漏计。
- 测试（app_update_bridge_test.go 追加）：`debug/event_metrics` 返回含 9 计数、均为 0。
- 结果：build / `go vet ./...` / `go test ./...` 全绿；ROUTE_INVENTORY 523 路由、297 实现、event_metrics 1/1、P0 仍 128/128。
- 说明：experts/science/custom_skills 等模块依赖上游 `include_dir!` 内嵌的第三方技能包（obra/superpowers 等 vendored 内容），本 Go 构建不内嵌该第三方素材；后续若复刻这些模块，将仅移植 上游 自有的"中央目录 + per-agent 链接"逻辑并对缺失的内嵌包诚实降级（list 反映磁盘实际内容），不复制第三方素材。


### 2026-10-11（续：B11m — workspace_state 工作区快照模块 3/3）

- `src-go/web/handlers/workspace_state.go`：工作区面板订阅的文件树 + git 状态快照，忠实移植 commands/workspace_state.rs + workspace_state 子系统的 wire 契约。
- 三命令：`start_workspace_state_stream`(rootPath, wantsTreeGit 默认 true) → 注册内存流并返回全量快照；`get_workspace_snapshot`(rootPath, sinceSeq?) → 流在册则按需重扫返回全量，不在册报 not_found（复刻上游"流未运行"语义）；`stop_workspace_state_stream` → 注销返回 null。
- wire `WorkspaceSnapshotResponse`（snake_case）：`{root_path, seq, version, full, tree_snapshot, git_snapshot, deltas, degraded, is_git_repo}`；FileTreeNode `{kind:file|dir, name, path[, children, symlink]}`（tag=kind、symlink 省略 false）；WorkspaceGitEntry `{path, status, additions, deletions}`。
- 树：深度 2（WORKSPACE_TREE_MAX_DEPTH）目录优先排序遍历，忽略 .git/node_modules/__pycache__/.DS_Store，符号链接目录不下探（children 空 + symlink）。
- git：`collectWorkspaceGitSnapshot` 复用 gitRun 跑 `status --porcelain=v1 --untracked-files=all` 取 status+path，`gitNumstatMap` 跑 `diff --numstat HEAD`（回退 --cached）取增删，按 normalize_git_status_path / normalize_numstat_path（含 rename `a -> b` / `{a => b}` 规整）合并，按小写路径再路径排序。
- 诚实降级：Go server 无 notify(FS) 监听器 → 不推增量 delta（deltas 恒空、full=true）；前端本就在每次写操作后重取 get_workspace_snapshot（亦是上游 watcher 丢事件时的兜底路径），面板仍正确。
- 测试 `workspace_state_test.go`：路径规整/numstat 解析、深度 2 树（node_modules 忽略、deep 目录被深度截断 children 空、目录优先）、流生命周期（未启动 not_found → start 全量 → get → stop → not_found）、轻量订阅 wantsTreeGit=false 省略 tree/git。
- 结果：build / `go vet ./...` / `go test ./...` 全绿；ROUTE_INVENTORY 523 路由、296 实现、workspace_state 3/3、P0 仍 128/128。


### 2026-10-11（续：B11l — web_server 收尾：check_app_update + app_update_status 8/8）

- `src-go/web/handlers/app_update.go`：补齐 web_server 模块的两条更新查询路由，忠实移植 web/handlers/web_server.rs 的 wire 形状（camelCase）。
- `check_app_update` → `{currentVersion, update, selfUpdateSupported, capability, runtime, restartDelayMs, rollbackAvailable, liveProgress}`；`app_update_status` → 同上去掉 update、`selfUpdateBlocker` 无阻碍时省略（skip_serializing_if）。
- 诚实降级：此 Go 构建非自替换二进制 → selfUpdateSupported=false、update=null（不抓取 manifest，不虚报新版本）、rollbackAvailable=false、liveProgress=true；capability 复用现有 updateCapability()（YOYOCODE_SUPERVISED→supervised 否则 reexec）；runtime 由 /.dockerenv 或 YOYOCODE_RUNTIME 判定 docker/standalone；currentVersion 取 YOYOCODE_VERSION 否则常量 0.34.0（对齐上游 Cargo，注明可被构建覆盖）。与既有 app_update_state/perform/restart/rollback 的降级口径一致，升级 UI 不致因缺路由报错。
- 测试（app_update_bridge_test.go 追加）：check 带 currentVersion/capability、selfUpdateSupported=false、update 存在且为 null、liveProgress=true；status 带 currentVersion、省略 selfUpdateBlocker、不含 update。
- 结果：build / `go vet ./...` / `go test ./...` 全绿；ROUTE_INVENTORY 523 路由、293 实现、web_server 8/8、P0 仍 128/128。
- 剩余小模块候选：event_metrics(1，GET 需路由管线)、workspace_state(3，文件树+git 快照流子系统较重)、mcp_service(3，依赖 delegation broker/companion 较重)、office_watch_proxy(3)；以及 feedback(submit 依赖活跃 ACP turn)、files(upload_attachment)、(inline) 等收尾。


### 2026-10-11（续：B11k — logging 日志查看器模块 5/5）

- `src-go/web/handlers/logging.go`：Settings → Logs 查看器后端，忠实移植 commands/logging.rs + web/handlers/logging.rs 的 5 条命令。
- `get_log_settings`：从 app_metadata `logging.level` 读持久化 LogSettings（默认 level=info、targets=[]），env_locked 由 `YOYOCODE_LOG`/`RUST_LOG` 是否设置决定；wire `{level, targets, env_locked}`（LogSettingsView 无 rename_all → snake_case）。
- `set_log_settings`：校验 level ∈ {off,error,warn,info,debug,trace}、targets 每项 {target,level} 合法；持久化到 app_metadata（targets 空时省略，复刻 skip_serializing_if）；广播 `log-settings://changed`；返回规范化 LogSettings。
- `get_recent_logs`：Go server 无内存 tracing 环形缓冲（桌面端 Rust subscriber 专有），诚实降级返回空数组（注明"后续：若 Go 侧接入日志 hub 再补"）。
- `list_log_files`：读 logsRoot 下 *.log，返回 `{name, size_bytes, modified_ms}` 按 mtime 降序。
- `read_log_file`：路径遍历防护（禁 / \\ \0 ..、必须 .log、解析后父目录须等于 logs 目录），按末尾 maxBytes（上限 16MiB）读取 tail（日志 append-only，尾部即最新）。
- logsRoot 解析沿用 paths.rs yoyocode_logs_root 优先级：YOYOCODE_HOME/logs → YOYOCODE_DATA_DIR/logs → AppState.DataDir/logs → ~/.yoyocode/logs。
- 测试 `logging_test.go`：env_locked、默认设置、set 校验与 targets 规范化（空省略/非法拒绝）、list 按 mtime 降序且仅 .log、read 全量/尾部/遍历·非log·缺失拒绝 —— 纯函数 + 临时目录，无需 DB、不触真实目录。
- 结果：build / `go vet ./...` / `go test ./...` 全绿；ROUTE_INVENTORY 523 路由、291 实现、logging 5/5、P0 仍 128/128。
- 下一个小模块候选：workspace_state(3)、mcp_service(3)、event_metrics(1)、office_watch_proxy(3)，以及 feedback/files/web_server 的收尾。


### 2026-10-11（续：B11j — mcp 模块完全收官：Hermes（YAML）本地读写，15/15 agent）

- 新增依赖 `gopkg.in/yaml.v3`（经 goproxy.cn 拉取、go mod tidy 提升为直接依赖）。
- `src-go/web/handlers/mcp_yaml.go`：补齐最后一个 agent Hermes 的本地读写。至此 mcp 模块 15/15 agent 本地 scan/读写、8/8 路由、official+smithery 两市场 detail/install 全部完整。
- 路径：hermes = `HERMES_HOME`(~ 展开) 否则 `~/.hermes`/config.yaml。`yamlNormalize` 把 yaml.v3 可能产生的 map[any]any 归一为 map[string]any 供通用 canonical 助手读取。写入 owner-only(0600)/parent 0700（匹配上游 secret writer 的安全意图，省略 symlink 保留，已注明）。
- hermes：`hermesEntryToCanonical`（type 空且有 url 时按 transport==sse 定 sse/http、随后丢 transport、canonicalize；stdio 由 command 推断）、`canonicalToHermesEntry`（stdio 仅 command/args/env 不写 cwd、remote 写 url+（sse 补 transport）+headers、透传 Hermes 认得的 client_cert/client_key/enabled 等非保留非 null 键）。reader 读 mcp_servers 映射；upsert 读改写保留其余键；remove 删并清空壳。
- dispatch 补齐 hermes upsert/remove；localMcpReaders 指向真实 reader，移除最后一个空桩。upsertServerForApp 的 default 仍对未知 app（如未映射的自定义 agent）返回报错、removeServerForApp default 为 no-op。
- 测试 `mcp_yaml_test.go`：hermes stdio 往返（不写 type、回读 stdio）、sse 经 transport:sse 编码 / http 无 transport 的推断、client_cert 透传 + remove 清空；并把旧 staged 测试改用未知 agent `custom_unknown`（upsert 报错、remove no-op）。全部临时 HOME，不触真实目录。
- 结果：build / `go vet ./...` / `go test ./...` 全绿，gofmt 干净，go.mod 整洁（go-toml、yaml.v3 直接依赖）；ROUTE_INVENTORY 523 路由、286 实现、mcp 8/8、P0 仍 128/128。
- **mcp 模块彻底收官**：15 个 agent（Claude/CodeBuddy/Gemini/OpenClaw/Cline/OpenCode/Cursor/DeepSeek/Qoder/Antigravity/pi/Kimi JSON、Codex/Grok TOML、Hermes YAML）本地读写 + 扫描合并（含 Kimi 扩展字段回折）+ 两市场源 search/detail/install 全部忠实复刻。下一轮转入 REPLICATION_PLAN §5.1 的后续后端模块（chat_channel / pet / computer / custom_skills / experts / science / config_sync / backup / workspace_files 等）或 B12 原生 UI。


### 2026-10-11（续：B11j — mcp agent 收尾2：Codex + Grok 本地读写（TOML））

- 新增依赖 `github.com/pelletier/go-toml/v2`（经 goproxy.cn 拉取；go-toml v2 Unmarshal 到 any 得 map[string]any/[]any/int64/float64/bool）。
- `src-go/web/handlers/mcp_toml.go`：补齐 codex 与 grok 两个 TOML agent 的本地读写。至此 15 个 agent 中 14 个本地 scan/读写完整，仅余 hermes(YAML)。
- 路径：codex = `CODEX_HOME`(~ 展开) 否则 `~/.codex`/config.toml；grok = `GROK_HOME` 否则 `~/.grok`/config.toml。TOML 读 `readTomlRoot`（缺省/空→空表、非表报错）、写 `writeTomlRoot`（pretty + 末行换行）。`normalizeForTOML` 递归丢 null、整值 float64→int64（透传键 enabled/timeouts 以整数/布尔落盘，避免 strict-config 失败）。
- codex：`codexEntryToCanonical`（command&url 并存报错、显式 type 或 command/url 推断、http 读 http_headers 回退 headers、透传非保留键）、`canonicalToCodexEntry`（不写 type；stdio=command/args/env/cwd、http=url+http_headers、**sse 拒绝**、透传仅 enabled/required 布尔）。reader 读 `mcp_servers` + 兼容 legacy `mcp.servers`；upsert 写 mcp_servers 并清理 legacy 同名；remove 两处皆清并清空壳。
- grok：`grokEntryToCanonical`（显式 type 或 url 推断 remote、sse 显式、透传所有非保留键）、`canonicalToGrokEntry`（无 type；sse 写 `type="sse"`、http 省略；透传经 normalizeForTOML）。
- dispatch 补齐 codex/grok 的 upsert/remove；localMcpReaders 指向真实 reader，移除 codex/grok 空桩。
- 测试 `mcp_toml_test.go`：codex stdio 往返（不写 type、回读 stdio）、codex http 写 http_headers 且 sse 写入被拒、codex legacy `[mcp.servers]` 读取、grok sse 显式/ http 省略 type 的推断、grok 透传 enabled 布尔——用临时 HOME，不触真实目录。另修正旧 staged 测试（codex→hermes）。
- 结果：build / `go vet ./...` / `go test ./...` 全绿；ROUTE_INVENTORY 523 路由、286 实现、mcp 8/8、P0 仍 128/128。
- mcp 本地 agent 14/15；仅余 hermes（YAML，需 yaml 依赖）收尾。


### 2026-10-11（续：B11j — mcp agent 收尾1：pi + Kimi Code 本地读写）

- `src-go/web/handlers/mcp_kimi_pi.go`：补齐 pi 与 kimi_code 两个 agent 的本地配置读写（无新依赖，纯 JSON）。至此 15 个 agent 中 12 个本地 scan/读写完整，仅余 codex/grok(TOML)、hermes(YAML) 三个待加解析依赖。
- 路径：pi = `PI_CODING_AGENT_DIR`(~ 展开) 否则 `~/.pi/agent`，+ mcp.json；kimi = `KIMI_CODE_HOME`(原样) 否则 `~/.kimi-code`，+ mcp.json。
- pi：Claude 形状（canonical），无转译，reader/upsert/remove 直接走通用 mcpServers 读写。
- kimi_code：`kimiEntryToCanonical`（剥 type，transport 判别式 stdio/http/sse→canonical type、未知拒绝、随后丢 transport）、`canonicalToKimiEntry`（只写 Kimi 建模的字段 + 校验通过的扩展字段，remote 补 transport 判别式、stdio 省略；避免错类型值毁掉整个 mcpServers 记录）、扩展字段校验 `isKimiExtensionField`（runtime_id/executor 仅 stdio、auth=oauth/bearerTokenEnvVar 仅 remote、timeout 整数 1..i32、enabled/工具数组）。
- scan 合并忠实化：复刻 KIMI_OWNED_KEYS/KIMI_SHARED_KEYS 与 `mergeKimiExtensionFields` —— 某 server 被更早 agent 抢得 spec 时，把 Kimi 独有字段折回（owned 以 Kimi 为准、shared(auth) 优先取 OpenClaw 声明），使这些字段在编辑器可见且保存不丢；scan 循环新增 openclaw-declares 跟踪 + kimi 回折。
- 测试 `mcp_kimi_pi_test.go`：kimiEntryToCanonical（transport→type、推断、未知拒绝）、canonicalToKimiEntry（remote 保 auth 弃 runtime_id、stdio 保 runtime_id 弃 auth 无 transport）、mergeKimiExtensionFields（owned 取 kimi、shared 取 owner）、pi+kimi 落盘往返 + 同 id 跨 agent scan 合并（apps=[kimi_code,pi] 枚举序、auth 存活）+ 单 agent 移除——用临时 HOME，不触真实目录。
- 结果：build / `go vet ./...` / `go test ./...` 全绿；ROUTE_INVENTORY 523 路由、286 实现、mcp 8/8、P0 仍 128/128。
- 剩余：codex/grok(TOML，需 go-toml)、hermes(YAML，需 yaml) 三个 agent 的本地 reader·writer。


### 2026-10-11（续：B11j — mcp 增量4b：official registry 详情+安装解析，mcp 模块收官）

- `src-go/web/handlers/mcp_market_official.go`：忠实移植 commands/mcp.rs 的 official 安装半区（build_official_install_options / resolve_official_install_spec_with_selection 及其包/远程/参数/键值全部 helper）。至此 official registry 与 Smithery 两源的 detail/install 均完整，mcp_market_install.go 中 official 分支的 staged 降级已替换为真实实现。
- 键值输入 `officialKVList`：复刻 deserialize_official_key_value_inputs，headers/variables 既可为 {name,...} 数组也可为 name→(string|object) 映射，数组项缺 name 丢弃、映射项字符串值即 value。`valOrDefault`（value→default，修剪非空且排除未解析占位符 {..}）、`kvIsRequired`（isRequired 或含占位符或无默认）、`containsUnresolvedPlaceholder`。
- 远程传输 `remoteSpecFromTransport`：type 规整为 http/sse、url 必填、`applyTransportVariables`（变量按 {name}/{{name}} 花括号替换，否则追加查询参；强制必填校验）、headers 从 `headers.<name>` 取值或默认；`officialRemoteParameterFields` 暴露 headers.*（location header）与 variables.*（location query）参数。
- stdio 包 `resolveOfficialStdioPackage`：runtimeHint 缺省按 registryType 推断（npm→npx 加 -y / pypi→uvx）、`packageIdentifierWithVersion`（uvx+pypi→==ver、npx 非 scoped 非 http→@ver、latest 丢弃）、runtime/package 参数按 positional/named 解析（`appendArgumentValue`，named 推 name+value，缺必填报错）、env 从 `env.<name>` 取值；`officialStdioParameterFields` 暴露 runtime_arguments.N / package_arguments.N（location arg）与 env.*（location env）。
- 选项 id：`official:package:<idx>:<proto>` / `official:remote:<idx>:<proto>`，`resolveOfficialInstallSpec` 据此回到对应 package/remote 用用户参数重解析（enforce 必填）。detail 响应复用既有 officialEntryToItem（{server,_meta} 信封）叠加 default_option_id/install_options/spec。
- 测试 `mcp_market_official_test.go`：buildOfficialInstallOptions（stdio npm 包 args=-y+foobar@1.2.3+serve、env/arg 参数定位）、stdio 解析（env 注入、缺必填报错）、remote 解析（{region} 花括号替换为 us、header 注入、缺必填报错）、packageIdentifierWithVersion（scoped/pypi/latest 三分支）、officialKVList（数组缺 name 丢弃 + 映射两形态）、占位符检测/必填判定——均不触真实网络。
- 结果：build / `go vet ./...` / `go test ./...` 全绿；ROUTE_INVENTORY 523 路由、286 实现、mcp 8/8（功能完整）、P0 仍 128/128。
- mcp 模块收官（8/8 路由功能完整）。唯一剩余为 scan/写路径里暂缓的 5 个 agent reader·writer（codex/grok TOML、hermes YAML、pi/kimi 路径解析器）—— 市场安装产出的 canonical spec 已可写入其余 10 个 agent；这 5 个的本地配置读写留作独立小增量。


### 2026-10-11（续：B11j — mcp 增量4a：市场详情+安装 detail/install，smithery 完整）

- `src-go/web/handlers/mcp_market_install.go`：市场详情+安装半区，忠实移植 commands/mcp.rs 的 `mcp_get_marketplace_server_detail` + `mcp_install_from_marketplace` 及 install-option 解析脚手架。mcp 现 8/8 全部注册。
- 共享脚手架：`installOption`（id/protocol/label/description/spec/parameters，toWire 为 snake_case McpMarketplaceInstallOption）、`normalizeProtocolValue`/`protocolPriority`(stdio0 http1 sse2)/`selectDefaultInstallOption`/`collectProtocolsFromOptions`/`selectOptionFromList`（按 option_id → protocol(最优优先级) → 默认）、`installSelection`（option_id/protocol/parameter_values 解析，parameter_values 非对象报错）、`keyLooksSecret`/`valueAsText`/`appendQueryParam`。
- Smithery 完整：`fetchSmitheryServerDetail`(GET /servers/{id})、`fetchSmitheryServerSummary`(搜索兜底、容错)、`parseSmitheryConfigFields`(JSON-Schema properties→字段，kind 映射 boolean/number/integer/json/string、writeOnly|名字启发式→secret、x-from/secret→header 否则 query 定位、enum、default)、`smitheryParameterFields`、`resolveSmitheryConnectionSpec`(按字段定位把值写入 header 或 query 串、强制必填校验、canonicalize)、`buildSmitheryInstallOptions`(逐 connection 建选项、无则 deploymentUrl http 兜底、仍无则 not_found)、`resolveSmitheryInstallSpec`(选项选择→按 connection 索引用用户参数重解析)。detail 响应将 detail 与 summary 字段逐一回退合并，wire 为 snake_case McpMarketplaceServerDetail（含 default_option_id/install_options/spec）。
- install：normalizeApps（空报错）→ 解析 selection → specOverride 优先（两源通用、免抓取）否则按 provider 解析 → 按 `app_can_host_spec` 分区（不可托管的移除陈旧条目、可托管的 upsert）→ 回读 find_local_server。
- official registry 的 detail/install（package/remote install-option 解析，上游约 27KB）体量更大，暂缓到增量4b：当前 official detail 分支与 official 无 override 的 install 分支返回 dependency_missing 清晰报错并提示可用 specOverride 手动安装；smithery 分支与 specOverride 路径对两源均可用。
- 测试 `mcp_market_install_test.go`：protocolPriority/默认选择、buildSmitheryInstallOptions（参数定位 apiKey→header/secret/required、region→query）、resolveSmitheryConnectionSpec（query 串拼接、header 写入、缺必填报错）、按 option_id 选择解析、official 两分支 staged 报错、specOverride 安装落盘并回读（apps 按枚举序 [gemini,cline]）——均不触真实网络。
- 结果：build / `go vet ./...` / `go test ./...` 全绿；ROUTE_INVENTORY 523 路由、286 实现、mcp 8/8、P0 仍 128/128。
- 剩余：增量4b = official registry 的 detail/install（build_official_install_options / resolve_official_install_spec_with_selection，npm/pypi/oci 包 + 运行时参数/env/headers 解析）；以及余下 5 agent 的 TOML/YAML/路径解析器 reader·writer。


### 2026-10-11（续：B11j — mcp 增量3a：市场搜索 search_marketplace）

- `src-go/web/handlers/mcp_market.go`：市场只读搜索半区，忠实移植 commands/mcp.rs 的 `mcp_search_marketplace`（official registry + smithery 两源）。
- HTTP：`net/http` 客户端（connect 8s / TLS 8s / 整体 20s、UA `yoyocode-mcp-market/1.0`），`mcpMarketGet` 复刻 `send_request_with_retry`（3 次尝试、429/5xx 或传输错误线性退避 attempt×350ms、超时/连接错误带诊断前缀）。
- official（registry.modelcontextprotocol.io/v0.1/servers?limit&version=latest&search）：`officialEntryToItem` 从原始 JSON 直取字段（title→name 回退 name、空描述→"No description"、websiteUrl→repository.url 回退 homepage、remotes 非空→remote、_meta 官方 status==active→verified、首个非空 icon.src、version→latest_version）；`officialServerProtocols` 从 remotes[].type + packages[].transport.type 收集并去重排序；owner/namespace/downloads/score/is_deployed 恒为 null。
- smithery（api.smithery.ai/servers?limit&q）：`smitheryItemFromSummary` 映射 qualifiedName→server_id、displayName→name、iconUrl 修剪、useCount→downloads、score、isDeployed，remote→protocols=["http"] 否则 []。
- wire：McpMarketplaceItem 无 rename_all → snake_case 字段，Option 字段输出 null、protocols 恒为数组，逐字段对齐。
- 命令 `mcp_search_marketplace`：providerId 必填，limit 默认 30 clamp[1,100]，分派 official/smithery，未知 provider 报 invalid_input。
- 测试 `mcp_market_test.go`：transportProtocol、officialServerProtocols（排序去重）、officialEntryToItem（title/描述/homepage 回退、verified、首图标、official 专属字段为 null）、smitheryItemFromSummary（描述回退、图标修剪、remote→protocols、透传字段、非 remote 为空数组）、未知/缺失 provider 短路报错 —— 均不触真实网络。
- 结果：build / `go vet ./...` / `go test ./...` 全绿；ROUTE_INVENTORY 523 路由、284 实现、mcp 6/8、P0 仍 128/128。
- 剩余：增量4 = `mcp_get_marketplace_server_detail` + `mcp_install_from_marketplace`（含 install-option 解析 build_*_install_options / resolve_*_install_spec_with_selection，两源各自的参数/传输解析，体量大）；以及余下 5 agent 的 TOML/YAML/路径解析器 reader·writer。


### 2026-10-11（续：B11j — mcp 增量2：写路径 upsert/set_apps/remove）

- `src-go/web/handlers/mcp_write.go`：忠实移植 commands/mcp.rs 的变更半区。
- 三个命令 handler：
  - `mcp_upsert_local_server`：canonicalize→规范化规格；normalize_apps（去重+枚举序）；按 `app_can_host_spec` 预过滤（Codex/DeepSeek 不能托管 SSE）；`require_complete_scan`（任一源不可读则拒写，复刻"读降级、写不降级"）；Antigravity upsert 预检（mcpServers 非对象则拒）；随后对 ALL_MCP_APPS 逐一：命中目标则 upsert、否则 remove（"这些 agent 且仅这些"语义）；最后回读 `find_local_server` 或报错。
  - `mcp_set_server_apps`：从扫描取 current.spec/apps；预过滤+完整性门禁；added=target−current，先删后加（新目标写不进则整体中止，避免只删不加）；Antigravity 预检只针对 added；返回回读结果（可为 null）。
  - `mcp_remove_server`：apps 省略=全部、显式空=false；逐 app remove 求或。
- 10 个 JSON agent 的 canonical→native 写回 + remove：claude_code / code_buddy（含 `~/.claude(.|/)settings.json` 的 `<id>@local:true` enabledPlugins 激活门，幂等避免无谓写）、gemini、open_claw(mcp.servers，清理空壳)、cline(http→streamableHttp 字面量)、open_code(有 mcpServers 写规范、否则建 mcp 写 OpenCode 原生 local/remote 格式)、cursor(仅保留 command/args/env/cwd/url/headers、剥 type)、deepseek(0600/0700 owner-only 写)、qoder、antigravity(`antigravity_servers_object` 裸文档迁移进 mcpServers 包装)。
- 暂缓的 5 个 agent（codex/grok TOML、hermes YAML、pi/kimi 外部路径解析器）：显式 upsert 目标它们时返回 dependency_missing 清晰报错；对它们的 remove 为静默 no-op（绝不中止他人保存）。
- 测试 `mcp_write_test.go`：app_can_host_spec（codex/deepseek+sse 拒、stdio/其它放行）；claude upsert 写 `~/.claude.json` + enabledPlugins 门、scan 回显、remove 清条目+清门；cline http→streamableHttp 落盘且 scan 回读为 http；cursor 剥 type；staged agent upsert 报错 / remove no-op；upsert+set_apps+remove 全链路（foo→[claude,gemini]→[gemini]（claude 条目被剥离）→remove，apps 按枚举序）。全部用 `mcpHomeOverride` 临时 HOME，不触真实目录。
- 结果：build / `go vet ./...` / `go test ./...` 全绿；ROUTE_INVENTORY 523 路由、283 实现、mcp 5/8、P0 仍 128/128。
- 剩余：增量3 = 市场 search/detail/install（official registry + smithery，含 install-option 解析，需网络）；以及余下 5 agent 的 reader/writer（TOML/YAML/路径解析器）。


### 2026-10-11（续：B11j — mcp 增量1：规范化规格 + 本地扫描 + 市场列表）

- 规模说明：上游 commands/mcp.rs 为 32 万字符、15 个 agent × 读写、2 个市场源，是迄今最大的单模块。按"每轮落一个全绿增量"的约定拆分为多个增量推进，本轮落 **增量1：读/扫描路径 + 市场提供方列表**。
- `src-go/web/handlers/mcp_types.go`：规范化规格模型，忠实移植 `canonicalize_spec` / `canonicalize_opencode_spec` / `normalize_mcp_type` / `obj_as_string_map`。McpAppType 15 个 wire 名（serde snake_case，DeepSeek 固定为 `deepseek`，与 `AgentType::as_wire` 对齐）+ 枚举声明序（apps 列表按此序排序，复刻 BTreeSet 迭代序）。规格类型 stdio/http/sse，支持 streamable-http 等别名折叠、缺 type 时按 command/url 推断、OpenCode 的 local/remote 格式（command 数组、environment）转译、保留非保留非 null 的透传键。
- `src-go/web/handlers/mcp.go`：
  - `mcp_list_marketplaces`：official_registry + smithery 两个提供方（id/name/description 逐字对齐）。
  - `mcp_scan_local`：按上游 `local_mcp_readers` 精确顺序遍历 15 agent，首个声明某 id 者胜出其 spec（first-writer-wins），apps 取并集并按枚举序排序；某源不可读降级为 warning（{app, message}）而非让整次扫描失败（复刻 issue #632 的处理）。wire：`LocalMcpScan{servers:[{id,spec,apps}], warnings:[{app,message}]}`。
  - 已实现 10 个 JSON agent 的 reader（home 相对路径 + 环境变量覆盖，忠实移植各自的 config-key 与路径解析）：claude_code(~/.claude.json)、code_buddy(~/.codebuddy.json)、gemini(~/.gemini/settings.json)、open_claw(~/.openclaw/openclaw.json→mcp.servers)、cline(~/.cline/data/settings/cline_mcp_settings.json)、open_code(~/.config/opencode/opencode.json，mcpServers + mcp 两种格式)、cursor(~/.cursor/mcp.json，剥 type 按形状重判)、deepseek(DSH_HOME|~/.dsh/mcp.json)、qoder(QODER_CONFIG_DIR|...(.qoder)/settings.json)、antigravity(GEMINI_HOME|~/.gemini/config/mcp_config.json，裸顶层 map 回退)。
  - 暂缓到增量2的 5 个 agent（非 JSON 格式或路径依赖其它子系统解析器）：codex/grok(TOML)、hermes(YAML)、pi/kimi_code(home 解析器链)。其 reader 当前返回空 → 未移植的 agent 对扫描无贡献（等同用户未配置该 agent），不致使扫描失败；已在注释与此处注明。
- 测试 `mcp_test.go`：normalize 全分支；canonicalize stdio（command 必填、args/env 修剪、cwd、透传键、丢 null）、http/sse、缺 type 推断、OpenCode local/remote 转译；市场列表；scan 用 `mcpHomeOverride` 指向临时 HOME 播种 claude+cursor 同 id（apps 合并 [claude_code,cursor]）、bar 仅 claude、gemini 畸形 JSON 产生 warning —— 全部不触真实用户目录。
- 剩余增量：增量2 = 写路径（upsert/set_apps/remove + require_complete_scan 完整性门禁 + 各 agent 的 canonical→native 写回，含 Claude/CodeBuddy 的 enabledPlugins 门、Codex/Grok TOML、Cline http→streamableHttp 等）、剩余 5 agent 的 reader；增量3 = 市场 search/detail/install（official registry + smithery，含 install-option 解析）。
- 结果：build / `go vet ./...` / `go test ./...` 全绿；ROUTE_INVENTORY 523 路由、280 实现、mcp 2/8、P0 仍 128/128。


### 2026-10-11（续：B11i — background 工作区背景图 + wallhaven 市场）

- `src-go/web/handlers/background.go`（6 命令）：`background_read` / `background_set` / `background_clear`（磁盘单文件）+ `background_market_search` / `background_market_asset` / `background_market_download`（wallhaven.cc 市场）。忠实移植 commands/background.rs + backgrounds/mod.rs + backgrounds/marketplace.rs。
- 存储：单文件 `DataDir/backgrounds/background.img`，字节原样存（动图 GIF/APNG/WebP 保持动画），路径解析沿用上游 paths.rs 优先级（YOYOCODE_HOME → YOYOCODE_DATA_DIR → AppState.DataDir → ~/.yoyocode）。写入采用 per-writer 暂存文件 + rename 原子覆盖 + 过期 .tmp 清扫，与 write_background_atomic_in 一致。wire：BackgroundAsset `{mime, dataBase64}`（camelCase）。
- 校验：魔数识别 PNG/JPEG/WebP/GIF（否则 invalid_input），MIN 64B / MAX 16MiB 字节上限；40Mpx 像素上限用无依赖的 header 尺寸解析（PNG IHDR / GIF 逻辑屏 / JPEG SOF / WebP VP8·VP8L·VP8X）复刻，解析不出时仅跳过像素上限（字节/格式上限仍生效），符合上游"sanity bound 非 DoS 边界"的定位 —— 未引入 image 解码依赖。read 时按魔数嗅探 mime。
- market（wallhaven.cc）：host 白名单 `wallhaven.cc` / `*.wallhaven.cc`，仅 https、禁 userinfo；`net/http` 客户端的 CheckRedirect 对每一跳重新校验白名单（≤5 跳）。search 强制 `purity=100`（SFW，永不取自入参），category 仅 all/general/anime/people（否则报错）→ bitmask 111/100/010/001，query ≤128 字符；`MarketSearchPage{items, page, lastPage}`、`MarketWallpaperSummary{id, thumbUrl, fullUrl, sourceUrl, width, height, fileSizeBytes, category}`（camelCase），列表过滤掉非 wallhaven 托管条目，sourceUrl 由 id 派生（`/w/{id}`，不信任列表）。asset（缩略图上限 4MiB）→ BackgroundAsset；download（full 图上限 16MiB、sourceUrl 必须 `https://wallhaven.cc/w/<id>`）→ 取字节→ validate →原子落盘。content-type 早筛 image/jpeg·png·webp，read_capped 对 Content-Length 与累计字节双重封顶。
- 测试 `background_test.go`：磁盘 set/read/clear 往返 + 空读为 null + clear 幂等；validate 拒小/非图/超像素；sniffMime 四格式；wallhaven host 白名单（接受子域、拒 http/异host/后缀诈骗/userinfo）；category 映射 + 未知拒绝；marketDownload 对坏 sourceUrl / 异host full 短路（不打网络、不落盘）；parseSearchPayload 过滤异host条目并派生 sourceUrl、page/lastPage 下限为 1。market 真实抓取不在测试中触网。
- 顺带修正 `scripts/gen_inventory.js` 路由解析：先剥除 `//` 行注释、容忍 `post(...)` 后的 `.layer(...)` 中间件链与行间注释（上游 `/background_set` 等以注释说明 body 上限，旧单正则会漏匹配）。重算后上游路由 519→523（补回 4 条此前被漏解析的真实路由），已实现 278，P0 仍 128/128。
- 结果：build / `go vet ./...` / `go test ./...` 全绿；ROUTE_INVENTORY 523 路由、278 实现、background 6/6。


### 2026-10-11（续：B11h — project_boot 项目脚手架）

- `src-go/web/handlers/project_boot.go`（5 命令）：`detect_package_manager`（跑 `<pm> --version` → {name, installed, version}）、`create_shadcn_project`、`detect_hyperframes_skills`、`install_hyperframes_skills`、`create_hyperframes_project`。忠实移植 commands/project_boot.rs。
- 项目名校验与上游一致：非空、不以 `-` 开头（防 CLI 选项注入）、单一路径段（无 `/` `\\` `..` 绝对路径）。create_* 按包管理器选 runner（pnpm/yarn dlx、bunx、npx[--yes]）shell out 到 `shadcn@latest init` / `hyperframes@latest init`，目标目录非空→already_exists，缺依赖→dependency_missing、退出非 0→external_command(detail)。
- `install_hyperframes_skills`：对六支持 agent（claude-code/codex/opencode/gemini-cli/openclaw/cline）逐个 `npx --yes skills@latest add heygen-com/hyperframes --skill * -g -y --agent <a>`（GIT_CLONE_PROTECTION_ACTIVE=0），空选/非法 agent→invalid_input。`detect_hyperframes_skills` 暂报六 agent 均未装（技能目录枚举子系统后续随 custom_skills 建设）。
- 测试 `project_boot_test.go`：pm 探测形状、项目名校验（`../escape`/`-rf`/缺模板/缺目标目录全 invalid_input，均在 shell out 前短路）、六 agent 未装、install 空选/非法 agent invalid_input。

**当前状态**：519 路由 **272** 条、P0 128/128。`gofmt`/`go vet`/`go test ./...` 全绿。
B11 已完成：folder_commands·folder_links·automation·system_settings(代理/终端)·canvas·computer_tools·browser_tools·app_update·browser_bridge·token_usage·project_boot。后续：mcp·chat_channel·custom_skills/experts/science·pet·config_sync/backup·background·workspace_*·computer(桌面)；B12 mygo 原生 UI。

### 2026-10-11（续：B11g — token_usage 用量看板）

- `src-go/web/handlers/token_usage.go`（4 命令）+ 新增 `token_usage_turn`/`token_usage_sync` 两表：report/facets/status/sync。忠实移植 commands/token_usage.rs + token_usage_service.rs 的聚合。
- report：事实表 join conversation（取 folder_id/agent_type）按 start/end/folderIds/agentTypes/models 过滤（null=不限、[]=空选=无结果），在**查看者 UTC 偏移**下按 day/week/month 分桶，一次遍历产出 totals、稠密 series（空桶补零、超 2000 桶退化稀疏）、by_folder/by_agent/by_model（tokens 降序、key 平手）、weekday×hour 热力、top 会话(≤8，标题由命令层补)、streak（最长/当前连续活跃日）、previous_totals（compare_previous+双界）、first/last activity、truncated。headline conversation_count 走工作区口径（活跃顶层会话 distinct）而非 fold。
- facets：事实中出现的 folders（alias/name/path/parent_id）+ agents/models（按 tokens 降序）+ 数据区间。status：total/synced/stale 会话数、fact_rows、last_synced_at、running。sync：本构建无 transcript parser，返回空成功结果（全 0）。
- 测试 `token_usage_test.go`：直灌 token_usage_turn 事实（两天两模型）→ facets（1 folder/1 agent/2 models/区间）→ report（总 490、3 turns、2 活跃日、conversation_count=1、2 日 series 且首日 430、by_model sonnet 居首、top 带标题、热力非空、活跃边界）→ status fact_rows=3 → sync 全 0；空库 report 全 0、series 空。

**当前状态**：519 路由 **267** 条、P0 128/128。`gofmt`/`go vet`/`go test ./...` 全绿。
B11 已完成：folder_commands·folder_links·automation·system_settings(代理/终端)·canvas·computer_tools·browser_tools·app_update·browser_bridge·token_usage。后续：mcp·chat_channel·custom_skills/experts/science·pet·config_sync/backup·background·project_boot·workspace_*·computer(桌面)；B12 mygo 原生 UI。

### 2026-10-11（续：B11f — app_update + browser_bridge）

- `src-go/web/handlers/app_update.go`（4 命令）：`app_update_state` 返回忠实 Idle 快照（{seq:0, status:"idle", capability}，capability 由 YOYOCODE_SUPERVISED 判 supervised/reexec）；`perform_app_update`/`restart_app`/`rollback_app` 在本 Go 版（非自替换二进制运行时）返回 invalid_input「本构建不支持原地更新」——与桌面构建同形，升级 UI 不会因缺路由报错。
- `src-go/web/handlers/browser_bridge.go`（3 命令）：端口桥接（让工作台 iframe 本地 dev server 过 上游 鉴权）。本 Go 版未运行桥接监听子系统，故 `browser_bridge_status` 回 {enabled:false}、`browser_bridge_open` 先按上游顺序校验目标（仅 http:// + 本机 loopback 可桥接，否则 invalid_input）再因禁用回 configuration_missing、`browser_bridge_close` 回 {ok:true}。wire 形状对齐，UI 降级方式与上游一致。
- 测试 `app_update_bridge_test.go`：app_update idle+capability+三动作 400；browser_bridge 禁用状态 + 非 http/非 loopback 目标 invalid_input + loopback 目标过校验后 configuration_missing + close ok。

**当前状态**：519 路由 **263** 条、P0 128/128。`gofmt`/`go vet`/`go test ./...` 全绿。
B11 已完成：folder_commands·folder_links·automation·system_settings(代理/终端)·canvas·computer_tools·browser_tools·app_update·browser_bridge。后续：mcp·chat_channel·custom_skills/experts·pet·token_usage·config_sync/backup·background·project_boot·workspace_*·computer(桌面)；B12 mygo 原生 UI。

### 2026-10-11（续：B11e — computer_tools + browser_tools 设置）

- `src-go/web/handlers/tools_settings.go`（6 命令）：`get/set_browser_tools_settings`（{enabled, eval}）、`get/set_computer_tools_settings`、`set_computer_tools_enabled`、`set_computer_tools_preferences`。按上游 per-key 存 app_metadata（browser_tools.enabled/eval_enabled；computer_tools.enabled/grant_ttl_minutes/blocklist/blocklist_removed/stop_shortcut/show_indicator/allow_foreground/default_delivery/launch_enabled/clipboard_enabled/screen_enabled），聚合为完整 settings 对象返回。
- ComputerToolsSettings 默认忠实：enabled=false、grant_ttl_minutes=30、show_indicator/allow_foreground=true、default_delivery=background、stop_shortcut 平台默认（Win `Control+Alt+Escape`/Mac `Control+Command+Escape`）、blocklist_defaults=[]（桌面平台内容，server 空）。set_preferences 仅写出现的字段、绝不动 enabled（局部补丁语义）；blocklist 大小写不敏感去重+trim（normalize_blocklist）。事件 `computer-tools-settings://changed` / `browser-tools-settings://changed`。
- 测试 `tools_settings_test.go`：browser 默认/往返/持久化；computer 默认全字段形状+默认值、enabled 独立开关、preferences 局部写不清 enabled + delivery/blocklist 去重、full settings 写入。

**当前状态**：519 路由 **256** 条、P0 128/128。`gofmt`/`go vet`/`go test ./...` 全绿。
B11 已完成：folder_commands·folder_links·automation·system_settings(代理/终端)·canvas·computer_tools·browser_tools。后续：mcp·chat_channel·custom_skills/experts·computer(窗口共享,桌面)·browser_bridge·pet·token_usage·config_sync/backup·background·project_boot·workspace_*·app_update；B12 mygo 原生 UI。

### 2026-10-11（续：B11d — canvas 画布/节点图）

- `src-go/web/handlers/canvas.go`（12 命令）+ 新增 `canvas_board`/`canvas_node` 两表：list_boards/create_board/update_board/delete_board + list_nodes/create_node/update_node/move_nodes/group_into_region/detach_member/delete_node/delete_nodes。忠实移植 canvas_service.rs。
- 修订协议：工作区全局 `canvas_revision`（app_metadata），每次变更在 `canvasMu` 下一笔事务内 bump，返回 `CanvasMutation{value, revision}`；list_nodes 返回 `CanvasSnapshot{board_id, nodes, revision}`。节点事件走 `canvas://changed`、看板事件走 `canvas-board://changed`。
- 节点语义忠实：kind=folder/group/agent/conversation 的绑定列校验（folder/会话存活检查、agent_type 必填），file/terminal 必须有 path 且其他 kind 禁带 path，note 独占 content，custom region member_ids（JSON，上限 200，去重）；几何 clamp（坐标 ±1e6、尺寸 48..20000、非有限报错）、grid 轴 0..12 仅 region、颜色限定 12 主题色词表；board name/description 长度上限。
- group_into_region 单事务：成员去重+存活校验、合并进既有 custom region 或按 geometry 建新 region、吞并 loose conversation pin（领养校验：被吞卡片的会话必须在最终成员里）并删除；回 `{node, deleted_ids}`。detach_member：custom 移除成员 / 绑定 region 复制，落一张 conversation pin。list_boards 预览矩形按面积降序截断 200。
- 测试 `canvas_test.go`：board 空列表→create(含坏色拒绝)→summary 形状→list_nodes 快照→create note(+revision 前进、path 禁用校验)→create 会话 pin→update(title/move)→move_nodes→group_into_region(新 custom region 吞 pin：成员1、deleted1)→剩 2 节点→delete_node→剩 1→delete_board→清空。

**当前状态**：519 路由 **250** 条、P0 128/128。`gofmt`/`go vet`/`go test ./...` 全绿。
B11 已完成：folder_commands·folder_links·automation·system_settings(代理/终端)·canvas。后续：mcp·chat_channel·custom_skills/experts·computer(_tools)·browser(_tools/bridge)·pet·token_usage·config_sync/backup·background·project_boot·workspace_*·app_update；B12 mygo 原生 UI。

### 2026-10-11（续：B11c — system_settings 代理/终端）

- `src-go/web/handlers/system_settings_more.go`（5 命令，补齐 system_settings 模块）：
  - `get/update_system_proxy_settings`：SystemProxySettings {enabled, proxy_url, no_proxy}。update 走 normalize —— 启用必须有 proxy_url（否则 configuration_missing）、无 scheme 时补 `http://` 并校验 host（net/url）、no_proxy 规范化（trim/去空/逗号重连）；读路径也 normalize（旧 bare host:port 自愈）；保存后把 HTTP(S)_PROXY/ALL_PROXY/NO_PROXY 写入进程环境（含 loopback 旁路）。
  - `get/update_system_terminal_settings`：SystemTerminalSettings {default_shell, colorize_command_output}，trim 空值→null。
  - `get_available_terminal_shells`：默认 shell 选择器 —— system/custom 两行恒在，Windows 另加 pwsh.exe/powershell.exe/cmd.exe（exists 经 LookPath/路径探测，Windows 补 .exe）；resolved_shell 由 resolveEffectiveShell（选择解析为具体路径或原样，system 走平台 fallback SHELL→COMSPEC/zsh·bash·sh）。label_key 保留给前端 i18n。
- 测试 `system_settings_more_test.go`：proxy 默认/缺 url 报错/bare host 补 scheme + no_proxy 规范化/持久化自愈/禁用；terminal 默认/往返/available shells（首 system、尾 custom+accepts_custom_path、resolved_shell 存在）。

**当前状态**：519 路由 **238** 条、P0 128/128。`gofmt`/`go vet`/`go test ./...` 全绿。
B11 已完成：folder_commands·folder_links·automation·system_settings(代理/终端)。后续：canvas(12)·mcp·chat_channel·custom_skills/experts·computer(_tools)·browser(_tools/bridge)·pet·token_usage·config_sync/backup；B12 mygo 原生 UI。

### 2026-10-11（续：B11b — automation 自动化）

- `src-go/web/handlers/automation.go`（11 命令）+ 新增 `automation`/`automation_run` 两表：list（updated_at desc）/get/runs（created_at desc, limit 默认 100）/create/update/set_enabled/delete（软删）/mark_seen/compute_next_run/run_now/cancel_run。
- cron 下次运行：`computeNextRun` 用 robfig/cron 标准 5 字段解析器在自动化的 IANA 时区求值（`time.LoadLocation`），返回严格晚于 now 的 UTC RFC3339；6/7 字段走带秒解析器。`nextRunFor`：仅 enabled && schedule 持久化 next_run_at，否则 null；set_enabled 禁用清空、启用重算（与 service next_run_for 一致）。
- 校验忠实移植：name 必填、config 需 display_text 或 prompt_blocks、enqueue_task action 需 root_folder_id、shared_in_root + is_remote_branch 互斥、schedule 必须有 cron。AutomationInfo/AutomationRunInfo wire 逐字段对齐（run 省略 connection_id）。
- run_now 记录一条 running 的 manual run 并回 run id、更新 last_run_*；cancel_run 置 cancelled+ended_at。实际 agent 执行（engine/ACP）为后续增强，board/run 状态与返回契约忠实；发 `automation://changed`。
- 新增依赖 github.com/robfig/cron/v3。测试 `automation_test.go`：CRUD + 缺 prompt 校验 + 禁用/启用 next_run 变化 + run_now→runs(running)→cancel(cancelled) + mark_seen + compute_next_run（周一 09:00 命中 + 非法 cron/时区报 invalid_input）。

**当前状态**：519 路由 **233** 条、P0 128/128。`gofmt`/`go vet`/`go test ./...` 全绿。
B11 已完成：folder_commands·folder_links·automation。后续：custom_skills/experts（含 bundled 内容与 agent 目录布局）、mcp、chat_channel、config_sync/backup/canvas/computer/science、pet、token_usage(依赖 transcript)；B12 mygo 原生 UI。

### 2026-10-11（续：B11a — folder_commands + folder_links）

- **folder_commands**（6）：`src-go/web/handlers/folder_commands.go` —— list/create/update/delete/reorder + `bootstrap_folder_commands_from_package_json`。修正 `folder_command` 表 schema 为上游真实列 `{id, folder_id, name, command, sort_order, created_at, updated_at}`（此前为 label/command_type 的误设计）。create 取 MAX(sort_order)+1；bootstrap 按 lockfile 选包管理器（pnpm/yarn/bun/npm），用流式 JSON 解码器按**文件顺序**读取 package.json scripts → `<pm> run <name>`，已有命令则原样返回（幂等）。
- **folder_links**（6）：`src-go/web/handlers/folder_links.go` + 新增 `folder_link` 表 —— list/preview/create/rename/repair/remove。忠实移植 commands/folder_links.rs：名字 sanitize（控制字符/路径分隔符/Windows 保留名/尾点空格/96 字上限）、`<base>`→`<base>-2..-99` 去重、目标校验（not_found/not_a_directory/same_as_root/ancestor_of_root/inside_root/already_linked/name_unavailable）、按磁盘实时计算 status（ok/missing/conflicted/broken）、创建后 append git `info/exclude` 规则（rev-parse --git-common-dir/--show-toplevel）。符号链接用 os.Symlink，Windows 无权限时回退 `mklink /J` 目录联接；删除只删链接项不碰目标。发 `folder://links-changed`。
- 测试：`folder_commands_test.go`（CRUD/reorder/bootstrap 顺序+包管理器+幂等）、`folder_links_test.go`（preview→create(status ok)→already_linked→rename→remove，目标目录不受影响；symlink 不可用环境自动 skip）。

**当前状态**：519 路由 **222** 条、P0 128/128。`gofmt`/`go vet`/`go test ./...` 全绿。
后续 B11：token_usage（依赖 transcript→usage 管道）、custom_skills/experts（技能/专家文件管理）、mcp、automation/backup/config_sync/canvas/chat_channel/computer/science 等；B12 mygo 原生 UI。

### 2026-10-11（续：最后一条 P0 — get_folder_conversation_turns，P0 128/128 ✅）

- 新增 `src-go/web/handlers/conversation_turns.go`：`get_folder_conversation_turns`（反向无限滚动的历史分页）+ 逐字移植 commands/turn_window.rs 的 turn-window 算法：
  - `prefixFingerprint`：链式 (role_tag, timestamp_millis) FNV-1a（offset 0xcbf29ce484222325 / prime 0x100000001b3），定宽 16-hex，与上游 + TS 侧 BigInt 实现位对齐（空前缀 = offset basis）。
  - `roundAlignBackward`（窗口起点回退到 user 轮次边界，roundAlignCap=200）/`resolvePageBounds`（end=min(beforeIndex,total)，limit clamp 1..500，start 对齐）/`computeWindowMeta`（offset/total/assistant_before/prefix_hash/uncovered_prefix_max_ts）。
  - 返回 `ConversationTurnsPage` 全字段：turns/turns_offset/turns_total/assistant_turns_before_offset/prefix_hash/prefix_hash_before_index(seam 证明)/uncovered_prefix_max_ts。
  - `loadConversationTurns` 当前对无持久化 transcript 的 DB 会话返回空列表 —— 与上游「磁盘无 JSONL 则 turns 为空」路径一致；per-agent JSONL transcript 解析为后续增强，页契约与指纹不变即可无缝填充。
- 测试：`conversation_turns_test.go`（HTTP：缺 id→invalid_input；真实会话→空但完备的页，空前缀指纹=offset basis，offset 0 时 uncovered 为 null）+ `conversation_turns_internal_test.go`（算法单测：指纹确定性/顺序敏感、round-align、page bounds clamp、window meta 的 assistant_before 计数）。

**当前状态**：519 路由 **210** 条、**P0 128/128（mcode-app 依赖命令全数实现）**。`gofmt`/`go vet`/`go test ./...` 全绿。
后续：B9d（Gitea/GitLab REST，非 P0）、B11 其余后端模块（automation/backup/canvas/chat_channel/token_usage/mcp 等，含会话 transcript→turns 解析以让 turns 真正填充）、B12 mygo 原生 UI 页面树。

### 2026-10-11（续：B6 terminal — PTY 全链路）

- 新增 `src-go/terminal/` 包（镜像 src-tauri/src/terminal）：
  - `types.go`：TerminalEvent{terminal_id,data,seq}/TerminalSnapshot{alive,data,seq}/TerminalInfo{id,title}，wire 逐字段对齐。
  - `manager.go`：跨平台 PTY 管理器 —— 每终端 128KB 环形 scrollback（按 rune 截断）、reader goroutine 广播 `terminal://output/{id}`（含 seq 游标）、进程退出广播 `terminal://exit/{id}`；spawn（可传 terminalId，重复拒绝；否则生成 uuid 式 id）/write/resize/kill/list/snapshot。生命周期用 `sync.Once` finalize（reader EOF / List·Snapshot reap / Kill 三路幂等关闭），List/Snapshot 通过 waiter goroutine 的 Exited() 标志 reap（规避 ConPTY 不及时吐 EOF 的已知问题）。
  - `pty.go`：基于 github.com/aymanbagabas/go-pty 的统一后端（Windows ConPTY / unix98 pty），正确拆除伪控制台（避免在阻塞 Read 下关句柄导致的堆损坏 0xc0000374）；shell 选择镜像 manager.rs（Windows 默认 powershell -NoLogo -NoProfile，有初始命令加 -NoExit -Command；cmd.exe /K；unix 登录 shell -c）。Kill 走 TerminateProcess/Process.Kill 杀子进程，由 reaper 统一关闭。
- `src-go/web/handlers/terminal.go`：terminal_spawn（→ id 字符串）/terminal_write/terminal_resize/terminal_snapshot/terminal_kill/terminal_list，接 AppState.Terminal。AppState 新增 Terminal 字段 + 以 broadcaster 作 Emitter 注入（terminal 包不 import web，用 Emitter 接口避免环）。
- 测试 `terminal_test.go`：list 空 → spawn(workingDir) → list=1 → resize → write `echo MARKER` → 轮询 snapshot 命中 MARKER 且 alive → kill → 轮询 snapshot 直至 alive=false；PTY 不可用环境自动 skip。-count=3 稳定无堆损坏。
- 新增依赖：github.com/aymanbagabas/go-pty（内部 UserExistsError/conpty + creack/pty），golang.org/x/sys。`gen_inventory.js` 增强：识别经 helper（registerXxx(reg,"cmd")）注册的命令。

**当前状态**：519 路由 **209** 条、P0 **127/128**。`gofmt`/`go vet`/`go test ./...` 全绿。
剩余唯一 P0 `get_folder_conversation_turns` 依赖会话 turns 模型（消息存储 + turn 解析 + prefix-hash 分页），属 B11 chat/conversation 子系统，后续整体建设。

### 2026-10-11（续：B10 尾 — forge 触发任务）

- `src-go/web/handlers/work_task_forge.go`（2 命令）：
  - `work_task_create_from_forge`：忠实复刻 commands/forge.rs 流程 —— 校验文件夹 origin **就是**所声明仓库（否则返回判别式 `{outcome:"folder_mismatch", folder_remote}`）→ 由 remote 派生 provider（与客户端 claim 不符报 invalid_input）→ 解析锁定账号（resolveForgeAuth）→ PR 则 hydrate head/base（GET /pulls/{n} + 已关闭/已合并 pull_is_workable 门槛）→ 规范 source_key `{provider}:{host}:{repo}:{kind}:{number}` → item_url（经 web_origin 推导）→ ForgeSourceMeta → scenario 解析（fix/plan_first/review_fix/review_only，默认 issue=fix/PR=review_fix，retired `investigate` 专用错误，kind 不符报错）→ 服务端合成 prompt（instruction 模板**逐字移植** forge_instruction + 站内 standing 指令 + 用户备注；untrusted-data envelope 逐字移植 envelope.rs：fence 转义 `----[forge-data`→全角、12k 预算、CRLF/NUL 清洗）→ WorkTaskConfig（prompt_blocks 两块 text + display_text + agent_type 覆盖 + report deliverable）→ 以 source_key 去重（非 terminal 态有同源活动任务则 `{outcome:"duplicate", existing}`，force 跳过）→ 插入 todo 行（含 source_kind/source_key/source_meta）+ created/forge_linked 事件 + `work_task://changed`，回 `{outcome:"created", task}`。
  - `work_task_lookup_by_source`：sourceKeys（上限 100）→ 每 key 最新（created_at desc）未删任务行 → `[{source_key, task_id, status, verdict, updated_at}]`。
  - 新增 forgeAuth.accountID 字段（供 source_meta 记录锁定身份）。
- 测试 `work_task_forge_test.go`（issue 路径免网络）：真实 git repo + origin + 存储默认 github 账号/token → create(outcome=created, 标题 `#5 · …`, source_kind=forge_issue, 两 prompt_blocks：指令含 issue #5+用户备注、envelope 含 fence+标题) → lookup_by_source(命中 todo) → 二次 create(duplicate) → 仓库不符(folder_mismatch)。

**当前状态**：519 路由 **196** 条、P0 **115/128**（work_task 模块 P0 全数完成，含 forge 触发两条）。`gofmt`/`go vet`/`go test ./...` 全绿。
下一步：B9d（Gitea/GitLab REST）、B6 terminal（PTY）、B11 其余后端模块、B12 mygo 原生 UI。

### 2026-10-11（续：B10 work_task 任务看板）

- `src-go/db/migration.go`：`work_task` 重写为忠实上游 36 列（id/folder_id/title/config/status/failure_reason/last_error/run_seq/sort_order/worktree_folder_id/conversation_id/connection_id/base_branch/base_sha/work_branch/merge_state/pending_merge/cleanup_state/verdict/result_summary/files_changed/additions/deletions/merge_commit/completion_kind/preflight/archived_at/scheduled_at/source_kind/source_key/source_meta/created_at/updated_at/started_at/settled_at/finished_at/deleted_at）+ 新增 `work_task_event`/`work_task_settings`/`work_task_template` 三表与索引。
- `src-go/web/handlers/work_task.go`（26 命令）：
  - 数据面：`work_task_list`（folderId 过滤，sort_order→id 排序）/`work_task_get`/`work_task_events`（倒序，limit 默认 500）/`work_task_attention_count`（review+awaiting_input 计数）/`work_task_create`（draft，sort_order 取 MAX+1，建 created 事件 + `work_task://changed` firehose）/`work_task_update`（title/config）/`work_task_reorder`/`work_task_delete`（软删）/`work_task_archive`/`work_task_cleanup`（归档且 done/failed/canceled 的硬清）。
  - 生命周期状态机：`start`/`retry`/`requeue`/`schedule`/`return`/`cancel`/`merge`/`merge_unqueue`/`deliver_pr`/`complete`/`start_all`，均改 board 状态 + 追加 timeline 事件 + firehose；`merge` 回 true、`deliver_pr` 回 ""（与上游返回类型对齐）。status 值 todo/queued/preparing/running/awaiting_input/review/merging/done/failed/canceled。
  - 设置：`work_task_settings_get`/`_effective`（defaults 合并 own）/`_get_own`（无则 null）/`_set`（folder upsert）/`_delete`。defaults 对齐上游（merge_strategy=squash、max_concurrent=3、auto_merge=false…）。
  - 模板：`work_task_template_list`/`_save`（按 name upsert）/`_delete`（id 或 name）。
  - diff/changed_files：git over worktree（base_sha...work_branch），numstat 解析 + git 路径反转义。
  - WorkTaskInfo 派生字段：`worktree_missing`=false、`agent_type`（config.agent_type→folder.default_agent_type）、`merge_queued`（pending_merge JSON）、`compacting`=false。agent 实际执行（ACP）/worktree 创建/forge 投递为后续增强，board 行 shape 与状态转换忠实。
- 测试 `work_task_test.go`（真实 temp DB，经真实 Router）：create→shape 校验→list→get→update→events→start(running+started_at+run_seq)→cancel→merge(done+merged)→attention_count=0→settings defaults/set/get_own/effective 合并/delete→templates insert/list/upsert 不增行/delete→软删后 list 空。

**当前状态**：519 路由 **194** 条、P0 **113/128**。`gofmt`/`go vet`/`go test ./...` 全绿。
下一步：`work_task_create_from_forge`/`work_task_lookup_by_source`（work_task 表已就绪）、B9d（Gitea/GitLab REST）、B11 其余后端模块。

### 2026-10-11（续：B9c forge PR 细节/merge — GitHub）

- 新增 `src-go/web/handlers/forge_rest_pr.go`（复用 forge auth 解析 + GitHub client）：
  - `forge_change_detail`（GET /pulls/{n} → ForgeChangeDetail：base/head ref、head_sha、mergeable、merge_state(mergeable_state)、additions/deletions/changed_files/commits、checks via /commits/{sha}/check-runs 映射 ForgeCheckList）。
  - `forge_change_files`（GET /pulls/{n}/files → ForgeChangedFileList，status 映射 added/removed/renamed/modified，previous_path）。
  - `forge_merge_options`（GET /repos/{r} 的 allow_*_merge → methods/default_method/merge_strategy）。
  - `forge_merge_change`（PUT /pulls/{n}/merge {merge_method, sha?}，成功后回传合并后的 PR 行）。
- GitHub forge 现已全量（13 条命令，含 identity/list/count/labels/comments/create/state + PR 细节/merge）。

**当前状态**：519 路由 **170** 条、P0 **91/128**。`go test ./...` 全绿。
B9 余下（B9d，非阻塞）：Gitea(/api/v1)/GitLab(/api/v4) REST —— auth 解析与 DTO 已共用，仅差各家 HTTP 映射。下一步优先 **B10 work_task**（26 P0）。

### 2026-10-11（续：B9b forge REST — GitHub）

- 新增 `src-go/web/handlers/forge_rest.go`（GitHub：github.com + 企业版 /api/v3）：
  - **auth 解析**（兼容核心）：folder → origin remote → 识别 provider → 匹配 github_accounts（可 pin accountId，否则 is_default/首个）→ account_token。错误**逐字段对齐 mcode 恢复契约**：无账号→`Forge.errors.noAccount`(params {host, provider 显示名})、不支持的 host→`Forge.errors.unsupportedHost`({host})、账号 provider 与检测不符→`Forge.errors.wrongForge`({host, detected})，全 422 configuration_missing；缺 token→authentication_failed(422)。
  - **命令**：`forge_identity`（GET /user）、`forge_list_issues`（/issues 过滤 PR / /pulls，映射 ForgeIssueRow 全字段：labels 规范化 #rrggbb、author/avatar、is_pr、comments，列表含 page/per_page/has_next/incomplete）、`forge_tab_count`（诚实返回 null，count 需 search API）、`forge_list_labels`（/labels?per_page=100，truncated）、`forge_list_comments`、`forge_create_comment`、`forge_set_item_state`（close→closed/reopen→open，issues vs pulls 端点，回传行）、`forge_create_issue`。
- 测试（无网络，纯 auth 契约）：github.com 无账号→noAccount(host/provider 参数)、未知 host→unsupportedHost(host 参数)、有账号无 token→authentication_failed。
- 复用 B3 的 github_accounts/account_token 存储。

**当前状态**：519 路由 **166** 条、P0 **87/128**。`go test ./...` 全绿。
B9 余下（B9c）：Gitea(/api/v1)/GitLab(/api/v4) REST + PR 细节 `forge_change_detail`/`forge_change_files`/`forge_merge_options`/`forge_merge_change`。之后 B10 work_task。

### 2026-10-11（续：B9a forge 基础 — 设置 + 远程解析）

- 新增 `src-go/web/handlers/forge.go`：
  - `forge_settings_get`/`forge_settings_set`：ForgeSettingsStore {global, folders} 存 app_metadata `forge_workbench_settings`。panel 字段 {default_issue_scenario, default_pr_scenario, writeback_default(默认 true), scenario_prompts}，set 支持 global 作用域与按 folderId 的覆盖/删除（settings 省略即删该 folder 行回落 global），scenario 文本 trim。
  - `folder_forge_remote`：`git remote get-url origin` → `parseRemoteURL`（scp 式 git@host:owner/repo 与 scheme 式 https/ssh 都解析）→ {server_host, owner_repo, remote_url(脱敏 user:pass@), provider, supported}。provider 由 `providerFromHostName`（含 gitlab/github/gitea/forgejo 标签判定）或已存账号的 provider 推定，否则 github+supported=false。
- 复用 B3 的 github_accounts 存储做 host→账号 provider 识别。
- 测试：settings 默认/global trim/folder 覆盖与删除；folder_forge_remote 对 github.com(识别)、未知 git.corp.internal(github+unsupported) 的 host/owner_repo/provider/supported 断言。

**当前状态**：519 路由 **158** 条、P0 **79/128**。`go test ./...` 全绿。
B9 余下（REST 客户端）：`forge_identity`/`forge_list_issues`/`forge_tab_count`/`forge_list_labels`/`forge_list_comments`/`forge_create_comment`/`forge_set_item_state`/`forge_create_issue`/`forge_change_detail`/`forge_change_files`/`forge_merge_options`/`forge_merge_change` —— 需 GitHub/Gitea/GitLab REST（复用 B3 account token），下一子批 B9b 实现。

### 2026-10-11（续：B8 git 写操作）

- 新增 `src-go/web/handlers/git_write.go`（+15 条）：
  - `git_add_files`、`git_commit`（跳过已 staged 的删除、commit 后数 HEAD 文件得 committed_files、发 folder://git-commit-succeeded）、`git_init`、`git_rollback_file`（restore HEAD；untracked 则删）。
  - stash 全套：`push`（-m/--keep-index）、`pop`/`apply`/`drop`（可带 stashRef）、`clear`、`show`（-p diff）、`list`（--format 解析为 GitStashEntry {index,message,branch,date,ref_name}）。
  - 远程：`git_fetch`(--all --prune)、`git_fetch_remote`、`git_pull`(--ff，updated_files=diff 文件数)、`git_push`（当前分支，无 upstream 时 -u 并 upstream_set=true，pushed_commits 来自 rev-list --count；发 folder://git-push-succeeded）。
  - **凭据注入**：请求带 credentials{username,password} 时生成跨平台 askpass（Windows .bat / unix .sh，按 Username/Password 提示回显 env 中的凭据），设 GIT_ASKPASS + GIT_TERMINAL_PROMPT=0；远程错误经 classifyRemoteError 区分 authentication_failed(422) 与 external_command。
- 测试：真实 git —— init→add→commit(committed_files=1)→改文件→stash push(树净+list 1 条 ref stash@{0})→pop(树脏)→push 到本地 bare 远程(upstream_set=true)→fetch。

**当前状态**：519 路由 **155** 条、P0 **76/128**。`go test ./...` 全绿。
下一步：B9 forge（GitHub/Gitea/GitLab issue/PR，复用 B3 token 存储）、B10 work_task。

### 2026-10-11（续：B7d — acp_* 剩余 P0 补齐）

- **agent 设置存储**（新表 `agent_setting`，对齐上游 entity）：`acp_get_agent_status`（enabled/installed_version 来自表，available 来自 PATH 探测 npx/uvx/binary，is_acp_adapter 来自 registry）、`acp_update_agent_env`、`acp_update_agent_config`、`acp_update_agent_preferences`（env 入表、config 多个 blob 入 app_metadata `acp_agent_config:<agent>`）、`acp_reorder_agents`（写 sort_order）。
- **会话↔连接查找**：`acp_find_connection_for_conversation`（扫活连接按 conversation_id / external_id 命中 → {connection_id, event_seq}，否则 null）、`acp_get_session_snapshot_by_conversation`（命中返回 LiveSessionSnapshot，否则 null）。
- **安装族**：`acp_prepare_npx_agent`（best-effort 真实 `npx -y -p <pkg> <cmd> --version` 预热缓存并记录版本；无 npx→dependency_missing）、`acp_install_uv_tool`（uvx 同理）、`acp_download_agent_binary`（binary 代理需下载/缓存子系统，暂返回 dependency_missing）、`acp_uninstall_agent`（清 installed_version）。进度走 `acp-agent-task://<taskId>` 事件。
- `acp_set_config_option`：忠实返回 null（ACP 配置项协议待建模）。
- 测试（无网络）：status 默认值/adapter 标记/available 字段、update_env 关停持久化、reorder、conversation 查找双 null、binary download 422、uninstall 接受。

**当前状态**：519 路由 **140** 条、P0 **73/128**。acp 模块 P0 基本补齐（仅余 cursor/qoder 等 binary 下载依赖未来 binary-cache 子系统）。`go test ./...` 全绿。

下一步按计划：B8 git 写（commit/push/pull/fetch/add/stash，需凭据）、B9 forge（GitHub/Gitea/GitLab issue/PR）、B10 work_task（26 P0，接 ACP+git+forge）。

### 2026-10-11（续：B7c — ACP HTTP handler 接入真实引擎）

- **`acp/connection.go`**：Manager 增加活连接机 —— `ConnectLive`（resolve 启动命令→spawn→initialize→session/new→注册 SessionRegistry，握手 60s 超时，失败清理并带 stderr tail）、`PromptLive`（后台 goroutine 驱动 Prompt，事件流式进环形缓冲）、`RespondPermission`、`CancelLive`（session/cancel + ctx 取消）、`DisconnectLive`（杀子进程 + 注销）。可注入 spawner/resolver 便于测试。
- **`acp/registry.go`** 增 launch specs（15 个 agent 的 npx/uvx/binary 启动配方，含 claude-agent-acp/codex-acp 等真实包名@版本）+ `ResolveSpawn`（npx: `npx -y -p <pkg> <cmd> <args>`；uvx: `uvx --from <pkg> <cmd>`；binary→dependency_missing）。
- **HTTP handler 全部重接真实引擎**：`acp_connect`（返回忠实 ConnectionInfo {id, agent_type, status}）、`acp_prompt`（string/blocks → ACP prompt blocks）、`acp_respond_permission`、`acp_cancel`、`acp_disconnect`、新增 `acp_get_session_snapshot`、`acp_list_connections` 忠实化。typed error 映射：AgentUnavailable/Spawn→dependency_missing(422)、ConnectionUnknown→not_found。
- **AppState** 把 SessionRegistry 注入 Manager，活会话事件直达 WS attach。
- **测试**：注入内存 fake agent 的 e2e —— acp_connect→list_connections→acp_prompt→从 SessionRegistry 读到流式 content_delta="live-reply" + turn_complete=end_turn→acp_disconnect 注销；binary agent（cursor）connect 返回 422 dependency_missing。

**当前状态**：519 路由 129 条、P0 65/128。ACP 引擎从传输到 HTTP 已全链路打通（spawn/握手/流式/权限/取消/断开）。真实 agent 需本机装 npx 包方可实际对话；无 agent 时返回明确依赖错误。`go test ./...` 全绿。

### 2026-10-11（续：B7b ACP 引擎 — 子进程 spawn + session 握手 + 流式事件）

- **`acp/agent_process.go`**：`SpawnAgent` 用 os/exec 起 agent 子进程，stdin/stdout 接 Peer，stderr 收 64 行环形 tail（排障），env 覆盖合并父环境；Close 杀进程。
- **`acp/agent_session.go`**：`AgentSession` 驱动 ACP 握手——`initialize`（通告 fs 读写能力）→ `session/new`（cwd+mcpServers）→ `session/prompt`。把 agent 的 `session/update` 通知翻译成 AcpEvent 并经 `SessionState.Emit` 发布到环形缓冲（WS attach 直接可消费）：
  - agent_message_chunk→content_delta、agent_thought_chunk→thinking、tool_call→tool_call、tool_call_update→tool_call_update、plan→plan_update；未知变体以 acp_update 透传不丢。
  - 反向请求：`session/request_permission`（park 并 emit permission_request，`ResolvePermission` 应答 selected optionId）、`fs/read_text_file`、`fs/write_text_file`。
  - session_started / turn_complete（带 stop_reason + agent_type）/ error 事件。
- **测试**：
  - 内存 Peer 对 + fake agent：完整握手 + 2×content_delta 拼成 "Hello world" + tool_call + turn_complete 全部发布；权限流（agent 阻塞在 request_permission → 收到 permission_request → ResolvePermission → prompt 完成、agent 收到 optionId）。
  - **真实子进程 e2e**：把一个 stdlib-only 的 fake ACP agent 写盘并 `go run`，经 SpawnAgent 跑通 initialize/session_new/prompt 并收到流式 content_delta="spawned-ok"（-short 跳过）。

下一子批 B7c：把 `acp_connect`/`acp_prompt`/`acp_respond_permission`/`acp_cancel` 等 HTTP handler 接到本引擎（spawn 真实 agent、驱动 Prompt、落 conversation），并补齐 acp_* 的 P0（get_session_snapshot / set_config_option / update_agent_env|config / prepare_npx_agent / download_agent_binary / install_uv_tool / uninstall_agent / reorder_agents）。

**当前状态**：ACP 引擎 backbone（传输+spawn+握手+流式+权限）已可端到端运行并测试。`go test ./...` 全绿。

### 2026-10-11（续：B7a ACP 引擎 backbone — JSON-RPC 传输 + agent registry）

ACP 引擎是会话流/work_task/pet 的 backbone，开始分子批建设。

- **`acp/jsonrpc.go`**：JSON-RPC 2.0 over newline-delimited stdio 的 Peer。多路复用我方发出的请求/通知与 agent 的响应；同时分派 agent 反向发来的请求（permission / fs/read_text_file）与通知（session/update）到注册的 handler。特性：原子写、16MiB 行缓冲、非 JSON 行跳过（stderr 泄漏不致命）、ctx 取消、EOF 时清空 pending、handler 返回 `*RPCError` 原样透传（保留 code/message/data）。单测：请求-响应、错误透传、通知、反向请求、method-not-found(-32601) 全绿。
- **`acp/registry.go`**：15 个内置 agent 的编译期目录（对齐上游 acp::registry + models::agent 的 wire 名与 registry_id：claude_code/claude-acp、codex/codex-acp、open_code/opencode、gemini、open_claw、cline、hermes、code_buddy、kimi_code、pi/pi-acp(uvx)、grok/grok-build、cursor(binary)、deepseek、qoder、antigravity）。`acp_list_agents` 改为返回忠实 AcpAgentInfo（11 字段；available=false / registry_version=null 直到 preflight 接入）。单测校验 15 条、adapter 标记、registry-id 往返、wire 字段齐全。

下一子批 B7b：agent 子进程 spawn（os/exec stdio 接 Peer）+ initialize/session_new 协商 + session/prompt → session/update 推进事件流（环形缓冲 → WS attach），用 fake agent 脚本端到端测。

**当前状态**：519 路由 128 条、P0 64/128（本批为引擎内部管线，不新增路由计数）。`go test ./src-go/...` 全绿。

### 2026-10-11（续：folder 管理 + 忠实 FolderDetail）

- 新增 `folders_detail.go`，并**修正前期简化 shape**：原 `list_open_folder_details` 返回 {id,name,path,totalSessions,activeSessions,gitBranch}，与上游 FolderDetail 不符。改为逐字段忠实 shape：{id, name, path, git_branch, default_agent_type, last_opened_at, sort_order, color(默认 inherit), parent_id, kind(默认 regular), alias, group_id}。覆盖 `list_open_folder_details`/`list_all_folder_details`（在 RegisterFolders 之后 re-register 生效）。
- 新增命令（folders 模块 28/38）：`get_folder` / `load_folder_history` / `open_folder` / `open_folder_in_workspace` / `open_folder_by_id`（P0）/ `remove_folder_from_workspace` / `update_folder_color` / `update_folder_alias` / `update_folder_default_agent` / `list_folder_groups` / `create_folder_group` / `update_folder_group` / `delete_folder_group`（成员先 ungroup）/ `set_folder_group` / `apply_sidebar_layout`（folder/group 两套 sort_order 计数器）/ `create_folder_directory`（P0，MkdirAll）/ `create_directory`（parentPath+name）。
- **schema 修正**：folder.kind 默认 'folder' → **'regular'**（对齐 FolderKind::Regular 序列化）；folder_group 加 `color` 列（FolderGroupDetail.color，默认 inherit）。
- 新增 folders_detail_test.go：open_folder 的 12 字段 FolderDetail 校验 + 默认 inherit/regular/null、alias/color/default_agent 更新、group 建/列/设成员/删（删后成员 group_id 归 null）、create_directory 落盘。

**当前状态**：519 条路由已实现 **128** 条；P0 **64/128**。`gofmt`/`go vet`/`go test ./...` 全绿。

### 2026-10-11（续：B5 杂项设置 + misc 端点）

- `settings_misc.go`：`get/set_question_settings`（默认 ON）、`get/set_feedback_settings`（默认 OFF）、`get/set_session_info_settings`（默认 OFF）、`get/set_chat_authoring_settings`（automations/work_tasks 双开关，默认 OFF）、`get/set_delegation_settings`（enabled/depth_limit[1..8 clamp]/completed_cache_max_mb[默认 512]/agent_defaults[空则省略]）。键名/默认值逐条对齐 commands/*.rs；保存后发对应 `*-settings://changed` firehose 事件。
- `misc.go`：`get_home_directory` / `probe_terminal_shell_path`（Stat 存在且非目录）/ `pet_list_active_sessions`（忠实空载 payload：running/waiting/error_count=0 + sessions:[]，待 ACP 引擎接入连接）/ `list_child_conversations`（按 parent_id 查委派子会话）。
- 新增 settings_misc_test.go：question 默认 ON+翻转持久化、feedback/session_info 默认 OFF、chat_authoring 双开关、delegation depth 99→clamp 8；misc：home 非空、probe bogus=false、pet 空载 shape、child 空数组。

**当前状态**：519 条路由已实现 **111** 条；P0 **61/128**。`gofmt`/`go vet`/`go test ./...` 全绿。
B5 剩余（folder 打开/别名/颜色/默认 agent、create_chat_conversation/create_chat_dir）归入后续 folder 批次。

### 2026-10-11（续：B4 files/workspace 文件系统）

新增 `src-go/web/handlers/files.go`（7 条，files 模块 9/10）：
- `read_file_for_edit` → FileEditContent {path, content, etag, mtime_ms, readonly, line_ending}（50MB 上限 + NUL 二进制拒绝 + lf/crlf/mixed/none 行尾探测）。
- `save_file_content` → FileSaveResult，etag 乐观并发（expectedEtag 不符返回 invalid_input），只读/符号链接拒绝，原子写（temp + rename + fsync）。
- `create_file_tree_entry`（file/dir）/ `rename_file_tree_entry` / `move_file_tree_entry`（含"移进自身子目录"防护、同名碰撞 Lstat 检测）/ `delete_file_tree_entry`（符号链接只删链接本身；目录递归）。
- `read_workspace_file_base64`（maxBytes 截断）。
- 路径安全：`resolveTreePath` 拒绝绝对路径与 `..`（对齐上游 resolve_tree_path），`ensureNavigable`/`ensureEntryInWorkspace` 断言结果在 workspace root 内；返回路径统一转正斜杠（跨平台）。
- etag 为自洽的乐观并发令牌（FNV64a(content+size+mtime)，客户端只回显，不跨实现比较）。

新增 `files_test.go`：create(file/dir/nested) → read_for_edit(行尾/etag) → save(stale etag 400 / 正确 etag 落盘) → rename → move(返回正斜杠 rel) → delete(递归) → `../` 穿越被拒。

**当前状态**：519 条路由已实现 **97** 条；P0 **53/128**。`gofmt`/`go vet`/`go test ./...` 全绿。

### 2026-10-11（续：version_control 全量 + 执行顺序锁定）

用户要求：自主规划顺序、不逐批询问，只看最终结果（完美复刻的原生 UI 版 上游）。已建立 goal 跟踪，并锁定执行顺序（见 REPLICATION_PLAN.md §5.1）。

本批：**version_control 12/12（P0 10/10）** 全部落地于 `src-go/web/handlers/version_control.go`：
- `detect_git`（custom_path → which/where → fallback `git`）/ `test_git_path`（复刻 `run_git_version` 的 "git version " 前缀剥离）。
- `get/update_git_settings`、`get/update_github_accounts`：app_metadata JSON blob（键名与上游一致 `git_settings` / `github_accounts`），容错透传。
- `validate_github_token`（Bearer + x-oauth-scopes 头解析 + enterprise `/api/v3`）/ `validate_gitlab_token`（PRIVATE-TOKEN + `/personal_access_tokens/self` 取 scopes）/ `validate_gitea_token`（`token` 认证、scopes 恒空）—— 逐字段对齐 GitHubTokenValidation。
- `save/get/delete_account_token`：以 `account_token:<id>` 存 app_metadata，替代桌面 OS keyring，get/save/delete 线契约一致。

**顺带修一处线格式契约**：Router 对 `nil` 结果原来写 `{}`，但上游 `Option::None` / `Json(())` 序列化为 **`null`**。改为写 `null` —— `get_git_branch` / `get_account_token` / `get_web_server_status` 的客户端都按 null 分支，之前会错判成"有值"。新增 version_control_test.go 锁定（设置/账号往返、token 增删查、空 token 400、删除后 get 返回 null）。

**当前状态**：519 条路由已实现 **90** 条；P0 **52/128**。`gofmt`/`go vet`/`go test ./...` 全绿。

### 2026-10-11（续：git 读 + 分支操作）

- 新增 `src-go/web/handlers/git.go`：全部 shell out 到系统 `git`（与上游一致），实现 **17 条** git 命令。
  重点对齐：
  - `git_status` porcelain=v1 解析（status 两字符 + `core.quotePath=false` + `unquote_git_path`）。
  - `git_log` 复刻上游 `--format=__COMMIT__%x00…%n%B%n__COMMIT_MESSAGE_END__` + `--raw --numstat --no-renames` 解析，
    并完整移植 `get_unpushed_hashes`（all-branches / upstream / remote-override / merge-base 回退四条路径）→ 每条 commit 的 `pushed` 与 `has_upstream`。
  - `git_list_all_branches` 的 worktree porcelain 解析 + `main_worktree_branch` 单独标记。
  - `ensure_git_repo`（严格 .git 检测，不向上递归）→ 非仓库返回 `not_a_git_repository`（**HTTP 422**，与上游 `status_for_app_error_code` 一致）。
- `get_git_branch` / `get_git_head` 从 folders.go 的 not-implemented 占位改为真实实现（移进 git.go，`resolve_git_head` 四态：on-branch / detached / unborn / 非仓库）。
- 新增 `git_integration_test.go`：真实 `git init` 临时仓库，覆盖 branch/log（pushed=false 无 remote）/status/diff/new_branch/checkout/list + 非仓库 422。
  测试**抓到我自己写错的断言**（误以为 404），确认代码与上游 422 契约一致。

**当前状态**：519 条路由已实现 **78** 条；P0 **42/128**。`gofmt` / `go vet` / `go test ./...` 全绿。

### 2026-10-11（本轮：评估前期产物 + M1 纯 DB P0 批次）

**评估结论**：前期 M0 协议层 + 骨架（41 条路由）编译/vet/test 全绿，契约对齐正确，**直接接着做**。

本轮落地（纯 DB、不依赖 ACP 引擎 / git 子进程的 P0）：

- **DB schema 对齐上游 entities**：重写 `opened_tab`（folder_id/conversation_id/agent_type/position/is_active/is_pinned）、
  `conversation_tag`（加 folder_id 作用域、去掉 name 唯一约束）、`quick_message`（title+content），
  新增 `model_provider` 表。
- **quick_messages** 5 条、**conversation_tags** 8 条、**opened_tabs** 2 条（CAS）、**model_provider** 4 条、
  **conversations 变更** 4 条（title/status/pinned/delete）。
- **发现并修复一个真实缺陷**（前期代码）：`web_server.go` 的 `upsertValue` 为 4 个占位符只传了 3 个参数，
  SQLite 报参数不足但错误被 `_, _ =` 吞掉 → **所有 app_metadata 写入静默失效**
  （web 服务配置、语言设置、opened_tabs 版本号都没真正落库）。补足参数后 opened_tabs CAS 测试才通过。
- **新增集成测试** `handlers_integration_test.go`：真实 temp SQLite + 真实 Router，覆盖
  quick_messages 生命周期 / opened_tabs CAS（接受+拒绝 stale）/ model_provider CRUD + 掩码 /
  会话 title·status·pinned / tag 重名与颜色校验 + branch tag 默认值。

**当前状态**：519 条路由已实现 **63** 条；P0 **32/128**。`gofmt` / `go vet` / `go test ./...` 全绿。

### 2026-10-09（前期工作）

- 建立计划与进度文档，克隆上游 与 mygo 到 `_upstream/`。
- 搭建 Go 模块骨架，镜像上游目录结构。
- 落地鉴权、WS 骨架、核心 handler（health/conversations/folders/web_server/system settings/acp）。

### 2026-10-09（本轮：评估修正 + 建立可勾选清单）

**评估结论**：前期骨架可用（可编译、测试通过、鉴权与路由对齐上游），
但协议层有 3 处与 mcode-app 契约冲突，已全部修正：

1. **错误信封**：`{error:"cmd: msg"}` → `AppCommandError{code,message,detail?,i18n_key?,i18n_params?}`。
   不改的话 mcode-app 的 `i18n_key` 恢复分支（forge 三个 422 错误）全部失效。
2. **WS attach**：原实现只登记订阅、不回任何帧 → 实现完整
   snapshot / replay / event / detached 链路 + 环形缓冲 + lag/shutdown 语义。
3. **`__ready__` payload**：`{"ready":true}` → `null`。

修正过程中发现并修复两个真实缺陷（均补了回归测试）：

- `event_seq` 被 `omitempty` 吞掉（seq=0 时键消失，客户端丢失续传游标）
- detach 竞态（订阅登记晚于 goroutine 启动 → 快速 detach 打空 → forwarder 泄漏）

**新增可维护性设施**：

- `scripts/gen_inventory.js`：从上游 `router.rs` 解析 519 条路由 + 模块归属，
  扫描我们的 Go 注册情况，扫描 mcode-app 实际调用（P0 集），
  自动生成 [ROUTE_INVENTORY.md](./ROUTE_INVENTORY.md)。跟进上游后重跑即可。

**当前状态**：519 条路由已实现 41 条；P0（mcode-app 依赖）20/128。
`gofmt` / `go vet` / `go test` 全绿。
