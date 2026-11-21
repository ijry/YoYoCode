//! Per-conversation execution barrier, separate from queue delivery state.

use chrono::Utc;
use sea_orm::{
    ConnectionTrait, DatabaseConnection, DatabaseTransaction, DbBackend, Statement,
    TransactionTrait,
};
use serde::Serialize;

use crate::db::error::DbError;

#[derive(Debug, Clone, Serialize)]
pub struct MessageQueueState {
    pub owner_id: String,
    pub status: String,
    pub active_attempt_id: Option<String>,
    pub failed_attempt_id: Option<String>,
    pub error: Option<String>,
    /// Why the queue is holding back from auto-draining despite being
    /// `"open"`. `"manual_stop"` = the user stopped the running turn; the
    /// drain skips the owner until a later turn completes successfully or the
    /// user releases the hold explicitly. `None` = normal draining.
    pub hold: Option<String>,
    pub revision: i64,
}

/// The hold value written on a user stop.
pub const HOLD_MANUAL_STOP: &str = "manual_stop";

/// 队首消息算不算一条「继续」指令（报错自动放行的判定，见 `complete_attempt`）。
///
/// 规则刻意保守：去掉首尾空白后**非空、单行、≤120 字符**，且以应用 10 种语言的
/// 任一「继续」关键词**开头**（拉丁文不区分大小写）。开头贴合关键词的长说明
/// （>120 字符或换行）更可能是任务说明书而不是一句「接着干」，宁可不放行 ——
/// 放错了就是把一条被暂停保护的队列悄悄排空。
const CONTINUE_INSTRUCTION_PREFIXES: [&str; 25] = [
    "继续", "繼續", "接着", "接著",
    "continue", "resume", "carry on", "go on",
    "続行", "続け", "続き",
    "계속", "이어서",
    "continúa", "continuar", "sigue",
    "fortsetzen", "weiter",
    "continuez", "reprend",
    "continua", "retoma",
    "متابعة", "تابع", "استمر",
];

pub(crate) fn is_continue_instruction(raw: &str) -> bool {
    let text = raw.trim();
    if text.is_empty() || text.contains('\n') || text.chars().count() > 120 {
        return false;
    }
    let lowered = text.to_lowercase();
    CONTINUE_INSTRUCTION_PREFIXES
        .iter()
        .any(|prefix| lowered.starts_with(prefix))
}

/// 从持久化草稿 JSON 里取展示文本（`displayText` 优先，文本块拼接兜底）。
fn draft_prompt_text(draft_json: &str) -> Option<String> {
    let value: serde_json::Value = serde_json::from_str(draft_json).ok()?;
    if let Some(text) = value.get("displayText").and_then(|v| v.as_str()) {
        return Some(text.to_string());
    }
    let blocks = value.get("blocks")?.as_array()?;
    let mut text = String::new();
    for block in blocks {
        if block.get("type").and_then(|v| v.as_str()) == Some("text") {
            if let Some(piece) = block.get("text").and_then(|v| v.as_str()) {
                if !text.is_empty() {
                    text.push('\n');
                }
                text.push_str(piece);
            }
        }
    }
    if text.trim().is_empty() {
        None
    } else {
        Some(text)
    }
}

/// 队首 pending 行是不是一条「继续」指令。排序与定时透明规则和 `claim_next`
/// 的队首选择完全一致（sort_order, id；未到时间的定时行不算队首）。
async fn next_pending_is_continue<C: ConnectionTrait>(
    conn: &C,
    owner: &str,
) -> Result<bool, DbError> {
    let row = conn
        .query_one(statement(
            "SELECT draft_json FROM message_queue_item WHERE owner_id=? AND delivery_state='pending' AND (scheduled_at IS NULL OR scheduled_at<=?) ORDER BY sort_order ASC, id ASC LIMIT 1",
            vec![owner.into(), Utc::now().to_rfc3339().into()],
        ))
        .await?;
    let Some(row) = row else {
        return Ok(false);
    };
    let draft: String = row.try_get("", "draft_json")?;
    Ok(draft_prompt_text(&draft)
        .map(|text| is_continue_instruction(&text))
        .unwrap_or(false))
}

