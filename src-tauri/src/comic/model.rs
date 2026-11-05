//! Serde models for the `story/` tree. Every struct here is serialized
//! pretty-printed into its JSON document; files are the source of truth, so
//! all fields tolerate absence (defaults) to keep hand-edits and older
//! projects loadable.

use serde::{Deserialize, Serialize};

// ---------------------------------------------------------------------------
// series.json — project config
// ---------------------------------------------------------------------------

/// A reference to a row of the global media-provider pool. Both id and name
/// are stored: resolution prefers the id, and falls back to the name when a
/// project is carried to another machine whose pool has different ids.
#[derive(Clone, Debug, Default, Serialize, Deserialize, PartialEq)]
pub struct ProviderRef {
    pub id: i32,
    pub name: String,
}

#[derive(Clone, Debug, Default, Serialize, Deserialize, PartialEq)]
#[serde(default)]
pub struct ProviderRefs {
    pub image: Option<ProviderRef>,
    pub video: Option<ProviderRef>,
    pub tts: Option<ProviderRef>,
}

impl ProviderRefs {
    pub fn get(&self, kind: &str) -> Option<&ProviderRef> {
        match kind {
            "image" => self.image.as_ref(),
            "video" => self.video.as_ref(),
            "tts" => self.tts.as_ref(),
            _ => None,
        }
    }

    pub fn set(&mut self, kind: &str, provider: Option<ProviderRef>) -> Result<(), String> {
        match kind {
            "image" => self.image = provider,
            "video" => self.video = provider,
            "tts" => self.tts = provider,
            other => return Err(format!("unknown provider kind: {other}")),
        }
        Ok(())
    }
}

/// Unselected candidate cleanup. `keep_count` caps how many losing
/// candidates each shot keeps once a selection is bound (0 = keep all).
#[derive(Clone, Debug, Serialize, Deserialize, PartialEq)]
#[serde(default)]
pub struct CandidateRetention {
    pub keep_count: u32,
    pub max_age_days: u32,
}

impl Default for CandidateRetention {
    fn default() -> Self {
        Self {
            keep_count: 8,
            max_age_days: 14,
        }
    }
}

/// `story/series.json`.
#[derive(Clone, Debug, Default, Serialize, Deserialize, PartialEq)]
#[serde(default)]
pub struct SeriesConfig {
    pub title: String,
    /// Free-text note about the adaptation origin (novel title/author, ...).
    pub adapted_from: Option<String>,
    /// Inline style bible summary; long-form rules live in `bible/*.md`.
    pub style_bible: Option<String>,
    /// Story-relative paths of `bible/*.md` documents to inject into prompts.
    pub bible_refs: Vec<String>,
    pub global_seed: Option<u64>,
    /// Default candidate count for image generation (production default 4).
    pub default_candidate_count: u32,
    /// Cost guardrail: max images one generation request may ask for.
    pub max_images_per_request: u32,
    /// Cost guardrail: max concurrent vendor jobs the worker runs per provider.
    pub max_concurrent_tasks: u32,
    pub providers: ProviderRefs,
    pub candidate_retention: CandidateRetention,
}

impl SeriesConfig {
    /// Production defaults applied on top of `Default` for new projects.
    pub fn with_defaults(title: String) -> Self {
        Self {
            title,
            default_candidate_count: 4,
            max_images_per_request: 12,
            max_concurrent_tasks: 2,
            candidate_retention: CandidateRetention::default(),
            ..Default::default()
        }
    }
}

// ---------------------------------------------------------------------------
// source/ — the adapted-from novel
// ---------------------------------------------------------------------------

#[derive(Clone, Copy, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum SourceMode {
    /// Uploaded into the project (session attachment or workbench upload).
    Upload,
    /// Copied in from a local path chosen by the user.
    ExternalPath,
    /// Original screenplay — no novel source; never prompt for one.
    Original,
}

/// `story/source/source.json`.
#[derive(Clone, Debug, Default, Serialize, Deserialize, PartialEq)]
#[serde(default)]
pub struct SourceMeta {
    pub mode: Option<SourceMode>,
    /// Story-relative path of the ingested novel text (`source/novel.txt`).
    pub novel_file: Option<String>,
    /// The external path it was copied from, when known (provenance only —
    /// the copy inside the project is authoritative).
    pub external_path: Option<String>,
    pub title: Option<String>,
    pub char_count: u64,
    pub chapter_count: u32,
    /// Detected text encoding of the original bytes (`utf-8`, `gb18030`, ...).
    pub encoding: Option<String>,
}

/// One chapter in `story/source/chapters.json`. Offsets are **byte offsets
/// into the UTF-8 `novel.txt`** so a window read can seek without re-parsing.
#[derive(Clone, Debug, Serialize, Deserialize, PartialEq)]
pub struct Chapter {
    pub no: u32,
    pub title: String,
    pub start_offset: u64,
    pub end_offset: u64,
    pub chars: u64,
}

