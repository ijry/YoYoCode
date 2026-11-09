//! The single write entry point for a project's `story/` tree.
//!
//! MCP comic tools, Tauri commands, and web handlers all mutate resources
//! through [`StoryStore`], which enforces three invariants:
//!
//! 1. **Jail** — every path resolves through [`crate::comic::paths::
//!    resolve_in_story`]; nothing outside `story/` is touched.
//! 2. **Serialization** — one write at a time per project folder (a global
//!    registry hands out one mutex per canonical folder path), so a session's
//!    MCP writes, another session's writes, and workbench direct edits cannot
//!    interleave.
//! 3. **Atomicity** — documents are written to a sibling temp file and
//!    renamed over the target, so a reader (the workbench, an agent's own
//!    file tools) sees either the old or the new document, never a torn one.

use std::collections::{HashMap, HashSet};
use std::path::{Path, PathBuf};
use std::sync::Arc;
use std::sync::{Mutex, OnceLock};

use serde::de::DeserializeOwned;
use serde::Serialize;

use super::model::{
    AssetSummary, ChapterIndex, CharacterCard, EpisodeMeta, EpisodeStatus, EpisodeSummary,
    NextAction, ProjectOverview, PropCard, SceneCard, SeriesConfig, ShotMeta, ShotState,
    SourceMeta, StoryShot, Storyboard,
};
use super::paths::{is_comic_project, resolve_in_story, story_root, validate_slug};

