# 错误轮次暂停待发送队列与原会话继续

- 新的会话级 `message_queue_state` 与 `message_queue_attempt` 分开记录发送 attempt 与暂停/恢复控制，不修改已提交队列条目的 submitted 水位。
- manager 的普通/排队发送在命令入队前创建 attempt；emit 边界在 TurnComplete/Error/Disconnected 清空 state 前捕获稳定结果，lifecycle 按 seq 结算，成功或失败与实际 attempt 关联。
- `end_turn` 且无 active terminal AIR failure 才是成功；普通 warning、非终结模式设置错误、用户主动取消不视为任务错误。恢复轮次取消/再次失败仍暂停。
- 暂停只拦**队列**，不拦**对话**：`paused` 下用户直接在输入框发送的普通提示词由 `begin_attempt` 同事务受理（状态转 `open` + active attempt），不再入队等按钮。暂停因此自动解除并广播 `message-queue:state`；该轮失败会以这条用户消息为新 failed attempt 重新暂停，成功则放开队列。`resume_requested`/`resuming` 期间仍走队列，因为会话会拒绝第二个回合。
- queue claim 事务检查会话 open 且无 active attempt；save、重启、重排、delete 和未来定时修改不能解除暂停。
- 用户点击“继续未完成任务”调用 `message_queue_continue {ownerId,failedAttemptId}`，CAS 创建 resume_requested。Rust优先在原会话发继续指令，不重发原任务，不放普通队列尾部；resuming期间保留pause，仅实际success后open。
- 顶部提示在drag list外，queue空也渲染；错误摘要、继续按钮及恢复中disabled，两客户端同语义。提示右上角可手动关闭（按 `failed_attempt_id` 记忆，仅隐藏提示、不改暂停状态；新失败会重新弹出），10 语 i18n `dismissNotice`。
- host capability `pause_recovery:true`；新 `message_queue_state` API与`message-queue://state`广播revision防旧响应降级。旧host无该能力不启新入口。
- migration `m20261106_000002_message_queue_pause`；restart未结算attempt变paused，原external_id/cwd续连，无可靠结果时不盲目自动继续。

## 验证

SQLite暂停/恢复6例、SessionState结果捕获4例、manager+bus+lifecycle+drainer A失败→B不发→继续→success→B闭环已跑；UI空队列提示/恢复禁按钮已跑；App Jest与Web编译成功，准确数量以实际测试日志为准；全部状态保存/继续均宿主控制。
