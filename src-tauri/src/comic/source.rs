//! Novel-source ingestion: register, transcode, chapter-split, window-read.
//!
//! Chinese web novels mostly ship as GBK/GB18030 txt; [`ingest`] detects the
//! encoding, converts to UTF-8, writes `source/novel.txt`, splits chapters
//! (heading-pattern first, fixed-size windows as fallback), and records both
//! `source.json` and `chapters.json`. Chapter offsets are byte offsets into
//! the stored UTF-8 file, so [`read_window`] can slice without re-splitting.
//!
//! The split report is surfaced to the LLM/user for confirmation — web-novel
//! formatting varies wildly (卷/章/番外, anti-piracy watermarks), so
//! [`re_split`] accepts adjusted knobs (window size, min heading length)
//! without re-uploading.

use serde::{Deserialize, Serialize};

use super::model::{Chapter, ChapterIndex, SourceMeta, SourceMode};
use super::paths::resolve_in_story;
use super::store::{write_bytes_atomic, StoreError, StoryStore};

/// How many chapter titles the ingest report previews (the LLM relays these
/// to the user so they can sanity-check the split).
const PREVIEW_TITLES: usize = 8;
/// Fallback window size when no heading pattern matches (chars, not bytes).
const FALLBACK_WINDOW_CHARS: usize = 6000;

/// Knobs for a (re)split. Defaults fit most CJK web novels.
#[derive(Clone, Debug, Serialize, Deserialize)]
#[serde(default)]
pub struct SplitOptions {
    /// Min chars a heading line may carry after the pattern (filters body
    /// lines that merely mention "第X章").
    pub max_heading_len: usize,
    /// Fallback window size when the heading pattern does not match.
    pub fallback_window_chars: usize,
}

impl Default for SplitOptions {
    fn default() -> Self {
        Self {
            max_heading_len: 40,
            fallback_window_chars: FALLBACK_WINDOW_CHARS,
        }
    }
}

/// The report returned by [`ingest`] / [`re_split`] — also the payload the
/// LLM echoes back to the user for split confirmation.
#[derive(Clone, Debug, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub struct SourceReport {
    pub mode: String,
    pub title: Option<String>,
    pub encoding: String,
    pub total_chars: u64,
    pub chapter_count: u32,
    /// `pattern` when chapter headings matched, `fallback_window` otherwise.
    pub strategy: String,
    pub preview_titles: Vec<String>,
}

/// What to ingest. Exactly one variant per registration path.
pub enum SourceInput {
    /// Raw bytes from a session attachment or workbench upload.
    Upload {
        title: Option<String>,
        bytes: Vec<u8>,
    },
    /// A local path chosen by the user; the file is copied *into* the story
    /// tree — the project never references the original again.
    ExternalPath { path: String, title: Option<String> },
    /// Original screenplay — records the mode, clears any previous novel.
    Original,
}

/// Register the project's novel source (or mark it original) and split it.
pub fn ingest(
    store: &StoryStore,
    input: SourceInput,
    options: &SplitOptions,
) -> Result<SourceReport, StoreError> {
    match input {
        SourceInput::Original => {
            let meta = SourceMeta {
                mode: Some(SourceMode::Original),
                title: None,
                ..Default::default()
            };
            store.write_source_meta(&meta)?;
            Ok(SourceReport {
                mode: "original".into(),
                title: None,
                encoding: "utf-8".into(),
                total_chars: 0,
                chapter_count: 0,
                strategy: "single".into(),
                preview_titles: Vec::new(),
            })
        }
        SourceInput::Upload { title, bytes } => ingest_bytes(store, bytes, title, None, options),
        SourceInput::ExternalPath { path, title } => {
            let src = std::path::PathBuf::from(&path);
            if !src.is_file() {
                return Err(StoreError::Invalid(format!("novel file not found: {path}")));
            }
            let bytes = std::fs::read(&src)?;
            ingest_bytes(store, bytes, title, Some(path), options)
        }
    }
}

