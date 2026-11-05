//! Conversation-driven speech generation and explicit audio binding.

use std::path::Path;

use sea_orm::DatabaseConnection;
use serde::Deserialize;
use serde_json::Value;
use tokio_util::sync::CancellationToken;

use crate::web::event_bridge::EventEmitter;

use super::provider::{GenerationError, SpeechOptions};

#[derive(Clone, Debug, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct SpeechRequest {
    pub episode: String,
    pub shot: String,
    pub text: Option<String>,
    #[serde(flatten)]
    pub speech: SpeechOptions,
    #[serde(alias = "provider_id")]
    pub provider_id: Option<i32>,
    #[serde(alias = "provider_name")]
    pub provider_name: Option<String>,
}

pub async fn request(
    conn: &DatabaseConnection,
    emitter: &EventEmitter,
    folder_id: i32,
    folder_path: &Path,
    params: SpeechRequest,
) -> Result<Value, GenerationError> {
    let store = super::store::StoryStore::open(folder_path)
        .map_err(|error| GenerationError::new("bad_request", error.message()))?;
    let board = store
        .read_storyboard(&params.episode)
        .map_err(|error| GenerationError::new("bad_request", error.message()))?;
    let shot = board
        .shots
        .iter()
        .find(|shot| shot.id == params.shot)
        .ok_or_else(|| GenerationError::new("bad_request", "speech shot is not in this episode"))?;
    let text = params.text.unwrap_or_else(|| {
        shot.dialogue
            .iter()
            .map(|line| line.text.trim())
            .filter(|line| !line.is_empty())
            .collect::<Vec<_>>()
            .join("\n")
    });
    if text.trim().is_empty() {
        return Err(GenerationError::new(
            "bad_request",
            "shot has no dialogue; provide speech text explicitly",
        ));
    }
    let target = format!("episodes/{}/shots/{}", params.episode, params.shot);
    let request = super::provider::GenerationRequest {
        prompt: text,
        speech: Some(params.speech),
        ..Default::default()
    };
    let id = super::task::submit_generation(
        conn,
        emitter,
        folder_id,
        folder_path,
        super::provider::MediaKind::Tts,
        "shot",
        &target,
        request,
        params.provider_id,
        params.provider_name,
    )
    .await?;
    Ok(
        serde_json::json!({ "taskId":id,"kind":"tts","target":target,"hint":"Poll comic_check_generation; bind the completed speech with comic_set_shot_audio. Existing audio is unchanged until binding." }),
    )
}

