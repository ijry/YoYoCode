//! Real stdio companion → authenticated local transport → comic resource store.
//! Uses a temporary project and in-memory DB; no agent CLI or media vendor runs.

#![cfg(all(feature = "server-bin", feature = "mcp-bin"))]

use std::collections::BTreeMap;
use std::path::PathBuf;
use std::sync::Arc;
use std::time::Duration;

use async_trait::async_trait;
use codeg_lib::acp::delegation::broker::{ConversationDepthLookup, DelegationBroker};
use codeg_lib::acp::delegation::listener::{
    DelegationListener, ParentSessionLookup, TokenEntry, TokenRegistry,
};
use codeg_lib::acp::delegation::spawner::{
    ConnectionSpawner, DelegationLink, ResumedSpawn, SpawnerError,
};
use codeg_lib::acp::delegation::types::DelegationError;
use codeg_lib::acp::question::{QuestionSpec, RegisteredQuestion, SessionQuestionAccess};
use codeg_lib::comic::store::StoryStore;
use codeg_lib::commands::comic::McpComicTools;
use codeg_lib::db::migration::Migrator;
use codeg_lib::models::AgentType;
use codeg_lib::web::event_bridge::EventEmitter;
use sea_orm_migration::MigratorTrait;
use serde_json::{json, Value};
use tokio::io::{AsyncBufReadExt, AsyncWriteExt, BufReader, Lines};
use tokio::process::{Child, ChildStdin, ChildStdout, Command};

struct NoSessions;

#[async_trait]
impl ConversationDepthLookup for NoSessions {
    async fn parent_of(&self, _: i32) -> Result<Option<i32>, DelegationError> {
        Ok(None)
    }
}

#[async_trait]
impl ParentSessionLookup for NoSessions {
    async fn current_conversation_id(&self, _: &str) -> Option<i32> {
        None
    }
}

#[async_trait]
impl ConnectionSpawner for NoSessions {
    async fn spawn(
        &self,
        _: &str,
        _: AgentType,
        _: Option<String>,
        _: Option<String>,
        _: BTreeMap<String, String>,
    ) -> Result<String, SpawnerError> {
        Err(SpawnerError::Spawn(
            "no agent in comic resource test".into(),
        ))
    }

    async fn send_prompt_linked_for_delegation(
        &self,
        _: &str,
        _: String,
        _: DelegationLink,
        _: Option<String>,
    ) -> Result<i32, SpawnerError> {
        Err(SpawnerError::Send("no agent in comic resource test".into()))
    }

    async fn spawn_for_resume(
        &self,
        _: &str,
        _: AgentType,
        _: Option<String>,
        _: &str,
        _: Option<String>,
        _: BTreeMap<String, String>,
    ) -> Result<ResumedSpawn, SpawnerError> {
        Err(SpawnerError::Spawn(
            "no agent in comic resource test".into(),
        ))
    }

    async fn send_resume_prompt(
        &self,
        _: &str,
        _: String,
        _: i32,
        _: i32,
        _: Option<String>,
    ) -> Result<(), SpawnerError> {
        Err(SpawnerError::Send("no agent in comic resource test".into()))
    }

    async fn has_live_connection_for_conversation(&self, _: i32) -> bool {
        false
    }

    async fn cancel(&self, _: &str) -> Result<(), SpawnerError> {
        Ok(())
    }

    async fn disconnect(&self, _: &str) -> Result<(), SpawnerError> {
        Ok(())
    }
}

struct NoAuxTools;

#[async_trait]
impl codeg_lib::acp::feedback::SessionFeedbackAccess for NoAuxTools {
    async fn read_pending_feedback(
        &self,
        _: &str,
    ) -> Vec<codeg_lib::acp::feedback::PendingFeedback> {
        Vec::new()
    }

    async fn commit_feedback_delivered(&self, _: &str, _: Vec<String>) {}
}

#[async_trait]
impl SessionQuestionAccess for NoAuxTools {
    async fn register_question(&self, _: &str, _: Vec<QuestionSpec>) -> Option<RegisteredQuestion> {
        None
    }

    async fn cancel_question(&self, _: &str, _: &str) {}

    async fn cancel_questions_by_parent(&self, _: &str) {}
}

#[async_trait]
impl codeg_lib::acp::session_info::SessionInfoAccess for NoAuxTools {
    async fn resolve(&self, id: i32, _: u32) -> codeg_lib::acp::session_info::SessionInfo {
        codeg_lib::acp::session_info::SessionInfo::not_found(id)
    }
}