fn ingest_bytes(
    store: &StoryStore,
    bytes: Vec<u8>,
    title: Option<String>,
    external_path: Option<String>,
    options: &SplitOptions,
) -> Result<SourceReport, StoreError> {
    let (text, encoding) = decode_to_utf8(&bytes);

    // Write the canonical UTF-8 text inside the jail.
    let novel_rel = "source/novel.txt";
    let novel_path =
        resolve_in_story(store.folder_path(), novel_rel).map_err(StoreError::Invalid)?;
    if let Some(parent) = novel_path.parent() {
        std::fs::create_dir_all(parent)?;
    }
    write_bytes_atomic(&novel_path, text.as_bytes())
        .map_err(|e| StoreError::Invalid(format!("write {novel_rel}: {e}")))?;

    let index = split_chapters(&text, options);
    store.write_chapters(&index)?;

    let total_chars = text.chars().count() as u64;
    let from_external = external_path.is_some();
    let meta = SourceMeta {
        mode: Some(if from_external {
            SourceMode::ExternalPath
        } else {
            SourceMode::Upload
        }),
        novel_file: Some(novel_rel.to_string()),
        external_path,
        title: title.clone(),
        char_count: total_chars,
        chapter_count: index.chapters.len() as u32,
        encoding: Some(encoding.clone()),
    };
    store.write_source_meta(&meta)?;

    Ok(SourceReport {
        mode: if from_external {
            "external_path".into()
        } else {
            "upload".into()
        },
        title,
        encoding,
        total_chars,
        chapter_count: index.chapters.len() as u32,
        strategy: index.strategy,
        preview_titles: index
            .chapters
            .iter()
            .take(PREVIEW_TITLES)
            .map(|c| c.title.clone())
            .collect(),
    })
}

/// Re-split the stored novel with new knobs (no re-upload).
pub fn re_split(store: &StoryStore, options: &SplitOptions) -> Result<SourceReport, StoreError> {
    let text = store.read_novel_text()?;
    let index = split_chapters(&text, options);
    store.write_chapters(&index)?;

    let mut meta = store.read_source_meta()?;
    meta.chapter_count = index.chapters.len() as u32;
    store.write_source_meta(&meta)?;

    Ok(SourceReport {
        mode: "resplit".into(),
        title: meta.title,
        encoding: meta.encoding.unwrap_or_else(|| "utf-8".into()),
        total_chars: meta.char_count,
        chapter_count: index.chapters.len() as u32,
        strategy: index.strategy,
        preview_titles: index
            .chapters
            .iter()
            .take(PREVIEW_TITLES)
            .map(|c| c.title.clone())
            .collect(),
    })
}

/// Decode raw bytes to UTF-8. Order: UTF-8 BOM → strict UTF-8 → GB18030
/// (superset of GBK/GB2312) → lossy UTF-16 heuristics. Returns the decoded
/// text and a short encoding label for provenance.
fn decode_to_utf8(bytes: &[u8]) -> (String, String) {
    if bytes.starts_with(&[0xEF, 0xBB, 0xBF]) {
        return (
            String::from_utf8_lossy(&bytes[3..]).into_owned(),
            "utf-8-sig".into(),
        );
    }
    if bytes.starts_with(&[0xFF, 0xFE]) {
        let (decoded, _, had_errors) = encoding_rs::UTF_16LE.decode(bytes);
        return (
            decoded.into_owned(),
            if had_errors {
                "utf-16-lossy".into()
            } else {
                "utf-16le".into()
            },
        );
    }
    if bytes.starts_with(&[0xFE, 0xFF]) {
        let (decoded, _, had_errors) = encoding_rs::UTF_16BE.decode(bytes);
        return (
            decoded.into_owned(),
            if had_errors {
                "utf-16-lossy".into()
            } else {
                "utf-16be".into()
            },
        );
    }
    if let Ok(text) = std::str::from_utf8(bytes) {
        return (text.to_string(), "utf-8".into());
    }
    // GB18030 never fails to decode (every byte sequence maps), so this is
    // the final CJK answer; `decode` reports had_errors for mojibake.
    let (decoded, _, had_errors) = encoding_rs::GB18030.decode(bytes);
    (
        decoded.into_owned(),
        if had_errors {
            "gb18030-lossy".into()
        } else {
            "gb18030".into()
        },
    )
}

