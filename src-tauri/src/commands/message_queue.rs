#[cfg(feature = "tauri-runtime")]
use crate::acp::message_queue_drain::DrainHandle;
#[cfg(feature = "tauri-runtime")]
use crate::db::error::DbError;
#[cfg(feature = "tauri-runtime")]
use crate::db::service::message_queue_service::{self, QueueItemInput};
#[cfg(feature = "tauri-runtime")]
use crate::db::AppDatabase;
#[cfg(feature = "tauri-runtime")]
use crate::models::MessageQueueItem;
#[cfg(feature = "tauri-runtime")]
use crate::web::event_bridge::{
    emit_event, EventEmitter, MessageQueueChange, MESSAGE_QUEUE_CHANGED_EVENT,
};

#[cfg(feature = "tauri-runtime")]
#[tauri::command]
pub fn message_queue_capabilities() -> serde_json::Value {
    serde_json::json!({ "backend_drain": true, "pause_recovery": true, "scheduled_send": true })
}

#[cfg(feature = "tauri-runtime")]
#[tauri::command]
pub async fn message_queue_state(db: tauri::State<'_, AppDatabase>, owner_id: String) -> Result<crate::db::service::message_queue_control_service::MessageQueueState, DbError> {
    crate::db::service::message_queue_control_service::get_state(&db.conn, &owner_id).await
}

#[cfg(feature = "tauri-runtime")]
#[tauri::command]
pub async fn message_queue_continue(
    db: tauri::State<'_, AppDatabase>, app: tauri::AppHandle, drain: tauri::State<'_, DrainHandle>,
    owner_id: String, failed_attempt_id: String,
) -> Result<(), DbError> {
    let state = crate::db::service::message_queue_control_service::request_continue(&db.conn, &owner_id, &failed_attempt_id).await?;
    emit_event(&EventEmitter::Tauri(app), crate::web::event_bridge::MESSAGE_QUEUE_STATE_EVENT, state);
    drain.notify(owner_id);
    Ok(())
}

/// Lift the user-stop hold (`hold = 'manual_stop'`) so the queue drains again.
/// The drainer wakes on the notify; a held owner simply stops returning
/// `Waiting` once `can_drain` sees `hold IS NULL`.
#[cfg(feature = "tauri-runtime")]
#[tauri::command]
pub async fn message_queue_release_hold(
    db: tauri::State<'_, AppDatabase>, app: tauri::AppHandle, drain: tauri::State<'_, DrainHandle>,
    owner_id: String,
) -> Result<(), DbError> {
    let state = crate::db::service::message_queue_control_service::release_hold(&db.conn, &owner_id).await?;
    emit_event(&EventEmitter::Tauri(app), crate::web::event_bridge::MESSAGE_QUEUE_STATE_EVENT, state);
    drain.notify(owner_id);
    Ok(())
}

/// Send ONE queued item immediately at the user's explicit request — the
/// manual override for a queue held by a user stop. Fire-and-forget like
/// `message_queue_retry`: the drain worker owns the connection setup and
/// broadcasts the row's delivery state, so a failure surfaces on the row.
#[cfg(feature = "tauri-runtime")]
#[tauri::command]
pub async fn message_queue_send_now(
    drain: tauri::State<'_, DrainHandle>,
    owner_id: String,
    item_id: String,
) -> Result<(), DbError> {
    drain.send_now(owner_id, item_id);
    Ok(())
}

#[cfg(feature = "tauri-runtime")]
#[tauri::command]
pub async fn message_queue_retry(
    db: tauri::State<'_, AppDatabase>,
    app: tauri::AppHandle,
    drain: tauri::State<'_, DrainHandle>,
    owner_id: String,
    item_id: String,
) -> Result<(), DbError> {
    message_queue_service::retry(&db.conn, &owner_id, &item_id).await?;
    let items = message_queue_service::list(&db.conn, &owner_id).await?;
    emit_event(
        &EventEmitter::Tauri(app),
        MESSAGE_QUEUE_CHANGED_EVENT,
        MessageQueueChange {
            owner_id: owner_id.clone(),
            origin: "backend-retry".into(),
            items,
        },
    );
    drain.notify(owner_id);
    Ok(())
}

