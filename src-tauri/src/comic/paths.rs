//! Story-tree path resolution and the story jail.
//!
//! A comic project's resources live under `<folder>/story/`. Every write —
//! from MCP comic tools, Tauri commands, or web handlers — resolves its
//! target through [`resolve_in_story`], which rejects absolute paths, `..`
//! escapes, and anything that normalizes outside the root. The one deliberate
//! exception is reading an external novel path at registration time
//! ([`source`]), which copies the file *into* the jail before anything else
//! touches it.

use std::path::{Component, Path, PathBuf};

/// The well-known directory name inside a comic project folder.
pub const STORY_DIR_NAME: &str = "story";

/// True when `folder_path` looks like a comic project — the `story/` root
/// exists. This is the gate for conditional MCP comic-tool injection and for
/// showing the comic workbench entry.
pub fn is_comic_project(folder_path: &Path) -> bool {
    folder_path.join(STORY_DIR_NAME).is_dir()
}

/// The story root of a comic project.
pub fn story_root(folder_path: &Path) -> PathBuf {
    folder_path.join(STORY_DIR_NAME)
}

/// Resolve a story-relative reference to an absolute path, jailed inside the
/// story root. Rejects empty/absolute inputs and any path that escapes the
/// root via `..` (before or after symlink-free normalization; component-level
/// check, no filesystem canonicalization, so a missing file still resolves).
pub fn resolve_in_story(folder_path: &Path, rel: &str) -> Result<PathBuf, String> {
    let rel = rel.trim().replace('\\', "/");
    if rel.is_empty() {
        return Err("empty story-relative path".into());
    }
    let rel_path = Path::new(&rel);
    if rel_path.is_absolute() || rel.starts_with('/') {
        return Err(format!("absolute path is not allowed inside story/: {rel}"));
    }
    let mut clean = PathBuf::new();
    for comp in rel_path.components() {
        match comp {
            Component::Normal(part) => {
                let part = part.to_string_lossy();
                let stem = part
                    .split('.')
                    .next()
                    .unwrap_or_default()
                    .to_ascii_uppercase();
                if part.contains([':', '\0', '<', '>', '|', '?', '*', '"'])
                    || part.ends_with(['.', ' '])
                    || matches!(stem.as_str(), "CON" | "PRN" | "AUX" | "NUL")
                    || (stem.len() == 4
                        && (stem.starts_with("COM") || stem.starts_with("LPT"))
                        && matches!(stem.as_bytes()[3], b'1'..=b'9'))
                {
                    return Err(format!("invalid story path component: {part}"));
                }
                clean.push(part.into_owned());
            }
            Component::CurDir => continue,
            // `..` (and any Windows prefix/root component) is an escape attempt.
            _ => return Err(format!("path escapes the story root: {rel}")),
        }
    }
    if clean.as_os_str().is_empty() {
        return Err(format!("path resolves to the story root itself: {rel}"));
    }
    let root = story_root(folder_path);
    let target = root.join(clean);
    let relative = target
        .strip_prefix(&root)
        .map_err(|e| e.to_string())?
        .to_path_buf();
    let mut current = root;
    // A lexical jail alone follows directory symlinks and Windows junctions.
    // Reject every existing reparse point before a read or write reaches it.
    check_not_link(&current)?;
    for component in relative.components() {
        current.push(component);
        check_not_link(&current)?;
    }
    Ok(target)
}

fn check_not_link(path: &Path) -> Result<(), String> {
    match std::fs::symlink_metadata(path) {
        Ok(metadata) => {
            #[cfg(windows)]
            let reparse = {
                use std::os::windows::fs::MetadataExt;
                metadata.file_attributes() & 0x400 != 0
            };
            #[cfg(not(windows))]
            let reparse = false;
            if metadata.file_type().is_symlink() || reparse {
                return Err(format!(
                    "links are not allowed in a comic resource path: {}",
                    path.display()
                ));
            }
            Ok(())
        }
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => Ok(()),
        Err(error) => Err(error.to_string()),
    }
}

/// Validate a slug used as a directory name (`ep001`, `char-xiao-ming`, ...).
/// Lowercase letters, digits, `-`, `_`; 1..=64 chars; no leading/trailing
/// separator. Slugs become directories, so this is also the jail's first line.
pub fn validate_slug(slug: &str) -> Result<(), String> {
    if slug.is_empty() || slug.len() > 64 {
        return Err(format!("slug length must be 1..=64: {slug:?}"));
    }
    let ok = slug
        .chars()
        .all(|c| c.is_ascii_lowercase() || c.is_ascii_digit() || c == '-' || c == '_');
    if !ok {
        return Err(format!(
            "slug may only contain lowercase letters, digits, '-' and '_': {slug:?}"
        ));
    }
    if slug.starts_with('-') || slug.starts_with('.') || slug.ends_with('-') {
        return Err(format!("slug may not start/end with '-' or '.': {slug:?}"));
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    fn folder() -> PathBuf {
        PathBuf::from("D:/proj")
    }

    #[test]
    fn resolves_normal_relative_paths() {
        let p = resolve_in_story(&folder(), "episodes/ep001/shot.json").unwrap();
        assert_eq!(p, PathBuf::from("D:/proj/story/episodes/ep001/shot.json"));
    }

    #[test]
    fn normalizes_windows_separators_and_dot() {
        let p = resolve_in_story(&folder(), "characters\\abc\\./ref/a.png").unwrap();
        assert_eq!(p, PathBuf::from("D:/proj/story/characters/abc/ref/a.png"));
    }

    #[test]
    fn rejects_escape_and_absolute() {
        assert!(resolve_in_story(&folder(), "../outside.txt").is_err());
        assert!(resolve_in_story(&folder(), "a/../../outside.txt").is_err());
        assert!(resolve_in_story(&folder(), "D:/absolute.png").is_err());
        assert!(resolve_in_story(&folder(), "/absolute.png").is_err());
        assert!(resolve_in_story(&folder(), "").is_err());
        assert!(resolve_in_story(&folder(), ".").is_err());
    }

    #[test]
    fn rejects_windows_device_and_stream_names_on_every_platform() {
        for rel in [
            "characters/a.png:secret",
            "C:relative",
            "assets/CON",
            "assets/nul.png",
            "assets/dir.",
        ] {
            assert!(resolve_in_story(&folder(), rel).is_err(), "accepted {rel}");
        }
    }

    #[test]
    fn slug_rules() {
        assert!(validate_slug("ep001").is_ok());
        assert!(validate_slug("char-xiao-ming_2").is_ok());
        assert!(validate_slug("Big").is_err());
        assert!(validate_slug("a/b").is_err());
        assert!(validate_slug("-lead").is_err());
        assert!(validate_slug("").is_err());
    }
}
