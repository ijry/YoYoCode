# 会话模型切换记录

Codeg 在会话内显示并持久化通过模型选择器或共享配置接口发起的模型切换。

## 行为

- 新会话第一条消息发送前的模型选择属于初始配置，不生成切换记录。
- 已有消息的会话每次有效切换各生成一条记录，包括重新打开历史会话后、发送下一条消息前的切换。
- A → B → A 保留两条记录。重复选择、拒绝、失败、未执行、启动偏好恢复和模型目录广播不生成成功记录。
- 智能体确认生效时显示“模型已切换”；只接受请求的旧协议显示“已请求切换”，不将接受请求当作生效确认。
- 模型名称保存切换时的名称快照；缺失时显示完整模型 ID。
- 切换提示作为会话元数据独立显示，不计入回复数或 token 使用量，也不会作为提示内容发送给模型。
- 刷新、重开、实时事件重放按稳定记录 ID 去重。Markdown/HTML 导出包含这些记录。

本次不从 CLI 原生 `/model` 命令或历史回复的模型字段推测过去的切换，也不补造旧记录。

数据库写入失败时保留原记录 ID 并重试，关闭连接不会取消重试；若数据库持续不可写且进程在写入成功前崩溃，尚在内存中的待写记录仍可能丢失。

## 实现

Rust 的 `acp/model_switch.rs` 从已响应的显式配置请求生成记录，`db/service/model_switch_service.rs` 写入 SQLite。历史加载在分页之前投影为专用 system turn，实时事件和快照携带同一记录身份。前端 `lib/model-switch-history.ts` 在组合原生、已完成和流式回复后插入提示，保持消息原有顺序。

分叉保留原会话完整记录，新分支继承分叉边界之前的记录。会话内 transcript rollover 保留连续历史。

## 验证记录

- 前端相关组合测试：11 个文件，580 个用例通过，包含实时/快照去重、连续切换、历史与本地回复衔接、时间戳精度差异、分页和分叉。
- `pnpm build`：通过，静态导出 41 个页面。
- 最终 `pnpm exec tsc --noEmit`：通过。
- 本次运行时与展示逻辑定向 lint：通过。
- `git diff --check`：通过。
- 全量 `pnpm test` 首轮：755 个文件通过、30 个文件失败（49 个用例失败，另有 2 个加载失败的测试文件）。降低并发复跑失败文件后，27 个文件通过，剩余 3 个文件：
  - `src/components/tasks/tasks-page.focus.test.tsx`
  - `src/components/tasks/tasks-page.search.test.tsx`
  - `src/components/message/use-transcript-known-invocations.test.tsx`
- 前两个任务页测试在现有 `comic-production-entry` 导入链中遇到 `useAppWorkspaceStore.getState is not a function`；最后一个源码匹配测试要求 LF 换行，未修改的表面文件含 CRLF。未为本功能修改这些无关模块。
- 全仓库 `pnpm lint .` 未通过：主要为现有文件的 CRLF/Prettier 问题；未执行全仓库自动格式化。
- 浏览器环境可用，但没有运行中的会话页面；真实智能体端到端切换、重启恢复未通过 GUI 验证。

- Rust 模型切换定向测试：`cargo test --no-default-features --features server-bin --lib model_switch -- --nocapture`，17/17 通过。覆盖首次发送前排除、旧协议接受状态、重复重放、写入失败重试、重开真实 Claude 历史、分叉记录继承、请求等待取消与分页统计。
- 运行中发现的两条 Rust 警告位于现有 `comic/task.rs` 和 `comic/video.rs`，未为本功能改动。

- 现有分叉回归 `fork_session`：16/16 通过；配置等待回归 `waiting_setters`：1/1 通过。Rust 相关测试合计 34 个通过。
- 服务器模式：`cargo check --no-default-features --features server-bin --bin codeg-server` 通过。

- 桌面模式：`cargo check` 通过。
- MCP 模式：`cargo check --no-default-features --features mcp-bin --bin yoyocode-mcp` 通过（以当前 Cargo.toml 的二进制名称为准）。
- 本轮未运行完整 Rust 测试集、桌面/MCP clippy。
- Server 严格 clippy 未通过：本次新增的 `nonminimal_bool` 诊断已修复，复查已确认消失；剩余诊断位于现有 comic、codex_catalog_commit、commands/acp 和 computer helper 等代码。修复为等价布尔表达式简化，前述 34 个测试和三种模式 check 在此单行简化之前通过。
