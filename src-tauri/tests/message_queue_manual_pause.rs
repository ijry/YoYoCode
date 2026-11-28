//! Manual queue pause uses the real persisted control/claim services. Kept as
//! an integration target so it can run independently of unrelated lib unit tests.
#![cfg(feature = "test-utils")]

use codeg_lib::db::service::message_queue_control_service::{
    self as control, QueueTurnCompletion, HOLD_MANUAL_PAUSE, HOLD_MANUAL_STOP,
};
use codeg_lib::db::service::message_queue_service::{self as queue, QueueItemInput};
use codeg_lib::db::test_helpers;
use sea_orm::{Database, DatabaseConnection};

fn draft(text: &str) -> String {
    serde_json::json!({"blocks":[{"type":"text","text":text}],"displayText":text}).to_string()
}

async fn save(conn: &DatabaseConnection, texts: &[(&str, &str)]) {
    queue::save(
        conn,
        "42",
        texts
            .iter()
            .map(|(id, text)| QueueItemInput {
                id: (*id).into(),
                draft_json: draft(text),
                mode_id: None,
                adopt_send_time_mode: false,
            })
            .collect(),
    )
    .await
    .unwrap();
}

async fn attempt(conn: &DatabaseConnection) -> control::QueueTurnAttempt {
    control::begin_attempt(
        conn,
        "42",
        "connection",
        "message",
        &draft("direct prompt"),
        None,
    )
    .await
    .unwrap()
}

fn completion(
    attempt: &control::QueueTurnAttempt,
    success: bool,
    user_cancelled: bool,
) -> QueueTurnCompletion {
    QueueTurnCompletion {
        owner_id: attempt.owner_id.clone(),
        attempt_id: attempt.id.clone(),
        success,
        cancelled: user_cancelled,
        user_cancelled,
        error: (!success).then(|| "provider error".into()),
    }
}

#[tokio::test]
async fn pause_is_persistent_idempotent_and_only_explicit_release_allows_automatic_claims() {
    let dir = tempfile::tempdir().unwrap();
    let db = test_helpers::fresh_disk_db(dir.path()).await;
    save(&db.conn, &[("a", "first"), ("b", "second")]).await;
    let state = control::pause_queue(&db.conn, "42").await.unwrap();
    let again = control::pause_queue(&db.conn, "42").await.unwrap();
    assert_eq!(state.hold.as_deref(), Some(HOLD_MANUAL_PAUSE));
    assert_eq!(state.revision, again.revision);
    db.conn.close().await.unwrap();

    let conn = Database::connect(format!(
        "sqlite:{}?mode=rw",
        dir.path().join("source.db").to_string_lossy()
    ))
    .await
    .unwrap();
    control::recover_interrupted_attempts(&conn).await.unwrap();
    queue::recover_abandoned_claims(&conn).await.unwrap();
    assert_eq!(
        control::get_state(&conn, "42")
            .await
            .unwrap()
            .hold
            .as_deref(),
        Some(HOLD_MANUAL_PAUSE)
    );
    assert!(queue::claim_next(&conn, "42", "automatic")
        .await
        .unwrap()
        .is_none());
    control::release_hold(&conn, "42").await.unwrap();
    let claim = queue::claim_next(&conn, "42", "resumed")
        .await
        .unwrap()
        .unwrap();
    assert_eq!(claim.item.id, "a");
    conn.close().await.unwrap();
}

#[tokio::test]
async fn explicit_single_row_send_and_success_do_not_resume_the_remaining_queue() {
    let db = test_helpers::fresh_in_memory_db().await;
    save(&db.conn, &[("a", "first"), ("b", "send this now")]).await;
    control::pause_queue(&db.conn, "42").await.unwrap();
    let claim = queue::claim_item(&db.conn, "42", "b", "manual")
        .await
        .unwrap()
        .expect("manual send bypasses hold");
    let turn = attempt(&db.conn).await;
    queue::mark_sending(&db.conn, &claim).await.unwrap();
    queue::acknowledge(&db.conn, &claim).await.unwrap();
    let state = control::complete_attempt(&db.conn, &completion(&turn, true, false))
        .await
        .unwrap();
    assert_eq!(state.status, "open");
    assert_eq!(state.hold.as_deref(), Some(HOLD_MANUAL_PAUSE));
    assert!(queue::claim_next(&db.conn, "42", "automatic")
        .await
        .unwrap()
        .is_none());
    control::release_hold(&db.conn, "42").await.unwrap();
    assert_eq!(
        queue::claim_next(&db.conn, "42", "resumed")
            .await
            .unwrap()
            .unwrap()
            .item
            .id,
        "a"
    );
}