#[derive(Debug, Clone)]
pub struct QueueTurnAttempt {
    pub id: String,
    pub owner_id: String,
    pub recovery_of: Option<String>,
    /// This attempt was a DIRECT prompt admitted while the queue was paused,
    /// so it lifted that pause on its way in. Callers re-broadcast the control
    /// state when it is set — otherwise the interruption banner keeps asking
    /// "continue the unfinished task?" while the user's own turn is already
    /// running.
    pub pause_lifted: bool,
}

#[derive(Debug, Clone)]
pub struct QueueTurnCompletion {
    pub attempt_id: String,
    pub owner_id: String,
    pub success: bool,
    pub cancelled: bool,
    /// The cancelled turn was stopped by a person (Stop button / chat stop
    /// command), not an internal subsystem — this is what earns the hold.
    pub user_cancelled: bool,
    pub error: Option<String>,
}

fn statement(sql: &str, values: Vec<sea_orm::Value>) -> Statement {
    Statement::from_sql_and_values(DbBackend::Sqlite, sql, values)
}

async fn write_transaction(conn: &DatabaseConnection) -> Result<DatabaseTransaction, DbError> {
    let txn = conn.begin().await?;
    txn.execute(statement(
        "UPDATE message_queue_state SET revision=revision WHERE owner_id=''",
        vec![],
    ))
    .await?;
    Ok(txn)
}

async fn state_on<C: ConnectionTrait>(conn: &C, owner: &str) -> Result<MessageQueueState, DbError> {
    match conn.query_one(statement(
        "SELECT owner_id,status,active_attempt_id,failed_attempt_id,error,hold,revision FROM message_queue_state WHERE owner_id=?",
        vec![owner.into()],
    )).await? {
        Some(row) => Ok(MessageQueueState {
            owner_id: row.try_get("", "owner_id")?, status: row.try_get("", "status")?,
            active_attempt_id: row.try_get("", "active_attempt_id")?,
            failed_attempt_id: row.try_get("", "failed_attempt_id")?,
            error: row.try_get("", "error")?,
            hold: row.try_get("", "hold")?,
            revision: row.try_get("", "revision")?,
        }),
        None => Ok(MessageQueueState { owner_id: owner.into(), status: "open".into(), active_attempt_id: None, failed_attempt_id: None, error: None, hold: None, revision: 0 }),
    }
}

pub async fn get_state(
    conn: &DatabaseConnection,
    owner: &str,
) -> Result<MessageQueueState, DbError> {
    state_on(conn, owner).await
}

pub async fn can_drain<C: ConnectionTrait>(conn: &C, owner: &str) -> Result<bool, DbError> {
    let state = state_on(conn, owner).await?;
    Ok(state.status == "open" && state.active_attempt_id.is_none() && state.hold.is_none())
}

pub async fn begin_attempt(
    conn: &DatabaseConnection,
    owner: &str,
    connection: &str,
    message: &str,
    draft_json: &str,
    recovery_of: Option<&str>,
) -> Result<QueueTurnAttempt, DbError> {
    let txn = write_transaction(conn).await?;
    let state = state_on(&txn, owner).await?;
    if state.active_attempt_id.is_some() {
        return Err(DbError::Conflict(
            "a queue-controlled turn is already active".into(),
        ));
    }
    // `"paused"` is admitted here on purpose, and ONLY for a non-recovery
    // (direct) prompt: the pause parks the QUEUE after an unconfirmed failure,
    // it must not gag the CONVERSATION. When the user types into the composer
    // and sends, that message IS their recovery decision — the same intent
    // "继续未完成任务" expresses, minus the canned prompt — so it goes out now
    // instead of being enqueued behind a barrier only a button could lift.
    // This is still safe for the queue: the state flips to `"open"` with an
    // active attempt, `can_drain` stays false (active attempt), the failed row
    // keeps blocking the FIFO head, and `complete_attempt` re-pauses on
    // failure / opens the queue on success. A recovery attempt (the continue
    // button, or a queue resume) still needs its own `"resume_requested"`.
    let pause_lifted = recovery_of.is_none() && state.status == "paused";
    match recovery_of {
        Some(failed)
            if state.status == "resume_requested"
                && state.failed_attempt_id.as_deref() == Some(failed) => {}
        None if state.status == "open" || pause_lifted => {}
        _ => {
            return Err(DbError::Conflict(
                "queue is paused or recovery request has changed".into(),
            ))
        }
    }
    let id = uuid::Uuid::new_v4().to_string();
    let now = Utc::now().to_rfc3339();
    txn.execute(statement(
        "INSERT INTO message_queue_attempt (id,owner_id,connection_id,message_id,draft_json,recovery_of,status,created_at) VALUES (?,?,?,?,?,?,'active',?)",
        vec![id.clone().into(), owner.into(), connection.into(), message.into(), draft_json.into(), recovery_of.into(), now.clone().into()],
    )).await?;
    txn.execute(statement(
        "INSERT INTO message_queue_state (owner_id,status,active_attempt_id,failed_attempt_id,error,revision,updated_at) VALUES (?,?,?,?,?,1,?)
         ON CONFLICT(owner_id) DO UPDATE SET status=excluded.status,active_attempt_id=excluded.active_attempt_id,revision=message_queue_state.revision+1,updated_at=excluded.updated_at",
        vec![owner.into(), (if recovery_of.is_some() { "resuming" } else { "open" }).into(), id.clone().into(), state.failed_attempt_id.into(), state.error.into(), now.into()],
    )).await?;
    txn.commit().await?;
    Ok(QueueTurnAttempt {
        id,
        owner_id: owner.into(),
        recovery_of: recovery_of.map(str::to_owned),
        pause_lifted,
    })
}