#[derive(Clone, Debug, Default, Serialize, Deserialize, PartialEq)]
#[serde(default)]
pub struct ChapterIndex {
    /// How the split was produced: `pattern` | `fallback_window` | `single`.
    pub strategy: String,
    pub chapters: Vec<Chapter>,
}

// ---------------------------------------------------------------------------
// episodes/ep001 — episode.json / script.md / storyboard.json
// ---------------------------------------------------------------------------

#[derive(Clone, Copy, Debug, Default, Eq, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum EpisodeStatus {
    #[default]
    Draft,
    Scripted,
    Storyboarded,
    Rendering,
    Done,
}

/// `story/episodes/<ep>/episode.json`.
#[derive(Clone, Debug, Default, Serialize, Deserialize, PartialEq)]
#[serde(default)]
pub struct EpisodeMeta {
    pub id: String,
    pub title: String,
    pub synopsis: String,
    pub status: EpisodeStatus,
    /// Chapter numbers this episode adapts (0 when original).
    pub source_chapters: Vec<u32>,
}

/// One dialogue line inside a shot.
#[derive(Clone, Debug, Default, Serialize, Deserialize, PartialEq)]
pub struct DialogueLine {
    /// Character slug, when the line belongs to a card.
    pub character: Option<String>,
    pub text: String,
}

/// Character reference with an outfit qualifier: `xiao-ming@school`.
#[derive(Clone, Debug, Default, Serialize, Deserialize, PartialEq)]
pub struct CharacterRef {
    pub slug: String,
    /// Outfit slug within the character card; empty = default look.
    #[serde(default)]
    pub outfit: String,
}

#[derive(Clone, Debug, Default, Serialize, Deserialize, PartialEq)]
#[serde(default)]
pub struct ShotRefs {
    pub characters: Vec<CharacterRef>,
    pub scene: Option<String>,
    pub props: Vec<String>,
}

#[derive(Clone, Copy, Debug, Default, Eq, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum ShotState {
    /// Placeholder, not yet prompted.
    #[default]
    Draft,
    /// Prompt built (comic_build_shot_prompt), not yet generated.
    Prompted,
    /// Generation in flight.
    Generating,
    /// Candidates exist, none bound yet.
    Candidate,
    /// A candidate is bound as the shot image.
    Selected,
    /// Video clip in flight / bound (`clip.mp4`).
    Rendering,
    /// Video + audio ready for compose.
    Done,
}

#[derive(Clone, Debug, Serialize, Deserialize, PartialEq)]
pub struct ShotAudio {
    pub path: String,
    pub duration_secs: f64,
    pub task_id: i32,
}

/// One shot inside `storyboard.json`. Heavy per-shot state (prompt, seed,
/// generation history, artifact paths) lives in the shot's own `shot.json`;
/// the storyboard keeps only what the board renders.
#[derive(Clone, Debug, Default, Serialize, Deserialize, PartialEq)]
#[serde(default)]
pub struct StoryShot {
    pub id: String,
    pub no: u32,
    /// What the frame shows (the visual brief).
    pub description: String,
    pub dialogue: Vec<DialogueLine>,
    pub duration_secs: f32,
    /// Camera move for the video stage (推/拉/摇/移/static, ...).
    pub camera: Option<String>,
    /// Shot size (远景/全景/中景/近景/特写, ...).
    pub shot_size: Option<String>,
    /// Transition into the next shot.
    pub transition: Option<String>,
    pub refs: ShotRefs,
    pub state: ShotState,
    /// Story-relative path of the bound image (`.../candidates/cand-02.png`
    /// or `.../first-frame.png`); empty while unbound.
    pub selected: Option<String>,
    pub audio: Option<ShotAudio>,
    pub video: Option<ShotClip>,
}

/// A shot's bound generated video clip (`video/task-<id>.mp4`), measured at
/// bind time. The composer extends the still-frame duration to fit it.
#[derive(Clone, Debug, Serialize, Deserialize, PartialEq)]
pub struct ShotClip {
    pub path: String,
    pub duration_secs: f64,
    pub task_id: i32,
}

/// `story/episodes/<ep>/storyboard.json`.
#[derive(Clone, Debug, Default, Serialize, Deserialize, PartialEq)]
#[serde(default)]
pub struct Storyboard {
    pub shots: Vec<StoryShot>,
}

/// One record in a shot's generation history — the task-level snapshot that
/// survives provider switches (files keep the full provenance).
#[derive(Clone, Debug, Default, Serialize, Deserialize, PartialEq)]
#[serde(default)]
pub struct GenerationRecord {
    pub task_id: i32,
    pub kind: String,
    pub provider_id: Option<i32>,
    pub provider_name: Option<String>,
    pub model: Option<String>,
    pub prompt: Option<String>,
    pub seed: Option<u64>,
    pub at: String,
    pub parameters: serde_json::Value,
}