#[tokio::test]
async fn an_already_claimed_row_may_finish_after_pause_without_releasing_it() {
    let db = test_helpers::fresh_in_memory_db().await;
    save(&db.conn, &[("a", "first"), ("b", "later")]).await;
    let claim = queue::claim_next(&db.conn, "42", "already-claimed")
        .await
        .unwrap()
        .unwrap();
    let turn = attempt(&db.conn).await;
    control::pause_queue(&db.conn, "42").await.unwrap();
    queue::mark_sending(&db.conn, &claim).await.unwrap();
    queue::acknowledge(&db.conn, &claim).await.unwrap();
    control::complete_attempt(&db.conn, &completion(&turn, true, false))
        .await
        .unwrap();
    assert!(queue::claim_next(&db.conn, "42", "next")
        .await
        .unwrap()
        .is_none());
}

#[tokio::test]
async fn a_queued_continue_after_an_error_must_not_undo_manual_pause() {
    let db = test_helpers::fresh_in_memory_db().await;
    save(&db.conn, &[("a", "continue the task")]).await;
    let turn = attempt(&db.conn).await;
    control::pause_queue(&db.conn, "42").await.unwrap();
    let state = control::complete_attempt(&db.conn, &completion(&turn, false, false))
        .await
        .unwrap();
    assert_eq!(state.hold.as_deref(), Some(HOLD_MANUAL_PAUSE));
    assert_eq!(state.status, "paused");
    assert!(queue::claim_next(&db.conn, "42", "automatic")
        .await
        .unwrap()
        .is_none());
    // Releasing the manual hold does not silently erase independent error recovery.
    let released = control::release_hold(&db.conn, "42").await.unwrap();
    assert_eq!(released.status, "paused");
    assert!(released.hold.is_none());
}

#[tokio::test]
async fn a_user_stop_does_not_replace_manual_pause_with_a_temporary_hold() {
    let db = test_helpers::fresh_in_memory_db().await;
    save(&db.conn, &[("a", "later")]).await;
    let turn = attempt(&db.conn).await;
    control::pause_queue(&db.conn, "42").await.unwrap();
    let state = control::complete_attempt(&db.conn, &completion(&turn, false, true))
        .await
        .unwrap();
    assert_eq!(state.hold.as_deref(), Some(HOLD_MANUAL_PAUSE));
    let next = attempt(&db.conn).await;
    let state = control::complete_attempt(&db.conn, &completion(&next, true, false))
        .await
        .unwrap();
    assert_eq!(state.hold.as_deref(), Some(HOLD_MANUAL_PAUSE));
}

#[tokio::test]
async fn an_explicit_recovery_preserves_manual_pause_and_failure_metadata() {
    let db = test_helpers::fresh_in_memory_db().await;
    let failed = attempt(&db.conn).await;
    control::complete_attempt(&db.conn, &completion(&failed, false, false))
        .await
        .unwrap();
    let paused = control::pause_queue(&db.conn, "42").await.unwrap();
    assert_eq!(
        paused.failed_attempt_id.as_deref(),
        Some(failed.id.as_str())
    );
    assert_eq!(paused.error.as_deref(), Some("provider error"));
    control::request_continue(&db.conn, "42", &failed.id)
        .await
        .unwrap();
    let recovery = control::begin_attempt(
        &db.conn,
        "42",
        "connection",
        "recovery",
        &draft("resume"),
        Some(&failed.id),
    )
    .await
    .unwrap();
    let recovered = control::complete_attempt(&db.conn, &completion(&recovery, true, false))
        .await
        .unwrap();
    assert_eq!(recovered.status, "open");
    assert_eq!(recovered.hold.as_deref(), Some(HOLD_MANUAL_PAUSE));
    assert!(!control::can_drain(&db.conn, "42").await.unwrap());
}

