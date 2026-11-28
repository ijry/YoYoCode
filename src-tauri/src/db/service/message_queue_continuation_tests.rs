use super::*;
use crate::db::service::message_queue_control_service as control;
use crate::db::test_helpers;

fn input(id: &str) -> QueueItemInput {
    QueueItemInput {
        id: id.into(),
        draft_json: serde_json::json!({
            "displayText": id,
            "blocks": [{"type":"text","text":id}, {"type":"image","data":"base64-image","mimeType":"image/png"}],
            "attachments": [{"path":"/repo/context.txt"}]
        }).to_string(),
        mode_id: Some("plan".into()),
        adopt_send_time_mode: true,
    }
}

async fn copy(db: &DatabaseConnection, source: &str, target: &str) -> Result<(), DbError> {
    let txn = write_transaction(db).await?;
    copy_for_continuation_on(&txn, source, target).await?;
    txn.commit().await?;
    Ok(())
}

async fn finish_first_prompt(db: &DatabaseConnection, owner: &str) {
    let attempt = control::begin_attempt(db, owner, "new-connection", "new-message", "{}", None)
        .await
        .unwrap();
    control::complete_attempt(
        db,
        &control::QueueTurnCompletion {
            attempt_id: attempt.id,
            owner_id: owner.into(),
            success: true,
            cancelled: false,
            user_cancelled: false,
            error: None,
        },
    )
    .await
    .unwrap();
}

#[tokio::test]
async fn continuation_copies_drafts_order_schedule_modes_and_item_pause_without_moving_source() {
    let db = test_helpers::fresh_in_memory_db().await;
    save(
        &db.conn,
        "42",
        vec![input("later"), input("paused"), input("next")],
    )
    .await
    .unwrap();
    set_schedule(
        &db.conn,
        "42",
        "later",
        Some(Utc::now() + chrono::Duration::hours(2)),
    )
    .await
    .unwrap();
    set_paused(&db.conn, "42", "paused", true).await.unwrap();
    let original = list(&db.conn, "42").await.unwrap();
    copy(&db.conn, "42", "43").await.unwrap();
    let copied = list(&db.conn, "43").await.unwrap();
    assert_eq!(copied.len(), 3);
    for (old, new) in original.iter().zip(&copied) {
        assert_ne!(old.id, new.id);
        assert_eq!(old.draft_json, new.draft_json);
        assert_eq!(old.mode_id, new.mode_id);
        assert_eq!(old.adopt_send_time_mode, new.adopt_send_time_mode);
        assert_eq!(old.scheduled_at, new.scheduled_at);
        assert_eq!(old.delivery_state, new.delivery_state);
    }
    let after = list(&db.conn, "42").await.unwrap();
    assert_eq!(
        serde_json::to_value(&original).unwrap(),
        serde_json::to_value(after).unwrap()
    );
    assert!(claim_next(&db.conn, "43", "too-early")
        .await
        .unwrap()
        .is_none());
    finish_first_prompt(&db.conn, "43").await;
    let claim = claim_next(&db.conn, "43", "after-continuation")
        .await
        .unwrap()
        .unwrap();
    assert_eq!(claim.item.draft_json, original[2].draft_json);
}

#[tokio::test]
async fn continuation_never_replays_delivered_items_or_reuses_execution_credentials() {
    let db = test_helpers::fresh_in_memory_db().await;
    let states = [
        "claimed",
        "sending",
        "failed",
        "uncertain",
        "submitted",
        "removed",
    ];
    save(&db.conn, "42", states.iter().map(|s| input(s)).collect())
        .await
        .unwrap();
    for state in states {
        db.conn.execute(Statement::from_sql_and_values(DbBackend::Sqlite,
            "UPDATE message_queue_item SET delivery_state=?,claim_token='old-token',last_error='old-error' WHERE owner_id='42' AND item_uid=?",
            vec![state.into(), state.into()])).await.unwrap();
    }
    control::begin_attempt(&db.conn, "42", "old-connection", "old-message", "{}", None)
        .await
        .unwrap();
    let source_control = control::get_state(&db.conn, "42").await.unwrap();
    copy(&db.conn, "42", "43").await.unwrap();
    let copied = list(&db.conn, "43").await.unwrap();
    assert_eq!(copied.len(), 4);
    assert_eq!(
        copied
            .iter()
            .map(|item| item.delivery_state.as_deref())
            .collect::<Vec<_>>(),
        vec![
            Some("uncertain"),
            Some("uncertain"),
            Some("failed"),
            Some("uncertain")
        ]
    );
    let rows = message_queue_item::Entity::find()
        .filter(message_queue_item::Column::OwnerId.eq("43"))
        .all(&db.conn)
        .await
        .unwrap();
    assert!(rows.iter().all(|row| row.claim_token.is_none()));
    let state = control::get_state(&db.conn, "43").await.unwrap();
    assert!(state.active_attempt_id.is_none());
    assert!(state.failed_attempt_id.is_none());
    let attempts = db
        .conn
        .query_one(Statement::from_string(
            DbBackend::Sqlite,
            "SELECT COUNT(*) AS n FROM message_queue_attempt WHERE owner_id='43'".to_owned(),
        ))
        .await
        .unwrap()
        .unwrap();
    assert_eq!(attempts.try_get::<i64>("", "n").unwrap(), 0);
    assert_eq!(
        control::get_state(&db.conn, "42")
            .await
            .unwrap()
            .active_attempt_id,
        source_control.active_attempt_id
    );
    finish_first_prompt(&db.conn, "43").await;
    assert!(claim_next(&db.conn, "43", "must-not-replay")
        .await
        .unwrap()
        .is_none());
}

