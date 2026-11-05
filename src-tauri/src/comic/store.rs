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

use std::collections::HashMap;
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
        let root = story_root(folder_path);
        std::fs::create_dir_all(&root)?;
        for dir in [
            "bible",
            "source",
            "episodes",
            "characters",
            "scenes",
            "props",
        ] {
            std::fs::create_dir_all(root.join(dir))?;
        }
        let series_path = root.join("series.json");
        if !series_path.exists() {
            let config = SeriesConfig::with_defaults(title.to_string());
            write_json_atomic(&series_path, &config)?;
        }
        Self::open(folder_path)
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
        self.read_json(&format!("episodes/{ep}/episode.json"))
    }

    pub fn write_episode(&self, ep: &str, meta: &EpisodeMeta) -> Result<(), StoreError> {
        let _guard = self.write_guard();
        validate_slug(ep).map_err(StoreError::Invalid)?;
        self.write_json(&format!("episodes/{ep}/episode.json"), meta)
    }

    /// Create or replace an episode; also creates its `shots/` directory.
    pub fn upsert_episode(&self, meta: &EpisodeMeta) -> Result<(), StoreError> {
        let _guard = self.write_guard();
        validate_slug(&meta.id).map_err(StoreError::Invalid)?;
        let rel = format!("episodes/{}/episode.json", meta.id);
        self.write_json(&rel, meta)?;
        let shots_dir = self.resolve(&format!("episodes/{}/shots", meta.id))?;
        std::fs::create_dir_all(shots_dir)?;
        Ok(())
    }

    pub fn read_script(&self, ep: &str) -> Result<String, StoreError> {
        let path = self.resolve(&format!("episodes/{ep}/script.md"))?;
        let bytes = std::fs::read(&path)
            .map_err(|e| StoreError::Invalid(format!("read episodes/{ep}/script.md: {e}")))?;
        Ok(String::from_utf8_lossy(&bytes).into_owned())
    }

    pub fn write_script(&self, ep: &str, script: &str) -> Result<(), StoreError> {
        let _guard = self.write_guard();
        let path = self.resolve(&format!("episodes/{ep}/script.md"))?;
        if let Some(parent) = path.parent() {
            std::fs::create_dir_all(parent)?;
        }
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

    fn write_storyboard_locked(&self, ep: &str, board: &Storyboard) -> Result<(), StoreError> {
        self.write_json(&format!("episodes/{ep}/storyboard.json"), board)
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

    /// Validate and upsert one shot within an episode's storyboard,
    /// renumbering `no` to stay ordered. Reference slugs are checked against
    /// existing cards; unknown references are a value-level error.
    pub fn upsert_shot(&self, ep: &str, shot: &StoryShot) -> Result<(), StoreError> {
        let _guard = self.write_guard();
        let mut board = self.read_storyboard(ep)?;
        validate_slug(&shot.id).map_err(StoreError::Invalid)?;
        self.validate_shot_refs(shot)?;
        let mut updated = shot.clone();
        self.preserve_shot_videos_locked(ep, std::slice::from_mut(&mut updated))?;
        match board.shots.iter_mut().find(|s| s.id == shot.id) {
            Some(existing) => *existing = updated,
            None => board.shots.push(updated),
        }
        board.shots.sort_by_key(|s| s.id.clone());
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
        mut shots: Vec<StoryShot>,
    ) -> Result<Storyboard, StoreError> {
        let _guard = self.write_guard();
        for shot in &shots {
            validate_slug(&shot.id).map_err(StoreError::Invalid)?;
            self.validate_shot_refs(shot)?;
        }
        self.preserve_shot_videos_locked(ep, &mut shots)?;
        shots.sort_by(|a, b| a.id.cmp(&b.id));
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
    /// order. `comic_upsert_storyboard` and `upsert_shot` renumber by id
    /// sort; this is the display-order override the board's drag-and-drop
    /// writes through.
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
    use crate::comic::model::{EpisodeMeta, ShotClip};

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