#[cfg(feature = "tauri-runtime")]
#[cfg_attr(feature = "tauri-runtime", tauri::command)]
pub async fn message_queue_list(
    db: tauri::State<'_, AppDatabase>,
    owner_id: String,
) -> Result<Vec<MessageQueueItem>, DbError> {
    message_queue_service::list(&db.conn, &owner_id).await
}

#[cfg(feature = "tauri-runtime")]
#[cfg_attr(feature = "tauri-runtime", tauri::command)]
pub async fn message_queue_save(
    db: tauri::State<'_, AppDatabase>,
    app: tauri::AppHandle,
    drain: tauri::State<'_, DrainHandle>,
    owner_id: String,
    items: Vec<QueueItemInput>,
    origin: String,
) -> Result<(), DbError> {
    let saved = message_queue_service::save(&db.conn, &owner_id, items).await?;
    let emitter = EventEmitter::Tauri(app);
    emit_event(
        &emitter,
        MESSAGE_QUEUE_CHANGED_EVENT,
        MessageQueueChange {
            owner_id: owner_id.clone(),
            origin,
            items: saved,
        },
    );
    // A save onto an idle conversation is a drain trigger of its own: the
    // first item of a fresh queue should go out without waiting for the
    // periodic sweep.
    drain.notify(owner_id);
    Ok(())
}

#[cfg(feature = "tauri-runtime")]
#[tauri::command]
pub async fn message_queue_enqueue(
    db: tauri::State<'_, AppDatabase>,
    app: tauri::AppHandle,
    drain: tauri::State<'_, DrainHandle>,
    owner_id: String,
    item: QueueItemInput,
    scheduled_at: Option<String>,
    origin: String,
) -> Result<Vec<MessageQueueItem>, DbError> {
    let at = message_queue_service::parse_schedule_input(scheduled_at)?;
    message_queue_service::enqueue(&db.conn, &owner_id, item, at).await?;
    let items = message_queue_service::list(&db.conn, &owner_id).await?;
    emit_event(
        &EventEmitter::Tauri(app),
        MESSAGE_QUEUE_CHANGED_EVENT,
        MessageQueueChange {
            owner_id: owner_id.clone(),
            origin,
            items: items.clone(),
        },
    );
    drain.notify(owner_id);
    Ok(items)
}

#[cfg(feature = "tauri-runtime")]
#[tauri::command]
pub async fn message_queue_set_schedule(
    db: tauri::State<'_, AppDatabase>,
    app: tauri::AppHandle,
    drain: tauri::State<'_, DrainHandle>,
    owner_id: String,
    item_id: String,
    scheduled_at: Option<String>,
) -> Result<Vec<MessageQueueItem>, DbError> {
    let at = message_queue_service::parse_schedule_input(scheduled_at)?;
    message_queue_service::set_schedule(&db.conn, &owner_id, &item_id, at).await?;
    let items = message_queue_service::list(&db.conn, &owner_id).await?;
    emit_event(
        &EventEmitter::Tauri(app),
        MESSAGE_QUEUE_CHANGED_EVENT,
        MessageQueueChange {
            owner_id: owner_id.clone(),
            origin: "backend-schedule".into(),
            items: items.clone(),
        },
    );
    drain.notify(owner_id);
    Ok(items)
}

#[cfg(feature = "tauri-runtime")]
#[tauri::command]
pub async fn message_queue_remove(
    db: tauri::State<'_, AppDatabase>,
    app: tauri::AppHandle,
    drain: tauri::State<'_, DrainHandle>,
    owner_id: String,
    item_id: String,
) -> Result<Vec<MessageQueueItem>, DbError> {
    message_queue_service::remove(&db.conn, &owner_id, &item_id).await?;
    let items = message_queue_service::list(&db.conn, &owner_id).await?;
    emit_event(
        &EventEmitter::Tauri(app),
        MESSAGE_QUEUE_CHANGED_EVENT,
        MessageQueueChange {
            owner_id: owner_id.clone(),
            origin: "backend-remove".into(),
            items: items.clone(),
        },
    );
    drain.notify(owner_id);
    Ok(items)
}
