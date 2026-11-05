use sea_orm::entity::prelude::*;

/// One entry of the global media-provider pool used by comic (漫剧) projects.
/// A row is a single vendor/endpoint configuration for one media kind —
/// image, video, or tts — mirroring the `model_provider` shape for chat
/// models. Comic projects reference these rows by id (+ name snapshot) in
/// their `series.json`; switching a project's provider never mutates rows
/// here, and each generation task snapshots what it actually used.
#[derive(Clone, Debug, PartialEq, DeriveEntityModel)]
#[sea_orm(table_name = "media_provider")]
pub struct Model {
    #[sea_orm(primary_key)]
    pub id: i32,
    /// `image` | `video` | `tts`.
    pub kind: String,
    /// Human-facing display name, unique per kind; comic projects fall back
    /// to resolving by name when an id no longer exists (project carried
    /// across machines).
    pub name: String,
    pub api_url: String,
    pub api_key: String,
    pub model: String,
    /// Vendor-specific configuration as JSON — e.g. a ComfyUI workflow
    /// document, a video provider's mode/version switches, extra headers.
    pub extra_json: String,
    pub is_default: bool,
    pub created_at: DateTimeUtc,
    pub updated_at: DateTimeUtc,
}

#[derive(Copy, Clone, Debug, EnumIter, DeriveRelation)]
pub enum Relation {}

impl ActiveModelBehavior for ActiveModel {}
