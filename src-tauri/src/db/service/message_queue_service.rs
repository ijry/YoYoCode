use std::collections::{HashMap, HashSet};

use chrono::{DateTime, Utc};
use sea_orm::{
    ActiveModelTrait, ActiveValue::NotSet, ColumnTrait, Condition, ConnectionTrait,
    DatabaseConnection, DatabaseTransaction, DbBackend, EntityTrait, QueryFilter, QueryOrder,
    Set, Statement, TransactionTrait,
};
use serde::Deserialize;

use crate::db::entities::message_queue_item;
use crate::db::error::DbError;
use crate::models::MessageQueueItem;

/// A queue item as supplied by the frontend when saving. Field names are
/// camelCase on the wire (both the Tauri command and the axum web handler
/// expect it), so serde rename handles the mapping to the Rust fields.
#[derive(Debug, Clone, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct QueueItemInput {
    pub id: String,
    pub draft_json: String,
    pub mode_id: Option<String>,
    pub adopt_send_time_mode: bool,
}

const PENDING: &str = "pending";
const CLAIMED: &str = "claimed";
const SENDING: &str = "sending";
const SUBMITTED: &str = "submitted";
const REMOVED: &str = "removed";
const FAILED: &str = "failed";
const UNCERTAIN: &str = "uncertain";

fn to_item(m: message_queue_item::Model) -> MessageQueueItem {
    MessageQueueItem {
        id: m.item_uid,
        draft_json: m.draft_json,
        mode_id: m.mode_id,
        adopt_send_time_mode: m.adopt_send_time_mode,
        scheduled_at: m.scheduled_at,
        delivery_state: (m.delivery_state != PENDING).then_some(m.delivery_state),
        last_error: m.last_error,
    }
}

async fn list_on<C: ConnectionTrait>(
    conn: &C,
    owner_id: &str,
) -> Result<Vec<MessageQueueItem>, DbError> {
    let rows = message_queue_item::Entity::find()
        .filter(message_queue_item::Column::OwnerId.eq(owner_id))
        .filter(
            message_queue_item::Column::DeliveryState.is_not_in([SUBMITTED, REMOVED]),
        )
        .order_by_asc(message_queue_item::Column::SortOrder)
        .order_by_asc(message_queue_item::Column::Id)
        .all(conn)
        .await?;
    Ok(rows.into_iter().map(to_item).collect())
}

pub async fn list(
    conn: &DatabaseConnection,
    owner_id: &str,
) -> Result<Vec<MessageQueueItem>, DbError> {
    list_on(conn, owner_id).await
}

/// Take SQLite's writer lock before reading the queue. A deferred transaction
/// that reads first races another writer when promoted; the harmless UPDATE
/// reserves the writer without changing any item or its timestamps.
async fn write_transaction(conn: &DatabaseConnection) -> Result<DatabaseTransaction, DbError> {
    let txn = conn.begin().await?;
    txn.execute(Statement::from_string(
        DbBackend::Sqlite,
        "UPDATE message_queue_item SET id = id WHERE id = -1".to_owned(),
    ))
    .await?;
    Ok(txn)
}

