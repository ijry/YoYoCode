use super::*;
use crate::acp::connection::ConnectionCommand;
use crate::db::service::message_queue_service::QueueItemInput;
use crate::db::test_helpers;
use crate::models::agent::AgentType;

fn queue_input(id: &str, text: &str) -> QueueItemInput {
    QueueItemInput {
        id: id.into(),
        draft_json: serde_json::json!({
            "blocks": [{"type": "text", "text": text}],
            "displayText": text,
        })
        .to_string(),
        mode_id: None,
        adopt_send_time_mode: false,
    }
}

#[tokio::test]
async fn malformed_draft_remains_visible_instead_of_being_deleted() {
    let db = test_helpers::fresh_in_memory_db().await;
    let folder = test_helpers::seed_folder(&db, "/tmp/message-queue-invalid").await;
    let cid = test_helpers::seed_conversation(&db, folder, AgentType::Codex).await;
    let owner = cid.to_string();
    let mut item = queue_input("broken", "not sendable");
    item.draft_json = "not-json".into();
    message_queue_service::save(&db.conn, &owner, vec![item])
        .await
        .unwrap();
    try_drain_owner(
        &db.conn,
        &ConnectionManager::new(),
        &EventEmitter::Noop,
        Path::new("/tmp"),
        &owner,
    )
    .await;
    let queue = message_queue_service::list(&db.conn, &owner).await.unwrap();
    assert_eq!(queue.len(), 1, "invalid content remains recoverable");
    assert_eq!(queue[0].draft_json, "not-json");
    assert_eq!(queue[0].delivery_state.as_deref(), Some("failed"));
}

#[tokio::test]
async fn claiming_a_head_does_not_overwrite_a_newly_enqueued_tail() {
    let db = test_helpers::fresh_in_memory_db().await;
    message_queue_service::save(
        &db.conn,
        "42",
        vec![queue_input("a", "A"), queue_input("b", "B")],
    )
    .await
    .unwrap();
    message_queue_service::save(
        &db.conn,
        "42",
        vec![
            queue_input("a", "A"),
            queue_input("b", "B"),
            queue_input("c", "C"),
        ],
    )
    .await
    .unwrap();
    message_queue_service::claim_next(&db.conn, "42", "claim-a")
        .await
        .unwrap();
    let queue = message_queue_service::list(&db.conn, "42").await.unwrap();
    assert_eq!(
        queue
            .iter()
            .map(|item| item.id.as_str())
            .collect::<Vec<_>>(),
        ["a", "b", "c"]
    );
}

#[tokio::test]
async fn persisted_queue_starts_without_a_client_notify() {
    let db = test_helpers::fresh_in_memory_db().await;
    let folder = test_helpers::seed_folder(&db, "/tmp/message-queue-startup").await;
    let cid = test_helpers::seed_conversation(&db, folder, AgentType::Codex).await;
    message_queue_service::save(&db.conn, &cid.to_string(), vec![queue_input("a", "A")])
        .await
        .unwrap();
    let manager = ConnectionManager::new();
    let mut commands = manager
        .insert_test_connection_live("startup", AgentType::Codex, None, EventEmitter::Noop)
        .await;
    manager
        .get_state("startup")
        .await
        .unwrap()
        .write()
        .await
        .conversation_id = Some(cid);
    let bus = Arc::new(crate::acp::InternalEventBus::new(Arc::new(
        crate::acp::EventBusMetrics::default(),
    )));
    let data = tempfile::tempdir().unwrap();
    let handle = spawn_drain_task(
        db.conn,
        manager,
        EventEmitter::Noop,
        Arc::from(data.path()),
        bus,
    );
    let command = tokio::time::timeout(Duration::from_secs(3), commands.recv()).await;
    drop(handle);
    assert!(
        command.is_ok(),
        "boot scans queues with zero client pages or notifies"
    );
    assert!(matches!(
        command.unwrap(),
        Some(ConnectionCommand::Prompt { .. })
    ));
}