pub async fn complete_attempt(
    conn: &DatabaseConnection,
    completion: &QueueTurnCompletion,
) -> Result<MessageQueueState, DbError> {
    let txn = write_transaction(conn).await?;
    let state = state_on(&txn, &completion.owner_id).await?;
    if state.active_attempt_id.as_deref() != Some(&completion.attempt_id) {
        txn.commit().await?;
        return Ok(state);
    }
    let attempt = txn
        .query_one(statement(
            "SELECT recovery_of FROM message_queue_attempt WHERE id=?",
            vec![completion.attempt_id.clone().into()],
        ))
        .await?
        .ok_or_else(|| DbError::NotFound("queue attempt".into()))?;
    let recovery_of: Option<String> = attempt.try_get("", "recovery_of")?;
    let open = completion.success || (completion.cancelled && recovery_of.is_none());
    let result_status = if completion.success {
        "completed"
    } else if completion.cancelled {
        "cancelled"
    } else {
        "failed"
    };
    txn.execute(statement(
        "UPDATE message_queue_attempt SET status=?,completed_at=? WHERE id=? AND status='active'",
        vec![
            result_status.into(),
            Utc::now().to_rfc3339().into(),
            completion.attempt_id.clone().into(),
        ],
    ))
    .await?;
    // Hold bookkeeping: a USER stop (not an automated cancel, not a recovery)
    // on a still-non-empty queue parks the drain on `"open"` until the next
    // successful turn or an explicit release; any successful turn lifts it.
    // Every other outcome leaves the previous hold untouched (paused blocks
    // the drain on its own).
    let hold = if completion.success {
        None
    } else if completion.user_cancelled && recovery_of.is_none() {
        let pending = txn
            .query_one(statement(
                "SELECT COUNT(*) AS n FROM message_queue_item WHERE owner_id=? AND delivery_state='pending'",
                vec![completion.owner_id.clone().into()],
            ))
            .await?
            .ok_or_else(|| DbError::NotFound("message_queue_state row".into()))?;
        let pending: i64 = pending.try_get("", "n")?;
        if pending > 0 {
            Some(HOLD_MANUAL_STOP.to_string())
        } else {
            None
        }
    } else {
        state.hold.clone()
    };
    // 报错（非用户停止、也非恢复尝试被取消）且**队首本身就是一条「继续」指令**
    // 时不落 paused：那条队列消息就是用户的恢复决定 —— 和「继续未完成任务」按钮
    // 表达的是同一个意图，只是用的是自己的话。保持 open 让 drainer 把它发出去；
    // 它出队后如果再次报错，队首就是普通消息，队列照旧停下来等人。
    let auto_resumed = !open
        && result_status == "failed"
        && next_pending_is_continue(&txn, &completion.owner_id)
            .await
            .unwrap_or(false);
    let open = open || auto_resumed;
    let hold = if auto_resumed { None } else { hold };
    txn.execute(statement(
        "UPDATE message_queue_state SET status=?,active_attempt_id=NULL,failed_attempt_id=?,error=?,hold=?,revision=revision+1,updated_at=? WHERE owner_id=? AND active_attempt_id=?",
        vec![(if open { "open" } else { "paused" }).into(),
            (if open { None } else { recovery_of.or(Some(completion.attempt_id.clone())) }).into(),
            (if open { None } else { completion.error.clone().or(Some("previous task did not finish".into())) }).into(),
            hold.into(),
            Utc::now().to_rfc3339().into(), completion.owner_id.clone().into(), completion.attempt_id.clone().into()],
    )).await?;
    let result = state_on(&txn, &completion.owner_id).await?;
    txn.commit().await?;
    Ok(result)
}