/// Split into chapters. Strategy `pattern` when at least two heading lines
/// match; otherwise `fallback_window` fixed-size char windows; a short text
/// (< fallback window) becomes a single chapter.
pub fn split_chapters(text: &str, options: &SplitOptions) -> ChapterIndex {
    let total_chars = text.chars().count() as u64;
    let headings = find_headings(text, options.max_heading_len);
    if headings.len() >= 2 {
        return build_pattern_index(text, headings);
    }
    let window = options.fallback_window_chars.clamp(1, 200_000);
    if total_chars as usize <= window {
        return single_chapter(text, "single", total_chars);
    }

    // Fallback: fixed char windows, breaking at the last blank-line boundary
    // inside each window when one exists (avoids cutting mid-paragraph).
    let mut chapters = Vec::new();
    let mut start_char: usize = 0;
    let mut no: u32 = 0;
    let char_count = text.chars().count();
    let char_offsets: Vec<usize> = text
        .char_indices()
        .map(|(i, _)| i)
        .chain(std::iter::once(text.len()))
        .collect();
    while start_char < char_count {
        no += 1;
        let mut end_char = (start_char + window).min(char_count);
        if end_char < char_count {
            // Look back up to 500 chars for a paragraph break.
            let look_from = start_char.max(end_char.saturating_sub(500));
            let slice = &text[char_offsets[look_from]..char_offsets[end_char]];
            if let Some(pos) = slice.rfind("\n\n") {
                let boundary = look_from + slice[..pos + 2].chars().count();
                if boundary > start_char {
                    end_char = boundary;
                }
            }
        }
        chapters.push(Chapter {
            no,
            title: format!("片段 {no}"),
            start_offset: char_offsets[start_char] as u64,
            end_offset: char_offsets[end_char] as u64,
            chars: (end_char - start_char) as u64,
        });
        start_char = end_char;
    }
    ChapterIndex {
        strategy: "fallback_window".into(),
        chapters,
    }
}

fn single_chapter(text: &str, strategy: &str, total_chars: u64) -> ChapterIndex {
    ChapterIndex {
        strategy: strategy.into(),
        chapters: vec![Chapter {
            no: 1,
            title: "全文".into(),
            start_offset: 0,
            end_offset: text.len() as u64,
            chars: total_chars,
        }],
    }
}

/// A heading line: `第一章`, `第1234章 标题`, `第一百零五回 …`, `Chapter 12: …`,
/// `卷一 …`, `番外 …`. Matched line-start, allowing leading whitespace/BOM.
fn heading_re() -> &'static regex::Regex {
    static RE: std::sync::OnceLock<regex::Regex> = std::sync::OnceLock::new();
    RE.get_or_init(|| {
        regex::Regex::new(
            r"(?m)^[ \t\u{FEFF}]*(第[0-9〇零一二两三四五六七八九十百千万]+[章节回卷集部篇][^\n]{0,80}|Chapter\s+\d+[^\n]{0,80}|卷[0-9一二三四五六七八九十百千]+[^\n]{0,80}|番外[^\n]{0,80})[ \t]*$",
        )
        .expect("chapter heading regex")
    })
}

fn find_headings(text: &str, max_heading_len: usize) -> Vec<(usize, usize, String)> {
    // (byte_start_of_line, byte_end_of_line, title)
    let mut out = Vec::new();
    for cap in heading_re().captures_iter(text) {
        let whole = cap.get(0).expect("whole match");
        let title = cap
            .get(1)
            .map(|m| m.as_str())
            .unwrap_or("")
            .trim()
            .to_string();
        // Long "headings" are body sentences mentioning 第X章; drop them.
        if title.chars().count() > max_heading_len {
            continue;
        }
        out.push((whole.start(), whole.end(), title));
    }
    out
}

fn build_pattern_index(text: &str, headings: Vec<(usize, usize, String)>) -> ChapterIndex {
    let mut chapters = Vec::new();
    for (idx, (start, end, title)) in headings.iter().enumerate() {
        let body_start = *end;
        let body_end = headings
            .get(idx + 1)
            .map(|(next_start, _, _)| *next_start)
            .unwrap_or(text.len());
        if body_end <= body_start {
            continue; // heading with no body yet (trailing)
        }
        let body = &text[body_start..body_end];
        chapters.push(Chapter {
            no: (idx + 1) as u32,
            title: title.clone(),
            start_offset: *start as u64,
            end_offset: body_end as u64,
            chars: body.chars().count() as u64,
        });
    }
    ChapterIndex {
        strategy: "pattern".into(),
        chapters,
    }
}