/// Replace editable items while protecting the backend's live claim and
/// submitted-id watermarks. Old clients may save an outdated whole queue;
/// they must not resurrect a delivered prompt or delete an admitted one.
pub async fn save(
    conn: &DatabaseConnection,
    owner_id: &str,
    items: Vec<QueueItemInput>,
) -> Result<Vec<MessageQueueItem>, DbError> {
    let mut seen = HashSet::new();
    for item in &items {
        if item.id.is_empty() || !seen.insert(item.id.as_str()) {
            return Err(DbError::Validation(
                "queue item ids must be non-empty and unique".into(),
            ));
        }
    }
    let txn = write_transaction(conn).await?;
    let rows = message_queue_item::Entity::find()
        .filter(message_queue_item::Column::OwnerId.eq(owner_id))
        .order_by_asc(message_queue_item::Column::SortOrder)
        .order_by_asc(message_queue_item::Column::Id)
        .all(&txn)
        .await?;
    let existing: HashMap<&str, &message_queue_item::Model> = rows
        .iter()
        .map(|row| (row.item_uid.as_str(), row))
        .collect();
    let protected: Vec<&message_queue_item::Model> = rows
        .iter()
        .filter(|row| matches!(row.delivery_state.as_str(), CLAIMED | SENDING))
        .collect();
    let retained: HashSet<&str> = items.iter().map(|item| item.id.as_str()).collect();
    for row in &rows {
        // Scheduled rows are only touched through the schedule/remove patch
        // APIs: a legacy full save that omits one keeps it (and its time)
        // instead of deleting it, and removed ids stay tombstoned so a stale
        // list cannot resurrect them.
        if !matches!(row.delivery_state.as_str(), CLAIMED | SENDING | SUBMITTED | REMOVED)
            && row.scheduled_at.is_none()
            && !retained.contains(row.item_uid.as_str())
        {
            message_queue_item::Entity::delete_by_id(row.id)
                .exec(&txn)
                .await?;
        }
    }
    let now = Utc::now();
    for (position, row) in protected.iter().enumerate() {
        let mut active: message_queue_item::ActiveModel = (*row).clone().into();
        active.sort_order = Set(position as i32);
        active.update(&txn).await?;
    }
    let mut position = protected.len() as i32;
    for item in items {
        if let Some(row) = existing.get(item.id.as_str()) {
            if matches!(row.delivery_state.as_str(), CLAIMED | SENDING | SUBMITTED | REMOVED) {
                continue;
            }
            let changed = row.draft_json != item.draft_json
                || row.mode_id != item.mode_id
                || row.adopt_send_time_mode != item.adopt_send_time_mode;
            let mut active: message_queue_item::ActiveModel = (*row).clone().into();
            active.draft_json = Set(item.draft_json);
            active.mode_id = Set(item.mode_id);
            active.adopt_send_time_mode = Set(item.adopt_send_time_mode);
            active.sort_order = Set(position);
            active.updated_at = Set(now);
            if changed {
                active.delivery_state = Set(PENDING.into());
                active.claim_token = Set(None);
                active.last_error = Set(None);
            }
            active.update(&txn).await?;
        } else {
            message_queue_item::ActiveModel {
                id: NotSet,
                owner_id: Set(owner_id.into()),
                item_uid: Set(item.id),
                draft_json: Set(item.draft_json),
                mode_id: Set(item.mode_id),
                adopt_send_time_mode: Set(item.adopt_send_time_mode),
                scheduled_at: Set(None),
                sort_order: Set(position),
                delivery_state: Set(PENDING.into()),
                claim_token: Set(None),
                last_error: Set(None),
                created_at: Set(now),
                updated_at: Set(now),
            }
            .insert(&txn)
            .await?;
        }
        position += 1;
    }
    let saved = list_on(&txn, owner_id).await?;
    txn.commit().await?;
    Ok(saved)
}

/// A database claim keeps the item's original body until prompt admission
/// succeeds. The token prevents a stale task from settling a later retry.
#[derive(Debug, Clone)]
pub struct QueueClaim {
    pub owner_id: String,
    pub token: String,
    pub item: MessageQueueItem,
}

