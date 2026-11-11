//! Comic (漫剧) production: a project's `story/` tree is the source of truth
//! for a full novel→script→storyboard→keyframe→video→compose pipeline.
//!
//! Layout of one comic project (a folder whose directory contains `story/`):
//!
//! ```text
//! story/
//!   series.json          project config (title, style bible refs, provider refs, guardrails)
//!   bible/*.md           style/worldbuilding notes the LLM may read directly
//!   source/              the adapted-from novel: novel.txt + source.json + chapters.json
//!   episodes/ep001/      episode.json + script.md + storyboard.json
//!     shots/shot-0001/   shot.json + candidates/*.png + first-frame.png + clip.mp4 + audio/
//!     export/ep001.mp4
//!   characters/<slug>/   character.json (outfits[]) + turnaround/*.png + expressions/
//!   scenes/<slug>/       scene.json + ref/*.png
//!   props/<slug>/        prop.json + ref/*.png
//! ```
//!
//! Every resource reference stored inside JSON documents is story-relative
//! (`episodes/ep001/shots/shot-0003/candidates/cand-02.png`), so a project
//! moves or syncs intact. [`store`] is the single write entry point — MCP
//! comic tools, Tauri commands, and web handlers all go through it, behind a
//! per-folder write mutex; [`paths`] keeps every path jailed inside `story/`.
//!
//! The database only holds what files cannot: the global media-provider pool
//! ([`crate::db::entities::media_provider`]) and durable generation tasks
//! ([`crate::db::entities::comic_generation_task`]).

pub mod audio;
pub mod compose;
pub mod compose_task;
mod media_tools;
pub mod model;
pub mod paths;
pub mod preview;
pub mod prompt;
pub mod provider;
pub mod source;
pub mod speech;
pub mod store;
pub mod task;
pub mod video;
