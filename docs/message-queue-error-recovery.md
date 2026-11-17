# 错误轮次暂停待发送队列与原会话继续

- 新的会话级 `message_queue_state` 与 `message_queue_attempt` 分开记录发送 attempt 与暂停/恢复控制，不修改已提交队列条目的 submitted 水位。
- manager 的普通/排队发送在命令入队前创建 attempt；emit 边界在 TurnComplete/Error/Disconnected 清空 state 前捕获稳定结果，lifecycle 按 seq 结算，成功或失败与实际 attempt 关联。
- `end_turn` 且无 active terminal AIR failure 才是成功；普通 warning、非终结模式设置错误、用户主动取消不视为任务错误。恢复轮次取消/再次失败仍暂停。
- 暂停只拦**队列**，不拦**对话**：`paused` 下用户直接在输入框发送的普通提示词由 `begin_attempt` 同事务受理（状态转 `open` + active attempt），不再入队等按钮。暂停因此自动解除并广播 `message-queue:state`；该轮失败会以这条用户消息为新 failed attempt 重新暂停，成功则放开队列。`resume_requested`/`resuming` 期间仍走队列，因为会话会拒绝第二个回合。
- manager 有两道重复的门槛：`send_prompt_linked_full` 在写 `InProgress`/发事件**之前**也查一遍 state，`begin_attempt` 在入队前才是原子 CAS。两处必须同步，否则前置检查会先拒掉直发。现在以 `manual_override` 区分来源：composer 直发与用户手点的队列行为 `true`（可越过 paused），自动 drain 的 `send_queued_prompt` 与续跑指令为 `false`（paused 必须拒绝）。新增 `send_manual_queued_prompt` 包装给 `send_item_now` 用。
- queue claim 事务检查会话 open 且无 active attempt；save、重启、重排、delete 和未来定时修改不能解除暂停。
- 用户点击“继续未完成任务”调用 `message_queue_continue {ownerId,failedAttemptId}`，CAS 创建 resume_requested。Rust优先在原会话发继续指令，不重发原任务，不放普通队列尾部；resuming期间保留pause，仅实际success后open。
- 顶部提示在drag list外，queue空也渲染；错误摘要、继续按钮及恢复中disabled，两客户端同语义。提示右上角可手动关闭（按 `failed_attempt_id` 记忆，仅隐藏提示、不改暂停状态；新失败会重新弹出），10 语 i18n `dismissNotice`。
- `paused` 下每条 PENDING 队列行也获得「发送此消息并继续会话」按钮：`send_item_now` 接受 `paused`，按 id claim 指定行（可跳过 FIFO 头选任意一行）→ `begin_attempt` 同事务解除暂停并广播状态；成功才放开队列，失败重新暂停并在该行显示错误。`resume_requested`/`resuming`、claimed/sending 与 failed/uncertain 行不提供（失败行仍走带重复发送确认的 retry）。`send_item_now` 仍拒绝 live attempt 与续跑回合。
- host capability `pause_recovery:true`；新 `message_queue_state` API与`message-queue://state`广播revision防旧响应降级。旧host无该能力不启新入口。
- migration `m20261106_000002_message_queue_pause`；restart未结算attempt变paused，原external_id/cwd续连，无可靠结果时不盲目自动继续。

## 验证

SQLite暂停/恢复6例、SessionState结果捕获4例、manager+bus+lifecycle+drainer A失败→B不发→继续→success→B闭环已跑；UI空队列提示/恢复禁按钮已跑；App Jest与Web编译成功，准确数量以实际测试日志为准；全部状态保存/继续均宿主控制。