pub async fn claim_next(
    conn: &DatabaseConnection,
    owner_id: &str,
    token: &str,
) -> Result<Option<QueueClaim>, DbError> {
    let txn = write_transaction(conn).await?;
    if !super::message_queue_control_service::can_drain(&txn, owner_id).await? {
        txn.commit().await?;
        return Ok(None);
    }
    let active = message_queue_item::Entity::find()
        .filter(message_queue_item::Column::OwnerId.eq(owner_id))
        .filter(message_queue_item::Column::DeliveryState.is_in([CLAIMED, SENDING]))
        .one(&txn)
        .await?;
    if active.is_some() {
        txn.commit().await?;
        return Ok(None);
    }
    let now = Utc::now();
    let head = message_queue_item::Entity::find()
        .filter(message_queue_item::Column::OwnerId.eq(owner_id))
        .filter(
            message_queue_item::Column::DeliveryState.is_not_in([SUBMITTED, REMOVED]),
        )
        // A pending message whose schedule has not arrived is transparent:
        // the head skips it so ordinary messages keep flowing. Failed,
        // uncertain and in-flight items still block the queue until resolved.
        .filter(
            Condition::any()
                .add(message_queue_item::Column::DeliveryState.ne(PENDING))
                .add(message_queue_item::Column::ScheduledAt.is_null())
                .add(message_queue_item::Column::ScheduledAt.lte(now)),
        )
        .order_by_asc(message_queue_item::Column::SortOrder)
        .order_by_asc(message_queue_item::Column::Id)
        .one(&txn)
        .await?;
    let Some(head) = head.filter(|head| head.delivery_state == PENDING) else {
        txn.commit().await?;
        return Ok(None);
    };
    let mut active: message_queue_item::ActiveModel = head.into();
    active.delivery_state = Set(CLAIMED.into());
    active.claim_token = Set(Some(token.into()));
    active.last_error = Set(None);
    active.updated_at = Set(Utc::now());
    let claimed = active.update(&txn).await?;
    txn.commit().await?;
    Ok(Some(QueueClaim {
        owner_id: owner_id.into(),
        token: token.into(),
        item: to_item(claimed),
    }))
}

/// Claim ONE specific item for a user-initiated send (`message_queue_send_now`).
/// Unlike [`claim_next`] it targets an id instead of the FIFO head and
/// deliberately ignores the control layer's hold — the user is overriding it —
/// but it still honors the one-claim-at-a-time invariant and only takes a
/// pending row. A scheduled row may be sent ahead of its time: the schedule is
/// a "not before", and the user clicking send now is explicitly "now".
pub async fn claim_item(
    conn: &DatabaseConnection,
    owner_id: &str,
    item_id: &str,
    token: &str,
) -> Result<Option<QueueClaim>, DbError> {
    let txn = write_transaction(conn).await?;
    let active = message_queue_item::Entity::find()
        .filter(message_queue_item::Column::OwnerId.eq(owner_id))
        .filter(message_queue_item::Column::DeliveryState.is_in([CLAIMED, SENDING]))
        .one(&txn)
        .await?;
    if active.is_some() {
        txn.commit().await?;
        return Ok(None);
    }
    let row = message_queue_item::Entity::find()
        .filter(message_queue_item::Column::OwnerId.eq(owner_id))
        .filter(message_queue_item::Column::ItemUid.eq(item_id))
        .filter(message_queue_item::Column::DeliveryState.eq(PENDING))
        .one(&txn)
        .await?;
    let Some(row) = row else {
        txn.commit().await?;
        return Ok(None);
    };
    let mut active: message_queue_item::ActiveModel = row.into();
    active.delivery_state = Set(CLAIMED.into());
    active.claim_token = Set(Some(token.into()));
    active.last_error = Set(None);
    active.updated_at = Set(Utc::now());
    let claimed = active.update(&txn).await?;
    txn.commit().await?;
    Ok(Some(QueueClaim {
        owner_id: owner_id.into(),
        token: token.into(),
        item: to_item(claimed),
    }))
}

async fn settle_claim(
    conn: &DatabaseConnection,
    claim: &QueueClaim,
    state: &str,
    error: Option<&str>,
) -> Result<(), DbError> {
    let updated = message_queue_item::Entity::update_many()
        .col_expr(
            message_queue_item::Column::DeliveryState,
            sea_orm::sea_query::Expr::value(state),
        )
        // A settled delivery is no longer a scheduled one: only pending items
        // may carry a schedule, so a failed/uncertain row must not keep the
        // past instant — that would shield it from legacy full saves forever
        // (a zombie) and make `retry` fire immediately with a stale plan.
        .col_expr(
            message_queue_item::Column::ScheduledAt,
            sea_orm::sea_query::Expr::value(None::<DateTime<Utc>>),
        )
        .col_expr(
            message_queue_item::Column::LastError,
            sea_orm::sea_query::Expr::value(error),
        )
        .col_expr(
            message_queue_item::Column::ClaimToken,
            sea_orm::sea_query::Expr::value(None::<String>),
        )
        .col_expr(
            message_queue_item::Column::UpdatedAt,
            sea_orm::sea_query::Expr::value(Utc::now()),
        )
        .filter(message_queue_item::Column::OwnerId.eq(&claim.owner_id))
        .filter(message_queue_item::Column::ItemUid.eq(&claim.item.id))
        .filter(message_queue_item::Column::ClaimToken.eq(&claim.token))
        .filter(message_queue_item::Column::DeliveryState.is_in([CLAIMED, SENDING]))
        .exec(conn)
        .await?;
    if updated.rows_affected != 1 {
        return Err(DbError::Conflict(
            "queue claim no longer belongs to this sender".into(),
        ));
    }
    Ok(())
}

