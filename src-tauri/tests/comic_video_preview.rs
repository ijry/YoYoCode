//! Comic video preview grant route: authenticated issuance, public
//! range-serving of one bounded artifact, and the denials in between.
//!
//! The grant URL is the one surface a `<video>` element can reach without
//! codeg's bearer token, so this suite pins the authorization boundary on the
//! wire: no token for issuance, opaque token for bytes, per-request
//! re-validation of task ownership, and 404 (never a reason leak) on denial.

use std::sync::Arc;

use axum_test::TestServer;
use chrono::Utc;
use codeg_lib::app_state::AppState;
use codeg_lib::comic::store::StoryStore;
use codeg_lib::db::entities::comic_generation_task;
use codeg_lib::db::test_helpers::{fresh_in_memory_db, seed_folder};
use codeg_lib::web::router::build_router;
use codeg_lib::web::shutdown::ShutdownSignal;
use sea_orm::{ActiveModelTrait, ActiveValue::NotSet, DatabaseConnection, Set};
use serde_json::{json, Value};

const TEST_TOKEN: &str = "integration-test-token";

/// The router's state owns the in-memory DB, and the tests seed the SAME
/// handle — a second `fresh_in_memory_db()` would be a different database
/// the router can never see.
async fn build_server() -> (
    TestServer,
    tempfile::TempDir,
    tempfile::TempDir,
    codeg_lib::db::AppDatabase,
) {
    let data_dir = tempfile::tempdir().expect("data dir");
    let static_dir = tempfile::tempdir().expect("static dir");
    let db = fresh_in_memory_db().await;
    let state = Arc::new(AppState::new_for_test(
        codeg_lib::db::AppDatabase {
            conn: db.conn.clone(),
        },
        data_dir.path().to_path_buf(),
    ));
    let shutdown = Arc::new(ShutdownSignal::new());
    let router = build_router(
        state,
        TEST_TOKEN.to_string(),
        static_dir.path().to_path_buf(),
        shutdown,
    );
    let server = TestServer::new(router).expect("test server");
    (server, data_dir, static_dir, db)
}

async fn seed_succeeded_video_task(
    conn: &DatabaseConnection,
    folder_id: i32,
    project_dir: &std::path::Path,
) -> i32 {
    let store = StoryStore::init(project_dir, "Preview").unwrap();
    let now = Utc::now();
    let inserted = comic_generation_task::ActiveModel {
        id: NotSet,
        folder_id: Set(folder_id),
        kind: Set("video".into()),
        status: Set("succeeded".into()),
        provider_id: Set(None),
        provider_name: Set(Some("Ark".into())),
        model: Set(Some("seedance".into())),
        request_json: Set("{}".into()),
        result_json: Set("{}".into()),
        artifact_path: Set(None),
        target_kind: Set(Some("shot".into())),
        target_id: Set(Some("episodes/ep001/shots/shot-0001".into())),
        error_kind: Set(None),
        error_message: Set(None),
        candidates_json: Set("[]".into()),
        created_at: Set(now),
        updated_at: Set(now),
    }
    .insert(conn)
    .await
    .unwrap();
    // The artifact name carries the row's own task number — the same
    // ownership rule the preview route enforces on every request.
    let artifact = format!(
        "episodes/ep001/shots/shot-0001/video/task-{}.mp4",
        inserted.id
    );
    store.write_artifact(&artifact, &vec![b'A'; 64]).unwrap();
    let mut updated: comic_generation_task::ActiveModel = inserted.into();
    updated.artifact_path = Set(Some(artifact));
    updated.update(conn).await.unwrap().id
}

#[tokio::test]
async fn issuance_requires_the_bearer_token_and_a_matching_task() {
    let (server, data_dir, _static, db) = build_server().await;
    let folder_id = seed_folder(&db, &data_dir.path().to_string_lossy()).await;
    let task_id = seed_succeeded_video_task(&db.conn, folder_id, data_dir.path()).await;

    let denied = server
        .post("/api/comic_request_video_preview")
        .json(&json!({ "folderId": folder_id, "taskId": task_id }))
        .await;
    assert_eq!(denied.status_code(), 401);

    // A task from another folder must not mint a grant, even with the token.
    let foreign = server
        .post("/api/comic_request_video_preview")
        .add_header("authorization", format!("Bearer {TEST_TOKEN}"))
        .json(&json!({ "folderId": folder_id + 1, "taskId": task_id }))
        .await;
    assert_eq!(foreign.status_code(), 404);

    let issued = server
        .post("/api/comic_request_video_preview")
        .add_header("authorization", format!("Bearer {TEST_TOKEN}"))
        .json(&json!({ "folderId": folder_id, "taskId": task_id }))
        .await;
    assert_eq!(issued.status_code(), 200);
    let body: Value = issued.json();
    assert!(body["token"].as_str().is_some_and(|t| t.len() >= 32));
}

#[tokio::test]
async fn grant_url_serves_bounded_ranges_without_a_token() {
    let (server, data_dir, _static, db) = build_server().await;
    let folder_id = seed_folder(&db, &data_dir.path().to_string_lossy()).await;
    let task_id = seed_succeeded_video_task(&db.conn, folder_id, data_dir.path()).await;
    let issued = server
        .post("/api/comic_request_video_preview")
        .add_header("authorization", format!("Bearer {TEST_TOKEN}"))
        .json(&json!({ "folderId": folder_id, "taskId": task_id }))
        .await;
    let token = issued.json::<Value>()["token"]
        .as_str()
        .expect("issued token")
        .to_string();

    // No auth header, no Range: still bounded, still marked 206.
    let full = server
        .get(&format!("/api/comic_video_preview/{token}"))
        .await;
    assert_eq!(full.status_code(), 206);
    assert_eq!(full.header("content-type"), "video/mp4");
    assert_eq!(full.header("content-range"), "bytes 0-63/64");
    assert_eq!(full.header("accept-ranges"), "bytes");
    assert_eq!(full.text().len(), 64);

    let segment = server
        .get(&format!("/api/comic_video_preview/{token}"))
        .add_header("range", "bytes=10-19")
        .await;
    assert_eq!(segment.status_code(), 206);
    assert_eq!(segment.header("content-range"), "bytes 10-19/64");
    assert_eq!(segment.text().len(), 10);

    let unsatisfiable = server
        .get(&format!("/api/comic_video_preview/{token}"))
        .add_header("range", "bytes=999-")
        .await;
    assert_eq!(unsatisfiable.status_code(), 416);

    let garbage = server.get("/api/comic_video_preview/not-a-grant").await;
    assert_eq!(garbage.status_code(), 404);
}