#[async_trait]
impl codeg_lib::acp::work_task_tools::WorkTaskToolAccess for NoAuxTools {
    async fn report_progress(
        &self,
        _: &str,
        _: &str,
    ) -> codeg_lib::acp::work_task_tools::TaskReportAck {
        codeg_lib::acp::work_task_tools::TaskReportAck::rejected("no task engine")
    }

    async fn complete(
        &self,
        _: &str,
        _: &str,
        _: Option<&str>,
    ) -> codeg_lib::acp::work_task_tools::TaskReportAck {
        codeg_lib::acp::work_task_tools::TaskReportAck::rejected("no task engine")
    }
}

#[async_trait]
impl codeg_lib::acp::chat_authoring::ChatAuthoringAccess for NoAuxTools {
    async fn create_automation(
        &self,
        _: codeg_lib::acp::chat_authoring::AuthoringContext,
        _: codeg_lib::acp::chat_authoring::NewAutomationSpec,
    ) -> codeg_lib::acp::chat_authoring::AuthoringOutcome {
        codeg_lib::acp::chat_authoring::AuthoringOutcome::rejected("automation", "not part of test")
    }

    async fn create_work_task(
        &self,
        _: codeg_lib::acp::chat_authoring::AuthoringContext,
        _: codeg_lib::acp::chat_authoring::NewWorkTaskSpec,
    ) -> codeg_lib::acp::chat_authoring::AuthoringOutcome {
        codeg_lib::acp::chat_authoring::AuthoringOutcome::rejected("task", "not part of test")
    }
}

struct Companion {
    child: Child,
    input: ChildStdin,
    output: Lines<BufReader<ChildStdout>>,
    next_id: u32,
}

impl Companion {
    fn spawn(socket: &std::path::Path, parent: &str, features: &str) -> Self {
        let mut child = Command::new(env!("CARGO_BIN_EXE_yoyocode-mcp"))
            .args([
                "--parent-connection-id",
                parent,
                "--token",
                "comic-test-token",
                "--features",
                features,
            ])
            .arg("--socket-path")
            .arg(socket)
            .stdin(std::process::Stdio::piped())
            .stdout(std::process::Stdio::piped())
            .stderr(std::process::Stdio::inherit())
            .kill_on_drop(true)
            .spawn()
            .expect("spawn the real yoyocode-mcp binary");
        let input = child.stdin.take().unwrap();
        let output = BufReader::new(child.stdout.take().unwrap()).lines();
        Self {
            child,
            input,
            output,
            next_id: 0,
        }
    }

    async fn request(&mut self, method: &str, params: Value) -> Value {
        self.next_id += 1;
        let id = self.next_id;
        let mut line = serde_json::to_vec(
            &json!({ "jsonrpc": "2.0", "id": id, "method": method, "params": params }),
        )
        .unwrap();
        line.push(b'\n');
        self.input.write_all(&line).await.unwrap();
        self.input.flush().await.unwrap();
        tokio::time::timeout(Duration::from_secs(15), async {
            loop {
                let line = self
                    .output
                    .next_line()
                    .await
                    .unwrap()
                    .expect("companion response");
                let reply: Value = serde_json::from_str(&line).expect("stdout is JSON-RPC only");
                if reply.get("id") == Some(&json!(id)) {
                    return reply;
                }
            }
        })
        .await
        .expect("bounded MCP response wait")
    }