pub async fn mark_sending(conn: &DatabaseConnection, claim: &QueueClaim) -> Result<(), DbError> {
    let updated = message_queue_item::Entity::update_many()
        .col_expr(
            message_queue_item::Column::DeliveryState,
            sea_orm::sea_query::Expr::value(SENDING),
        )
        .filter(message_queue_item::Column::OwnerId.eq(&claim.owner_id))
        .filter(message_queue_item::Column::ItemUid.eq(&claim.item.id))
        .filter(message_queue_item::Column::ClaimToken.eq(&claim.token))
        .filter(message_queue_item::Column::DeliveryState.eq(CLAIMED))
        .exec(conn)
        .await?;
    if updated.rows_affected != 1 {
        return Err(DbError::Conflict("queue item is no longer claimed".into()));
    }
    Ok(())
}

pub async fn acknowledge(conn: &DatabaseConnection, claim: &QueueClaim) -> Result<(), DbError> {
    settle_claim(conn, claim, SUBMITTED, None).await
}

pub async fn release(
    conn: &DatabaseConnection,
    claim: &QueueClaim,
    error: Option<&str>,
) -> Result<(), DbError> {
    settle_claim(conn, claim, PENDING, error).await
}

pub async fn fail(
    conn: &DatabaseConnection,
    claim: &QueueClaim,
    error: &str,
) -> Result<(), DbError> {
    settle_claim(conn, claim, FAILED, Some(error)).await
}

pub async fn retry(
    conn: &DatabaseConnection,
    owner_id: &str,
    item_id: &str,
) -> Result<(), DbError> {
    let updated = message_queue_item::Entity::update_many()
        .col_expr(
            message_queue_item::Column::DeliveryState,
            sea_orm::sea_query::Expr::value(PENDING),
        )
        .col_expr(
            message_queue_item::Column::LastError,
            sea_orm::sea_query::Expr::value(None::<String>),
        )
        .filter(message_queue_item::Column::OwnerId.eq(owner_id))
        .filter(message_queue_item::Column::ItemUid.eq(item_id))
        .filter(message_queue_item::Column::DeliveryState.is_in([FAILED, UNCERTAIN]))
        .exec(conn)
        .await?;
    if updated.rows_affected != 1 {
        return Err(DbError::Conflict(
            "only a failed or uncertain queue item can be retried".into(),
        ));
    }
    Ok(())
}

/// Only the elected drainer calls this on boot. A sending item has crossed
/// the dispatch boundary; leave it visible and require confirmation, rather
/// than automatically repeating a prompt the agent may already have run.
pub async fn recover_abandoned_claims(conn: &DatabaseConnection) -> Result<(), DbError> {
    message_queue_item::Entity::update_many()
        .col_expr(
            message_queue_item::Column::DeliveryState,
            sea_orm::sea_query::Expr::value(UNCERTAIN),
        )
        .col_expr(
            message_queue_item::Column::ClaimToken,
            sea_orm::sea_query::Expr::value(None::<String>),
        )
        // Same rule as `settle_claim`: uncertain means the outcome is unknown,
        // not scheduled — retrying it is an explicit "send now".
        .col_expr(
            message_queue_item::Column::ScheduledAt,
            sea_orm::sea_query::Expr::value(None::<DateTime<Utc>>),
        )
        .col_expr(
            message_queue_item::Column::LastError,
            sea_orm::sea_query::Expr::value("host stopped before queue delivery was confirmed"),
        )
        .filter(message_queue_item::Column::DeliveryState.eq(SENDING))
        .exec(conn)
        .await?;
    message_queue_item::Entity::update_many()
        .col_expr(
            message_queue_item::Column::DeliveryState,
            sea_orm::sea_query::Expr::value(PENDING),
        )
        .col_expr(
            message_queue_item::Column::ClaimToken,
            sea_orm::sea_query::Expr::value(None::<String>),
        )
        .filter(message_queue_item::Column::DeliveryState.eq(CLAIMED))
        .exec(conn)
        .await?;
    Ok(())
}