#[tokio::test]
async fn busy_turn_keeps_the_queue_and_settled_status_wakes_the_next_item() {
    let db = test_helpers::fresh_in_memory_db().await;
    let folder = test_helpers::seed_folder(&db, "/tmp/message-queue-settle").await;
    let cid = test_helpers::seed_conversation(&db, folder, AgentType::Codex).await;
    message_queue_service::save(&db.conn, &cid.to_string(), vec![queue_input("a", "A")])
        .await
        .unwrap();
    let manager = ConnectionManager::new();
    let bus = Arc::new(crate::acp::InternalEventBus::new(Arc::new(
        crate::acp::EventBusMetrics::default(),
    )));
    let broadcaster = Arc::new(crate::web::event_bridge::WebEventBroadcaster::new());
    let emitter = EventEmitter::web_only(broadcaster, bus.clone());
    let mut commands = manager
        .insert_test_connection_live("settling", AgentType::Codex, None, emitter.clone())
        .await;
    let state = manager.get_state("settling").await.unwrap();
    {
        let mut state = state.write().await;
        state.conversation_id = Some(cid);
        state.turn_in_flight = true;
        state.status = ConnectionStatus::Prompting;
    }
    let outcome = try_drain_owner(
        &db.conn,
        &manager,
        &emitter,
        Path::new("/tmp"),
        &cid.to_string(),
    )
    .await;
    assert!(matches!(outcome, DrainOutcome::Waiting));
    assert_eq!(
        message_queue_service::list(&db.conn, &cid.to_string())
            .await
            .unwrap()
            .len(),
        1
    );
    let data = tempfile::tempdir().unwrap();
    let handle = spawn_drain_task(
        db.conn.clone(),
        manager.clone_ref(),
        emitter.clone(),
        Arc::from(data.path()),
        bus,
    );
    {
        let mut state = state.write().await;
        state.turn_in_flight = false;
        state.status = ConnectionStatus::Connected;
    }
    crate::web::event_bridge::emit_with_state(
        &state,
        &emitter,
        AcpEvent::ConversationStatusChanged {
            conversation_id: cid,
            status: crate::db::entities::conversation::ConversationStatus::PendingReview,
        },
    )
    .await;
    assert!(matches!(
        tokio::time::timeout(Duration::from_secs(3), commands.recv())
            .await
            .unwrap(),
        Some(ConnectionCommand::Prompt { .. })
    ));
    drop(handle);
}

