# Rust 持久化待发送队列自治发送

## 目标与范围

`message_queue_item` 的发送由 Rust 管理，不依赖 Tauri webview、网页标签或 mcode-app 页面保持打开。desktop 和 server 共享同一实现；客户端负责入队、查看、修改待发送内容，不再自行取走宿主拥有的队首。

核心模块：`acp/message_queue_drain.rs`（调度与 ACP 调用）、`db/service/message_queue_service.rs`（SQLite 原子状态机）；队列保存的 Tauri/Axum 入口通知 drainer，lifecycle 在回合状态落库后通知它。

## 调度与连接

- 启动及每 30 秒从 DB 扫描 `pending` owners，兜底恢复关闭页面/漏广播/重启前遗留的队列。
- 同一 owner 至多一个 worker，同时至多 8 个会话；单条连接/模式请求慢不会阻塞其他会话。
- 拒绝/建连失败按 1 秒递增到 60 秒重试；TurnBusy 用最短退避，不改 FIFO。
- drainer 用数据库同名 `.message-queue.lock` 选主；两个运行时共用 data-dir 时仅一个恢复 claim 和发送，另一个定期重试选主。
- 优先复用匹配会话或 external_id 的活连接，不使用错误/已断开的连接。无连接时按 `origin_cwd ?? folder.path`、原 `agent_type` 和 `external_id` 自主拉起；复用 `build_session_runtime_env`、安装校验与 `spawn_agent`，不会静默下载智能体或变更权限。
- 首次 spawn 返回不等于 `session/new` 完成。队列等待 `external_id` 和 `Connected` 后再发送；握手超时保留消息。
- mode 设置和 prompt 发送共用 manager 的 `prompt_lock`；mode 拒绝、未执行或超时不发送。`mode_id == null` 不更改当前模式，`adopt_send_time_mode` 只保留 wire 兼容，后端不读取 UI。

## 数据状态与失败纪律

迁移 `m20261106_000001_message_queue_delivery` 增加 `delivery_state / claim_token / last_error`。

```
pending -> claimed -> sending -> submitted
             |            |
             +-- pending <-+  （明确的发送拒绝/失败）
             |
             +-- failed       （坏 draft_json，需要编辑/删除/显式重试）

重启：claimed -> pending；sending -> uncertain（不自动重复发送）
```

- `claim_next` 事务只认领真实队首；正文一直保留到 manager 接受 prompt，失败释放同一 claim token，不能由 stale task 结算另一轮。
- 保存仍接受原有整条队列数组；保护 claimed/sending 正文，保留 submitted id 水位，忽略陈旧客户端带回的已发送 id。普通 editable items 仍采用最后一次保存的顺序与内容。
- `list` 隐藏 submitted；pending 的 wire 字节形状保持不变，其他状态增加可选 `delivery_state/last_error`。
- 坏 JSON 不删除、不跳到下一条打乱顺序；失败条目保持可见，编辑正文/模式可重新 pending，`message_queue_retry` 显式恢复失败/uncertain；用户确认后才重试。
- **不宣称 exactly-once**：ACP 没有幂等交付确认。跨进程崩溃发生于调用已发出但确认未持久化时保留 uncertain，提示重试可能重复发送；manager 已接受的 prompt 是已提交，不自动回队。

## 协议与客户端兼容

- 新 `message_queue_capabilities` 返回 `{backend_drain:true}`，无 ACP 连接时也可查询。HTTP 是 `POST /api/message_queue_capabilities`，Tauri 同名 command。
- 新 `message_queue_retry {ownerId,itemId}` 经现有认证层；list/save 的 casing 与原始 payload 不变。
- snapshot `message_queue_backend_drain` 保留为宿主能力佐证；客户端不得依赖第一次 live snapshot 才关闭自己的 flusher。
- capabilities 未知/网络失败：停客户端出队；明确 `not_implemented`/旧 IPC command-not-found：保留 legacy frontend flush。mcode-app 同样先查询能力再显示「立即发送」。
- 新宿主上旧缓存网页/旧客户端不受新门控约束，升级客户端仍必要，不能声称所有旧客户端的双发都由服务端消灭。
- `message-queue://changed` 继续携带全队列；Rust 发 `origin=backend-drain`。客户端以 revision 防止旧 list/save 响应盖掉较新广播，重连刷新权威列表。手机保存带回原始 `draft_json/blocks` 和 mode，不能丢 resource 块或未知 draft 字段。

## 原生客户端复制

1. 对每台宿主查询 capabilities；未知态禁止任何出队发送，旧宿主才保留人工队首发送。
2. `claimed/sending` 只读；`failed/uncertain` 显示错误、支持删除/编辑或确认后调用 retry，不从本机构造另一次直接 prompt。
3. pending 的 `draft_json` 必须同时有 blocks 与 displayText；来自另一端的原始 JSON 透传。
4. 收到广播按 owner 和 origin 过滤，并以本地 revision 保护异步响应；断线后重新拉取，不把未同步镜像回写宿主。
5. 手机退出后 Rust 继续发；宿主进程关闭期间不执行，启动后恢复。

## 验证记录

测试与完整日志位于 `docs/superpowers/plans/2026-11-06-*`（未提交的实施记录）。当前已验证 SQLite state/claim、防陈旧保存、mode admission lock、启动扫描和 turn-status 顺序；最终验证结果以交付报告为准，不以管道退出码推断。