pub async fn pending_owners(conn: &DatabaseConnection) -> Result<Vec<String>, DbError> {
    let now = Utc::now();
    let rows = conn
        .query_all(Statement::from_sql_and_values(
            DbBackend::Sqlite,
            r#"
            SELECT DISTINCT m.owner_id
            FROM message_queue_item m
            WHERE m.delivery_state = 'pending'
              AND (m.scheduled_at IS NULL OR m.scheduled_at <= ?)
              AND NOT EXISTS (
                  SELECT 1 FROM message_queue_item h
                  WHERE h.owner_id = m.owner_id
                    AND h.delivery_state NOT IN ('submitted', 'removed')
                    AND NOT (h.delivery_state = 'pending'
                             AND h.scheduled_at IS NOT NULL AND h.scheduled_at > ?)
                    AND (h.sort_order < m.sort_order
                         OR (h.sort_order = m.sort_order AND h.id < m.id))
              )
            ORDER BY m.owner_id
            "#,
            [now.into(), now.into()],
        ))
        .await?;
    let mut owners = Vec::new();
    for row in rows {
        owners.push(row.try_get::<String>("", "owner_id")?);
    }
    Ok(owners)
}

/// RFC 3339 wire input for a one-off schedule time; absent or empty means
/// ordinary. Parsed here so the Tauri command and the HTTP handler accept
/// exactly the same shape as `work_task_schedule`.
pub fn parse_schedule_input(raw: Option<String>) -> Result<Option<DateTime<Utc>>, DbError> {
    match raw.as_deref().map(str::trim).filter(|s| !s.is_empty()) {
        None => Ok(None),
        Some(raw) => Ok(Some(
            chrono::DateTime::parse_from_rfc3339(raw)
                .map_err(|error| DbError::Validation(format!("invalid scheduled_at: {error}")))?
                .with_timezone(&Utc),
        )),
    }
}

