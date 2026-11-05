//! Shot prompt assembly — the consistency core of the pipeline.
//!
//! A shot's image prompt is not freeform: it is assembled from the project's
//! style bible, the referenced cards' prompt fragments (characters with their
//! outfit, scene, props), and the shot's own description — so every frame of
//! the same character/scene shares the same identity language, and the seed
//! anchors the look across candidates. The assembled prompt is persisted to
//! the shot's `shot.json` (`prompt`, `seed`) so regeneration and the
//! workbench echo the exact words that produced an image.

use serde::{Deserialize, Serialize};
use serde_json::Value;

use super::model::{CharacterCard, StoryShot, Storyboard};
use super::store::{StoreError, StoryStore};

/// The assembled prompt plus the reference set the adapters may attach.
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub struct ShotPrompt {
    pub episode: String,
    pub shot: String,
    pub prompt: String,
    pub negative_prompt: Option<String>,
    pub seed: Option<u64>,
    /// Story-relative reference images (turnarounds first, then scene refs).
    pub references: Vec<String>,
}

/// Assemble (and persist) the prompt for one shot.
pub fn build_shot_prompt(
    store: &StoryStore,
    episode: &str,
    shot_id: &str,
) -> Result<ShotPrompt, StoreError> {
    let series = store.read_series()?;
    let board: Storyboard = store.read_storyboard(episode)?;
    let shot: StoryShot = board
        .shots
        .iter()
        .find(|s| s.id == shot_id)
        .cloned()
        .ok_or_else(|| StoreError::Invalid(format!("episode {episode} has no shot {shot_id}")))?;
    let meta = store.read_shot_meta(episode, shot_id)?;

    let mut sections: Vec<String> = Vec::new();

    // 1. Style bible — inline summary first, then bible/*.md contents (capped).
    if let Some(bible) = series.style_bible.as_deref() {
        let trimmed = bible.trim();
        if !trimmed.is_empty() {
            sections.push(trimmed.to_string());
        }
    }
    for rel in series.bible_refs.iter().take(4) {
        if let Ok(path) = store.resolve(rel) {
            if let Ok(bytes) = std::fs::read(path) {
                let text = String::from_utf8_lossy(&bytes);
                let capped: String = text.chars().take(1_500).collect();
                let capped = capped.trim();
                if !capped.is_empty() {
                    sections.push(capped.to_string());
                }
            }
        }
    }

    // 2. Referenced cards — characters (@outfit), scene, props.
    let mut references: Vec<String> = Vec::new();
    for character in &shot.refs.characters {
        let Ok(card) = store.read_character(&character.slug) else {
            continue;
        };
        let fragment = outfit_fragment(&card, &character.outfit);
        if !fragment.is_empty() {
            sections.push(fragment);
        }
        // Turnaround images are the identity anchors; outfit views win when set.
        let views: [Option<&String>; 3] = if let Some(outfit) = card
            .outfits
            .iter()
            .find(|o| o.slug == character.outfit && !character.outfit.is_empty())
        {
            [
                outfit.front.as_ref(),
                outfit.side_45.as_ref(),
                outfit.side.as_ref(),
            ]
        } else {
            [
                card.turnaround.front.as_ref(),
                card.turnaround.side_45.as_ref(),
                card.turnaround.side.as_ref(),
            ]
        };
        for rel in views.into_iter().flatten() {
            references.push(rel.clone());
        }
        for rel in card.refs.iter().take(2) {
            references.push(rel.clone());
        }
    }
    if let Some(scene_slug) = &shot.refs.scene {
        if let Ok(card) = store.read_scene(scene_slug) {
            if !card.prompt_fragment.is_empty() {
                sections.push(card.prompt_fragment.clone());
            }
            for rel in card.refs.iter().take(2) {
                references.push(rel.clone());
            }
        }
    }
    for prop_slug in shot.refs.props.iter().take(4) {
        if let Ok(card) = store.read_prop(prop_slug) {
            if !card.prompt_fragment.is_empty() {
                sections.push(card.prompt_fragment.clone());
            }
            if let Some(rel) = card.refs.first() {
                references.push(rel.clone());
            }
        }
    }
    references.truncate(8);

    // 3. The frame itself — shot size, the description, the dialogue context.
    let mut frame = String::new();
    if let Some(size) = shot.shot_size.as_deref() {
        frame.push_str(size);
        frame.push('。');
    }
    frame.push_str(shot.description.trim());
    if !shot.dialogue.is_empty() {
        let lines: Vec<String> = shot
            .dialogue
            .iter()
            .take(3)
            .map(|d| {
                if let Some(character) = &d.character {
                    format!("{character}: {}", d.text)
                } else {
                    d.text.clone()
                }
            })
            .collect();
        frame.push_str(" 对话：");
        frame.push_str(&lines.join("；"));
    }
    sections.push(frame);

    // 4. Camera + transition hints (video stage reads them too).
    if let Some(camera) = shot.camera.as_deref() {
        if !camera.is_empty() {
            sections.push(format!("运镜：{camera}"));
        }
    }

    let seed = shot
        .refs
        .characters
        .first()
        .and_then(|c| store.read_character(&c.slug).ok())
        .and_then(|card| card.seed)
        .or(series.global_seed)
        .or(meta.seed);

    let prompt = sections.join("\n\n");
    let assembled = ShotPrompt {
        episode: episode.to_string(),
        shot: shot_id.to_string(),
        prompt,
        negative_prompt: meta.negative_prompt.clone(),
        seed,
        references,
    };

    // Persist so regeneration and the workbench echo the exact words.
    store.write_shot_meta(
        episode,
        shot_id,
        &super::model::ShotMeta {
            id: shot_id.to_string(),
            prompt: Some(assembled.prompt.clone()),
            seed: assembled.seed,
            negative_prompt: assembled.negative_prompt.clone(),
            generations: meta.generations,
        },
    )?;
    // draft → prompted (the pipeline state machine's own edge).
    mark_prompted(store, episode, shot_id)?;

    Ok(assembled)
}

