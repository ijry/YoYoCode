use super::*;
use crate::db::service::message_queue_service::{self, QueueItemInput};
use crate::db::test_helpers;
use sea_orm::DatabaseConnection;

fn draft() -> String {
    serde_json::json!({"blocks":[{"type":"text","text":"task A"}],"displayText":"task A"})
        .to_string()
}

async fn begin(db: &DatabaseConnection, recovery_of: Option<&str>) -> QueueTurnAttempt {
    begin_attempt(db, "42", "connection-A", "message-A", &draft(), recovery_of)
        .await
        .unwrap()
}

fn completed(attempt: &QueueTurnAttempt, success: bool, cancelled: bool) -> QueueTurnCompletion {
    QueueTurnCompletion {
        attempt_id: attempt.id.clone(),
        owner_id: attempt.owner_id.clone(),
        success,
        cancelled,
        error: (!success).then(|| "provider error".into()),
    }
}

#[tokio::test]
async fn a_failed_turn_blocks_later_queue_even_after_save_and_reload() {
    let db = test_helpers::fresh_in_memory_db().await;
    let attempt = begin(&db.conn, None).await;
    complete_attempt(&db.conn, &completed(&attempt, false, false))
        .await
        .unwrap();
    message_queue_service::save(
        &db.conn,
        "42",
        vec![QueueItemInput {
            id: "B".into(),
            draft_json: draft(),
            mode_id: None,
            adopt_send_time_mode: false,
        }],
    )
    .await
    .unwrap();
    assert!(message_queue_service::claim_next(&db.conn, "42", "claim-B")
        .await
        .unwrap()
        .is_none());
    let state = get_state(&db.conn, "42").await.unwrap();
    assert_eq!(state.status, "paused");
    assert_eq!(
        state.failed_attempt_id.as_deref(),
        Some(attempt.id.as_str())
    );
    assert_eq!(state.error.as_deref(), Some("provider error"));
}

#[tokio::test]
async fn only_a_successful_continuation_releases_the_paused_queue() {
    let db = test_helpers::fresh_in_memory_db().await;
    let failed = begin(&db.conn, None).await;
    complete_attempt(&db.conn, &completed(&failed, false, false))
        .await
        .unwrap();
    request_continue(&db.conn, "42", &failed.id).await.unwrap();
    assert!(
        request_continue(&db.conn, "42", &failed.id).await.is_err(),
        "duplicate continue must not issue a second prompt"
    );
    let resume = begin(&db.conn, Some(&failed.id)).await;
    assert_eq!(get_state(&db.conn, "42").await.unwrap().status, "resuming");
    assert!(begin_attempt(&db.conn, "42", "other", "B", &draft(), None)
        .await
        .is_err());
    let state = complete_attempt(&db.conn, &completed(&resume, true, false))
        .await
        .unwrap();
    assert_eq!(state.status, "open");
    assert!(state.failed_attempt_id.is_none());
}

#[tokio::test]
async fn a_continuation_error_or_cancellation_keeps_the_queue_paused() {
    for cancelled in [false, true] {
        let db = test_helpers::fresh_in_memory_db().await;
        let failed = begin(&db.conn, None).await;
        complete_attempt(&db.conn, &completed(&failed, false, false))
            .await
            .unwrap();
        request_continue(&db.conn, "42", &failed.id).await.unwrap();
        let resume = begin(&db.conn, Some(&failed.id)).await;
        let state = complete_attempt(&db.conn, &completed(&resume, false, cancelled))
            .await
            .unwrap();
        assert_eq!(state.status, "paused");
        assert!(state.failed_attempt_id.is_some());
    }
}

#[tokio::test]
async fn old_duplicate_completion_cannot_clear_a_new_failed_attempt() {
    let db = test_helpers::fresh_in_memory_db().await;
    let first = begin(&db.conn, None).await;
    let success = completed(&first, true, false);
    complete_attempt(&db.conn, &success).await.unwrap();
    let failed = begin(&db.conn, None).await;
    complete_attempt(&db.conn, &completed(&failed, false, false))
        .await
        .unwrap();
    assert_eq!(
        complete_attempt(&db.conn, &success).await.unwrap().status,
        "paused"
    );
}

#[tokio::test]
async fn an_ordinary_user_cancel_is_not_reported_as_an_execution_error() {
    let db = test_helpers::fresh_in_memory_db().await;
    let attempt = begin(&db.conn, None).await;
    assert_eq!(
        complete_attempt(&db.conn, &completed(&attempt, false, true))
            .await
            .unwrap()
            .status,
        "open"
    );
}

#[tokio::test]
async fn unfinished_attempts_remain_paused_across_host_restart() {
    let db = test_helpers::fresh_in_memory_db().await;
    let attempt = begin(&db.conn, None).await;
    recover_interrupted_attempts(&db.conn).await.unwrap();
    let state = get_state(&db.conn, "42").await.unwrap();
    assert_eq!(state.status, "paused");
    assert_eq!(
        state.failed_attempt_id.as_deref(),
        Some(attempt.id.as_str())
    );
    assert!(state.active_attempt_id.is_none());
}