/// Append one message atomically, optionally with a one-off schedule time.
/// Concurrent enqueues from several clients serialize on the writer lock,
/// and a retry with the same id updates the row instead of duplicating it.
/// The full-queue save never carries a schedule, so a time only enters the
/// queue through this path or `set_schedule`.
pub async fn enqueue(
    conn: &DatabaseConnection,
    owner_id: &str,
    item: QueueItemInput,
    scheduled_at: Option<DateTime<Utc>>,
) -> Result<(), DbError> {
    if item.id.is_empty() {
        return Err(DbError::Validation(
            "queue item ids must be non-empty".into(),
        ));
    }
    if scheduled_at.is_some_and(|at| at <= Utc::now()) {
        return Err(DbError::Validation(
            "a scheduled time must be in the future".into(),
        ));
    }
    let txn = write_transaction(conn).await?;
    let existing = message_queue_item::Entity::find()
        .filter(message_queue_item::Column::OwnerId.eq(owner_id))
        .filter(message_queue_item::Column::ItemUid.eq(&item.id))
        .one(&txn)
        .await?;
    let tail = message_queue_item::Entity::find()
        .filter(message_queue_item::Column::OwnerId.eq(owner_id))
        .order_by_desc(message_queue_item::Column::SortOrder)
        .one(&txn)
        .await?
        .map_or(-1, |row| row.sort_order);
    let now = Utc::now();
    match existing {
        Some(row) if matches!(row.delivery_state.as_str(), CLAIMED | SENDING) => {
            return Err(DbError::Conflict(
                "the queue item is being delivered".into(),
            ));
        }
        Some(row) if matches!(row.delivery_state.as_str(), SUBMITTED | REMOVED) => {
            return Err(DbError::Conflict(
                "the queue item id was already delivered or removed".into(),
            ));
        }
        Some(row) => {
            let mut active: message_queue_item::ActiveModel = row.into();
            active.draft_json = Set(item.draft_json);
            active.mode_id = Set(item.mode_id);
            active.adopt_send_time_mode = Set(item.adopt_send_time_mode);
            active.scheduled_at = Set(scheduled_at);
            active.sort_order = Set(tail + 1);
            active.delivery_state = Set(PENDING.into());
            active.claim_token = Set(None);
            active.last_error = Set(None);
            active.updated_at = Set(now);
            active.update(&txn).await?;
        }
        None => {
            message_queue_item::ActiveModel {
                id: NotSet,
                owner_id: Set(owner_id.into()),
                item_uid: Set(item.id),
                draft_json: Set(item.draft_json),
                mode_id: Set(item.mode_id),
                adopt_send_time_mode: Set(item.adopt_send_time_mode),
                scheduled_at: Set(scheduled_at),
                sort_order: Set(tail + 1),
                delivery_state: Set(PENDING.into()),
                claim_token: Set(None),
                last_error: Set(None),
                created_at: Set(now),
                updated_at: Set(now),
            }
            .insert(&txn)
            .await?;
        }
    }
    txn.commit().await?;
    Ok(())
}

/// Move one pending item between ordinary and scheduled, keeping its id,
/// body, attachments and mode. Anything not pending (in flight, failed,
/// uncertain, settled or removed) is rejected so the time can never change
/// behind a live claim; `Some(past)` is rejected outright.
pub async fn set_schedule(
    conn: &DatabaseConnection,
    owner_id: &str,
    item_id: &str,
    scheduled_at: Option<DateTime<Utc>>,
) -> Result<(), DbError> {
    if scheduled_at.is_some_and(|at| at <= Utc::now()) {
        return Err(DbError::Validation(
            "a scheduled time must be in the future".into(),
        ));
    }
    let txn = write_transaction(conn).await?;
    let updated = message_queue_item::Entity::update_many()
        .col_expr(
            message_queue_item::Column::ScheduledAt,
            sea_orm::sea_query::Expr::value(scheduled_at),
        )
        .col_expr(
            message_queue_item::Column::UpdatedAt,
            sea_orm::sea_query::Expr::value(Utc::now()),
        )
        .filter(message_queue_item::Column::OwnerId.eq(owner_id))
        .filter(message_queue_item::Column::ItemUid.eq(item_id))
        .filter(message_queue_item::Column::DeliveryState.eq(PENDING))
        .exec(&txn)
        .await?;
    txn.commit().await?;
    if updated.rows_affected != 1 {
        return Err(DbError::Conflict(
            "only a pending queue item can be rescheduled".into(),
        ));
    }
    Ok(())
}

/// Explicit delete for ordinary and scheduled items alike. The row becomes a
/// tombstone (like a submitted watermark) so a stale full save that still
/// lists the id cannot bring it back.
pub async fn remove(
    conn: &DatabaseConnection,
    owner_id: &str,
    item_id: &str,
) -> Result<(), DbError> {
    let txn = write_transaction(conn).await?;
    let updated = message_queue_item::Entity::update_many()
        .col_expr(
            message_queue_item::Column::DeliveryState,
            sea_orm::sea_query::Expr::value(REMOVED),
        )
        .col_expr(
            message_queue_item::Column::ScheduledAt,
            sea_orm::sea_query::Expr::value(None::<DateTime<Utc>>),
        )
        .col_expr(
            message_queue_item::Column::ClaimToken,
            sea_orm::sea_query::Expr::value(None::<String>),
        )
        .col_expr(
            message_queue_item::Column::LastError,
            sea_orm::sea_query::Expr::value(None::<String>),
        )
        .col_expr(
            message_queue_item::Column::UpdatedAt,
            sea_orm::sea_query::Expr::value(Utc::now()),
        )
        .filter(message_queue_item::Column::OwnerId.eq(owner_id))
        .filter(message_queue_item::Column::ItemUid.eq(item_id))
        .filter(
            message_queue_item::Column::DeliveryState.is_in([PENDING, FAILED, UNCERTAIN]),
        )
        .exec(&txn)
        .await?;
    txn.commit().await?;
    if updated.rows_affected != 1 {
        return Err(DbError::Conflict(
            "only a pending, failed or uncertain queue item can be removed".into(),
        ));
    }
    Ok(())
}

