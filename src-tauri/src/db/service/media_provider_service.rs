//! Persistence for the global media-provider pool (comic 漫剧 media
//! generation endpoints). Rows are referenced by comic projects'
//! `series.json` provider pointers; generation tasks snapshot what they used,
//! so deleting a row never rewrites history — projects fall back to resolving
//! by name, and a name miss surfaces as a value-level refusal at request time.

use chrono::Utc;
use sea_orm::{
    ActiveModelTrait, ActiveValue::NotSet, ColumnTrait, DatabaseConnection, EntityTrait,
    IntoActiveModel, QueryFilter, QueryOrder, Set,
};

use crate::db::entities::media_provider;
use crate::db::error::DbError;

/// The media kinds a provider row may serve.
pub const KINDS: [&str; 3] = ["image", "video", "tts"];

pub fn validate_kind(kind: &str) -> Result<(), DbError> {
    if KINDS.contains(&kind) {
        Ok(())
    } else {
        Err(DbError::Migration(format!(
            "media provider kind must be one of {KINDS:?}: {kind}"
        )))
    }
}

pub async fn list_all(conn: &DatabaseConnection) -> Result<Vec<media_provider::Model>, DbError> {
    Ok(media_provider::Entity::find()
        .order_by_asc(media_provider::Column::Kind)
        .order_by_asc(media_provider::Column::Name)
        .all(conn)
        .await?)
}

pub async fn list_by_kind(
    conn: &DatabaseConnection,
    kind: &str,
) -> Result<Vec<media_provider::Model>, DbError> {
    validate_kind(kind)?;
    Ok(media_provider::Entity::find()
        .filter(media_provider::Column::Kind.eq(kind))
        .order_by_asc(media_provider::Column::Name)
        .all(conn)
        .await?)
}

pub async fn get_by_id(
    conn: &DatabaseConnection,
    id: i32,
) -> Result<Option<media_provider::Model>, DbError> {
    Ok(media_provider::Entity::find_by_id(id).one(conn).await?)
}

/// Resolve a project's provider pointer: id first, name fallback (a project
/// carried across machines), `None` when neither matches.
pub async fn resolve_ref(
    conn: &DatabaseConnection,
    kind: &str,
    id: i32,
    name: &str,
) -> Result<Option<media_provider::Model>, DbError> {
    validate_kind(kind)?;
    if let Some(row) = get_by_id(conn, id).await? {
        if row.kind == kind {
            return Ok(Some(row));
        }
    }
    Ok(media_provider::Entity::find()
        .filter(media_provider::Column::Kind.eq(kind))
        .filter(media_provider::Column::Name.eq(name))
        .one(conn)
        .await?)
}

/// The default provider for a kind, when one is flagged.
pub async fn default_for_kind(
    conn: &DatabaseConnection,
    kind: &str,
) -> Result<Option<media_provider::Model>, DbError> {
    validate_kind(kind)?;
    Ok(media_provider::Entity::find()
        .filter(media_provider::Column::Kind.eq(kind))
        .filter(media_provider::Column::IsDefault.eq(true))
        .one(conn)
        .await?)
}

/// Fields for creating a pool entry. A params struct rather than 8 adjacent
/// arguments (clippy's count and, honestly, readability).
#[derive(Debug, Clone)]
pub struct NewMediaProvider {
    pub kind: String,
    pub name: String,
    pub api_url: String,
    pub api_key: String,
    pub model: String,
    pub extra_json: String,
    pub is_default: bool,
}

/// Per-field update patch; `None` = unchanged.
#[derive(Debug, Default, Clone)]
pub struct MediaProviderPatch {
    pub name: Option<String>,
    pub api_url: Option<String>,
    pub api_key: Option<String>,
    pub model: Option<String>,
    pub extra_json: Option<String>,
    pub is_default: Option<bool>,
}

pub async fn create(
    conn: &DatabaseConnection,
    params: NewMediaProvider,
) -> Result<media_provider::Model, DbError> {
    validate_kind(&params.kind)?;
    let now = Utc::now();
    let active = media_provider::ActiveModel {
        id: NotSet,
        kind: Set(params.kind),
        name: Set(params.name),
        api_url: Set(params.api_url),
        api_key: Set(params.api_key),
        model: Set(params.model),
        extra_json: Set(params.extra_json),
        is_default: Set(params.is_default),
        created_at: Set(now),
        updated_at: Set(now),
    };
    Ok(active.insert(conn).await?)
}

/// Update a row; `None` fields stay. `is_default` toggling clears the flag on
/// the kind's previous default so `(kind)` keeps at most one default.
pub async fn update(
    conn: &DatabaseConnection,
    id: i32,
    patch: MediaProviderPatch,
) -> Result<media_provider::Model, DbError> {
    let row = media_provider::Entity::find_by_id(id)
        .one(conn)
        .await?
        .ok_or_else(|| DbError::Migration(format!("media provider not found: {id}")))?;

    if let Some(true) = patch.is_default {
        if let Some(current) = default_for_kind(conn, &row.kind).await? {
            if current.id != id {
                let mut other = current.into_active_model();
                other.is_default = Set(false);
                other.updated_at = Set(Utc::now());
                other.update(conn).await?;
            }
        }
    }

    let mut active = row.into_active_model();
    if let Some(v) = patch.name {
        active.name = Set(v);
    }
    if let Some(v) = patch.api_url {
        active.api_url = Set(v);
    }
    if let Some(v) = patch.api_key {
        active.api_key = Set(v);
    }
    if let Some(v) = patch.model {
        active.model = Set(v);
    }
    if let Some(v) = patch.extra_json {
        active.extra_json = Set(v);
    }
    if let Some(v) = patch.is_default {
        active.is_default = Set(v);
    }
    active.updated_at = Set(Utc::now());
    Ok(active.update(conn).await?)
}

pub async fn delete(conn: &DatabaseConnection, id: i32) -> Result<(), DbError> {
    media_provider::Entity::delete_by_id(id).exec(conn).await?;
    Ok(())
}
