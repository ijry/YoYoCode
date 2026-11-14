use crate::acp::connection::{ConnectionCommand, SetterOutcome};
use crate::acp::error::AcpError;
use crate::acp::manager::ConnectionManager;
use crate::acp::types::PromptInputBlock;
use crate::db::{test_helpers, AppDatabase};
use crate::models::agent::AgentType;
use crate::web::event_bridge::EventEmitter;

async fn connection(
    manager: &ConnectionManager,
    cid: i32,
) -> tokio::sync::mpsc::Receiver<ConnectionCommand> {
    let receiver = manager
        .insert_test_connection_live("queued", AgentType::Codex, None, EventEmitter::Noop)
        .await;
    manager
        .get_state("queued")
        .await
        .unwrap()
        .write()
        .await
        .conversation_id = Some(cid);
    receiver
}

fn text(value: &str) -> Vec<PromptInputBlock> {
    vec![PromptInputBlock::Text { text: value.into() }]
}

#[tokio::test]
async fn queued_prompt_refuses_a_mode_the_agent_did_not_accept() {
    let db = test_helpers::fresh_in_memory_db().await;
    let folder = test_helpers::seed_folder(&db, "/tmp/queue-mode").await;
    let cid = test_helpers::seed_conversation(&db, folder, AgentType::Codex).await;
    let manager = ConnectionManager::new();
    let mut commands = connection(&manager, cid).await;
    let sender = manager.clone_ref();
    let task = tokio::spawn(async move {
        sender
            .send_queued_prompt(
                &db,
                "queued",
                text("A"),
                folder,
                cid,
                Some("plan".into()),
                "queue-a".into(),
            )
            .await
    });
    let ConnectionCommand::SetMode {
        reply: Some(reply), ..
    } = commands.recv().await.unwrap()
    else {
        panic!("mode must be applied before prompting");
    };
    reply
        .send(Ok(SetterOutcome::Refused {
            message: "mode unavailable".into(),
        }))
        .unwrap();
    assert!(
        task.await.unwrap().is_err(),
        "Refused is not a successful mode switch"
    );
    assert!(
        commands.try_recv().is_err(),
        "no prompt may run under the wrong mode"
    );
}

#[tokio::test]
async fn mode_and_queued_send_share_the_same_prompt_admission_lock() {
    let db = test_helpers::fresh_in_memory_db().await;
    let folder = test_helpers::seed_folder(&db, "/tmp/queue-mode-lock").await;
    let cid = test_helpers::seed_conversation(&db, folder, AgentType::Codex).await;
    let manager = ConnectionManager::new();
    let mut commands = connection(&manager, cid).await;
    let queued_manager = manager.clone_ref();
    let queued_db = AppDatabase {
        conn: db.conn.clone(),
    };
    let queued = tokio::spawn(async move {
        queued_manager
            .send_queued_prompt(
                &queued_db,
                "queued",
                text("queued A"),
                folder,
                cid,
                Some("plan".into()),
                "queue-a".into(),
            )
            .await
    });
    let ConnectionCommand::SetMode {
        reply: Some(reply), ..
    } = commands.recv().await.unwrap()
    else {
        panic!("expected the mode request");
    };
    let direct_manager = manager.clone_ref();
    let direct = tokio::spawn(async move {
        direct_manager
            .send_prompt_linked(
                &db,
                "queued",
                text("direct B"),
                Some(folder),
                Some(cid),
                None,
            )
            .await
    });
    tokio::task::yield_now().await;
    assert!(
        commands.try_recv().is_err(),
        "a direct send cannot cut in while setting the queued mode"
    );
    reply
        .send(Ok(SetterOutcome::Confirmed {
            current: "plan".into(),
        }))
        .unwrap();
    queued.await.unwrap().unwrap();
    let ConnectionCommand::Prompt {
        blocks,
        user_message,
        ..
    } = commands.recv().await.unwrap()
    else {
        panic!("expected the queued prompt");
    };
    assert_eq!(blocks, text("queued A"));
    assert_eq!(user_message.unwrap().0, "queue-a");
    assert!(matches!(
        direct.await.unwrap(),
        Err(AcpError::TurnInProgress)
    ));
}

#[tokio::test]
async fn a_paused_conversation_rejects_direct_send_without_changing_its_status() {
    use crate::db::service::message_queue_control_service as control;
    let db = test_helpers::fresh_in_memory_db().await;
    let folder = test_helpers::seed_folder(&db, "/tmp/queue-paused-direct").await;
    let cid = test_helpers::seed_conversation(&db, folder, AgentType::Codex).await;
    crate::db::service::conversation_service::update_status(&db.conn, cid, crate::db::entities::conversation::ConversationStatus::Cancelled).await.unwrap();
    let failed = control::begin_attempt(&db.conn, &cid.to_string(), "queued", "A", "{}", None).await.unwrap();
    control::complete_attempt(&db.conn, &control::QueueTurnCompletion { attempt_id:failed.id,owner_id:cid.to_string(),success:false,cancelled:false,user_cancelled:false,error:Some("failed A".into()) }).await.unwrap();
    let manager = ConnectionManager::new();
    let mut commands = connection(&manager, cid).await;
    assert!(manager.send_queued_prompt(&db, "queued", text("B"), folder, cid, None, "B".into()).await.is_err());
    assert!(commands.try_recv().is_err());
    assert_eq!(crate::db::service::conversation_service::get_by_id(&db.conn,cid).await.unwrap().status, "cancelled");
}

#[tokio::test]
async fn queued_prompt_never_uses_a_connection_bound_to_another_conversation() {
    let db = test_helpers::fresh_in_memory_db().await;
    let folder = test_helpers::seed_folder(&db, "/tmp/queue-binding").await;
    let first = test_helpers::seed_conversation(&db, folder, AgentType::Codex).await;
    let second = test_helpers::seed_conversation(&db, folder, AgentType::Codex).await;
    let manager = ConnectionManager::new();
    let mut commands = connection(&manager, first).await;
    assert!(manager
        .send_queued_prompt(
            &db,
            "queued",
            text("B"),
            folder,
            second,
            None,
            "queue-b".into()
        )
        .await
        .is_err());
    assert!(commands.try_recv().is_err());
}