pub async fn abort_admission(
    conn: &DatabaseConnection,
    attempt: &QueueTurnAttempt,
) -> Result<(), DbError> {
    let txn = write_transaction(conn).await?;
    txn.execute(statement("UPDATE message_queue_attempt SET status='not_sent',completed_at=? WHERE id=? AND status='active'", vec![Utc::now().to_rfc3339().into(), attempt.id.clone().into()])).await?;
    txn.execute(statement(
        "UPDATE message_queue_state SET status=?,active_attempt_id=NULL,revision=revision+1 WHERE owner_id=? AND active_attempt_id=?",
        vec![(if attempt.recovery_of.is_some() { "resume_requested" } else { "open" }).into(), attempt.owner_id.clone().into(), attempt.id.clone().into()],
    )).await?;
    txn.commit().await?;
    Ok(())
}

pub async fn request_continue(
    conn: &DatabaseConnection,
    owner: &str,
    failed: &str,
) -> Result<MessageQueueState, DbError> {
    let updated = conn.execute(statement(
        "UPDATE message_queue_state SET status='resume_requested',revision=revision+1,updated_at=? WHERE owner_id=? AND status='paused' AND active_attempt_id IS NULL AND failed_attempt_id=?",
        vec![Utc::now().to_rfc3339().into(), owner.into(), failed.into()],
    )).await?;
    if updated.rows_affected() != 1 {
        return Err(DbError::Conflict(
            "recovery already requested or failure has changed".into(),
        ));
    }
    get_state(conn, owner).await
}

/// Lift a user-stop hold back to normal draining. CAS on `hold IS NOT NULL`
/// so a double click (or a racing client) conflicts instead of double-bumping
/// the revision; the caller wakes the drain on success.
pub async fn release_hold(
    conn: &DatabaseConnection,
    owner: &str,
) -> Result<MessageQueueState, DbError> {
    let updated = conn.execute(statement(
        "UPDATE message_queue_state SET hold=NULL,revision=revision+1,updated_at=? WHERE owner_id=? AND hold IS NOT NULL",
        vec![Utc::now().to_rfc3339().into(), owner.into()],
    )).await?;
    if updated.rows_affected() != 1 {
        return Err(DbError::Conflict("queue is not holding".into()));
    }
    get_state(conn, owner).await
}

pub async fn pending_recoveries(conn: &DatabaseConnection) -> Result<Vec<String>, DbError> {
    let rows = conn.query_all(statement("SELECT owner_id FROM message_queue_state WHERE status='resume_requested' AND active_attempt_id IS NULL", vec![])).await?;
    rows.into_iter()
        .map(|row| row.try_get("", "owner_id").map_err(DbError::from))
        .collect()
}

pub async fn recover_interrupted_attempts(conn: &DatabaseConnection) -> Result<(), DbError> {
    conn.execute(statement(
        "UPDATE message_queue_state SET status='paused',failed_attempt_id=COALESCE(failed_attempt_id,active_attempt_id),active_attempt_id=NULL,error='previous task was interrupted before its result was confirmed',revision=revision+1 WHERE active_attempt_id IS NOT NULL", vec![],
    )).await?;
    conn.execute(statement("UPDATE message_queue_attempt SET status='interrupted',completed_at=? WHERE status='active'", vec![Utc::now().to_rfc3339().into()])).await?;
    Ok(())
}

#[cfg(test)]
#[path = "message_queue_control_tests.rs"]
mod tests;