    async fn call(&mut self, name: &str, args: Value) -> Value {
        let reply = self
            .request("tools/call", json!({ "name": name, "arguments": args }))
            .await;
        assert!(reply.get("error").is_none(), "{reply}");
        reply["result"]["structuredContent"].clone()
    }
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn comic_mcp_stdio_resource_round_trip_and_scope() {
    let project = tempfile::tempdir().unwrap();
    let store = StoryStore::init(project.path(), "MCP fixture").unwrap();
    let conn = sea_orm::Database::connect("sqlite::memory:").await.unwrap();
    Migrator::up(&conn, None).await.unwrap();
    let folder_id = codeg_lib::db::service::folder_service::add_folder(
        &conn,
        &project.path().to_string_lossy(),
    )
    .await
    .unwrap()
    .id;
    let tokens = Arc::new(TokenRegistry::default());
    tokens
        .register(
            "comic-test-token".into(),
            TokenEntry {
                parent_connection_id: "comic-parent".into(),
                working_dir: project.path().into(),
            },
        )
        .await;
    let aux = Arc::new(NoAuxTools);
    let listener = DelegationListener::new(
        Arc::new(DelegationBroker::new(
            Arc::new(NoSessions),
            Arc::new(NoSessions),
        )),
        tokens,
        Arc::new(NoSessions),
        aux.clone(),
        aux.clone(),
        aux.clone(),
        aux.clone(),
        aux.clone(),
        Arc::new(codeg_lib::acp::browser_tools::NoBrowserTabs),
        Arc::new(codeg_lib::acp::computer_tools::NoComputerDesktop),
        Arc::new(codeg_lib::acp::result_tool::NoResultTool),
    )
    .with_comic(Arc::new(McpComicTools::new(
        conn.clone(),
        EventEmitter::Noop,
        aux,
    )));
    #[cfg(windows)]
    let socket = PathBuf::from(format!(r"\\.\pipe\comic-mcp-test-{}", uuid::Uuid::new_v4()));
    #[cfg(unix)]
    let socket = project.path().join("comic.sock");
    let bound = DelegationListener::bind(&socket).await.unwrap();
    let socket_for_listener = socket.clone();
    let listener_task = tokio::spawn(listener.accept_loop(bound, socket_for_listener));
    let mut companion = Companion::spawn(&socket, "comic-parent", "comic");
    let init = companion.request("initialize", json!({ "protocolVersion": "2024-11-05", "capabilities": {}, "clientInfo": { "name": "comic-test", "version": "1" } })).await;
    assert!(init.get("error").is_none(), "{init}");
    let tools = companion.request("tools/list", json!({})).await;
    let names: Vec<&str> = tools["result"]["tools"]
        .as_array()
        .unwrap()
        .iter()
        .map(|tool| tool["name"].as_str().unwrap())
        .collect();
    assert!(names.contains(&"comic_get_project"));
    assert!(names.contains(&"comic_present_candidates"));
    assert!(names.contains(&"comic_compose_episode"));
    assert!(names.contains(&"comic_check_compose"));
    assert!(names.contains(&"comic_request_speech"));
    assert!(names.contains(&"comic_set_shot_audio"));
    assert!(names.iter().all(|name| name.starts_with("comic_")));
    let overview = companion.call("comic_get_project", json!({})).await;
    assert_eq!(overview["ok"], true);
    assert_eq!(overview["data"]["folderId"], folder_id);
    let written = companion
        .call(
            "comic_write_script",
            json!({ "episode": "ep001", "script": "An original fixture script." }),
        )
        .await;
    assert_eq!(written["ok"], true, "{written}");
    assert_eq!(
        store.read_script("ep001").unwrap(),
        "An original fixture script."
    );
    let invalid = companion.call("comic_upsert_storyboard", json!({ "episode": "ep001", "shots": [{ "id": "shot-0001", "description": "Frame", "refs": { "characters": [{ "slug": "missing" }] } }] })).await;
    assert_eq!(invalid["ok"], false);
    assert!(store.read_storyboard("ep001").is_err());
    let board = companion.call("comic_upsert_storyboard", json!({ "episode": "ep001", "shots": [{ "id": "shot-0001", "description": "Frame", "duration_secs": 1.0 }] })).await;
    assert_eq!(board["ok"], true, "{board}");
    assert_eq!(store.read_storyboard("ep001").unwrap().shots.len(), 1);
    let relative = "episodes/ep001/shots/shot-0001/candidates/task-1-01.png";
    let mut image = std::io::Cursor::new(Vec::new());
    image::DynamicImage::new_rgb8(4, 4)
        .write_to(&mut image, image::ImageFormat::Png)
        .unwrap();
    store.write_artifact(relative, &image.into_inner()).unwrap();
    let bound = companion
        .call(
            "comic_set_shot_image",
            json!({ "episode": "ep001", "shot": "shot-0001", "candidate": relative }),
        )
        .await;
    assert_eq!(bound["ok"], true, "{bound}");
    let voice_listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let voice_address = voice_listener.local_addr().unwrap();
    let audio_dir = tempfile::tempdir().unwrap();
    let audio_file = audio_dir.path().join("speech.wav");
    let generated = Command::new("ffmpeg")
        .args([
            "-nostdin",
            "-v",
            "error",
            "-f",
            "lavfi",
            "-i",
            "sine=frequency=440:sample_rate=16000",
            "-t",
            "0.75",
            "-c:a",
            "pcm_s16le",
        ])
        .arg(&audio_file)
        .output()
        .await
        .unwrap();
    assert!(generated.status.success());
    let audio_bytes = tokio::fs::read(&audio_file).await.unwrap();
    let voice_server = tokio::spawn(async move {
        use tokio::io::AsyncReadExt;
        let (mut socket, _) = voice_listener.accept().await.unwrap();
        let mut request = Vec::new();
        let body = loop {
            let mut bytes = [0_u8; 4096];
            let count = socket.read(&mut bytes).await.unwrap();
            assert!(count > 0);
            request.extend_from_slice(&bytes[..count]);
            if let Some(end) = request.windows(4).position(|bytes| bytes == b"\r\n\r\n") {
                let headers = String::from_utf8_lossy(&request[..end]);
                let length: usize = headers
                    .lines()
                    .find_map(|line| {
                        let (key, value) = line.split_once(':')?;
                        key.eq_ignore_ascii_case("content-length")
                            .then(|| value.trim().parse().unwrap())
                    })
                    .unwrap();
                if request.len() >= end + 4 + length {
                    break request[end + 4..end + 4 + length].to_vec();
                }
            }
        };
        socket.write_all(format!("HTTP/1.1 200 OK\r\nContent-Type: audio/wav\r\nContent-Length: {}\r\nConnection: close\r\n\r\n", audio_bytes.len()).as_bytes()).await.unwrap();
        socket.write_all(&audio_bytes).await.unwrap();
        serde_json::from_slice::<Value>(&body).unwrap()
    });
    codeg_lib::db::service::media_provider_service::create(
        &conn,
        codeg_lib::db::service::media_provider_service::NewMediaProvider {
            kind: "tts".into(),
            name: "Local voice fixture".into(),
            api_url: format!("http://{voice_address}/v1"),
            api_key: "test-key".into(),
            model: "tts-fixture".into(),
            extra_json: json!({"voice":"alloy"}).to_string(),
            is_default: true,
        },
    )
    .await
    .unwrap();
    let speech = companion.call("comic_request_speech", json!({"episode":"ep001","shot":"shot-0001","text":"Approved dialogue", "voice":"nova", "response_format":"wav", "speed":1.1})).await;
    assert_eq!(speech["ok"], true, "{speech}");
    let speech_id = speech["data"]["taskId"].as_i64().unwrap();
    let speech_done = companion
        .call(
            "comic_check_generation",
            json!({"task_id":speech_id,"wait_ms":60000}),
        )
        .await;
    assert_eq!(speech_done["data"]["status"], "succeeded", "{speech_done}");
    assert!(speech_done["data"]["candidates"]
        .as_array()
        .unwrap()
        .is_empty());
    let voice_request = voice_server.await.unwrap();
    assert_eq!(voice_request["voice"], "nova");
    assert_eq!(voice_request["speed"], 1.1);
    assert_eq!(voice_request["response_format"], "wav");
    let bound_voice = companion
        .call(
            "comic_set_shot_audio",
            json!({"episode":"ep001","shot":"shot-0001","task_id":speech_id}),
        )
        .await;
    assert_eq!(bound_voice["ok"], true, "{bound_voice}");
    assert_eq!(
        store.read_storyboard("ep001").unwrap().shots[0]
            .audio
            .as_ref()
            .unwrap()
            .task_id,
        speech_id as i32
    );
    let queued = companion.call("comic_compose_episode", json!({ "episode": "ep001", "width": 128, "height": 192, "ken_burns": false, "burn_subtitles": false })).await;
    assert_eq!(queued["ok"], true, "{queued}");
    let id = queued["data"]["taskId"].as_i64().unwrap();
    let completed = companion
        .call(
            "comic_check_compose",
            json!({ "task_id": id, "wait_ms": 60000 }),
        )
        .await;
    assert_eq!(completed["ok"], true, "{completed}");
    assert_eq!(completed["data"]["status"], "succeeded", "{completed}");
    assert_eq!(
        completed["data"]["artifactPath"],
        format!("episodes/ep001/export/task-{id}.mp4")
    );
    assert!(store
        .resolve(completed["data"]["artifactPath"].as_str().unwrap())
        .unwrap()
        .is_file());
    companion.child.kill().await.unwrap();
    companion.child.wait().await.unwrap();

    let mut wrong_parent = Companion::spawn(&socket, "other-parent", "comic");
    let denied = wrong_parent
        .call(
            "comic_write_script",
            json!({ "episode": "ep001", "script": "Unauthorized replacement" }),
        )
        .await;
    assert_eq!(denied["ok"], false);
    assert_eq!(
        store.read_script("ep001").unwrap(),
        "An original fixture script."
    );
    wrong_parent.child.kill().await.unwrap();
    wrong_parent.child.wait().await.unwrap();

    let mut disabled = Companion::spawn(&socket, "comic-parent", "feedback");
    let tools = disabled.request("tools/list", json!({})).await;
    assert!(tools["result"]["tools"]
        .as_array()
        .unwrap()
        .iter()
        .all(|tool| !tool["name"].as_str().unwrap().starts_with("comic_")));
    disabled.child.kill().await.unwrap();
    disabled.child.wait().await.unwrap();
    listener_task.abort();
}