/// Serialization error type — value-level, cheap to convert into tool
/// rejections (`acp::comic_tools`) and HTTP 4xx/5xx bodies alike.
#[derive(Debug, thiserror::Error)]
pub enum StoreError {
    #[error("not a comic project (no story/ directory): {0}")]
    NotComicProject(String),
    #[error("{0}")]
    Invalid(String),
    #[error("{0}")]
    Conflict(String),
    #[error("io: {0}")]
    Io(#[from] std::io::Error),
    #[error("json: {0}")]
    Json(#[from] serde_json::Error),
}

impl StoreError {
    pub fn message(&self) -> String {
        match self {
            StoreError::NotComicProject(m) | StoreError::Invalid(m) | StoreError::Conflict(m) => {
                m.clone()
            }
            StoreError::Io(e) => e.to_string(),
            StoreError::Json(e) => e.to_string(),
        }
    }
}

type FolderLock = Arc<Mutex<()>>;

fn folder_lock(folder_path: &Path) -> Arc<Mutex<()>> {
    static LOCKS: OnceLock<Mutex<HashMap<PathBuf, FolderLock>>> = OnceLock::new();
    let key = folder_path
        .canonicalize()
        .unwrap_or_else(|_| folder_path.to_path_buf());
    let registry = LOCKS.get_or_init(|| Mutex::new(HashMap::new()));
    let mut map = registry.lock().expect("folder lock registry poisoned");
    map.entry(key).or_default().clone()
}

pub struct StoryStore {
    folder_path: PathBuf,
    root: PathBuf,
    _lock: Arc<Mutex<()>>,
}

impl StoryStore {
    /// Open the story tree of a comic project. Fails when the folder is not
    /// one (`story/` missing) — creation is explicit via [`StoryStore::init`].
    pub fn open(folder_path: &Path) -> Result<Self, StoreError> {
        let root = story_root(folder_path);
        if !root.is_dir() {
            return Err(StoreError::NotComicProject(
                folder_path.display().to_string(),
            ));
        }
        Ok(Self {
            folder_path: folder_path.to_path_buf(),
            root,
            _lock: folder_lock(folder_path),
        })
    }

    /// Whether the folder already is a comic project.
    pub fn exists(folder_path: &Path) -> bool {
        is_comic_project(folder_path)
    }

    /// Create the `story/` skeleton for a new comic project. Idempotent:
    /// existing files are left untouched.
    pub fn init(folder_path: &Path, title: &str) -> Result<Self, StoreError> {
        // Canonicalize the same existing folder for init and subsequent opens,
        // including when init creates the project directory itself.
        std::fs::create_dir_all(folder_path)?;
        let store = Self {
            folder_path: folder_path.to_path_buf(),
            root: story_root(folder_path),
            _lock: folder_lock(folder_path),
        };
        {
            let _guard = store.write_guard();
            // Validate the whole skeleton before writing through any existing
            // directory: init must respect the same symlink/reparse-point jail.
            let series_path = store.resolve("series.json")?;
            let dirs = [
                "bible",
                "source",
                "episodes",
                "characters",
                "scenes",
                "props",
            ]
            .iter()
            .map(|dir| store.resolve(dir))
            .collect::<Result<Vec<_>, _>>()?;
            for dir in dirs {
                std::fs::create_dir_all(dir)?;
            }
            if !series_path.try_exists()? {
                let config = SeriesConfig::with_defaults(title.to_string());
                store.write_json("series.json", &config)?;
            }
        }
        Ok(store)
    }

    pub fn root(&self) -> &Path {
        &self.root
    }

    pub fn folder_path(&self) -> &Path {
        &self.folder_path
    }

    /// Hold for the duration of every mutating operation. All write methods
    /// start with `let _guard = self.write_guard();` — reads stay lock-free
    /// (atomic rename makes each document old-or-new, never torn).
    fn write_guard(&self) -> std::sync::MutexGuard<'_, ()> {
        self._lock.lock().expect("folder story lock poisoned")
    }

    /// Resolve a story-relative reference inside this project.
    pub fn resolve(&self, rel: &str) -> Result<PathBuf, StoreError> {
        resolve_in_story(&self.folder_path, rel).map_err(StoreError::Invalid)
    }

    /// Write a generated artifact (candidate image, clip, audio) into the
    /// story tree. Directories are created as needed; the write is atomic.
    pub fn write_artifact(&self, rel: &str, bytes: &[u8]) -> Result<(), StoreError> {
        let _guard = self.write_guard();
        let path = self.resolve(rel)?;
        if let Some(parent) = path.parent() {
            std::fs::create_dir_all(parent)?;
        }
        write_bytes_atomic(&path, bytes)
            .map_err(|e| StoreError::Invalid(format!("write artifact {rel}: {e}")))
    }

    /// Publish a large generated file without loading it into memory or replacing history.
    pub fn copy_artifact(&self, rel: &str, source: &Path) -> Result<(), StoreError> {
        let _guard = self.write_guard();
        let path = self.resolve(rel)?;
        let parent = path
            .parent()
            .ok_or_else(|| StoreError::Invalid("artifact has no parent".into()))?;
        std::fs::create_dir_all(parent)?;
        let mut input = std::fs::File::open(source)?;
        let mut staged = tempfile::NamedTempFile::new_in(parent)?;
        std::io::copy(&mut input, staged.as_file_mut())?;
        staged.as_file().sync_all()?;
        staged
            .persist_noclobber(path)
            .map_err(|error| StoreError::Io(error.error))?;
        Ok(())
    }

    fn read_json<T: DeserializeOwned>(&self, rel: &str) -> Result<T, StoreError> {
        let path = self.resolve(rel)?;
        let bytes =
            std::fs::read(&path).map_err(|e| StoreError::Invalid(format!("read {rel}: {e}")))?;
        serde_json::from_slice(&bytes).map_err(|e| StoreError::Invalid(format!("parse {rel}: {e}")))
    }

    fn write_json<T: Serialize>(&self, rel: &str, value: &T) -> Result<(), StoreError> {
        let path = self.resolve(rel)?;
        if let Some(parent) = path.parent() {
            std::fs::create_dir_all(parent)?;
        }
        write_json_atomic(&path, value)
            .map_err(|e| StoreError::Invalid(format!("write {rel}: {e}")))
    }

    // -- series --------------------------------------------------------------

    pub fn read_series(&self) -> Result<SeriesConfig, StoreError> {
        self.read_json("series.json")
    }

    pub fn write_series(&self, config: &SeriesConfig) -> Result<(), StoreError> {
        let _guard = self.write_guard();
        self.write_json("series.json", config)
    }

    // -- source --------------------------------------------------------------

    pub fn read_source_meta(&self) -> Result<SourceMeta, StoreError> {
        self.read_json("source/source.json")
    }

    pub fn write_source_meta(&self, meta: &SourceMeta) -> Result<(), StoreError> {
        let _guard = self.write_guard();
        self.write_json("source/source.json", meta)
    }

    pub fn has_source(&self) -> bool {
        self.read_source_meta()
            .map(|m| m.mode.is_some())
            .unwrap_or(false)
    }

    pub fn read_chapters(&self) -> Result<ChapterIndex, StoreError> {
        self.read_json("source/chapters.json")
    }

    pub fn write_chapters(&self, index: &ChapterIndex) -> Result<(), StoreError> {
        let _guard = self.write_guard();
        self.write_json("source/chapters.json", index)
    }

    /// The raw novel text, if ingested.
    pub fn read_novel_text(&self) -> Result<String, StoreError> {
        let meta = self.read_source_meta()?;
        let rel = meta
            .novel_file
            .ok_or_else(|| StoreError::Invalid("no novel ingested".into()))?;
        let bytes = std::fs::read(self.resolve(&rel)?)?;
        Ok(String::from_utf8_lossy(&bytes).into_owned())
    }

    // -- episodes ------------------------------------------------------------

    pub fn list_episode_ids(&self) -> Result<Vec<String>, StoreError> {
        let dir = self.resolve("episodes")?;
        let mut ids = Vec::new();
        let entries = std::fs::read_dir(&dir)
            .map_err(|e| StoreError::Invalid(format!("read episodes/: {e}")))?;
        for entry in entries.flatten() {
            if entry.path().is_dir() {
                ids.push(entry.file_name().to_string_lossy().into_owned());
            }
        }
        ids.sort();
        Ok(ids)
    }

    pub fn read_episode(&self, ep: &str) -> Result<EpisodeMeta, StoreError> {
        validate_slug(ep).map_err(StoreError::Invalid)?;
        let rel = format!("episodes/{ep}/episode.json");
        let path = self.resolve(&rel)?;
        // Older writers could create a script/storyboard without episode.json.
        // Keep those episodes readable without mutating a read or masking a
        // malformed existing document. Missing directories remain an error.
        if !path.try_exists()? && self.resolve(&format!("episodes/{ep}"))?.is_dir() {
            return Ok(Self::default_episode_meta(ep));
        }
        self.read_json(&rel)
    }

    fn default_episode_meta(ep: &str) -> EpisodeMeta {
        EpisodeMeta {
            id: ep.to_string(),
            title: ep.to_string(),
            ..Default::default()
        }
    }

    /// Caller holds the folder write lock. Never rewrite user-authored metadata.
    fn ensure_episode_locked(&self, ep: &str) -> Result<(), StoreError> {
        validate_slug(ep).map_err(StoreError::Invalid)?;
        let rel = format!("episodes/{ep}/episode.json");
        if !self.resolve(&rel)?.try_exists()? {
            self.write_json(&rel, &Self::default_episode_meta(ep))?;
        }
        Ok(())
    }

    pub fn write_episode(&self, ep: &str, meta: &EpisodeMeta) -> Result<(), StoreError> {
        let _guard = self.write_guard();
        validate_slug(ep).map_err(StoreError::Invalid)?;
        self.write_json(&format!("episodes/{ep}/episode.json"), meta)
    }

    /// Create or replace an episode; also creates its `shots/` directory.
    pub fn upsert_episode(&self, meta: &EpisodeMeta) -> Result<(), StoreError> {
        self.upsert_episode_checked(meta, false, None)
    }

    /// Workbench opt-in preconditions. Creation claims the whole episode directory,
    /// including legacy episodes whose script/board exists without metadata.
    pub fn upsert_episode_checked(
        &self,
        meta: &EpisodeMeta,
        create_only: bool,
        expected_meta: Option<&EpisodeMeta>,
    ) -> Result<(), StoreError> {
        let _guard = self.write_guard();
        validate_slug(&meta.id).map_err(StoreError::Invalid)?;
        let dir = self.resolve(&format!("episodes/{}", meta.id))?;
        if create_only && dir.try_exists()? {
            return Err(StoreError::Conflict(format!(
                "episode {} already exists",
                meta.id
            )));
        }
        if let Some(expected) = expected_meta {
            if &self.read_episode(&meta.id)? != expected {
                return Err(StoreError::Conflict(format!(
                    "episode {} changed while editing; refresh and try again",
                    meta.id
                )));
            }
        }
        if create_only {
            std::fs::create_dir(&dir)?;
        }
        let result = (|| {
            self.write_json(&format!("episodes/{}/episode.json", meta.id), meta)?;
            let shots_dir = self.resolve(&format!("episodes/{}/shots", meta.id))?;
            std::fs::create_dir_all(shots_dir)?;
            Ok(())
        })();
        if create_only && result.is_err() {
            // No other StoryStore writer can enter this newly claimed directory
            // until the guard drops. Roll back an incomplete allocation.
            let _ = std::fs::remove_dir_all(dir);
        }
        result
    }

    pub fn read_script(&self, ep: &str) -> Result<String, StoreError> {
        let path = self.resolve(&format!("episodes/{ep}/script.md"))?;
        let bytes = std::fs::read(&path)
            .map_err(|e| StoreError::Invalid(format!("read episodes/{ep}/script.md: {e}")))?;
        Ok(String::from_utf8_lossy(&bytes).into_owned())
    }

    pub fn write_script(&self, ep: &str, script: &str) -> Result<(), StoreError> {
        self.write_script_checked(ep, script, false)
    }

    /// Create-only script retries are idempotent for identical bytes, but must
    /// never replace a script supplied by another writer while the UI waited.
    pub fn write_script_checked(
        &self,
        ep: &str,
        script: &str,
        create_only: bool,
    ) -> Result<(), StoreError> {
        let _guard = self.write_guard();
        validate_slug(ep).map_err(StoreError::Invalid)?;
        let path = self.resolve(&format!("episodes/{ep}/script.md"))?;
        if create_only && path.try_exists()? {
            if std::fs::read(&path)? == script.as_bytes() {
                return Ok(());
            }
            return Err(StoreError::Conflict(format!(
                "episode {ep} already has a script; the existing script was not replaced"
            )));
        }
        self.ensure_episode_locked(ep)?;
        write_bytes_atomic(&path, script.as_bytes())
            .map_err(|e| StoreError::Invalid(format!("write episodes/{ep}/script.md: {e}")))
    }

    pub fn read_storyboard(&self, ep: &str) -> Result<Storyboard, StoreError> {
        self.read_json(&format!("episodes/{ep}/storyboard.json"))
    }

    pub fn write_storyboard(&self, ep: &str, board: &Storyboard) -> Result<(), StoreError> {
        let _guard = self.write_guard();
        let mut board = board.clone();
        self.preserve_shot_videos_locked(ep, &mut board.shots)?;
        self.write_storyboard_locked(ep, &board)
    }

    fn preserve_shot_videos_locked(
        &self,
        ep: &str,
        shots: &mut [StoryShot],
    ) -> Result<(), StoreError> {
        let path = self.resolve(&format!("episodes/{ep}/storyboard.json"))?;
        let current = if path.exists() {
            self.read_storyboard(ep)?
        } else {
            Storyboard::default()
        };
        // Ordinary edits cannot bind, clear, or roll back a confirmed clip.
        // Only bind_shot_video changes this field under the same write lock.
        for shot in shots {
            shot.video = current
                .shots
                .iter()
                .find(|current| current.id == shot.id)
                .and_then(|current| current.video.clone());
        }
        Ok(())
    }

    fn validate_shot_ids(shots: &[StoryShot]) -> Result<(), StoreError> {
        let mut ids = HashSet::new();
        for shot in shots {
            validate_slug(&shot.id).map_err(StoreError::Invalid)?;
            if !ids.insert(&shot.id) {
                return Err(StoreError::Invalid(format!(
                    "duplicate shot id: {}",
                    shot.id
                )));
            }
        }
        Ok(())
    }

    fn write_storyboard_locked(&self, ep: &str, board: &Storyboard) -> Result<(), StoreError> {
        Self::validate_shot_ids(&board.shots)?;
        let rel = format!("episodes/{ep}/storyboard.json");
        self.resolve(&rel)?;
        self.ensure_episode_locked(ep)?;
        self.write_json(&rel, board)
    }

    // -- shots ---------------------------------------------------------------

    fn shot_rel(ep: &str, shot: &str) -> String {
        format!("episodes/{ep}/shots/{shot}")
    }

    pub fn read_shot_meta(&self, ep: &str, shot: &str) -> Result<ShotMeta, StoreError> {
        self.read_json(&format!("{}/shot.json", Self::shot_rel(ep, shot)))
    }

    pub fn write_shot_meta(&self, ep: &str, shot: &str, meta: &ShotMeta) -> Result<(), StoreError> {
        let _guard = self.write_guard();
        self.write_shot_meta_locked(ep, shot, meta)
    }

    fn write_shot_meta_locked(
        &self,
        ep: &str,
        shot: &str,
        meta: &ShotMeta,
    ) -> Result<(), StoreError> {
        self.write_json(&format!("{}/shot.json", Self::shot_rel(ep, shot)), meta)
    }

    /// Bind verified speech without changing storyboard order or the selected frame.
    pub fn bind_shot_audio(
        &self,
        ep: &str,
        shot: &str,
        audio: super::model::ShotAudio,
    ) -> Result<(), StoreError> {
        let _guard = self.write_guard();
        let mut board = self.read_storyboard(ep)?;
        let target = board
            .shots
            .iter_mut()
            .find(|target| target.id == shot)
            .ok_or_else(|| StoreError::Invalid(format!("episode {ep} has no shot {shot}")))?;
        target.audio = Some(audio);
        self.write_storyboard_locked(ep, &board)
    }

    /// Bind a verified video clip only if the observed binding is still current.
    pub fn bind_shot_video(
        &self,
        ep: &str,
        shot: &str,
        video: super::model::ShotClip,
        expected_current_task_id: Option<i32>,
    ) -> Result<(), StoreError> {
        let _guard = self.write_guard();
        let mut board = self.read_storyboard(ep)?;
        let target = board
            .shots
            .iter_mut()
            .find(|target| target.id == shot)
            .ok_or_else(|| StoreError::Invalid(format!("episode {ep} has no shot {shot}")))?;
        let current_task_id = target.video.as_ref().map(|current| current.task_id);
        if current_task_id == Some(video.task_id) {
            return Ok(());
        }
        if current_task_id != expected_current_task_id {
            return Err(StoreError::Conflict(format!(
                "shot {shot} video changed since confirmation opened; refresh and confirm again"
            )));
        }
        target.video = Some(video);
        self.write_storyboard_locked(ep, &board)
    }

    /// Clear a shot's confirmed video clip only if the observed binding is
    /// still current. Unbinding is an explicit CAS action like binding: a
    /// stale expectation (including one opened before a newer clip was bound)
    /// refuses instead of silently clearing.
    pub fn clear_shot_video(
        &self,
        ep: &str,
        shot: &str,
        expected_current_task_id: Option<i32>,
    ) -> Result<(), StoreError> {
        let _guard = self.write_guard();
        let mut board = self.read_storyboard(ep)?;
        let target = board
            .shots
            .iter_mut()
            .find(|target| target.id == shot)
            .ok_or_else(|| StoreError::Invalid(format!("episode {ep} has no shot {shot}")))?;
        let current_task_id = target.video.as_ref().map(|current| current.task_id);
        if current_task_id.is_none() && expected_current_task_id.is_none() {
            return Ok(());
        }
        if current_task_id != expected_current_task_id {
            return Err(StoreError::Conflict(format!(
                "shot {shot} video changed since the unbind confirmation opened; \
                 refresh and confirm again"
            )));
        }
        target.video = None;
        self.write_storyboard_locked(ep, &board)
    }

    /// Validate and upsert one shot, keeping its existing position or appending
    /// a new shot. Renumber `no` in display order. Reference slugs are checked
    /// against existing cards; unknown references are a value-level error.
    pub fn upsert_shot(&self, ep: &str, shot: &StoryShot) -> Result<(), StoreError> {
        let _guard = self.write_guard();
        validate_slug(ep).map_err(StoreError::Invalid)?;
        validate_slug(&shot.id).map_err(StoreError::Invalid)?;
        self.validate_shot_refs(shot)?;
        self.resolve(&format!("{}/shot.json", Self::shot_rel(ep, &shot.id)))?;
        let mut board = if self
            .resolve(&format!("episodes/{ep}/storyboard.json"))?
            .try_exists()?
        {
            self.read_storyboard(ep)?
        } else {
            Storyboard::default()
        };
        let mut updated = shot.clone();
        self.preserve_shot_videos_locked(ep, std::slice::from_mut(&mut updated))?;
        match board.shots.iter_mut().find(|s| s.id == shot.id) {
            Some(existing) => *existing = updated,
            None => board.shots.push(updated),
        }
        for (idx, shot) in board.shots.iter_mut().enumerate() {
            shot.no = (idx + 1) as u32;
        }
        self.write_storyboard_locked(ep, &board)?;
        self.ensure_shot_dir(ep, &shot.id)?;
        Ok(())
    }

    /// Replace an episode's ENTIRE storyboard: validate every shot's
    /// references first, then renumber, write, and ensure every shot
    /// directory + meta exist. Nothing is written when any shot is invalid.
    pub fn replace_storyboard(
        &self,
        ep: &str,
        shots: Vec<StoryShot>,
    ) -> Result<Storyboard, StoreError> {
        self.replace_storyboard_checked(ep, shots, None)
    }

    /// A workbench import confirms the exact board it read, including an empty
    /// board. Compare under the same lock as all shot/media writes before any
    /// file or directory is changed. Omission retains agent upsert semantics.
    pub fn replace_storyboard_checked(
        &self,
        ep: &str,
        mut shots: Vec<StoryShot>,
        expected_storyboard: Option<&Storyboard>,
    ) -> Result<Storyboard, StoreError> {
        let _guard = self.write_guard();
        validate_slug(ep).map_err(StoreError::Invalid)?;
        if let Some(expected) = expected_storyboard {
            let current = if self
                .resolve(&format!("episodes/{ep}/storyboard.json"))?
                .try_exists()?
            {
                self.read_storyboard(ep)?
            } else {
                Storyboard::default()
            };
            if &current != expected {
                return Err(StoreError::Conflict(format!(
                    "episode {ep} storyboard changed since confirmation; refresh and confirm again"
                )));
            }
        }
        Self::validate_shot_ids(&shots)?;
        for shot in &shots {
            self.validate_shot_refs(shot)?;
            self.resolve(&format!("{}/shot.json", Self::shot_rel(ep, &shot.id)))?;
        }
        self.preserve_shot_videos_locked(ep, &mut shots)?;
        for (idx, shot) in shots.iter_mut().enumerate() {
            shot.no = (idx + 1) as u32;
        }
        let board = Storyboard { shots };
        self.write_storyboard_locked(ep, &board)?;
        let ids: Vec<String> = board.shots.iter().map(|s| s.id.clone()).collect();
        for id in ids {
            self.ensure_shot_dir(ep, &id)?;
        }
        Ok(board)
    }

    /// Ensure the per-shot directory and its `shot.json` exist.
    fn ensure_shot_dir(&self, ep: &str, shot: &str) -> Result<(), StoreError> {
        let shot_dir = self.resolve(&Self::shot_rel(ep, shot))?;
        std::fs::create_dir_all(shot_dir)?;
        let meta_rel = format!("{}/shot.json", Self::shot_rel(ep, shot));
        if !self.resolve(&meta_rel).map(|p| p.exists()).unwrap_or(false) {
            self.write_shot_meta_locked(
                ep,
                shot,
                &ShotMeta {
                    id: shot.to_string(),
                    ..Default::default()
                },
            )?;
        }
        Ok(())
    }

    /// Delete one shot from an episode's storyboard and remove its shot
    /// directory (candidates, bound frame, clip, audio go with it — the
    /// workbench asks before calling). Unknown shot id is an error.
    pub fn delete_shot(&self, ep: &str, shot: &str) -> Result<(), StoreError> {
        let _guard = self.write_guard();
        validate_slug(shot).map_err(StoreError::Invalid)?;
        let mut board = self.read_storyboard(ep)?;
        let before = board.shots.len();
        board.shots.retain(|s| s.id != shot);
        if board.shots.len() == before {
            return Err(StoreError::Invalid(format!(
                "episode {ep} has no shot {shot}"
            )));
        }
        for (idx, shot) in board.shots.iter_mut().enumerate() {
            shot.no = (idx + 1) as u32;
        }
        self.write_storyboard_locked(ep, &board)?;
        let dir = self.resolve(&Self::shot_rel(ep, shot))?;
        if dir.is_dir() {
            std::fs::remove_dir_all(dir)?;
        }
        Ok(())
    }

    /// Reorder an episode's shots to match the given id sequence (which must
    /// be exactly the existing ids, permuted). Renumbers `no` in the new
    /// display order, which subsequent shot edits preserve.
    pub fn reorder_shots(&self, ep: &str, order: &[String]) -> Result<Storyboard, StoreError> {
        let _guard = self.write_guard();
        let mut board = self.read_storyboard(ep)?;
        if order.len() != board.shots.len() {
            return Err(StoreError::Invalid(format!(
                "reorder must list all {} shots, got {}",
                board.shots.len(),
                order.len()
            )));
        }
        let mut ordered = Vec::with_capacity(board.shots.len());
        for id in order {
            match board.shots.iter().find(|s| &s.id == id) {
                Some(shot) => ordered.push(shot.clone()),
                None => {
                    return Err(StoreError::Invalid(format!(
                        "reorder lists unknown shot '{id}'"
                    )))
                }
            }
        }
        board.shots = ordered;
        for (idx, shot) in board.shots.iter_mut().enumerate() {
            shot.no = (idx + 1) as u32;
        }
        self.write_storyboard_locked(ep, &board)?;
        Ok(board)
    }

    /// Save an uploaded reference image into a card's slot and update the
    /// card JSON to point at it. `slot` is card-kind-specific:
    /// `turnaround/front` / `turnaround/side_45` / `turnaround/side` /
    /// `expressions` / `refs` (characters), `refs` (scenes / props), plus
    /// `outfit/<outfit-slug>/front|side_45|side` on characters. Returns the
    /// story-relative path stored in the card.
    pub fn save_asset_image(
        &self,
        kind: &str,
        slug: &str,
        slot: &str,
        bytes: &[u8],
    ) -> Result<String, StoreError> {
        let _guard = self.write_guard();
        validate_slug(slug).map_err(StoreError::Invalid)?;
        let rel_dir: String;
        let file_name: String;
        match (kind, slot) {
            ("character", s @ ("turnaround/front" | "turnaround/side_45" | "turnaround/side")) => {
                rel_dir = format!("characters/{slug}/turnaround");
                file_name = format!("{}.png", s.rsplit('/').next().unwrap_or("image"));
            }
            ("character", "expressions") => {
                rel_dir = format!("characters/{slug}/expressions");
                file_name = format!("expr-{}.png", chrono::Utc::now().timestamp_millis());
            }
            ("character", s) if s.starts_with("outfit/") => {
                let rest = s.strip_prefix("outfit/").unwrap_or_default();
                let (outfit, view) = rest
                    .split_once('/')
                    .ok_or_else(|| StoreError::Invalid(format!("bad outfit slot: {slot}")))?;
                validate_slug(outfit).map_err(StoreError::Invalid)?;
                if !matches!(view, "front" | "side_45" | "side") {
                    return Err(StoreError::Invalid(format!(
                        "outfit view must be front|side_45|side: {view}"
                    )));
                }
                rel_dir = format!("characters/{slug}/outfits/{outfit}");
                file_name = format!("{view}.png");
            }
            (_, "refs") if kind == "scene" || kind == "prop" || kind == "character" => {
                rel_dir = format!("{kind}s/{slug}/ref");
                file_name = format!("ref-{}.png", chrono::Utc::now().timestamp_millis());
            }
            _ => {
                return Err(StoreError::Invalid(format!(
                    "bad image slot for {kind}: {slot}"
                )))
            }
        }
        // Reject non-image bytes cheaply by sniffing the magic numbers we
        // accept (PNG / JPEG / WebP); the workbench previews only images.
        if !(bytes.starts_with(&[0x89, b'P', b'N', b'G'])
            || bytes.starts_with(&[0xFF, 0xD8, 0xFF])
            || (bytes.len() > 12 && &bytes[0..4] == b"RIFF" && &bytes[8..12] == b"WEBP"))
        {
            return Err(StoreError::Invalid(
                "only PNG / JPEG / WebP images are accepted".into(),
            ));
        }
        let dir_path = self.resolve(&rel_dir)?;
        std::fs::create_dir_all(&dir_path)?;
        let rel = format!("{rel_dir}/{file_name}");
        let path = self.resolve(&rel)?;
        write_bytes_atomic(&path, bytes)
            .map_err(|e| StoreError::Invalid(format!("write {rel}: {e}")))?;

        // Point the card at the new image.
        match kind {
            "character" => {
                let mut card: CharacterCard = self.read_character(slug)?;
                match slot {
                    "turnaround/front" => card.turnaround.front = Some(rel.clone()),
                    "turnaround/side_45" => card.turnaround.side_45 = Some(rel.clone()),
                    "turnaround/side" => card.turnaround.side = Some(rel.clone()),
                    "expressions" => card.expressions.push(rel.clone()),
                    "refs" => card.refs.push(rel.clone()),
                    s if s.starts_with("outfit/") => {
                        let outfit = s
                            .strip_prefix("outfit/")
                            .and_then(|r| r.split_once('/'))
                            .map(|(o, _)| o)
                            .unwrap_or_default();
                        let view = s.rsplit('/').next().unwrap_or_default();
                        if let Some(target) = card.outfits.iter_mut().find(|o| o.slug == outfit) {
                            let field = match view {
                                "front" => &mut target.front,
                                "side_45" => &mut target.side_45,
                                _ => &mut target.side,
                            };
                            *field = Some(rel.clone());
                        } else {
                            return Err(StoreError::Invalid(format!(
                                "character {slug} has no outfit '{outfit}'"
                            )));
                        }
                    }
                    _ => {}
                }
                self.upsert_character_locked(&card)?;
            }
            "scene" => {
                let mut card: SceneCard = self.read_scene(slug)?;
                card.refs.push(rel.clone());
                self.upsert_scene_locked(&card)?;
            }
            "prop" => {
                let mut card: PropCard = self.read_prop(slug)?;
                card.refs.push(rel.clone());
                self.upsert_prop_locked(&card)?;
            }
            _ => return Err(StoreError::Invalid(format!("unknown asset kind: {kind}"))),
        }
        Ok(rel)
    }

    fn validate_shot_refs(&self, shot: &StoryShot) -> Result<(), StoreError> {
        for character in &shot.refs.characters {
            validate_slug(&character.slug).map_err(StoreError::Invalid)?;
            let rel = format!("characters/{}/character.json", character.slug);
            if !self.resolve(&rel).map(|p| p.exists()).unwrap_or(false) {
                return Err(StoreError::Invalid(format!(
                    "shot {} references unknown character '{}'",
                    shot.id, character.slug
                )));
            }
            if !character.outfit.is_empty() {
                let card: CharacterCard = self.read_json(&rel)?;
                if !card.outfits.iter().any(|o| o.slug == character.outfit) {
                    return Err(StoreError::Invalid(format!(
                        "shot {} references unknown outfit '{}' on character '{}'",
                        shot.id, character.outfit, character.slug
                    )));
                }
            }
        }
        if let Some(scene) = &shot.refs.scene {
            validate_slug(scene).map_err(StoreError::Invalid)?;
            if !self
                .resolve(&format!("scenes/{scene}/scene.json"))
                .map(|p| p.exists())
                .unwrap_or(false)
            {
                return Err(StoreError::Invalid(format!(
                    "shot {} references unknown scene '{scene}'",
                    shot.id
                )));
            }
        }
        for prop in &shot.refs.props {
            validate_slug(prop).map_err(StoreError::Invalid)?;
            if !self
                .resolve(&format!("props/{prop}/prop.json"))
                .map(|p| p.exists())
                .unwrap_or(false)
            {
                return Err(StoreError::Invalid(format!(
                    "shot {} references unknown prop '{prop}'",
                    shot.id
                )));
            }
        }
        Ok(())
    }

    // -- assets (characters / scenes / props) --------------------------------

    pub fn list_characters(&self) -> Result<Vec<CharacterCard>, StoreError> {
        self.list_cards("characters", "character.json")
    }

    pub fn read_character(&self, slug: &str) -> Result<CharacterCard, StoreError> {
        self.read_json(&format!("characters/{slug}/character.json"))
    }

    pub fn upsert_character(&self, card: &CharacterCard) -> Result<(), StoreError> {
        let _guard = self.write_guard();
        self.upsert_character_locked(card)
    }

    fn upsert_character_locked(&self, card: &CharacterCard) -> Result<(), StoreError> {
        validate_slug(&card.slug).map_err(StoreError::Invalid)?;
        for outfit in &card.outfits {
            validate_slug(&outfit.slug).map_err(StoreError::Invalid)?;
        }
        self.write_json(&format!("characters/{}/character.json", card.slug), card)?;
        let dir = self.resolve(&format!("characters/{}", card.slug))?;
        std::fs::create_dir_all(dir.join("turnaround"))?;
        std::fs::create_dir_all(dir.join("expressions"))?;
        Ok(())
    }

    pub fn list_scenes(&self) -> Result<Vec<SceneCard>, StoreError> {
        self.list_cards("scenes", "scene.json")
    }

    pub fn read_scene(&self, slug: &str) -> Result<SceneCard, StoreError> {
        self.read_json(&format!("scenes/{slug}/scene.json"))
    }

    pub fn upsert_scene(&self, card: &SceneCard) -> Result<(), StoreError> {
        let _guard = self.write_guard();
        self.upsert_scene_locked(card)
    }

    fn upsert_scene_locked(&self, card: &SceneCard) -> Result<(), StoreError> {
        validate_slug(&card.slug).map_err(StoreError::Invalid)?;
        self.write_json(&format!("scenes/{}/scene.json", card.slug), card)?;
        let dir = self.resolve(&format!("scenes/{}", card.slug))?;
        std::fs::create_dir_all(dir.join("ref"))?;
        Ok(())
    }

    pub fn list_props(&self) -> Result<Vec<PropCard>, StoreError> {
        self.list_cards("props", "prop.json")
    }

    pub fn read_prop(&self, slug: &str) -> Result<PropCard, StoreError> {
        self.read_json(&format!("props/{slug}/prop.json"))
    }

    pub fn upsert_prop(&self, card: &PropCard) -> Result<(), StoreError> {
        let _guard = self.write_guard();
        self.upsert_prop_locked(card)
    }

    fn upsert_prop_locked(&self, card: &PropCard) -> Result<(), StoreError> {
        validate_slug(&card.slug).map_err(StoreError::Invalid)?;
        self.write_json(&format!("props/{}/prop.json", card.slug), card)?;
        let dir = self.resolve(&format!("props/{}", card.slug))?;
        std::fs::create_dir_all(dir.join("ref"))?;
        Ok(())
    }

    fn list_cards<T: DeserializeOwned>(
        &self,
        kind_dir: &str,
        doc: &str,
    ) -> Result<Vec<T>, StoreError> {
        let dir = self.resolve(kind_dir)?;
        let mut entries: Vec<(String, T)> = Vec::new();
        let read_dir = std::fs::read_dir(&dir)
            .map_err(|e| StoreError::Invalid(format!("read {kind_dir}/: {e}")))?;
        for entry in read_dir.flatten() {
            let doc_path = entry.path().join(doc);
            if doc_path.is_file() {
                match std::fs::read(&doc_path)
                    .map_err(StoreError::Io)
                    .and_then(|bytes| serde_json::from_slice::<T>(&bytes).map_err(StoreError::Json))
                {
                    Ok(card) => {
                        entries.push((entry.file_name().to_string_lossy().into_owned(), card))
                    }
                    // A hand-edited card failing to parse must not hide the rest.
                    Err(e) => {
                        tracing::warn!("skipping unparseable {}/{doc}: {}", kind_dir, e);
                    }
                }
            }
        }
        entries.sort_by(|a, b| a.0.cmp(&b.0));
        Ok(entries.into_iter().map(|(_, card)| card).collect())
    }

    // -- derived views ---------------------------------------------------------

    /// Shots that have an image bound (`selected` set).
    pub fn episode_progress(&self, ep: &str) -> Result<(u32, u32), StoreError> {
        let board = self.read_storyboard(ep)?;
        let total = board.shots.len() as u32;
        let done = board
            .shots
            .iter()
            .filter(|s| {
                matches!(
                    s.state,
                    ShotState::Selected | ShotState::Rendering | ShotState::Done
                )
            })
            .count() as u32;
        Ok((total, done))
    }

    pub fn overview(&self) -> Result<ProjectOverview, StoreError> {
        let series = self.read_series().ok();
        let has_source = self.has_source();
        let source = self.read_source_meta().ok();

        let mut episodes = Vec::new();
        for ep in self.list_episode_ids()? {
            let meta = self.read_episode(&ep).ok().unwrap_or_default();
            let (shots_total, shots_done) = self.episode_progress(&ep).unwrap_or((0, 0));
            episodes.push(EpisodeSummary {
                id: ep,
                title: meta.title,
                status: meta.status,
                shots_total,
                shots_done,
            });
        }

        let summarize = |kind: &str, cards: Vec<(String, String, bool)>| -> Vec<AssetSummary> {
            cards
                .into_iter()
                .map(|(slug, name, has)| AssetSummary {
                    kind: kind.to_string(),
                    slug,
                    name,
                    has_ref_images: has,
                })
                .collect()
        };
        let characters = summarize(
            "character",
            self.list_characters()?
                .into_iter()
                .map(|c| {
                    let has = c.turnaround.front.is_some()
                        || c.turnaround.side_45.is_some()
                        || c.turnaround.side.is_some()
                        || c.expressions.iter().any(|_| true);
                    (c.slug, c.name, has)
                })
                .collect(),
        );
        let scenes = summarize(
            "scene",
            self.list_scenes()?
                .into_iter()
                .map(|s| (s.slug, s.name, s.refs.iter().any(|_| true)))
                .collect(),
        );
        let props = summarize(
            "prop",
            self.list_props()?
                .into_iter()
                .map(|p| (p.slug, p.name, p.refs.iter().any(|_| true)))
                .collect(),
        );

        let next_action = self.compute_next_action(&episodes)?;

        Ok(ProjectOverview {
            source,
            has_source,
            source_hint: if has_source {
                None
            } else {
                Some(
                    "未登记小说原文。请让用户上传整本原文（可直接拖入会话），或指定原文文件路径；\
                     若为原创剧本请明确说明以跳过。"
                        .to_string(),
                )
            },
            series,
            episodes,
            characters,
            scenes,
            props,
            next_action,
        })
    }

    fn compute_next_action(
        &self,
        episodes: &[EpisodeSummary],
    ) -> Result<Option<NextAction>, StoreError> {
        if !self.has_source() {
            // Original-only projects set mode=original, which counts as a source.
            return Ok(Some(NextAction::ProvideSource));
        }
        if episodes.is_empty() {
            return Ok(Some(NextAction::AdaptScript));
        }
        for ep in episodes {
            let board = self.read_storyboard(&ep.id).ok();
            let shots = board.map(|b| b.shots).unwrap_or_default();
            let has_script = self
                .resolve(&format!("episodes/{}/script.md", ep.id))
                .map(|p| p.exists())
                .unwrap_or(false);
            if !has_script {
                return Ok(Some(NextAction::AdaptScript));
            }
            if shots.is_empty() {
                return Ok(Some(NextAction::CreateStoryboard {
                    episode: ep.id.clone(),
                }));
            }
            let unbound: Vec<&StoryShot> = shots
                .iter()
                .filter(|s| matches!(s.state, ShotState::Draft | ShotState::Prompted))
                .collect();
            let unbound_count = unbound.len() as u32;
            let candidates: Vec<&StoryShot> = shots
                .iter()
                .filter(|s| matches!(s.state, ShotState::Candidate))
                .collect();
            let videos_pending = shots
                .iter()
                .filter(|s| matches!(s.state, ShotState::Selected))
                .count() as u32;
            if !candidates.is_empty() {
                return Ok(Some(NextAction::GenerateImages {
                    episode: ep.id.clone(),
                    pending_shots: unbound_count + candidates.len() as u32,
                }));
            }
            if unbound_count > 0 {
                return Ok(Some(NextAction::GenerateImages {
                    episode: ep.id.clone(),
                    pending_shots: unbound_count,
                }));
            }
            if videos_pending > 0 {
                return Ok(Some(NextAction::GenerateVideos {
                    episode: ep.id.clone(),
                    pending_shots: videos_pending,
                }));
            }
            if ep.status != EpisodeStatus::Done {
                return Ok(Some(NextAction::ComposeEpisode {
                    episode: ep.id.clone(),
                }));
            }
        }
        Ok(Some(NextAction::None))
    }

    /// Case-insensitive substring search across cards' name/description/
    /// prompt fragment. Good enough for asset recall; the LLM combines it
    /// with `comic_read_source` for plot lookups.
    pub fn search_assets(&self, query: &str) -> Result<Vec<AssetSummary>, StoreError> {
        let q = query.to_lowercase();
        let mut hits = Vec::new();
        if q.is_empty() {
            return Ok(hits);
        }
        let mut push = |kind: &str, slug: &str, name: &str, hay: &str, has: bool| {
            if name.to_lowercase().contains(&q) || hay.to_lowercase().contains(&q) {
                hits.push(AssetSummary {
                    kind: kind.to_string(),
                    slug: slug.to_string(),
                    name: name.to_string(),
                    has_ref_images: has,
                });
            }
        };
        for c in self.list_characters()? {
            let hay = format!("{} {} {}", c.description, c.personality, c.prompt_fragment);
            push(
                "character",
                &c.slug,
                &c.name,
                &hay,
                c.turnaround.front.is_some(),
            );
        }
        for s in self.list_scenes()? {
            let hay = format!("{} {}", s.description, s.prompt_fragment);
            push("scene", &s.slug, &s.name, &hay, s.refs.iter().any(|_| true));
        }
        for p in self.list_props()? {
            let hay = format!("{} {}", p.description, p.prompt_fragment);
            push("prop", &p.slug, &p.name, &hay, p.refs.iter().any(|_| true));
        }
        Ok(hits)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::comic::model::{EpisodeMeta, ShotAudio, ShotClip};

    fn shot(id: &str) -> StoryShot {
        StoryShot {
            id: id.into(),
            ..Default::default()
        }
    }

    #[test]
    fn simultaneous_episode_creates_have_one_owner_and_do_not_overwrite() {
        let dir = tempfile::tempdir().unwrap();
        let store = StoryStore::init(dir.path(), "Creation race").unwrap();
        let barrier = std::sync::Barrier::new(2);
        let results = std::thread::scope(|scope| {
            let handles: Vec<_> = ["First", "Second"]
                .into_iter()
                .map(|title| {
                    let barrier = &barrier;
                    let path = dir.path();
                    scope.spawn(move || {
                        let writer = StoryStore::open(path).unwrap();
                        let meta = EpisodeMeta {
                            id: "ep001".into(),
                            title: title.into(),
                            ..Default::default()
                        };
                        barrier.wait();
                        (
                            meta.clone(),
                            writer.upsert_episode_checked(&meta, true, None),
                        )
                    })
                })
                .collect();
            handles
                .into_iter()
                .map(|h| h.join().unwrap())
                .collect::<Vec<_>>()
        });
        assert_eq!(
            results.iter().filter(|(_, result)| result.is_ok()).count(),
            1
        );
        let winner = results.iter().find(|(_, result)| result.is_ok()).unwrap();
        let loser = results.iter().find(|(_, result)| result.is_err()).unwrap();
        assert!(matches!(&loser.1, Err(StoreError::Conflict(_))));
        assert_eq!(store.read_episode("ep001").unwrap(), winner.0);
        assert!(store.root().join("episodes/ep001/shots").is_dir());
        // Existing MCP upserts intentionally retain their replacement behavior.
        store.upsert_episode(&loser.0).unwrap();
        assert_eq!(store.read_episode("ep001").unwrap(), loser.0);
    }

    #[test]
    fn create_only_preserves_legacy_content_and_concurrent_script() {
        let dir = tempfile::tempdir().unwrap();
        let store = StoryStore::init(dir.path(), "Create only").unwrap();
        store
            .write_artifact("episodes/ep001/script.md", b"Agent script")
            .unwrap();
        let meta = EpisodeMeta {
            id: "ep001".into(),
            title: "New".into(),
            ..Default::default()
        };
        assert!(matches!(
            store.upsert_episode_checked(&meta, true, None),
            Err(StoreError::Conflict(_))
        ));
        assert!(!store.root().join("episodes/ep001/episode.json").exists());
        assert!(matches!(
            store.write_script_checked("ep001", "User script", true),
            Err(StoreError::Conflict(_))
        ));
        assert_eq!(store.read_script("ep001").unwrap(), "Agent script");
        // A lost successful response can be retried without replacing content.
        store
            .write_script_checked("ep001", "Agent script", true)
            .unwrap();
        assert!(!store.root().join("episodes/ep001/episode.json").exists());
    }

    #[test]
    fn simultaneous_script_creates_have_one_winner() {
        let dir = tempfile::tempdir().unwrap();
        let store = StoryStore::init(dir.path(), "Script race").unwrap();
        let barrier = std::sync::Barrier::new(2);
        let results = std::thread::scope(|scope| {
            let handles: Vec<_> = ["User script", "Other writer"]
                .into_iter()
                .map(|script| {
                    let barrier = &barrier;
                    let path = dir.path();
                    scope.spawn(move || {
                        let writer = StoryStore::open(path).unwrap();
                        barrier.wait();
                        (script, writer.write_script_checked("ep001", script, true))
                    })
                })
                .collect();
            handles
                .into_iter()
                .map(|h| h.join().unwrap())
                .collect::<Vec<_>>()
        });
        assert_eq!(
            results.iter().filter(|(_, result)| result.is_ok()).count(),
            1
        );
        let winner = results.iter().find(|(_, result)| result.is_ok()).unwrap();
        assert_eq!(store.read_script("ep001").unwrap(), winner.0);
        assert!(results
            .iter()
            .any(|(_, result)| matches!(result, Err(StoreError::Conflict(_)))));
    }

    #[test]
    fn stale_episode_metadata_cannot_replace_a_concurrent_edit() {
        let dir = tempfile::tempdir().unwrap();
        let store = StoryStore::init(dir.path(), "Rename race").unwrap();
        let original = EpisodeMeta {
            id: "ep001".into(),
            title: "Original".into(),
            ..Default::default()
        };
        store.upsert_episode(&original).unwrap();
        let mut concurrent = original.clone();
        concurrent.synopsis = "Agent update".into();
        StoryStore::open(dir.path())
            .unwrap()
            .upsert_episode(&concurrent)
            .unwrap();
        let renamed = EpisodeMeta {
            title: "Renamed".into(),
            ..original.clone()
        };
        assert!(matches!(
            store.upsert_episode_checked(&renamed, false, Some(&original)),
            Err(StoreError::Conflict(_))
        ));
        assert_eq!(store.read_episode("ep001").unwrap(), concurrent);
    }

    #[test]
    fn simultaneous_confirmed_imports_have_one_winner_even_when_initially_empty() {
        for initially_filled in [false, true] {
            let dir = tempfile::tempdir().unwrap();
            let store = StoryStore::init(dir.path(), "Import race").unwrap();
            let expected = if initially_filled {
                store
                    .replace_storyboard("ep001", vec![shot("original")])
                    .unwrap()
            } else {
                Storyboard::default()
            };
            let barrier = std::sync::Barrier::new(2);
            let results = std::thread::scope(|scope| {
                let handles: Vec<_> = ["import-a", "import-b"]
                    .into_iter()
                    .map(|id| {
                        let barrier = &barrier;
                        let path = dir.path();
                        let expected = &expected;
                        scope.spawn(move || {
                            let writer = StoryStore::open(path).unwrap();
                            barrier.wait();
                            (
                                id,
                                writer.replace_storyboard_checked(
                                    "ep001",
                                    vec![shot(id)],
                                    Some(expected),
                                ),
                            )
                        })
                    })
                    .collect();
                handles
                    .into_iter()
                    .map(|h| h.join().unwrap())
                    .collect::<Vec<_>>()
            });
            assert_eq!(
                results.iter().filter(|(_, result)| result.is_ok()).count(),
                1
            );
            let winner = results.iter().find(|(_, result)| result.is_ok()).unwrap();
            let loser = results.iter().find(|(_, result)| result.is_err()).unwrap();
            assert!(matches!(&loser.1, Err(StoreError::Conflict(_))));
            assert_eq!(
                store.read_storyboard("ep001").unwrap().shots[0].id,
                winner.0
            );
            assert!(!store
                .root()
                .join(format!("episodes/ep001/shots/{}", loser.0))
                .exists());
        }
    }

    #[test]
    fn confirmed_import_checks_video_changes_and_retains_matching_video() {
        let dir = tempfile::tempdir().unwrap();
        let store = StoryStore::init(dir.path(), "Video import").unwrap();
        let original = store
            .replace_storyboard("ep001", vec![shot("shot-1")])
            .unwrap();
        let video = ShotClip {
            path: "video/task-7.mp4".into(),
            duration_secs: 3.0,
            task_id: 7,
        };
        store
            .bind_shot_video("ep001", "shot-1", video.clone(), None)
            .unwrap();
        assert!(matches!(
            store.replace_storyboard_checked("ep001", vec![shot("shot-1")], Some(&original)),
            Err(StoreError::Conflict(_))
        ));
        let refreshed = store.read_storyboard("ep001").unwrap();
        let replaced = store
            .replace_storyboard_checked("ep001", vec![shot("shot-1")], Some(&refreshed))
            .unwrap();
        assert_eq!(replaced.shots[0].video, Some(video));
        assert!(matches!(
            store.clear_shot_video("ep001", "shot-1", None),
            Err(StoreError::Conflict(_))
        ));
    }

    #[test]
    fn script_first_persists_readable_episode_metadata() {
        let dir = tempfile::tempdir().unwrap();
        let store = StoryStore::init(dir.path(), "Script first").unwrap();
        store.write_script("ep001", "Opening scene").unwrap();

        let reopened = StoryStore::open(dir.path()).unwrap();
        let meta = reopened.read_episode("ep001").unwrap();
        assert_eq!(meta.id, "ep001");
        assert_eq!(meta.title, "ep001");
        assert_eq!(meta.status, EpisodeStatus::Draft);
        assert_eq!(reopened.read_script("ep001").unwrap(), "Opening scene");
        assert!(store.root().join("episodes/ep001/episode.json").is_file());
    }

    #[test]
    fn episode_content_writes_preserve_existing_metadata_bytes() {
        let dir = tempfile::tempdir().unwrap();
        let store = StoryStore::init(dir.path(), "Existing metadata").unwrap();
        // Unknown user-authored fields and formatting must survive too.
        let metadata = br#"{ "id": "ep001", "title": "Custom title", "synopsis": "Keep me", "status": "done", "source_chapters": [2, 3], "custom": true }"#;
        store
            .write_artifact("episodes/ep001/episode.json", metadata)
            .unwrap();
        store.write_script("ep001", "New script").unwrap();
        store
            .replace_storyboard("ep001", vec![shot("shot-2")])
            .unwrap();
        store.upsert_shot("ep001", &shot("shot-1")).unwrap();
        let board = store.read_storyboard("ep001").unwrap();
        store.write_storyboard("ep001", &board).unwrap();
        assert_eq!(
            std::fs::read(store.root().join("episodes/ep001/episode.json")).unwrap(),
            metadata
        );
    }

    #[test]
    fn legacy_episode_read_uses_defaults_without_writing_metadata() {
        let dir = tempfile::tempdir().unwrap();
        let store = StoryStore::init(dir.path(), "Legacy episode").unwrap();
        store
            .write_artifact("episodes/ep001/script.md", b"Legacy script")
            .unwrap();

        let meta = store.read_episode("ep001").unwrap();
        assert_eq!(meta.id, "ep001");
        assert_eq!(meta.title, "ep001");
        assert_eq!(store.read_script("ep001").unwrap(), "Legacy script");
        assert_eq!(store.overview().unwrap().episodes[0].title, "ep001");
        assert!(!store.root().join("episodes/ep001/episode.json").exists());
        assert!(store.read_episode("missing").is_err());
        assert!(!store.root().join("episodes/missing").exists());
    }

    #[test]
    fn legacy_episode_fallback_does_not_hide_corrupt_metadata() {
        let dir = tempfile::tempdir().unwrap();
        let store = StoryStore::init(dir.path(), "Corrupt metadata").unwrap();
        store
            .write_artifact("episodes/ep001/episode.json", b"not json")
            .unwrap();
        assert!(store.read_episode("ep001").is_err());
        assert_eq!(
            std::fs::read(store.root().join("episodes/ep001/episode.json")).unwrap(),
            b"not json"
        );
    }

    #[test]
    fn storyboard_writes_persist_missing_episode_metadata() {
        let dir = tempfile::tempdir().unwrap();
        let store = StoryStore::init(dir.path(), "Storyboard first").unwrap();
        store
            .replace_storyboard("replaced", vec![shot("shot-1")])
            .unwrap();
        store.upsert_shot("upserted", &shot("shot-1")).unwrap();
        store
            .write_storyboard("written", &Storyboard { shots: vec![] })
            .unwrap();
        // A previous version could leave a storyboard with no episode.json.
        store
            .write_artifact("episodes/legacy/storyboard.json", br#"{"shots": []}"#)
            .unwrap();
        store.upsert_shot("legacy", &shot("shot-1")).unwrap();

        for ep in ["replaced", "upserted", "written", "legacy"] {
            let meta = store.read_episode(ep).unwrap();
            assert_eq!(meta.id, ep);
            assert_eq!(meta.title, ep);
            assert!(store
                .root()
                .join(format!("episodes/{ep}/episode.json"))
                .is_file());
            assert!(store.read_storyboard(ep).is_ok());
        }
    }

    #[test]
    fn editing_reordered_shot_keeps_order_media_and_shot_metadata() {
        let dir = tempfile::tempdir().unwrap();
        let store = StoryStore::init(dir.path(), "Manual order").unwrap();
        store
            .replace_storyboard("ep001", vec![shot("shot-1"), shot("shot-2")])
            .unwrap();
        store
            .reorder_shots("ep001", &["shot-2".into(), "shot-1".into()])
            .unwrap();
        let mut edited = store.read_storyboard("ep001").unwrap().shots[0].clone();
        edited.description = "Edited second shot".into();
        edited.state = ShotState::Selected;
        edited.selected = Some("episodes/ep001/shots/shot-2/frame.png".into());
        edited.audio = Some(ShotAudio {
            path: "episodes/ep001/shots/shot-2/audio.wav".into(),
            duration_secs: 2.0,
            task_id: 7,
        });
        let metadata = ShotMeta {
            id: "shot-2".into(),
            prompt: Some("Keep the generation prompt".into()),
            seed: Some(42),
            ..Default::default()
        };
        store.write_shot_meta("ep001", "shot-2", &metadata).unwrap();
        store.upsert_shot("ep001", &edited).unwrap();
        // New shots append even if their id sorts before the existing shots.
        store.upsert_shot("ep001", &shot("shot-0")).unwrap();

        let board = store.read_storyboard("ep001").unwrap();
        assert_eq!(
            board
                .shots
                .iter()
                .map(|s| (s.id.as_str(), s.no))
                .collect::<Vec<_>>(),
            vec![("shot-2", 1), ("shot-1", 2), ("shot-0", 3)]
        );
        assert_eq!(board.shots[0], edited);
        assert_eq!(store.read_shot_meta("ep001", "shot-2").unwrap(), metadata);
    }

    #[test]
    fn storyboard_replacement_preserves_input_order_and_renumbers() {
        let dir = tempfile::tempdir().unwrap();
        let store = StoryStore::init(dir.path(), "Input order").unwrap();
        let mut first = shot("shot-2");
        first.no = 99;
        let board = store
            .replace_storyboard("ep001", vec![first, shot("shot-1")])
            .unwrap();
        assert_eq!(
            board
                .shots
                .iter()
                .map(|s| (s.id.as_str(), s.no))
                .collect::<Vec<_>>(),
            vec![("shot-2", 1), ("shot-1", 2)]
        );
        assert_eq!(store.read_storyboard("ep001").unwrap(), board);
    }

    #[test]
    fn duplicate_storyboard_ids_are_rejected_before_writing() {
        let dir = tempfile::tempdir().unwrap();
        let store = StoryStore::init(dir.path(), "Duplicate ids").unwrap();
        let duplicate = vec![shot("shot-1"), shot("shot-1")];
        assert!(store.replace_storyboard("new", duplicate.clone()).is_err());
        assert!(!store.root().join("episodes/new").exists());
        assert!(store
            .write_storyboard(
                "direct",
                &Storyboard {
                    shots: duplicate.clone()
                }
            )
            .is_err());
        assert!(!store.root().join("episodes/direct").exists());

        let original = store
            .replace_storyboard("existing", vec![shot("shot-2")])
            .unwrap();
        assert!(store.replace_storyboard("existing", duplicate).is_err());
        assert_eq!(store.read_storyboard("existing").unwrap(), original);
        assert!(!store.root().join("episodes/existing/shots/shot-1").exists());
    }

    #[test]
    fn duplicate_reorder_ids_do_not_replace_another_shot() {
        let dir = tempfile::tempdir().unwrap();
        let store = StoryStore::init(dir.path(), "Duplicate reorder").unwrap();
        let original = store
            .replace_storyboard("ep001", vec![shot("shot-1"), shot("shot-2")])
            .unwrap();
        assert!(store
            .reorder_shots("ep001", &["shot-1".into(), "shot-1".into()])
            .is_err());
        assert_eq!(store.read_storyboard("ep001").unwrap(), original);
    }

    #[test]
    fn invalid_episode_and_shot_ids_cannot_write_outside_their_directories() {
        let dir = tempfile::tempdir().unwrap();
        let store = StoryStore::init(dir.path(), "Jail").unwrap();
        for ep in [
            "../outside",
            "..\\outside",
            "/absolute",
            "ep001/nested",
            "ep001:stream",
            "",
        ] {
            assert!(store.write_script(ep, "Escape").is_err(), "accepted {ep:?}");
            assert!(store.read_episode(ep).is_err(), "accepted {ep:?}");
            assert!(store.replace_storyboard(ep, vec![shot("shot-1")]).is_err());
            assert!(store.upsert_shot(ep, &shot("shot-1")).is_err());
        }
        assert!(store
            .replace_storyboard("ep001", vec![shot("../escape")])
            .is_err());
        assert!(store.list_episode_ids().unwrap().is_empty());
        assert!(!dir.path().join("outside").exists());
        assert!(!store.root().join("outside").exists());
    }

    #[test]
    fn init_keeps_existing_series_source_and_user_directories() {
        let dir = tempfile::tempdir().unwrap();
        let store = StoryStore::init(dir.path(), "Original title").unwrap();
        let series = std::fs::read(store.root().join("series.json")).unwrap();
        store.write_script("ep001", "Keep script").unwrap();
        store
            .write_artifact("source/novel.txt", b"Keep novel")
            .unwrap();
        store
            .write_artifact("custom/notes.txt", b"Keep notes")
            .unwrap();

        let reopened = StoryStore::init(dir.path(), "Replacement title").unwrap();
        assert_eq!(
            std::fs::read(reopened.root().join("series.json")).unwrap(),
            series
        );
        assert_eq!(reopened.read_script("ep001").unwrap(), "Keep script");
        assert_eq!(
            std::fs::read(reopened.root().join("source/novel.txt")).unwrap(),
            b"Keep novel"
        );
        assert_eq!(
            std::fs::read(reopened.root().join("custom/notes.txt")).unwrap(),
            b"Keep notes"
        );
    }

    #[test]
    fn init_waits_for_an_existing_folder_writer() {
        use std::sync::mpsc::{channel, RecvTimeoutError};
        use std::time::Duration;

        let dir = tempfile::tempdir().unwrap();
        let store = StoryStore::init(dir.path(), "Original").unwrap();
        let guard = store.write_guard();
        let folder = dir.path().to_path_buf();
        let (started_tx, started_rx) = channel();
        let (finished_tx, finished_rx) = channel();
        let worker = std::thread::spawn(move || {
            started_tx.send(()).unwrap();
            let result = StoryStore::init(&folder, "Concurrent init");
            finished_tx.send(()).unwrap();
            result
        });
        started_rx.recv().unwrap();
        let before_unlock = finished_rx.recv_timeout(Duration::from_millis(100));
        drop(guard);
        worker.join().unwrap().unwrap();
        assert_eq!(before_unlock, Err(RecvTimeoutError::Timeout));
        assert_eq!(store.read_series().unwrap().title, "Original");
    }

    #[test]
    fn original_project_next_action_never_requires_a_novel() {
        let dir = tempfile::tempdir().unwrap();
        let store = StoryStore::init(dir.path(), "Original screenplay").unwrap();
        store
            .write_source_meta(&SourceMeta {
                mode: Some(crate::comic::model::SourceMode::Original),
                ..Default::default()
            })
            .unwrap();
        let overview = store.overview().unwrap();
        assert!(overview.has_source);
        assert!(overview.source_hint.is_none());
        assert_eq!(overview.next_action, Some(NextAction::AdaptScript));
        store.write_script("ep001", "Original script").unwrap();
        assert_eq!(
            store.overview().unwrap().next_action,
            Some(NextAction::CreateStoryboard {
                episode: "ep001".into()
            })
        );
        assert!(!store.root().join("source/novel.txt").exists());
        assert!(!store.root().join("source/chapters.json").exists());
    }

    #[cfg(unix)]
    #[test]
    fn init_and_episode_operations_reject_linked_directories() {
        use std::os::unix::fs::symlink;

        let dir = tempfile::tempdir().unwrap();
        let outside = tempfile::tempdir().unwrap();
        symlink(outside.path(), dir.path().join("story")).unwrap();
        assert!(StoryStore::init(dir.path(), "Linked root").is_err());
        assert_eq!(std::fs::read_dir(outside.path()).unwrap().count(), 0);
        std::fs::remove_file(dir.path().join("story")).unwrap();

        let store = StoryStore::init(dir.path(), "Linked episode").unwrap();
        symlink(outside.path(), store.root().join("episodes/ep001")).unwrap();
        assert!(store.read_episode("ep001").is_err());
        assert!(store.write_script("ep001", "Escape").is_err());
        assert!(store
            .replace_storyboard("ep001", vec![shot("shot-1")])
            .is_err());
        assert_eq!(std::fs::read_dir(outside.path()).unwrap().count(), 0);
    }

    #[test]
    fn ordinary_shot_edits_cannot_overwrite_a_newer_confirmed_video() {
        let dir = tempfile::tempdir().unwrap();
        let store = StoryStore::init(dir.path(), "Video edit race").unwrap();
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
        let clip = |task_id| ShotClip {
            path: format!("episodes/ep001/shots/shot-0001/video/task-{task_id}.mp4"),
            duration_secs: 1.0,
            task_id,
        };
        store
            .bind_shot_video("ep001", "shot-0001", clip(42), None)
            .unwrap();
        let mut stale = store.read_storyboard("ep001").unwrap().shots[0].clone();
        store
            .bind_shot_video("ep001", "shot-0001", clip(43), Some(42))
            .unwrap();
        stale.description = "Updated brief".into();
        store.upsert_shot("ep001", &stale).unwrap();
        let updated = store.read_storyboard("ep001").unwrap().shots.remove(0);
        assert_eq!(updated.description, "Updated brief");
        assert_eq!(updated.video.as_ref().unwrap().task_id, 43);
    }

    #[test]
    fn storyboard_replacement_cannot_inject_or_clear_confirmed_videos() {
        let dir = tempfile::tempdir().unwrap();
        let store = StoryStore::init(dir.path(), "Video board edits").unwrap();
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
        let clip = ShotClip {
            path: "episodes/ep001/shots/shot-0001/video/task-42.mp4".into(),
            duration_secs: 1.0,
            task_id: 42,
        };
        store
            .bind_shot_video("ep001", "shot-0001", clip.clone(), None)
            .unwrap();
        let board = store
            .replace_storyboard(
                "ep001",
                vec![
                    StoryShot {
                        id: "shot-0001".into(),
                        description: "New brief".into(),
                        video: None,
                        ..Default::default()
                    },
                    StoryShot {
                        id: "shot-0002".into(),
                        video: Some(clip),
                        ..Default::default()
                    },
                ],
            )
            .unwrap();
        assert_eq!(board.shots[0].video.as_ref().unwrap().task_id, 42);
        assert!(board.shots[1].video.is_none());
        store
            .upsert_shot(
                "ep001",
                &StoryShot {
                    id: "shot-0003".into(),
                    video: board.shots[0].video.clone(),
                    ..Default::default()
                },
            )
            .unwrap();
        assert!(store.read_storyboard("ep001").unwrap().shots[2]
            .video
            .is_none());
    }

    #[test]
    fn stale_storyboard_writes_cannot_roll_back_a_confirmed_video() {
        let dir = tempfile::tempdir().unwrap();
        let store = StoryStore::init(dir.path(), "Video stale board").unwrap();
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
        let mut stale = store.read_storyboard("ep001").unwrap();
        store
            .bind_shot_video(
                "ep001",
                "shot-0001",
                ShotClip {
                    path: "episodes/ep001/shots/shot-0001/video/task-42.mp4".into(),
                    duration_secs: 1.0,
                    task_id: 42,
                },
                None,
            )
            .unwrap();
        stale.shots[0].state = ShotState::Candidate;
        store.write_storyboard("ep001", &stale).unwrap();
        let updated = store.read_storyboard("ep001").unwrap();
        assert_eq!(updated.shots[0].state, ShotState::Candidate);
        assert_eq!(updated.shots[0].video.as_ref().unwrap().task_id, 42);
    }

    #[test]
    fn clearing_a_video_requires_the_observed_binding() {
        let dir = tempfile::tempdir().unwrap();
        let store = StoryStore::init(dir.path(), "Video unbind").unwrap();
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
        let clip = ShotClip {
            path: "episodes/ep001/shots/shot-0001/video/task-42.mp4".into(),
            duration_secs: 1.0,
            task_id: 42,
        };
        store
            .bind_shot_video("ep001", "shot-0001", clip, None)
            .unwrap();
        // A stale expected binding refuses and keeps the clip.
        let error = store
            .clear_shot_video("ep001", "shot-0001", Some(41))
            .unwrap_err();
        assert!(matches!(error, StoreError::Conflict(_)));
        // A binding that appeared after an unbind confirmation opened refuses.
        let error = store
            .clear_shot_video("ep001", "shot-0001", None)
            .unwrap_err();
        assert!(matches!(error, StoreError::Conflict(_)));
        assert_eq!(
            store.read_storyboard("ep001").unwrap().shots[0]
                .video
                .as_ref()
                .unwrap()
                .task_id,
            42
        );
        store
            .clear_shot_video("ep001", "shot-0001", Some(42))
            .unwrap();
        assert!(store.read_storyboard("ep001").unwrap().shots[0]
            .video
            .is_none());
        // Already unbound: expected null is an idempotent no-op, a stale
        // expectation still refuses.
        store.clear_shot_video("ep001", "shot-0001", None).unwrap();
        let error = store
            .clear_shot_video("ep001", "shot-0001", Some(42))
            .unwrap_err();
        assert!(matches!(error, StoreError::Conflict(_)));
    }

    #[test]
    fn video_binding_rejects_a_stale_expected_task_id() {
        let dir = tempfile::tempdir().unwrap();
        let store = StoryStore::init(dir.path(), "Video CAS").unwrap();
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
        let clip = |task_id| ShotClip {
            path: format!("episodes/ep001/shots/shot-0001/video/task-{task_id}.mp4"),
            duration_secs: 1.0,
            task_id,
        };
        store
            .bind_shot_video("ep001", "shot-0001", clip(42), None)
            .unwrap();

        let error = store
            .bind_shot_video("ep001", "shot-0001", clip(43), Some(41))
            .unwrap_err();
        assert!(matches!(error, StoreError::Conflict(_)));
        assert_eq!(
            store.read_storyboard("ep001").unwrap().shots[0]
                .video
                .as_ref()
                .unwrap()
                .task_id,
            42
        );
        store
            .bind_shot_video("ep001", "shot-0001", clip(43), Some(42))
            .unwrap();
        store
            .bind_shot_video("ep001", "shot-0001", clip(43), Some(42))
            .unwrap();
        assert_eq!(
            store.read_storyboard("ep001").unwrap().shots[0]
                .video
                .as_ref()
                .unwrap()
                .task_id,
            43
        );
    }
}

// ---------------------------------------------------------------------------
// atomic write helpers
// ---------------------------------------------------------------------------

pub fn write_json_atomic<T: Serialize>(path: &Path, value: &T) -> Result<(), std::io::Error> {
    let bytes = serde_json::to_vec_pretty(value)?;
    write_bytes_atomic(path, &bytes)
}

pub fn write_bytes_atomic(path: &Path, bytes: &[u8]) -> Result<(), std::io::Error> {
    let tmp = sibling_tmp_path(path);
    std::fs::write(&tmp, bytes)?;
    // Windows: std::fs::rename replaces an existing destination.
    std::fs::rename(&tmp, path)
}

fn sibling_tmp_path(path: &Path) -> PathBuf {
    let file = path
        .file_name()
        .map(|f| f.to_string_lossy().into_owned())
        .unwrap_or_else(|| "document".to_string());
    let nanos = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map(|d| d.as_nanos())
        .unwrap_or(0);
    path.with_file_name(format!(".{file}.{nanos}.tmp"))
}
