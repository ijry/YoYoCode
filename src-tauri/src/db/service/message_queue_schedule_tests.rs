use super::*;
use crate::db::test_helpers;
use chrono::{Duration, Utc};

fn input(id: &str) -> QueueItemInput {
    QueueItemInput { id:id.into(),draft_json:serde_json::json!({"blocks":[{"type":"text","text":id}],"displayText":id}).to_string(),mode_id:None,adopt_send_time_mode:false }
}

#[tokio::test]
async fn a_future_message_does_not_block_an_ordinary_message() {
    let db=test_helpers::fresh_in_memory_db().await;
    save(&db.conn,"42",vec![input("future"),input("ordinary")]).await.unwrap();
    set_schedule(&db.conn,"42","future",Some(Utc::now()+Duration::minutes(30))).await.unwrap();
    let claim=claim_next(&db.conn,"42","ordinary").await.unwrap().unwrap();
    assert_eq!(claim.item.id,"ordinary");
    acknowledge(&db.conn,&claim).await.unwrap();
    assert!(claim_next(&db.conn,"42","not-due").await.unwrap().is_none());
}

#[tokio::test]
async fn converting_scheduled_to_ordinary_keeps_id_body_and_mode() {
    let db=test_helpers::fresh_in_memory_db().await;
    let mut entry=input("future"); entry.mode_id=Some("plan".into());
    enqueue(&db.conn,"42",entry,Some(Utc::now()+Duration::hours(2))).await.unwrap();
    let planned=list(&db.conn,"42").await.unwrap()[0].clone();
    assert!(planned.scheduled_at.is_some());
    set_schedule(&db.conn,"42","future",None).await.unwrap();
    let claim=claim_next(&db.conn,"42","converted").await.unwrap().unwrap();
    assert_eq!(claim.item.id,planned.id);
    assert_eq!(claim.item.draft_json,planned.draft_json);
    assert_eq!(claim.item.mode_id,planned.mode_id);
}

#[tokio::test]
async fn a_stale_legacy_save_cannot_drop_or_unschedule_a_timed_message() {
    let db=test_helpers::fresh_in_memory_db().await;
    let at=Utc::now()+Duration::hours(2);
    enqueue(&db.conn,"42",input("future"),Some(at)).await.unwrap();
    save(&db.conn,"42",vec![input("new-ordinary")]).await.unwrap();
    let planned=list(&db.conn,"42").await.unwrap().into_iter().find(|item|item.id=="future").unwrap();
    assert!(planned.scheduled_at.is_some());
    save(&db.conn,"42",vec![input("future"),input("new-ordinary")]).await.unwrap();
    assert!(list(&db.conn,"42").await.unwrap()[0].scheduled_at.is_some());
    remove(&db.conn,"42","future").await.unwrap();
    assert!(!list(&db.conn,"42").await.unwrap().iter().any(|item|item.id=="future"));
}

#[tokio::test]
async fn arrived_schedule_still_obeys_the_conversation_pause_gate() {
    let db=test_helpers::fresh_in_memory_db().await;
    enqueue(&db.conn,"42",input("future"),Some(Utc::now()+Duration::minutes(30))).await.unwrap();
    use crate::db::service::message_queue_control_service as control;
    let attempt=control::begin_attempt(&db.conn,"42","c","A","{}",None).await.unwrap();
    control::complete_attempt(&db.conn,&control::QueueTurnCompletion{attempt_id:attempt.id,owner_id:"42".into(),success:false,cancelled:false,error:Some("task error".into())}).await.unwrap();
    message_queue_item::Entity::update_many().col_expr(message_queue_item::Column::ScheduledAt,sea_orm::sea_query::Expr::value(Utc::now()-Duration::seconds(1))).filter(message_queue_item::Column::OwnerId.eq("42")).exec(&db.conn).await.unwrap();
    assert!(claim_next(&db.conn,"42","due-but-paused").await.unwrap().is_none());
}