fn outfit_fragment(card: &CharacterCard, outfit_slug: &str) -> String {
    let mut parts = Vec::new();
    if !card.name.is_empty() {
        parts.push(card.name.clone());
    }
    if !card.description.is_empty() {
        parts.push(card.description.clone());
    }
    if !outfit_slug.is_empty() {
        if let Some(outfit) = card.outfits.iter().find(|o| o.slug == outfit_slug) {
            if !outfit.prompt_fragment.is_empty() {
                parts.push(outfit.prompt_fragment.clone());
            }
        }
    } else if !card.prompt_fragment.is_empty() {
        parts.push(card.prompt_fragment.clone());
    }
    parts.join("，")
}

/// `draft` → `prompted` on the storyboard copy of the shot.
fn mark_prompted(store: &StoryStore, episode: &str, shot_id: &str) -> Result<(), StoreError> {
    use super::model::ShotState;
    let mut board: Storyboard = store.read_storyboard(episode)?;
    let Some(shot) = board.shots.iter_mut().find(|s| s.id == shot_id) else {
        return Ok(());
    };
    if matches!(shot.state, ShotState::Draft) {
        shot.state = ShotState::Prompted;
        store.write_storyboard(episode, &board)?;
    }
    Ok(())
}

/// The generation request a shot implies (prompt + provider-agnostic knobs).
pub fn request_for_shot(
    store: &StoryStore,
    episode: &str,
    shot_id: &str,
    count: u32,
    seed_override: Option<u64>,
    extra: Value,
) -> Result<(super::provider::GenerationRequest, ShotPrompt), StoreError> {
    let assembled = build_shot_prompt(store, episode, shot_id)?;
    let req = super::provider::GenerationRequest {
        prompt: assembled.prompt.clone(),
        negative_prompt: assembled.negative_prompt.clone(),
        seed: seed_override.or(assembled.seed),
        count: count.clamp(1, 64),
        reference_paths: assembled.references.clone(),
        reference_images: Vec::new(),
        // Generation size is a provider capability, not the final export size.
        size: None,
        speech: None,
        first_frame_path: None,
        first_frame: None,
        extra,
    };
    Ok((req, assembled))
}