#[tokio::test]
async fn continuation_preserves_manual_and_error_pauses_after_a_successful_initial_turn() {
    let db = test_helpers::fresh_in_memory_db().await;
    for (index, (status, hold)) in [
        ("open", Some("manual_pause")),
        ("paused", None),
        ("resume_requested", None),
        ("resuming", Some("manual_stop")),
    ]
    .into_iter()
    .enumerate()
    {
        let source = format!("source-{index}");
        let target = format!("target-{index}");
        save(&db.conn, &source, vec![input("task")]).await.unwrap();
        db.conn.execute(Statement::from_sql_and_values(DbBackend::Sqlite,
            "INSERT INTO message_queue_state(owner_id,status,hold,failed_attempt_id,error,revision,updated_at) VALUES(?,?,?,'old-attempt','old-error',9,?)",
            vec![source.clone().into(), status.into(), hold.into(), Utc::now().to_rfc3339().into()])).await.unwrap();
        copy(&db.conn, &source, &target).await.unwrap();
        let copied = control::get_state(&db.conn, &target).await.unwrap();
        assert_eq!(copied.hold.as_deref(), Some(control::HOLD_MANUAL_PAUSE));
        assert!(copied.active_attempt_id.is_none());
        assert!(copied.failed_attempt_id.is_none());
        finish_first_prompt(&db.conn, &target).await;
        assert!(!control::can_drain(&db.conn, &target).await.unwrap());
        control::release_hold(&db.conn, &target).await.unwrap();
        assert!(claim_next(&db.conn, &target, "resumed")
            .await
            .unwrap()
            .is_some());
        assert_eq!(
            control::get_state(&db.conn, &source).await.unwrap().status,
            if status == "resume_requested" {
                "paused"
            } else {
                status
            }
        );
    }
}

#[tokio::test]
async fn continuation_rolls_back_items_if_copying_control_state_fails() {
    let db = test_helpers::fresh_in_memory_db().await;
    save(&db.conn, "42", vec![input("task")]).await.unwrap();
    db.conn.execute_unprepared("CREATE TRIGGER reject_copy BEFORE INSERT ON message_queue_state WHEN NEW.owner_id='43' BEGIN SELECT RAISE(ABORT,'test failure'); END").await.unwrap();
    assert!(copy(&db.conn, "42", "43").await.is_err());
    assert!(list(&db.conn, "43").await.unwrap().is_empty());
    assert_eq!(list(&db.conn, "42").await.unwrap().len(), 1);
}

#[tokio::test]
async fn continuation_refuses_to_overwrite_an_existing_queue_or_itself() {
    let db = test_helpers::fresh_in_memory_db().await;
    save(&db.conn, "42", vec![input("old")]).await.unwrap();
    save(&db.conn, "43", vec![input("new")]).await.unwrap();
    assert!(copy(&db.conn, "42", "42").await.is_err());
    assert!(copy(&db.conn, "42", "43").await.is_err());
    assert_eq!(list(&db.conn, "43").await.unwrap()[0].id, "new");
}

#[tokio::test]
async fn continuation_keeps_the_old_queue_visible_but_prevents_double_dispatch() {
    let db = test_helpers::fresh_in_memory_db().await;
    save(&db.conn, "42", vec![input("task")]).await.unwrap();
    copy(&db.conn, "42", "43").await.unwrap();
    assert_eq!(list(&db.conn, "42").await.unwrap().len(), 1);
    assert!(!control::can_drain(&db.conn, "42").await.unwrap());
    finish_first_prompt(&db.conn, "42").await;
    assert!(!control::can_drain(&db.conn, "42").await.unwrap());
    assert_eq!(
        control::get_state(&db.conn, "42")
            .await
            .unwrap()
            .hold
            .as_deref(),
        Some(control::HOLD_MANUAL_PAUSE)
    );
}

#[tokio::test]
async fn continuation_withdraws_the_old_queued_recovery_before_it_can_start() {
    let db = test_helpers::fresh_in_memory_db().await;
    let old = control::begin_attempt(&db.conn, "42", "old", "old", "{}", None)
        .await
        .unwrap();
    control::complete_attempt(
        &db.conn,
        &control::QueueTurnCompletion {
            attempt_id: old.id.clone(),
            owner_id: "42".into(),
            success: false,
            cancelled: false,
            user_cancelled: false,
            error: Some("error".into()),
        },
    )
    .await
    .unwrap();
    control::request_continue(&db.conn, "42", &old.id)
        .await
        .unwrap();
    copy(&db.conn, "42", "43").await.unwrap();
    assert!(control::pending_recoveries(&db.conn)
        .await
        .unwrap()
        .is_empty());
    assert!(
        control::begin_attempt(&db.conn, "42", "late", "late", "{}", Some(&old.id))
            .await
            .is_err()
    );
}

#[tokio::test]
async fn an_empty_continuation_accepts_later_enqueues_after_its_initial_turn() {
    let db = test_helpers::fresh_in_memory_db().await;
    copy(&db.conn, "42", "43").await.unwrap();
    assert!(list(&db.conn, "43").await.unwrap().is_empty());
    finish_first_prompt(&db.conn, "43").await;
    save(&db.conn, "43", vec![input("later")]).await.unwrap();
    assert!(claim_next(&db.conn, "43", "new-task")
        .await
        .unwrap()
        .is_some());
}