/// The earliest schedule that has not arrived yet, across every
/// conversation. Due-but-unclaimed items are excluded: the periodic scan
/// already covers them, and returning them here would wake the drainer in a
/// tight loop while an owner is busy or paused.
pub async fn next_schedule(
    conn: &DatabaseConnection,
) -> Result<Option<DateTime<Utc>>, DbError> {
    let row = message_queue_item::Entity::find()
        .filter(message_queue_item::Column::DeliveryState.eq(PENDING))
        .filter(message_queue_item::Column::ScheduledAt.gt(Utc::now()))
        .order_by_asc(message_queue_item::Column::ScheduledAt)
        .one(conn)
        .await?;
    Ok(row.and_then(|row| row.scheduled_at))
}

#[cfg(test)]
#[path = "message_queue_schedule_tests.rs"]
mod schedule_tests;

#[cfg(test)]
mod tests {
    use super::*;
    use crate::db::test_helpers;

    fn item(id: &str, text: &str) -> QueueItemInput {
        QueueItemInput {
            id: id.into(),
            draft_json: serde_json::json!({
                "blocks": [{"type": "text", "text": text}], "displayText": text,
            })
            .to_string(),
            mode_id: None,
            adopt_send_time_mode: false,
        }
    }

    #[tokio::test]
    async fn submitted_ids_cannot_be_resurrected_by_a_stale_client_save() {
        let db = test_helpers::fresh_in_memory_db().await;
        save(&db.conn, "42", vec![item("a", "A"), item("b", "B")])
            .await
            .unwrap();
        let claim = claim_next(&db.conn, "42", "claim-a")
            .await
            .unwrap()
            .unwrap();
        acknowledge(&db.conn, &claim).await.unwrap();
        save(
            &db.conn,
            "42",
            vec![item("a", "A"), item("b", "B"), item("c", "C")],
        )
        .await
        .unwrap();
        let queue = list(&db.conn, "42").await.unwrap();
        assert_eq!(
            queue.iter().map(|q| q.id.as_str()).collect::<Vec<_>>(),
            ["b", "c"]
        );
    }

    #[tokio::test]
    async fn claiming_is_atomic_and_keeps_the_payload_until_acceptance() {
        let db = test_helpers::fresh_in_memory_db().await;
        save(&db.conn, "42", vec![item("a", "A"), item("b", "B")])
            .await
            .unwrap();
        let (first, second) = tokio::join!(
            claim_next(&db.conn, "42", "first"),
            claim_next(&db.conn, "42", "second"),
        );
        assert_eq!(
            usize::from(first.unwrap().is_some()) + usize::from(second.unwrap().is_some()),
            1
        );
        let queue = list(&db.conn, "42").await.unwrap();
        assert_eq!(
            queue.len(),
            2,
            "claimed content stays recoverable until admission succeeds"
        );
        assert_eq!(queue[0].delivery_state.as_deref(), Some("claimed"));
    }

