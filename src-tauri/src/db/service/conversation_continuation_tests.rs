use super::*;
use crate::db::{
    service::{message_queue_control_service as control, message_queue_service as queue},
    test_helpers,
};
use sea_orm::ConnectionTrait;

#[tokio::test]
async fn continuation_creates_fresh_execution_state_and_copies_compatible_selection() {
    let db = test_helpers::fresh_in_memory_db().await;
    let folder = test_helpers::seed_folder(&db, "/continuation-test").await;
    let source = create(
        &db.conn,
        folder,
        AgentType::Codex,
        Some("source".into()),
        None,
    )
    .await
    .unwrap();
    let selection = ConversationSelection {
        mode_id: Some("plan".into()),
        composer_mode_id: Some("code".into()),
        config_values: [("model".into(), "source-model".into())].into(),
    };
    set_selector_state(&db.conn, source.id, &selection)
        .await
        .unwrap();
    queue::save(
        &db.conn,
        &source.id.to_string(),
        vec![queue::QueueItemInput {
            id: "queued-task".into(),
            draft_json: "{\"displayText\":\"task\",\"blocks\":[]}".into(),
            mode_id: Some("plan".into()),
            adopt_send_time_mode: true,
        }],
    )
    .await
    .unwrap();
    control::pause_queue(&db.conn, &source.id.to_string())
        .await
        .unwrap();
    let target = create_continuation(
        &db.conn,
        source.id,
        folder,
        AgentType::Codex,
        Some("new".into()),
        None,
    )
    .await
    .unwrap();
    assert_ne!(target.id, source.id);
    assert!(target.external_id.is_none());
    assert_eq!(target.status, conversation::ConversationStatus::InProgress);
    assert_eq!(
        get_selector_state(&db.conn, target.id).await.unwrap(),
        Some(selection)
    );
    assert_eq!(
        queue::list(&db.conn, &target.id.to_string())
            .await
            .unwrap()
            .len(),
        1
    );
    assert_eq!(
        queue::list(&db.conn, &source.id.to_string())
            .await
            .unwrap()
            .len(),
        1
    );
    assert_eq!(
        control::get_state(&db.conn, &target.id.to_string())
            .await
            .unwrap()
            .hold
            .as_deref(),
        Some(control::HOLD_MANUAL_PAUSE)
    );
    let switched = create_continuation(
        &db.conn,
        source.id,
        folder,
        AgentType::ClaudeCode,
        None,
        None,
    )
    .await
    .unwrap();
    assert!(switched.selector_state.is_none());
}

#[tokio::test]
async fn continuation_creation_is_atomic_when_queue_copy_fails_or_source_is_missing() {
    let db = test_helpers::fresh_in_memory_db().await;
    let folder = test_helpers::seed_folder(&db, "/continuation-test").await;
    let source = create(&db.conn, folder, AgentType::Codex, None, None)
        .await
        .unwrap();
    db.conn.execute_unprepared("CREATE TRIGGER reject_continuation BEFORE INSERT ON message_queue_state BEGIN SELECT RAISE(ABORT,'test failure'); END").await.unwrap();
    assert!(
        create_continuation(&db.conn, source.id, folder, AgentType::Codex, None, None)
            .await
            .is_err()
    );
    assert!(
        create_continuation(&db.conn, -1, folder, AgentType::Codex, None, None)
            .await
            .is_err()
    );
    use sea_orm::PaginatorTrait;
    assert_eq!(
        conversation::Entity::find().count(&db.conn).await.unwrap(),
        1
    );
}

#[tokio::test]
async fn continuation_draft_selection_precedes_defaults_but_respects_explicit_overrides() {
    let db = test_helpers::fresh_in_memory_db().await;
    let folder = test_helpers::seed_folder(&db, "/selection-test").await;
    let source = create(&db.conn, folder, AgentType::Codex, None, None)
        .await
        .unwrap();
    let selection = ConversationSelection {
        mode_id: Some("plan".into()),
        composer_mode_id: Some("code".into()),
        config_values: [
            ("model".into(), "source-model".into()),
            ("effort".into(), "medium".into()),
        ]
        .into(),
    };
    set_selector_state(&db.conn, source.id, &selection)
        .await
        .unwrap();
    let fallback = ConversationSelection {
        mode_id: Some("default".into()),
        config_values: [("model".into(), "global-default".into())].into(),
        ..Default::default()
    };
    let inherited = selection_for_continuation_draft(
        &db.conn,
        source.id,
        AgentType::Codex,
        fallback.clone(),
        [("effort".into(), "high".into())].into(),
    )
    .await
    .unwrap();
    assert_eq!(inherited.mode_id, selection.mode_id);
    assert_eq!(inherited.composer_mode_id, selection.composer_mode_id);
    assert_eq!(
        inherited.config_values.get("model").unwrap(),
        "source-model"
    );
    assert_eq!(inherited.config_values.get("effort").unwrap(), "high");
    assert_eq!(
        selection_for_continuation_draft(
            &db.conn,
            source.id,
            AgentType::ClaudeCode,
            fallback.clone(),
            Default::default()
        )
        .await
        .unwrap(),
        fallback
    );
}

#[tokio::test]
async fn continuation_into_a_chat_folder_keeps_chat_kind_and_copies_queue_state() {
    let db = test_helpers::fresh_in_memory_db().await;
    let folder_id = test_helpers::seed_folder(&db, "/chat-test").await;
    let source = create(&db.conn, folder_id, AgentType::Codex, None, None)
        .await
        .unwrap();
    folder::Entity::update_many()
        .col_expr(
            folder::Column::Kind,
            sea_orm::sea_query::Expr::value(folder::FolderKind::Chat),
        )
        .filter(folder::Column::Id.eq(folder_id))
        .exec(&db.conn)
        .await
        .unwrap();
    let target = create_continuation(&db.conn, source.id, folder_id, AgentType::Codex, None, None)
        .await
        .unwrap();
    assert_eq!(target.kind, conversation::ConversationKind::Chat);
    assert!(!control::can_drain(&db.conn, &target.id.to_string())
        .await
        .unwrap());
}