#[tokio::test]
async fn due_message_becomes_eligible_once_and_claimed_time_cannot_change() {
    let db=test_helpers::fresh_in_memory_db().await;
    enqueue(&db.conn,"42",input("due"),Some(Utc::now()+Duration::minutes(30))).await.unwrap();
    message_queue_item::Entity::update_many().col_expr(message_queue_item::Column::ScheduledAt,sea_orm::sea_query::Expr::value(Utc::now()-Duration::seconds(1))).filter(message_queue_item::Column::OwnerId.eq("42")).exec(&db.conn).await.unwrap();
    let claim=claim_next(&db.conn,"42","due").await.unwrap().unwrap();
    assert!(set_schedule(&db.conn,"42","due",None).await.is_err());
    acknowledge(&db.conn,&claim).await.unwrap();
    assert!(claim_next(&db.conn,"42","again").await.unwrap().is_none());
}

#[tokio::test]
async fn next_deadline_is_only_a_future_time_and_invalid_past_time_is_rejected() {
    let db=test_helpers::fresh_in_memory_db().await;
    assert!(enqueue(&db.conn,"42",input("past"),Some(Utc::now()-Duration::minutes(1))).await.is_err());
    assert!(next_schedule(&db.conn).await.unwrap().is_none());
    let at=Utc::now()+Duration::minutes(30);
    enqueue(&db.conn,"42",input("future"),Some(at)).await.unwrap();
    assert_eq!(next_schedule(&db.conn).await.unwrap(),Some(at));
}

#[tokio::test]
async fn an_explicit_removal_cannot_be_resurrected_by_a_stale_full_save() {
    let db = test_helpers::fresh_in_memory_db().await;
    save(&db.conn, "42", vec![input("deleted")]).await.unwrap();
    remove(&db.conn, "42", "deleted").await.unwrap();
    save(&db.conn, "42", vec![input("deleted"), input("new")]).await.unwrap();
    assert_eq!(list(&db.conn, "42").await.unwrap()[0].id, "new");
    assert_eq!(list(&db.conn, "42").await.unwrap().len(), 1);
}

#[tokio::test]
async fn atomic_enqueues_from_two_clients_keep_both_messages_and_retries_are_idempotent() {
    let db = test_helpers::fresh_in_memory_db().await;
    let at = Utc::now() + Duration::hours(2);
    let (a, b) = tokio::join!(
        enqueue(&db.conn, "42", input("a"), Some(at)),
        enqueue(&db.conn, "42", input("b"), None),
    );
    a.unwrap();
    b.unwrap();
    enqueue(&db.conn, "42", input("a"), Some(at)).await.unwrap();
    assert_eq!(list(&db.conn, "42").await.unwrap().len(), 2);
}

#[tokio::test]
async fn scans_exclude_future_only_owners_and_do_not_skip_a_failed_eligible_head() {
    let db = test_helpers::fresh_in_memory_db().await;
    enqueue(&db.conn, "41", input("future"), Some(Utc::now() + Duration::hours(2))).await.unwrap();
    save(&db.conn, "42", vec![input("failed"), input("ordinary")]).await.unwrap();
    let claim = claim_next(&db.conn, "42", "failure").await.unwrap().unwrap();
    fail(&db.conn, &claim, "invalid draft").await.unwrap();
    assert!(pending_owners(&db.conn).await.unwrap().is_empty());
    assert!(set_schedule(&db.conn, "42", "failed", Some(Utc::now() + Duration::hours(2))).await.is_err());
    assert!(claim_next(&db.conn, "42", "blocked").await.unwrap().is_none());
    retry(&db.conn, "42", "failed").await.unwrap();
    assert_eq!(pending_owners(&db.conn).await.unwrap(), ["42"]);
}

#[tokio::test]
async fn a_failed_or_uncertain_scheduled_message_reverts_to_ordinary_once_settled() {
    let db = test_helpers::fresh_in_memory_db().await;
    enqueue(&db.conn, "42", input("timed"), Some(Utc::now() + Duration::hours(2))).await.unwrap();
    message_queue_item::Entity::update_many()
        .col_expr(message_queue_item::Column::ScheduledAt, sea_orm::sea_query::Expr::value(Utc::now() - Duration::seconds(1)))
        .filter(message_queue_item::Column::OwnerId.eq("42"))
        .exec(&db.conn)
        .await.unwrap();

    let claim = claim_next(&db.conn, "42", "dispatch").await.unwrap().unwrap();
    fail(&db.conn, &claim, "invalid draft").await.unwrap();
    let failed = list(&db.conn, "42").await.unwrap();
    assert_eq!(failed[0].delivery_state.as_deref(), Some("failed"));
    assert!(
        failed[0].scheduled_at.is_none(),
        "a settled delivery is no longer a scheduled one; keeping a past \
         instant would make legacy full saves unable to ever drop the row"
    );

    retry(&db.conn, "42", "timed").await.unwrap();
    let retried = list(&db.conn, "42").await.unwrap();
    assert_eq!(retried[0].scheduled_at, None);
    assert!(claim_next(&db.conn, "42", "retry-send").await.unwrap().is_some());
}

