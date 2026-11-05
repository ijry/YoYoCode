//! Task-scoped video preview grants and bounded range serving.
//!
//! A `<video>` element cannot attach codeg's bearer token, so the workbench
//! mints a short-lived grant (authenticated route) and plays the artifact
//! through a grant URL (public route / desktop `codeg-media:` protocol).
//! Every byte request re-validates, in order: the grant's TTL, the task row
//! (same folder, `kind == "video"`, still `succeeded`, artifact unchanged),
//! the folder row, and the story jail — the artifact path never comes from
//! the client, only from the grant, which only ever carries what the task
//! row recorded at issue time.
//!
//! Responses are bounded (`MAX_RANGE_BYTES`): no request path in this module
//! can buffer a whole 256 MiB clip, with or without a `Range` header.

use std::collections::HashMap;
use std::io::{Read, Seek, SeekFrom};
use std::sync::{Mutex, OnceLock};
use std::time::{Duration, SystemTime};

use sea_orm::{DatabaseConnection, EntityTrait};

use crate::comic::paths::{resolve_in_story, validate_slug};
use crate::db::entities::comic_generation_task;
use crate::db::service::folder_service;

/// Largest single response body. The player asks for the rest as it needs it.
pub const MAX_RANGE_BYTES: u64 = 16 * 1024 * 1024;
/// Same artifact ceiling the video verifier enforces.
const MAX_ARTIFACT_BYTES: u64 = 256 * 1024 * 1024;
/// How long a grant may be replayed by the player's range requests.
const GRANT_TTL: Duration = Duration::from_secs(15 * 60);
/// Story-relative shape of a bound video artifact, with the file's task
/// number equal to the row it belongs to.
const ARTIFACT_PATTERN: &str = concat!(
    r"^episodes/([a-z0-9_-]{1,64})/shots/([a-z0-9_-]{1,64})/video/task-(\d+)\.",
    r"(mp4|webm|mkv)$"
);

#[derive(Debug, Clone)]
pub struct VideoPreviewGrant {
    pub folder_id: i32,
    pub task_id: i32,
    pub artifact: String,
    pub expires_at: SystemTime,
}

/// A single bounded response. Callers turn this into their transport's
/// response type (axum for the server, `http::Response` for the desktop
/// protocol).
#[derive(Debug, PartialEq)]
pub struct RangeChunk {
    pub status: u16,
    pub content_type: &'static str,
    pub content_range: Option<String>,
    pub total: u64,
    pub bytes: Vec<u8>,
}

/// Why a preview request got nothing — answered as a plain 404 so callers
/// learn nothing about which check failed.
#[derive(Debug, thiserror::Error)]
#[error("video preview is not available")]
pub struct PreviewDenied;

/// The empty 404 body the desktop protocol answers a denied request with.
pub fn denied_response() -> http::Response<Vec<u8>> {
    http::Response::builder()
        .status(404)
        .header(http::header::CACHE_CONTROL, "no-store")
        .body(Vec::new())
        .expect("static response")
}

fn grants() -> &'static Mutex<HashMap<String, VideoPreviewGrant>> {
    static GRANTS: OnceLock<Mutex<HashMap<String, VideoPreviewGrant>>> = OnceLock::new();
    GRANTS.get_or_init(|| Mutex::new(HashMap::new()))
}

/// Mint a grant for one succeeded video task. The artifact must match the
/// task's own recorded path (including the `task-<id>` file number) and be a
/// jailed regular file within the video size cap. Returns the opaque token
/// and the grant it authorizes.
pub async fn issue(
    conn: &DatabaseConnection,
    folder_id: i32,
    task_id: i32,
) -> Result<(String, VideoPreviewGrant), PreviewDenied> {
    let row = comic_generation_task::Entity::find_by_id(task_id)
        .one(conn)
        .await
        .map_err(|_| PreviewDenied)?
        .ok_or(PreviewDenied)?;
    if row.folder_id != folder_id || row.kind != "video" || row.status != "succeeded" {
        return Err(PreviewDenied);
    }
    let artifact = row.artifact_path.ok_or(PreviewDenied)?;
    if !artifact_matches_task(&artifact, task_id) {
        return Err(PreviewDenied);
    }
    let folder = folder_service::get_folder_by_id(conn, folder_id)
        .await
        .map_err(|_| PreviewDenied)?
        .ok_or(PreviewDenied)?;
    let absolute = resolve_in_story(std::path::Path::new(&folder.path), &artifact)
        .map_err(|_| PreviewDenied)?;
    let metadata = std::fs::metadata(&absolute).map_err(|_| PreviewDenied)?;
    if !metadata.is_file() || metadata.len() == 0 || metadata.len() > MAX_ARTIFACT_BYTES {
        return Err(PreviewDenied);
    }

    let token = uuid::Uuid::new_v4().simple().to_string();
    let grant = VideoPreviewGrant {
        folder_id,
        task_id,
        artifact,
        expires_at: SystemTime::now() + GRANT_TTL,
    };
    {
        let mut grants = grants().lock().expect("preview grants poisoned");
        let now = SystemTime::now();
        grants.retain(|_, grant| grant.expires_at > now);
        grants.insert(token.clone(), grant.clone());
    }
    Ok((token, grant))
}

