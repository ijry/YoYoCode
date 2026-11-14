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

#[derive(Debug, Clone)]
pub struct QueueTurnAttempt {
    pub id: String,
    pub owner_id: String,
    pub recovery_of: Option<String>,
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
    match recovery_of {
        Some(failed)
            if state.status == "resume_requested"
                && state.failed_attempt_id.as_deref() == Some(failed) => {}
        None if state.status == "open" => {}
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