#[tokio::test]
async fn an_uncertain_delivery_after_recover_does_not_keep_the_old_schedule() {
    let db = test_helpers::fresh_in_memory_db().await;
    enqueue(&db.conn, "42", input("timed"), Some(Utc::now() + Duration::minutes(5))).await.unwrap();
    message_queue_item::Entity::update_many()
        .col_expr(message_queue_item::Column::ScheduledAt, sea_orm::sea_query::Expr::value(Utc::now() - Duration::seconds(1)))
        .filter(message_queue_item::Column::OwnerId.eq("42"))
        .exec(&db.conn)
        .await.unwrap();
    let claim = claim_next(&db.conn, "42", "before-crash").await.unwrap().unwrap();
    mark_sending(&db.conn, &claim).await.unwrap();
    recover_abandoned_claims(&db.conn).await.unwrap();
    let uncertain = list(&db.conn, "42").await.unwrap();
    assert_eq!(uncertain[0].delivery_state.as_deref(), Some("uncertain"));
    assert!(uncertain[0].scheduled_at.is_none());
    // A legacy full save that omits the row must now be able to drop it.
    save(&db.conn, "42", vec![input("fresh")]).await.unwrap();
    assert_eq!(
        list(&db.conn, "42").await.unwrap().iter().map(|i| i.id.as_str()).collect::<Vec<_>>(),
        ["fresh"]
    );
}

#[tokio::test]
async fn a_removed_tombstone_never_becomes_the_claim_head() {
    let db = test_helpers::fresh_in_memory_db().await;
    save(&db.conn, "42", vec![input("gone"), input("kept")]).await.unwrap();
    remove(&db.conn, "42", "gone").await.unwrap();
    let claim = claim_next(&db.conn, "42", "next").await.unwrap().unwrap();
    assert_eq!(claim.item.id, "kept");
}

#[tokio::test]
async fn scans_still_find_a_ordinary_head_behind_a_future_schedule() {
    let db = test_helpers::fresh_in_memory_db().await;
    enqueue(&db.conn, "42", input("future"), Some(Utc::now() + Duration::hours(2))).await.unwrap();
    enqueue(&db.conn, "42", input("ordinary"), None).await.unwrap();
    assert_eq!(
        pending_owners(&db.conn).await.unwrap(),
        ["42"],
        "the future schedule is transparent for the head scan, so the owner \
         must stay on the periodic list and the ordinary message keeps its \
         drain channel after a restart"
    );
}

#[tokio::test]
async fn scheduled_enqueues_and_removes_reject_conflicting_rows() {
    let db = test_helpers::fresh_in_memory_db().await;
    save(&db.conn, "42", vec![input("live")]).await.unwrap();
    let claim = claim_next(&db.conn, "42", "hold").await.unwrap().unwrap();
    assert!(enqueue(&db.conn, "42", input("live"), None).await.is_err());
    acknowledge(&db.conn, &claim).await.unwrap();
    assert!(remove(&db.conn, "42", "live").await.is_err());
    assert!(remove(&db.conn, "42", "missing").await.is_err());
}

#[tokio::test]
async fn next_schedule_excludes_already_due_items() {
    let db = test_helpers::fresh_in_memory_db().await;
    enqueue(&db.conn, "42", input("due"), Some(Utc::now() + Duration::minutes(5))).await.unwrap();
    message_queue_item::Entity::update_many()
        .col_expr(message_queue_item::Column::ScheduledAt, sea_orm::sea_query::Expr::value(Utc::now() - Duration::seconds(1)))
        .filter(message_queue_item::Column::OwnerId.eq("42"))
        .exec(&db.conn)
        .await.unwrap();
    assert!(
        next_schedule(&db.conn).await.unwrap().is_none(),
        "a due item must wake through the periodic scan, not a zero-delay \
         deadline that would busy-spin while the owner is busy or paused"
    );
}