/// Look up a still-valid grant. Expired entries are dropped on sight.
pub fn take_valid(token: &str) -> Option<VideoPreviewGrant> {
    let mut grants = grants().lock().expect("preview grants poisoned");
    let now = SystemTime::now();
    grants.retain(|_, grant| grant.expires_at > now);
    grants.get(token).cloned()
}

#[cfg(test)]
pub(crate) fn clear_grants_for_tests() {
    grants().lock().expect("preview grants poisoned").clear();
}

/// The artifact shape the task runner writes, with the file number matching
/// the owning task. Anything else — a compose export, another task's file, a
/// path with `..` or Windows separators — is refused before any filesystem
/// access.
pub fn artifact_matches_task(artifact: &str, task_id: i32) -> bool {
    static PATTERN: OnceLock<regex::Regex> = OnceLock::new();
    let pattern = PATTERN.get_or_init(|| regex::Regex::new(ARTIFACT_PATTERN).expect("artifact pattern"));
    let Some(captures) = pattern.captures(artifact) else {
        return false;
    };
    // Slugs are re-validated so a path that only lexically matches cannot
    // smuggle in a component the jail would reject later anyway.
    validate_slug(&captures[1]).is_ok()
        && validate_slug(&captures[2]).is_ok()
        && captures[3]
            .parse::<i32>()
            .is_ok_and(|number| number == task_id)
}

/// What a request's `Range` header asks for. Missing and malformed headers
/// fall back to `Full`, which is still served bounded from byte 0 — never as
/// an unbounded 200.
#[derive(Debug, PartialEq)]
pub(crate) enum ParsedRange {
    Full,
    Unsatisfiable,
    Range(u64, u64),
}

pub(crate) fn parse_range_header(header: Option<&str>, total: u64) -> ParsedRange {
    let Some(spec) = header.and_then(|header| header.trim().strip_prefix("bytes=")) else {
        return ParsedRange::Full;
    };
    if spec.contains(',') || total == 0 {
        return ParsedRange::Full;
    }
    let Some((start, end)) = spec.split_once('-') else {
        return ParsedRange::Full;
    };
    let parsed = match (start.trim(), end.trim()) {
        ("", suffix) => {
            let Ok(n) = suffix.parse::<u64>() else {
                return ParsedRange::Full;
            };
            if n == 0 {
                return ParsedRange::Unsatisfiable;
            }
            (total.saturating_sub(n), total - 1)
        }
        (start, "") => match start.trim().parse::<u64>() {
            Ok(start) => (start, total - 1),
            Err(_) => return ParsedRange::Full,
        },
        (start, end) => match (start.trim().parse::<u64>(), end.trim().parse::<u64>()) {
            (Ok(start), Ok(end)) => (start, end),
            _ => return ParsedRange::Full,
        },
    };
    if parsed.0 > parsed.1 || parsed.0 >= total {
        return ParsedRange::Unsatisfiable;
    }
    ParsedRange::Range(parsed.0, parsed.1.min(total - 1))
}

pub(crate) fn content_type(artifact: &str) -> &'static str {
    let extension = artifact.rsplit('.').next().unwrap_or_default();
    match extension {
        "mp4" | "m4v" => "video/mp4",
        "webm" => "video/webm",
        "mkv" => "video/x-matroska",
        _ => "application/octet-stream",
    }
}