/// `story/episodes/<ep>/shots/<shot>/shot.json`.
#[derive(Clone, Debug, Default, Serialize, Deserialize, PartialEq)]
#[serde(default)]
pub struct ShotMeta {
    pub id: String,
    /// Last prompt built for this shot (echoed back on regeneration).
    pub prompt: Option<String>,
    pub seed: Option<u64>,
    /// Negative prompt, when the vendor supports one.
    pub negative_prompt: Option<String>,
    pub generations: Vec<GenerationRecord>,
}

// ---------------------------------------------------------------------------
// characters/<slug> — character.json (+ scenes/, props/ analogues)
// ---------------------------------------------------------------------------

/// One outfit of a character — its own prompt fragment and turnaround, since
/// wardrobe changes are per-look in comic production.
#[derive(Clone, Debug, Default, Serialize, Deserialize, PartialEq)]
#[serde(default)]
pub struct Outfit {
    pub slug: String,
    pub name: String,
    pub prompt_fragment: String,
    /// Story-relative images, mirroring the card-level turnaround keys.
    pub front: Option<String>,
    pub side_45: Option<String>,
    pub side: Option<String>,
}

/// The standard three-view turnaround (正面 / 45° 侧 / 正侧).
#[derive(Clone, Debug, Default, Serialize, Deserialize, PartialEq)]
#[serde(default)]
pub struct Turnaround {
    pub front: Option<String>,
    pub side_45: Option<String>,
    pub side: Option<String>,
}

/// `story/characters/<slug>/character.json`.
#[derive(Clone, Debug, Default, Serialize, Deserialize, PartialEq)]
#[serde(default)]
pub struct CharacterCard {
    pub slug: String,
    pub name: String,
    /// Physical description (age, build, hair, ...).
    pub description: String,
    pub personality: String,
    /// Prompt fragment injected into every shot featuring this character.
    pub prompt_fragment: String,
    pub seed: Option<u64>,
    /// Default-look turnaround.
    pub turnaround: Turnaround,
    pub outfits: Vec<Outfit>,
    /// Story-relative expression sheet images.
    pub expressions: Vec<String>,
    /// Extra reference images (story-relative).
    pub refs: Vec<String>,
}

/// `story/scenes/<slug>/scene.json`.
#[derive(Clone, Debug, Default, Serialize, Deserialize, PartialEq)]
#[serde(default)]
pub struct SceneCard {
    pub slug: String,
    pub name: String,
    pub description: String,
    pub prompt_fragment: String,
    pub refs: Vec<String>,
}

/// `story/props/<slug>/prop.json`.
#[derive(Clone, Debug, Default, Serialize, Deserialize, PartialEq)]
#[serde(default)]
pub struct PropCard {
    pub slug: String,
    pub name: String,
    pub description: String,
    pub prompt_fragment: String,
    pub refs: Vec<String>,
}

// ---------------------------------------------------------------------------
// Aggregated views returned to the workbench and MCP tools
// ---------------------------------------------------------------------------

#[derive(Clone, Debug, Default, Serialize, Deserialize)]
#[serde(default)]
pub struct EpisodeSummary {
    pub id: String,
    pub title: String,
    pub status: EpisodeStatus,
    pub shots_total: u32,
    /// Shots that reached `selected` or beyond.
    pub shots_done: u32,
}

#[derive(Clone, Debug, Default, Serialize, Deserialize)]
#[serde(default)]
pub struct AssetSummary {
    pub kind: String,
    pub slug: String,
    pub name: String,
    pub has_ref_images: bool,
}

/// What the pipeline suggests next — derived purely from file state, so a
/// fresh session (or a crashed one's successor) resumes with zero context.
#[derive(Clone, Debug, Serialize, Deserialize, PartialEq)]
#[serde(tag = "action", rename_all = "snake_case")]
pub enum NextAction {
    /// No source registered and the project is not marked original.
    ProvideSource,
    /// Source exists but no episode has a script yet.
    AdaptScript,
    /// Script exists but character/scene cards referenced by it are missing.
    DesignAssets,
    /// Some episode has no storyboard yet.
    CreateStoryboard { episode: String },
    /// Episodes with unbound shots exist.
    GenerateImages { episode: String, pending_shots: u32 },
    /// Bound shots exist but no video clips yet.
    GenerateVideos { episode: String, pending_shots: u32 },
    /// An episode is fully rendered but not composed.
    ComposeEpisode { episode: String },
    /// Everything done (or nothing to do).
    None,
}

/// `comic_get_project` / workbench overview payload.
#[derive(Clone, Debug, Default, Serialize, Deserialize)]
#[serde(default)]
pub struct ProjectOverview {
    pub series: Option<SeriesConfig>,
    pub has_source: bool,
    /// Present when `has_source` is false: the exact sentence the LLM should
    /// lead with when the user asks for an adaptation.
    pub source_hint: Option<String>,
    pub source: Option<SourceMeta>,
    pub episodes: Vec<EpisodeSummary>,
    pub characters: Vec<AssetSummary>,
    pub scenes: Vec<AssetSummary>,
    pub props: Vec<AssetSummary>,
    pub next_action: Option<NextAction>,
}