    #[tokio::test]
    async fn client_edits_cannot_replace_or_drop_an_in_flight_claim() {
        let db = test_helpers::fresh_in_memory_db().await;
        save(&db.conn, "42", vec![item("a", "A"), item("b", "B")])
            .await
            .unwrap();
        let claim = claim_next(&db.conn, "42", "claim-a")
            .await
            .unwrap()
            .unwrap();
        save(&db.conn, "42", vec![item("b", "edited B"), item("c", "C")])
            .await
            .unwrap();
        let queue = list(&db.conn, "42").await.unwrap();
        assert_eq!(
            queue.iter().map(|q| q.id.as_str()).collect::<Vec<_>>(),
            ["a", "b", "c"]
        );
        release(&db.conn, &claim, Some("turn busy")).await.unwrap();
        assert!(list(&db.conn, "42").await.unwrap()[1]
            .draft_json
            .contains("edited B"));
        assert!(claim_next(&db.conn, "42", "second")
            .await
            .unwrap()
            .is_some());
    }

    #[tokio::test]
    async fn failed_head_is_retained_and_blocks_fifo_until_edited_or_retried() {
        let db = test_helpers::fresh_in_memory_db().await;
        save(&db.conn, "42", vec![item("a", "A"), item("b", "B")])
            .await
            .unwrap();
        let claim = claim_next(&db.conn, "42", "claim-a")
            .await
            .unwrap()
            .unwrap();
        fail(&db.conn, &claim, "invalid draft").await.unwrap();
        assert!(claim_next(&db.conn, "42", "second")
            .await
            .unwrap()
            .is_none());
        let failed = list(&db.conn, "42").await.unwrap();
        assert_eq!(failed[0].last_error.as_deref(), Some("invalid draft"));
        save(&db.conn, "42", vec![item("a", "A"), item("b", "B")])
            .await
            .unwrap();
        assert!(
            claim_next(&db.conn, "42", "third").await.unwrap().is_none(),
            "an unrelated stale save cannot retry a failed send"
        );
        save(&db.conn, "42", vec![item("a", "fixed A"), item("b", "B")])
            .await
            .unwrap();
        assert!(claim_next(&db.conn, "42", "fixed").await.unwrap().is_some());
    }

    #[tokio::test]
    async fn restart_preserves_ambiguous_delivery_without_automatic_resend() {
        let db = test_helpers::fresh_in_memory_db().await;
        save(&db.conn, "42", vec![item("a", "A")]).await.unwrap();
        let claim = claim_next(&db.conn, "42", "before-crash")
            .await
            .unwrap()
            .unwrap();
        mark_sending(&db.conn, &claim).await.unwrap();
        recover_abandoned_claims(&db.conn).await.unwrap();
        assert!(claim_next(&db.conn, "42", "after-restart")
            .await
            .unwrap()
            .is_none());
        let queue = list(&db.conn, "42").await.unwrap();
        assert_eq!(queue[0].delivery_state.as_deref(), Some("uncertain"));
        assert!(queue[0].draft_json.contains('A'));
        retry(&db.conn, "42", "a").await.unwrap();
        assert!(claim_next(&db.conn, "42", "confirmed-retry")
            .await
            .unwrap()
            .is_some());
    }

    #[tokio::test]
    async fn scan_lists_persisted_pending_owners_without_any_live_connection() {
        let db = test_helpers::fresh_in_memory_db().await;
        save(&db.conn, "42", vec![item("a", "A")]).await.unwrap();
        save(&db.conn, "43", vec![item("b", "B")]).await.unwrap();
        assert_eq!(pending_owners(&db.conn).await.unwrap(), ["42", "43"]);
        let claim = claim_next(&db.conn, "42", "claim-a")
            .await
            .unwrap()
            .unwrap();
        acknowledge(&db.conn, &claim).await.unwrap();
        assert_eq!(pending_owners(&db.conn).await.unwrap(), ["43"]);
    }

    #[tokio::test]
    async fn bad_queue_save_rolls_back_without_dropping_existing_content() {
        let db = test_helpers::fresh_in_memory_db().await;
        save(&db.conn, "42", vec![item("a", "A")]).await.unwrap();
        assert!(
            save(&db.conn, "42", vec![item("b", "B"), item("b", "duplicate")])
                .await
                .is_err()
        );
        assert_eq!(list(&db.conn, "42").await.unwrap()[0].id, "a");
    }
}
