use sea_orm::entity::prelude::*;

/// One media-generation job for comic (漫剧) production — an image batch for
/// a storyboard shot, a video clip, a TTS take, or an episode compose run.
/// The row is the durable side of the pipeline: it survives restarts so a
/// crashed session or a closed window loses nothing, the worker polls the
/// vendor from it, and every finished artifact is written into the project's
/// `story/` tree (files remain the source of truth). The row also snapshots
/// which provider/model produced it, so switching a project's provider never
/// rewrites history.
#[derive(Clone, Debug, PartialEq, DeriveEntityModel)]
#[sea_orm(table_name = "comic_generation_task")]
pub struct Model {
    #[sea_orm(primary_key)]
    pub id: i32,
    /// Owning project folder (comic projects are folders with a `story/` root).
    pub folder_id: i32,
    /// `image` | `video` | `tts` | `compose`.
    pub kind: String,
    /// `queued` | `running` | `succeeded` | `failed` | `cancelled`.
    pub status: String,
    pub provider_id: Option<i32>,
    /// Name snapshot of the provider at submit time (survives provider deletion).
    pub provider_name: Option<String>,
    pub model: Option<String>,
    /// Provider-agnostic request as JSON (prompt, references, count, seed, ...).
    pub request_json: String,
    /// Provider-specific progress/result payload as JSON (poll state, usage, ...).
    pub result_json: String,
    /// Absolute path of the primary finished artifact, if any.
    pub artifact_path: Option<String>,
    /// Where the artifact belongs — `shot` | `character` | `scene` | `prop` | `episode`.
    pub target_kind: Option<String>,
    /// Story-relative id of the target (e.g. `episodes/ep001/shots/shot-0003`).
    pub target_id: Option<String>,
    /// Classified failure: `content_policy` | `network` | `quota` | `auth` | `bad_request` | `internal`.
    pub error_kind: Option<String>,
    pub error_message: Option<String>,
    /// Candidate artifact paths as a JSON array (multi-candidate image runs).
    pub candidates_json: String,
    pub created_at: DateTimeUtc,
    pub updated_at: DateTimeUtc,
}

#[derive(Copy, Clone, Debug, EnumIter, DeriveRelation)]
pub enum Relation {}

impl ActiveModelBehavior for ActiveModel {}