#[allow(clippy::too_many_arguments)]
pub async fn bind(
    conn: &DatabaseConnection,
    emitter: &EventEmitter,
    folder_id: i32,
    folder_path: &Path,
    episode: &str,
    shot: &str,
    task_id: i32,
    cancel: &CancellationToken,
) -> Result<Value, GenerationError> {
    let view = super::task::check_task(conn, task_id, 0)
        .await
        .filter(|view| {
            view.folder_id == folder_id
                && view.kind == "tts"
                && view.status == "succeeded"
                && view.target_id.as_deref()
                    == Some(format!("episodes/{episode}/shots/{shot}").as_str())
        })
        .ok_or_else(|| {
            GenerationError::new(
                "bad_request",
                "completed speech task not found for this project/shot",
            )
        })?;
    let path = view
        .artifact_path
        .ok_or_else(|| GenerationError::new("bad_response", "speech task has no audio artifact"))?;
    if !path.starts_with(&format!("episodes/{episode}/shots/{shot}/audio/")) {
        return Err(GenerationError::new(
            "bad_request",
            "speech artifact does not belong to this shot",
        ));
    }
    let store = super::store::StoryStore::open(folder_path)
        .map_err(|error| GenerationError::new("bad_request", error.message()))?;
    let absolute = store
        .resolve(&path)
        .map_err(|error| GenerationError::new("bad_request", error.message()))?;
    let info = super::audio::inspect_path(&absolute, cancel).await?;
    if cancel.is_cancelled() {
        return Err(GenerationError::new(
            "cancelled",
            "speech binding cancelled",
        ));
    }
    store
        .bind_shot_audio(
            episode,
            shot,
            super::model::ShotAudio {
                path: path.clone(),
                duration_secs: info.duration_secs,
                task_id,
            },
        )
        .map_err(|error| GenerationError::new("internal", error.message()))?;
    crate::web::event_bridge::emit_event(
        emitter,
        crate::web::event_bridge::COMIC_CHANGED_EVENT,
        &crate::web::event_bridge::ComicChange::Upsert {
            folder_id,
            resource: "episode".into(),
            id: episode.into(),
        },
    );
    Ok(serde_json::json!({"bound":true,"audio":path,"durationSecs":info.duration_secs}))
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::comic::{
        audio,
        model::{EpisodeMeta, StoryShot},
        store::StoryStore,
    };
    use crate::db::{
        entities::comic_generation_task,
        test_helpers::{fresh_in_memory_db, seed_folder},
    };
    use chrono::Utc;
    use sea_orm::{ActiveModelTrait, ActiveValue::NotSet, Set};
    use serde_json::json;

    fn project(path: &Path) -> StoryStore {
        let store = StoryStore::init(path, "Speech fixture").unwrap();
        store
            .upsert_episode(&EpisodeMeta {
                id: "ep001".into(),
                ..Default::default()
            })
            .unwrap();
        store
            .replace_storyboard(
                "ep001",
                vec![StoryShot {
                    id: "shot-0001".into(),
                    ..Default::default()
                }],
            )
            .unwrap();
        store
    }

    async fn task(conn: &DatabaseConnection, folder_id: i32, path: &str) -> i32 {
        let now = Utc::now();
        comic_generation_task::ActiveModel {
            id: NotSet,
            folder_id: Set(folder_id),
            kind: Set("tts".into()),
            status: Set("succeeded".into()),
            provider_id: Set(None),
            provider_name: Set(Some("Voice provider".into())),
            model: Set(Some("voice-model".into())),
            request_json: Set("{}".into()),
            result_json: Set("{}".into()),
            artifact_path: Set(Some(path.into())),
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
        .unwrap()
        .id
    }

    #[tokio::test]
    async fn binding_speech_keeps_the_selected_image_and_verifies_task_ownership() {
        let db = fresh_in_memory_db().await;
        let dir = tempfile::tempdir().unwrap();
        let store = project(dir.path());
        let folder = seed_folder(&db, &dir.path().to_string_lossy()).await;
        let path = "episodes/ep001/shots/shot-0001/audio/task-7.wav";
        store.write_artifact(path, &audio::wav(0.6)).unwrap();
        let mut board = store.read_storyboard("ep001").unwrap();
        board.shots[0].selected =
            Some("episodes/ep001/shots/shot-0001/candidates/task-1-01.png".into());
        store.write_storyboard("ep001", &board).unwrap();
        let id = task(&db.conn, folder, path).await;
        assert!(bind(
            &db.conn,
            &EventEmitter::Noop,
            folder + 1,
            dir.path(),
            "ep001",
            "shot-0001",
            id,
            &CancellationToken::new()
        )
        .await
        .is_err());
        bind(
            &db.conn,
            &EventEmitter::Noop,
            folder,
            dir.path(),
            "ep001",
            "shot-0001",
            id,
            &CancellationToken::new(),
        )
        .await
        .unwrap();
        let board = store.read_storyboard("ep001").unwrap();
        assert!(board.shots[0]
            .selected
            .as_ref()
            .unwrap()
            .ends_with("task-1-01.png"));
        let value = serde_json::to_value(board).unwrap();
        assert_eq!(value["shots"][0]["audio"]["path"], path);
        assert_eq!(value["shots"][0]["audio"]["task_id"], id);
        assert!(
            (value["shots"][0]["audio"]["duration_secs"]
                .as_f64()
                .unwrap()
                - 0.6)
                .abs()
                < 0.02
        );
    }

    #[tokio::test]
    async fn empty_dialogue_and_wrong_shot_refuse_before_provider_submission() {
        let db = fresh_in_memory_db().await;
        let dir = tempfile::tempdir().unwrap();
        project(dir.path());
        let folder = seed_folder(&db, &dir.path().to_string_lossy()).await;
        let params: SpeechRequest =
            serde_json::from_value(json!({"episode":"ep001","shot":"shot-0001","voice":"nova"}))
                .unwrap();
        let err = request(&db.conn, &EventEmitter::Noop, folder, dir.path(), params)
            .await
            .unwrap_err();
        assert!(err.message.contains("dialogue"));
    }
}