#[tokio::test]
async fn next_prompt_waits_until_the_previous_turn_status_is_persisted() {
    let db = test_helpers::fresh_in_memory_db().await;
    let folder = test_helpers::seed_folder(&db, "/tmp/message-queue-status-write").await;
    let cid = test_helpers::seed_conversation(&db, folder, AgentType::Codex).await;
    message_queue_service::save(&db.conn, &cid.to_string(), vec![queue_input("a", "A")])
        .await
        .unwrap();
    let manager = ConnectionManager::new();
    let mut commands = manager
        .insert_test_connection_live("status-write", AgentType::Codex, None, EventEmitter::Noop)
        .await;
    let state = manager.get_state("status-write").await.unwrap();
    {
        let mut state = state.write().await;
        state.conversation_id = Some(cid);
        state.apply_event(&AcpEvent::TurnComplete {
            session_id: "external".into(),
            stop_reason: "end_turn".into(),
            agent_type: "codex".into(),
        });
    }
    let outcome = try_drain_owner(
        &db.conn,
        &manager,
        &EventEmitter::Noop,
        Path::new("/tmp"),
        &cid.to_string(),
    )
    .await;
    assert!(matches!(outcome, DrainOutcome::Waiting));
    assert!(
        commands.try_recv().is_err(),
        "the prior lifecycle write must not overwrite a new InProgress turn"
    );
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
#[allow(clippy::await_holding_lock)]
async fn server_drains_without_a_client_or_a_preexisting_agent_connection() {
    use crate::acp::custom_registry::{
        hydrate, hydrate_test_guard, BinaryPlatformSpec, CustomAgentDef, CustomAgentSpec,
        CustomDistributionKind,
    };
    let _registry = hydrate_test_guard();
    let data = tempfile::tempdir().unwrap();
    let script = data.path().join("queue-agent.mjs");
    let output = data.path().join("prompts.jsonl");
    std::fs::write(&script, r#"
import readline from 'node:readline';
import fs from 'node:fs';
const reply = (id, result) => process.stdout.write(JSON.stringify({jsonrpc:'2.0',id,result})+'\n');
readline.createInterface({input:process.stdin}).on('line', line => {
  const m=JSON.parse(line); fs.appendFileSync(process.argv[2]+'.rpc',line+'\n'); if(m.id===undefined) return;
  if(m.method==='initialize') reply(m.id,{protocolVersion:1,agentCapabilities:{loadSession:true},agentInfo:{name:'queue-test',version:'1.0'}});
  else if(m.method==='session/new') reply(m.id,{sessionId:'queue-session'});
  else if(m.method==='session/load') reply(m.id,{});
  else if(m.method==='session/prompt') {
    fs.appendFileSync(process.argv[2],JSON.stringify({cwd:process.cwd(),prompt:m.params.prompt})+'\n');
    process.stdout.write(JSON.stringify({jsonrpc:'2.0',method:'session/update',params:{sessionId:m.params.sessionId,update:{sessionUpdate:'agent_message_chunk',content:{type:'text',text:'accepted'}}}})+'\n');
    reply(m.id,{stopReason:'end_turn'});
  } else reply(m.id,{});
});
"#).unwrap();
    let def = CustomAgentDef {
        registry_id: "queue-headless-test".into(),
        name: "Queue test agent".into(),
        description: String::new(),
        version: "1.0".into(),
        distribution_kind: CustomDistributionKind::Binary,
        spec: CustomAgentSpec {
            binary: BTreeMap::from([(
                crate::acp::registry::current_platform().to_string(),
                BinaryPlatformSpec {
                    archive: "https://example.invalid/queue-test.zip".into(),
                    cmd: "node".into(),
                    args: vec![
                        script.to_string_lossy().into(),
                        output.to_string_lossy().into(),
                    ],
                    env: BTreeMap::new(),
                    sha256: None,
                },
            )]),
            ..Default::default()
        },
        icon_url: None,
        skills_shared_store: false,
        skills_dir: None,
        source: Default::default(),
        version_probe: None,
        supports_mcp: false,
    };
    assert!(hydrate(std::slice::from_ref(&def)).is_empty());
    let agent = AgentType::custom("queue-headless-test").unwrap();
    let db = test_helpers::fresh_in_memory_db().await;
    let folder = test_helpers::seed_folder(&db, data.path().to_str().unwrap()).await;
    let cid = test_helpers::seed_conversation(&db, folder, agent).await;
    message_queue_service::save(
        &db.conn,
        &cid.to_string(),
        vec![queue_input("a", "A"), queue_input("b", "B")],
    )
    .await
    .unwrap();
    let manager = ConnectionManager::new();
    let bus = Arc::new(crate::acp::InternalEventBus::new(Arc::new(
        crate::acp::EventBusMetrics::default(),
    )));
    let emitter = EventEmitter::web_only(
        Arc::new(crate::web::event_bridge::WebEventBroadcaster::new()),
        bus.clone(),
    );
    let lifecycle = tokio::spawn(crate::acp::lifecycle::lifecycle_subscriber_task(
        db.conn.clone(),
        manager.clone_ref(),
        bus.clone(),
        None,
    ));
    let handle = spawn_drain_task(
        db.conn.clone(),
        manager.clone_ref(),
        emitter,
        Arc::from(data.path()),
        bus,
    );
    let complete = tokio::time::timeout(Duration::from_secs(15), async {
        loop {
            if std::fs::read_to_string(&output).is_ok_and(|text| text.lines().count() == 2) {
                break;
            }
            tokio::time::sleep(Duration::from_millis(25)).await;
        }
    })
    .await;
    if complete.is_err() {
        let queue = message_queue_service::list(&db.conn, &cid.to_string())
            .await
            .unwrap();
        eprintln!("remaining queue: {queue:?}");
        eprintln!(
            "agent requests: {:?}",
            std::fs::read_to_string(format!("{}.rpc", output.display()))
        );
    }
    manager.disconnect_all().await;
    drop(handle);
    lifecycle.abort();
    hydrate(&[]);
    assert!(
        complete.is_ok(),
        "server must establish its own agent and drain both items without clients"
    );
    let lines = std::fs::read_to_string(output).unwrap();
    let prompts: Vec<serde_json::Value> = lines
        .lines()
        .map(|line| serde_json::from_str(line).unwrap())
        .collect();
    assert_eq!(prompts[0]["prompt"][0]["text"], "A");
    assert_eq!(prompts[1]["prompt"][0]["text"], "B");
    assert_eq!(
        prompts[0]["cwd"],
        data.path()
            .to_string_lossy()
            .replace('/', std::path::MAIN_SEPARATOR_STR)
    );
}

#[tokio::test]
async fn an_execution_failure_pauses_b_until_original_context_continuation_succeeds() {
    use crate::db::service::message_queue_control_service as control;
    let db = test_helpers::fresh_in_memory_db().await;
    let folder = test_helpers::seed_folder(&db, "/tmp/queue-continue-flow").await;
    let cid = test_helpers::seed_conversation(&db, folder, AgentType::Codex).await;
    let owner = cid.to_string();
    message_queue_service::save(&db.conn, &owner, vec![queue_input("a", "A"), queue_input("b", "B")]).await.unwrap();
    let manager = ConnectionManager::new();
    let bus = Arc::new(crate::acp::InternalEventBus::new(Arc::new(crate::acp::EventBusMetrics::default())));
    let emitter = EventEmitter::web_only(Arc::new(crate::web::event_bridge::WebEventBroadcaster::new()), bus.clone());
    let mut commands = manager.insert_test_connection_live("continue-flow", AgentType::Codex, None, emitter.clone()).await;
    let state = manager.get_state("continue-flow").await.unwrap();
    state.write().await.conversation_id = Some(cid);
    let lifecycle = tokio::spawn(crate::acp::lifecycle::lifecycle_subscriber_task(db.conn.clone(), manager.clone_ref(), bus, None));
    assert!(matches!(try_drain_owner(&db.conn, &manager, &emitter, Path::new("/tmp"), &owner).await, DrainOutcome::Sent));
    assert!(matches!(commands.recv().await, Some(ConnectionCommand::Prompt { .. })));
    crate::web::event_bridge::emit_with_state(&state, &emitter, AcpEvent::TurnComplete { session_id:"s".into(), stop_reason:"refusal".into(), agent_type:"codex".into() }).await;
    tokio::time::timeout(Duration::from_secs(3), async {
        while control::get_state(&db.conn, &owner).await.unwrap().status != "paused" { tokio::time::sleep(Duration::from_millis(5)).await; }
    }).await.unwrap();
    let paused = control::get_state(&db.conn, &owner).await.unwrap();
    assert!(matches!(try_drain_owner(&db.conn, &manager, &emitter, Path::new("/tmp"), &owner).await, DrainOutcome::Waiting));
    assert!(commands.try_recv().is_err());
    control::request_continue(&db.conn, &owner, paused.failed_attempt_id.as_deref().unwrap()).await.unwrap();
    assert!(matches!(try_drain_owner(&db.conn, &manager, &emitter, Path::new("/tmp"), &owner).await, DrainOutcome::Sent));
    let Some(ConnectionCommand::Prompt { blocks, .. }) = commands.recv().await else { panic!("continuation expected") };
    assert!(matches!(&blocks[0], PromptInputBlock::Text { text } if text.starts_with("继续之前因错误中断的任务")));
    assert_eq!(control::get_state(&db.conn, &owner).await.unwrap().status, "resuming");
    assert_eq!(message_queue_service::list(&db.conn, &owner).await.unwrap()[0].id, "b");
    crate::web::event_bridge::emit_with_state(&state, &emitter, AcpEvent::TurnComplete { session_id:"s".into(), stop_reason:"end_turn".into(), agent_type:"codex".into() }).await;
    tokio::time::timeout(Duration::from_secs(3), async {
        while control::get_state(&db.conn, &owner).await.unwrap().status != "open" { tokio::time::sleep(Duration::from_millis(5)).await; }
    }).await.unwrap();
    assert!(matches!(try_drain_owner(&db.conn, &manager, &emitter, Path::new("/tmp"), &owner).await, DrainOutcome::Sent));
    let Some(ConnectionCommand::Prompt { blocks, .. }) = commands.recv().await else { panic!("B expected") };
    assert_eq!(blocks, [PromptInputBlock::Text { text:"B".into() }]);
    lifecycle.abort();
}

#[test]
fn only_one_process_can_recover_and_drain_the_same_database() {
    let dir = tempfile::tempdir().unwrap();
    let first = elect(dir.path()).unwrap().unwrap();
    assert!(elect(dir.path()).unwrap().is_none());
    drop(first);
    assert!(elect(dir.path()).unwrap().is_some());
}

#[test]
fn parse_draft_json_reads_blocks() {
    let blocks =
        parse_draft_json(r#"{"blocks":[{"type":"text","text":"hi"}],"displayText":"hi"}"#).unwrap();
    assert_eq!(blocks, [PromptInputBlock::Text { text: "hi".into() }]);
}

#[test]
fn parse_draft_json_defaults_missing_display_text() {
    assert!(parse_draft_json(r#"{"blocks":[{"type":"text","text":"hi"}]}"#).is_ok());
}

#[test]
fn parse_draft_json_rejects_garbage_and_empty() {
    assert!(parse_draft_json("not json").is_err());
    assert!(parse_draft_json(r#"{"displayText":"x"}"#).is_err());
    assert!(parse_draft_json(r#"{"blocks":[]}"#).is_err());
}

#[test]
fn parse_draft_json_reads_image_and_resource_link_blocks() {
    let blocks = parse_draft_json(r#"{"blocks":[{"type":"image","data":"QUJD","mime_type":"image/png","uri":"/tmp/a.png"},{"type":"resource_link","uri":"https://f/a.txt","name":"a.txt"}]}"#).unwrap();
    assert_eq!(blocks.len(), 2);
}

#[test]
fn backoff_delay_doubles_and_caps() {
    assert_eq!(backoff_delay_ms(0), 1_000);
    assert_eq!(backoff_delay_ms(1), 1_000);
    assert_eq!(backoff_delay_ms(3), 4_000);
    assert_eq!(backoff_delay_ms(30), 60_000);
    assert_eq!(backoff_delay_ms(u32::MAX), 60_000);
}