/// Serve one bounded chunk of a granted artifact. The task row and folder are
/// re-read on every request: a task that failed, moved, or was re-pointed
/// invalidates outstanding grants for it.
pub async fn serve(
    conn: &DatabaseConnection,
    token: &str,
    range_header: Option<&str>,
) -> Result<RangeChunk, PreviewDenied> {
    let grant = take_valid(token).ok_or(PreviewDenied)?;
    let row = comic_generation_task::Entity::find_by_id(grant.task_id)
        .one(conn)
        .await
        .map_err(|_| PreviewDenied)?
        .ok_or(PreviewDenied)?;
    if row.folder_id != grant.folder_id
        || row.kind != "video"
        || row.status != "succeeded"
        || row.artifact_path.as_deref() != Some(grant.artifact.as_str())
    {
        return Err(PreviewDenied);
    }
    let folder = folder_service::get_folder_by_id(conn, grant.folder_id)
        .await
        .map_err(|_| PreviewDenied)?
        .ok_or(PreviewDenied)?;
    let artifact = grant.artifact.clone();
    let range_owned = range_header.map(str::to_owned);
    tokio::task::spawn_blocking(move || {
        let absolute = resolve_in_story(std::path::Path::new(&folder.path), &artifact)
            .map_err(|_| PreviewDenied)?;
        read_bounded_chunk(&absolute, &artifact, range_owned.as_deref())
    })
    .await
    .map_err(|_| PreviewDenied)?
}