/// A bounded read request. Either a chapter range (1-based, inclusive) or a
/// global char window; always capped, so tool results stay small.
#[derive(Clone, Debug, Serialize, Deserialize)]
#[serde(default)]
pub struct ReadWindow {
    pub from_chapter: Option<u32>,
    pub to_chapter: Option<u32>,
    /// Max chars returned (hard cap 20_000).
    pub max_chars: usize,
    /// Skip this many chars inside the selected span (pagination within a
    /// huge chapter).
    pub offset_chars: usize,
}

impl Default for ReadWindow {
    fn default() -> Self {
        Self {
            from_chapter: None,
            to_chapter: None,
            max_chars: 8_000,
            offset_chars: 0,
        }
    }
}

pub const READ_WINDOW_HARD_CAP: usize = 20_000;

/// Read a chapter-bounded, size-capped window of the novel. Returns the text
/// plus whether more remains (pagination hint for the LLM).
pub fn read_window(store: &StoryStore, req: &ReadWindow) -> Result<WindowRead, StoreError> {
    let index = store.read_chapters()?;
    let text = store.read_novel_text()?;
    if index.chapters.is_empty() {
        return Err(StoreError::Invalid("novel not ingested yet".into()));
    }

    let from = req.from_chapter.unwrap_or(1).max(1);
    let to = req.to_chapter.unwrap_or(from).max(from);
    let byte_start = index
        .chapters
        .iter()
        .find(|c| c.no == from)
        .map(|c| c.start_offset as usize)
        .ok_or_else(|| StoreError::Invalid(format!("chapter {from} not found")))?;
    let byte_end = index
        .chapters
        .iter()
        .filter(|c| c.no >= from && c.no <= to)
        .map(|c| c.end_offset as usize)
        .max()
        .unwrap_or(byte_start);
    let byte_end = byte_end.min(text.len());
    let byte_start = byte_start.min(byte_end);

    let span = text.get(byte_start..byte_end).ok_or_else(|| {
        StoreError::Invalid(
            "chapter index is not aligned with the UTF-8 source; re-import or re-split the source"
                .into(),
        )
    })?;
    let chars: Vec<char> = span.chars().collect();
    let offset = req.offset_chars.min(chars.len());
    let max = req.max_chars.clamp(1, READ_WINDOW_HARD_CAP);
    let take_end = (offset + max).min(chars.len());
    let body: String = chars[offset..take_end].iter().collect();

    Ok(WindowRead {
        from_chapter: from,
        to_chapter: to,
        offset_chars: offset,
        returned_chars: body.chars().count(),
        has_more: take_end < chars.len(),
        next_offset: if take_end < chars.len() {
            Some(take_end)
        } else {
            None
        },
        text: body,
    })
}

#[derive(Clone, Debug, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub struct WindowRead {
    pub from_chapter: u32,
    pub to_chapter: u32,
    pub offset_chars: usize,
    pub returned_chars: usize,
    pub has_more: bool,
    pub next_offset: Option<usize>,
    pub text: String,
}

/// Chapter outline for planning (`comic_get_source_outline`).
pub fn outline(store: &StoryStore, limit: usize) -> Result<Vec<Chapter>, StoreError> {
    let index = store.read_chapters()?;
    Ok(index.chapters.into_iter().take(limit.max(1)).collect())
}

#[cfg(test)]
mod tests {
    use super::super::store::write_json_atomic;
    use super::*;

    #[test]
    fn decodes_utf8_gbk_and_bom() {
        let (t, e) = decode_to_utf8("第一章 你好".as_bytes());
        assert_eq!(e, "utf-8");
        assert!(t.starts_with("第一章"));
        let gbk = encoding_rs::GB18030.encode("第二章 风起").0.into_owned();
        let (t, e) = decode_to_utf8(&gbk);
        assert_eq!(e, "gb18030");
        assert!(t.starts_with("第二章"));
        let mut with_bom = vec![0xEFu8, 0xBB, 0xBF];
        with_bom.extend_from_slice("序章".as_bytes());
        let (t, e) = decode_to_utf8(&with_bom);
        assert_eq!(e, "utf-8-sig");
        assert!(t.starts_with("序章"));
    }

