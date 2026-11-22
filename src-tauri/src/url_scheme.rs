//! The `yoyocode://` URL scheme and its legacy alias.
//!
//! Links are written as `yoyocode://…`. The pre-rename `codeg://…` form still
//! appears in stored conversations, agent transcripts, saved drafts and model
//! context, so every parser accepts both. Only the OS deep-link *registration*
//! moved to `yoyocode` — see `docs/url-scheme.md`.

/// Scheme new links are written with.
pub const SCHEME: &str = "yoyocode";

/// The pre-rename scheme, still accepted everywhere links are parsed.
pub const LEGACY_SCHEME: &str = "codeg";

/// Regex alternation matching both schemes, for mention scanners built from a
/// format string: a model echoing the legacy format must still route.
pub const SCHEME_ALT: &str = "(?:yoyocode|codeg)";

/// Prefixes accepted when parsing, longest first so `://` wins over `:`.
const PARSE_PREFIXES: [&str; 4] = ["yoyocode://", "yoyocode:", "codeg://", "codeg:"];

/// Strip a scheme prefix (`yoyocode` / `codeg`, with or without `//`), returning
/// the rest of the URL. `None` when the string is not a link in either scheme.
pub fn strip_scheme_prefix(raw: &str) -> Option<&str> {
    PARSE_PREFIXES.iter().find_map(|p| raw.strip_prefix(p))
}

/// Whether `raw` is a link in either scheme.
pub fn is_scheme_uri(raw: &str) -> bool {
    strip_scheme_prefix(raw).is_some()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn strips_both_schemes_with_and_without_slashes() {
        for scheme in [SCHEME, LEGACY_SCHEME] {
            assert_eq!(strip_scheme_prefix(&format!("{scheme}://session/1")), Some("session/1"));
            assert_eq!(strip_scheme_prefix(&format!("{scheme}:session/1")), Some("session/1"));
            assert_eq!(strip_scheme_prefix(&format!("{scheme}://")), Some(""));
            assert_eq!(strip_scheme_prefix(&format!("{scheme}:open")), Some("open"));
            assert!(is_scheme_uri(&format!("{scheme}://session/1")));
        }
        assert_eq!(strip_scheme_prefix("https://session/1"), None);
        assert_eq!(strip_scheme_prefix("yoyocodex://session/1"), None);
        assert_eq!(strip_scheme_prefix("/Applications/codeg.app/Contents/MacOS/codeg"), None);
        assert!(!is_scheme_uri("workspace://deep-link-pending"));
    }
}