/// Open through the no-follow final component and read at most
/// `MAX_RANGE_BYTES`. Path components were link-checked by
/// `resolve_in_story` just before this runs.
fn read_bounded_chunk(
    absolute: &std::path::Path,
    artifact: &str,
    range_header: Option<&str>,
) -> Result<RangeChunk, PreviewDenied> {
    let mut file =
        crate::commands::folders::open_no_follow(absolute).map_err(|_| PreviewDenied)?;
    let metadata = file.metadata().map_err(|_| PreviewDenied)?;
    if !metadata.is_file() {
        return Err(PreviewDenied);
    }
    let total = metadata.len();
    if total == 0 || total > MAX_ARTIFACT_BYTES {
        return Err(PreviewDenied);
    }
    let (start, end) = match parse_range_header(range_header, total) {
        ParsedRange::Unsatisfiable => {
            return Ok(RangeChunk {
                status: 416,
                content_type: content_type(artifact),
                content_range: Some(format!("bytes */{total}")),
                total,
                bytes: Vec::new(),
            });
        }
        ParsedRange::Full => (0, total.saturating_sub(1)),
        ParsedRange::Range(start, end) => (start, end),
    };
    let length = (end - start + 1).min(MAX_RANGE_BYTES);
    let end = start + length - 1;
    file.seek(SeekFrom::Start(start)).map_err(|_| PreviewDenied)?;
    let mut bytes = vec![0u8; length as usize];
    file.read_exact(&mut bytes).map_err(|_| PreviewDenied)?;
    Ok(RangeChunk {
        status: 206,
        content_type: content_type(artifact),
        content_range: Some(format!("bytes {start}-{end}/{total}")),
        total,
        bytes,
    })
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::io::Write;

    fn file_with(content: &[u8]) -> (tempfile::TempDir, std::path::PathBuf) {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("task-7.mp4");
        std::fs::File::create(&path)
            .unwrap()
            .write_all(content)
            .unwrap();
        (dir, path)
    }

    #[test]
    fn artifact_shape_pins_the_owning_task_number() {
        assert!(artifact_matches_task(
            "episodes/ep001/shots/shot-0001/video/task-42.mp4",
            42
        ));
        assert!(artifact_matches_task(
            "episodes/ep001/shots/shot-0001/video/task-42.webm",
            42
        ));
        // Wrong task number, wrong kind, export directory, traversal,
        // uppercase slug, and stray extensions are all refused.
        assert!(!artifact_matches_task(
            "episodes/ep001/shots/shot-0001/video/task-43.mp4",
            42
        ));
        assert!(!artifact_matches_task(
            "episodes/ep001/shots/shot-0001/first-frame.mp4",
            42
        ));
        assert!(!artifact_matches_task(
            "episodes/ep001/export/task-42.mp4",
            42
        ));
        assert!(!artifact_matches_task(
            "episodes/../ep001/shots/shot-0001/video/task-42.mp4",
            42
        ));
        assert!(!artifact_matches_task(
            "episodes/EP001/shots/shot-0001/video/task-42.mp4",
            42
        ));
        assert!(!artifact_matches_task(
            "episodes/ep001/shots/shot-0001/video/task-42.exe",
            42
        ));
    }

    #[test]
    fn range_parsing_never_yields_an_unbounded_span() {
        let total = 100u64;
        assert_eq!(parse_range_header(None, total), ParsedRange::Full);
        assert_eq!(
            parse_range_header(Some("bytes=10-19"), total),
            ParsedRange::Range(10, 19)
        );
        assert_eq!(
            parse_range_header(Some("bytes=90-"), total),
            ParsedRange::Range(90, 99)
        );
        assert_eq!(
            parse_range_header(Some("bytes=-10"), total),
            ParsedRange::Range(90, 99)
        );
        assert_eq!(
            parse_range_header(Some("bytes=0-999999"), total),
            ParsedRange::Range(0, 99)
        );
        assert_eq!(
            parse_range_header(Some("bytes=100-"), total),
            ParsedRange::Unsatisfiable
        );
        assert_eq!(
            parse_range_header(Some("bytes=-0"), total),
            ParsedRange::Unsatisfiable
        );
        assert_eq!(
            parse_range_header(Some("bytes=50-40"), total),
            ParsedRange::Unsatisfiable
        );
        // Malformed and multi-range fall back to the bounded full read.
        assert_eq!(
            parse_range_header(Some("bytes=abc"), total),
            ParsedRange::Full
        );
        assert_eq!(
            parse_range_header(Some("bytes=0-9,50-59"), total),
            ParsedRange::Full
        );
    }

    #[tokio::test]
    async fn bounded_chunk_reads_only_the_requested_segment() {
        let content: Vec<u8> = (0..4096u32).map(|i| (i % 251) as u8).collect();
        let (_dir, path) = file_with(&content);
        let chunk = read_bounded_chunk(&path, "episodes/ep001/shots/s/video/task-7.mp4", Some("bytes=100-199")).unwrap();
        assert_eq!(chunk.status, 206);
        assert_eq!(chunk.content_type, "video/mp4");
        assert_eq!(chunk.content_range.as_deref(), Some("bytes 100-199/4096"));
        assert_eq!(chunk.bytes, &content[100..200]);

        let chunk = read_bounded_chunk(&path, "episodes/ep001/shots/s/video/task-7.mp4", Some("bytes=-16")).unwrap();
        assert_eq!(chunk.content_range.as_deref(), Some("bytes 4080-4095/4096"));
        assert_eq!(chunk.bytes, &content[4080..]);

        let chunk = read_bounded_chunk(&path, "episodes/ep001/shots/s/video/task-7.mp4", Some("bytes=9999-")).unwrap();
        assert_eq!(chunk.status, 416);
        assert_eq!(chunk.content_range.as_deref(), Some("bytes */4096"));

        let chunk = read_bounded_chunk(&path, "episodes/ep001/shots/s/video/task-7.mp4", None).unwrap();
        assert_eq!(chunk.status, 206);
        assert_eq!(chunk.bytes.len(), 4096);
    }

    #[tokio::test]
    async fn a_chunk_response_is_hard_capped_even_when_the_range_is_not() {
        let content = vec![7u8; (MAX_RANGE_BYTES + 1024) as usize];
        let (_dir, path) = file_with(&content);
        let chunk = read_bounded_chunk(&path, "episodes/ep001/shots/s/video/task-7.webm", Some("bytes=0-")).unwrap();
        assert_eq!(chunk.status, 206);
        assert_eq!(chunk.bytes.len() as u64, MAX_RANGE_BYTES);
        assert_eq!(chunk.content_type, "video/webm");
        assert_eq!(
            chunk.content_range.as_deref(),
            Some(format!(
                "bytes 0-{}/{total}",
                MAX_RANGE_BYTES - 1,
                total = content.len() as u64
            )
            .as_str())
        );
    }

    #[test]
    fn expired_grants_stop_authorizing_immediately() {
        clear_grants_for_tests();
        let token = uuid::Uuid::new_v4().simple().to_string();
        grants()
            .lock()
            .unwrap()
            .insert(token.clone(), VideoPreviewGrant {
                folder_id: 1,
                task_id: 7,
                artifact: "episodes/ep001/shots/shot-0001/video/task-7.mp4".into(),
                expires_at: SystemTime::now() - Duration::from_secs(1),
            });
        assert!(take_valid(&token).is_none());
    }
}