    #[test]
    fn splits_by_pattern() {
        let text =
            "第一章 起风\n\n内容A。\n\n第二章 入局\n\n内容B。\n\n第一百零五回 收网\n\n内容C。";
        let idx = split_chapters(text, &SplitOptions::default());
        assert_eq!(idx.strategy, "pattern");
        assert_eq!(idx.chapters.len(), 3);
        assert_eq!(idx.chapters[0].title, "第一章 起风");
        assert_eq!(idx.chapters[2].title, "第一百零五回 收网");
        assert!(idx.chapters[0].chars > 0);
    }

    #[test]
    fn body_sentence_mentioning_chapter_is_not_a_heading() {
        let text = &format!(
            "第一章 开端\n\n{}第一章这个词出现在正文里，这一行很长很长，绝对超过四十个字符的限制了吧，应该被忽略才对。\n\n第二章 继续\n\n正文B。",
            "前情提要："
        );
        let idx = split_chapters(text, &SplitOptions::default());
        assert_eq!(idx.chapters.len(), 2);
    }

    #[test]
    fn falls_back_to_windows() {
        let body = "这是一段没有章节标题的长文。".repeat(600);
        let idx = split_chapters(&body, &SplitOptions::default());
        assert_eq!(idx.strategy, "fallback_window");
        assert!(idx.chapters.len() > 1);
        // Windows tile the text without overlap or gaps.
        let total: u64 = idx.chapters.iter().map(|c| c.chars).sum();
        assert_eq!(total, body.chars().count() as u64);
    }

    #[test]
    fn short_text_is_single_chapter() {
        let idx = split_chapters("短文。", &SplitOptions::default());
        assert_eq!(idx.strategy, "single");
        assert_eq!(idx.chapters.len(), 1);
    }

    #[test]
    fn window_read_caps_and_paginates() {
        let text = "第一章 满页\n".to_string() + &"字".repeat(30_000);
        let idx = split_chapters(&text, &SplitOptions::default());
        // 30k chars > 6000 window → pattern with 1 heading? No: only one
        // heading, so falls back to windows; use pattern index directly.
        let mut forced = ChapterIndex {
            strategy: "pattern".into(),
            chapters: vec![Chapter {
                no: 1,
                title: "第一章 满页".into(),
                start_offset: 0,
                end_offset: text.len() as u64,
                chars: text.chars().count() as u64,
            }],
        };
        let _ = idx;
        forced.chapters[0].end_offset = text.len() as u64;
        let store_dir = std::env::temp_dir().join(format!("comic-src-test-{}", std::process::id()));
        std::fs::create_dir_all(store_dir.join("story/source")).unwrap();
        std::fs::write(store_dir.join("story/source/novel.txt"), &text).unwrap();
        write_json_atomic(&store_dir.join("story/source/chapters.json"), &forced).unwrap();
        write_json_atomic(
            &store_dir.join("story/source/source.json"),
            &SourceMeta {
                mode: Some(SourceMode::Upload),
                novel_file: Some("source/novel.txt".into()),
                chapter_count: 1,
                char_count: text.chars().count() as u64,
                ..Default::default()
            },
        )
        .unwrap();
        let store = StoryStore::open(&store_dir).unwrap();

        let read = read_window(
            &store,
            &ReadWindow {
                from_chapter: Some(1),
                to_chapter: None,
                max_chars: 100,
                offset_chars: 0,
            },
        )
        .unwrap();
        assert_eq!(read.returned_chars, 100);
        assert!(read.has_more);
        assert_eq!(read.next_offset, Some(100));

        let read2 = read_window(
            &store,
            &ReadWindow {
                from_chapter: Some(1),
                to_chapter: None,
                max_chars: 100,
                offset_chars: read.next_offset.unwrap(),
            },
        )
        .unwrap();
        assert_eq!(read2.offset_chars, 100);
        std::fs::remove_dir_all(&store_dir).ok();
    }
}