#[tokio::test]
async fn existing_user_stop_hold_still_clears_on_success_and_is_not_silently_replaced() {
    let db = test_helpers::fresh_in_memory_db().await;
    save(&db.conn, &[("a", "later")]).await;
    let turn = attempt(&db.conn).await;
    let stopped = control::complete_attempt(&db.conn, &completion(&turn, false, true))
        .await
        .unwrap();
    assert_eq!(stopped.hold.as_deref(), Some(HOLD_MANUAL_STOP));
    assert!(control::pause_queue(&db.conn, "42").await.is_err());
    let next = attempt(&db.conn).await;
    let state = control::complete_attempt(&db.conn, &completion(&next, true, false))
        .await
        .unwrap();
    assert!(state.hold.is_none());
    assert!(control::can_drain(&db.conn, "42").await.unwrap());
}

#[tokio::test]
async fn item_pause_skips_only_that_row_and_survives_edits_and_queue_resume() {
    let db = test_helpers::fresh_in_memory_db().await;
    save(&db.conn, &[("a", "first"), ("b", "second")]).await;
    queue::set_paused(&db.conn, "42", "a", true).await.unwrap();
    save(&db.conn, &[("a", "edited"), ("b", "second")]).await;
    assert_eq!(
        queue::list(&db.conn, "42").await.unwrap()[0]
            .delivery_state
            .as_deref(),
        Some("paused")
    );
    control::pause_queue(&db.conn, "42").await.unwrap();
    control::release_hold(&db.conn, "42").await.unwrap();
    assert_eq!(queue::pending_owners(&db.conn).await.unwrap(), ["42"]);
    let next = queue::claim_next(&db.conn, "42", "claim-b")
        .await
        .unwrap()
        .unwrap();
    assert_eq!(next.item.id, "b");
    assert!(queue::set_paused(&db.conn, "42", "b", true).await.is_err());
    queue::acknowledge(&db.conn, &next).await.unwrap();
    assert!(queue::claim_next(&db.conn, "42", "none")
        .await
        .unwrap()
        .is_none());
    queue::set_paused(&db.conn, "42", "a", false).await.unwrap();
    assert_eq!(
        queue::claim_next(&db.conn, "42", "claim-a")
            .await
            .unwrap()
            .unwrap()
            .item
            .id,
        "a"
    );
}

#[tokio::test]
async fn paused_scheduled_item_survives_restart_and_can_be_rescheduled_or_removed() {
    let dir = tempfile::tempdir().unwrap();
    let db = test_helpers::fresh_disk_db(dir.path()).await;
    save(&db.conn, &[("a", "scheduled"), ("b", "later")]).await;
    let at = chrono::Utc::now() + chrono::Duration::hours(1);
    queue::set_schedule(&db.conn, "42", "a", Some(at))
        .await
        .unwrap();
    queue::set_paused(&db.conn, "42", "a", true).await.unwrap();
    assert!(queue::next_schedule(&db.conn).await.unwrap().is_none());
    db.conn.close().await.unwrap();
    let conn = Database::connect(format!(
        "sqlite:{}?mode=rw",
        dir.path().join("source.db").to_string_lossy()
    ))
    .await
    .unwrap();
    queue::recover_abandoned_claims(&conn).await.unwrap();
    let items = queue::list(&conn, "42").await.unwrap();
    assert_eq!(items[0].delivery_state.as_deref(), Some("paused"));
    assert_eq!(items[0].scheduled_at, Some(at));
    assert!(queue::set_paused(&conn, "other-owner", "a", false)
        .await
        .is_err());
    queue::set_schedule(&conn, "42", "a", None).await.unwrap();
    assert_eq!(
        queue::list(&conn, "42").await.unwrap()[0]
            .delivery_state
            .as_deref(),
        Some("paused")
    );
    queue::remove(&conn, "42", "a").await.unwrap();
    assert!(queue::set_paused(&conn, "42", "a", false).await.is_err());
    assert_eq!(queue::list(&conn, "42").await.unwrap().len(), 1);
}
