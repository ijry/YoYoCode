"""Render a seamless breathing-jellyfish GIF from the YoYoCode brand mark.

The silhouette comes from src-tauri/icons/icon.svg (the same traced jellyfish
used by the app icon and the macOS menu-bar template). All motion is driven by
sin/cos of one phase over an integer number of frames, so the last frame flows
back into the first with no jump.

    python3 scripts/jellyfish-breathe.gen.py

Requires Pillow only. The deliverables (transparent animated WebP and its
poster) are written to public/logo/animation/; the opaque GIF and the contact
sheets are scratch output in the system temp directory.
"""

import math
import re
import tempfile
from pathlib import Path

from PIL import Image, ImageChops, ImageDraw, ImageFilter

SCRIPT_DIR = Path(__file__).resolve().parent
REPO = SCRIPT_DIR.parent
SVG = REPO / "src-tauri/icons/icon.svg"
ASSETS = REPO / "public/logo/animation"
TMP = Path(tempfile.gettempdir())
OUT = TMP / "jellyfish-breathe.gif"
POSTER = TMP / "jellyfish-breathe-poster.png"
SHEET = TMP / "jellyfish-breathe-sheet.png"
ALPHA_WEBP = ASSETS / "jellyfish-breathe-alpha.webp"
ALPHA_POSTER = ASSETS / "jellyfish-breathe-alpha-poster.png"

SIZE = 480
FRAMES = 36
DURATION = 80
SS = 2  # supersampling for smooth polygon edges

C_TOP = (48, 221, 253)
C_MID = (104, 195, 252)
C_BOT = (159, 169, 252)
BG_TOP = (5, 16, 32)
BG_BOT = (9, 42, 68)

# SVG artwork -> canvas. The mark spans y=31..479 in the 512 viewBox.
ART_SCALE = 0.80
ART_CX, ART_CY = 256.0, 255.0
CANVAS_CX, CANVAS_CY = SIZE / 2, SIZE / 2 - 8

_PATH_TAG = re.compile(r"<path\b([^>]*)>", re.IGNORECASE | re.DOTALL)
_FILL = re.compile(r'\bfill="([^"]+)"', re.IGNORECASE)
_D = re.compile(r'\bd="([^"]+)"', re.IGNORECASE | re.DOTALL)
_TOKEN = re.compile(r"[A-Za-z]|[-+]?(?:\d*\.\d+|\d+)(?:[eE][-+]?\d+)?")
_COMMAND = re.compile(r"[A-Za-z]")


def lerp(a, b, t):
    return tuple(int(round(x + (y - x) * t)) for x, y in zip(a, b))


def smoothstep(a, b, x):
    t = max(0.0, min(1.0, (x - a) / (b - a)))
    return t * t * (3.0 - 2.0 * t)


def parse_points(d):
    tokens = _TOKEN.findall(d)
    pts, i = [], 0
    while i < len(tokens):
        if _COMMAND.fullmatch(tokens[i]):
            i += 1
            continue
        pts.append((float(tokens[i]), float(tokens[i + 1])))
        i += 2
    return pts


def read_artwork():
    svg = SVG.read_text(encoding="utf-8")
    paths = []
    for attrs in _PATH_TAG.findall(svg):
        d = _D.search(attrs)
        if not d:
            continue
        fill = _FILL.search(attrs)
        paths.append((fill.group(1) if fill else "", parse_points(d.group(1))))
    body = next(p for f, p in paths if "url(" in f)
    highlight = next(p for f, p in paths if f == "#ffffff")
    return body, highlight


BODY, HIGHLIGHT = read_artwork()


def deform(pt, phase):
    """A continuous deformation field: no hard split between bell and tentacles,
    so the single traced polygon stays intact and never self-intersects."""
    x, y = pt
    breath = math.sin(phase)
    w_bell = 1.0 - smoothstep(190.0, 315.0, y)
    # Influence starts right below the bell and grows toward the tips.
    w_tent = smoothstep(230.0, 475.0, y) ** 1.25

    # The bell widens and flattens as it relaxes, then narrows and tallens as
    # it contracts. This is the visible "breath".
    sx = 1.0 + 0.115 * breath * w_bell
    sy = 1.0 - 0.075 * breath * w_bell
    nx = ART_CX + (x - ART_CX) * sx
    ny = 210.0 + (y - 210.0) * sy

    # A travelling wave, not a rigid sway: phase lags with depth so the bend
    # visibly runs down each tentacle, while `spread` desynchronises the
    # neighbouring arms. The tips also lengthen as the bell contracts.
    lag = (y - 230.0) / 52.0
    spread = (x - ART_CX) / 165.0
    nx += 20.0 * w_tent * math.sin(phase - 0.6 - lag + spread)
    nx += 5.0 * w_tent * math.sin(phase - lag * 0.6)
    ny += 9.0 * w_tent * math.sin(phase - 1.1 - lag)
    ny += 7.0 * math.cos(phase)
    return nx, ny


def to_canvas(pt):
    x, y = pt
    return (
        (x - ART_CX) * ART_SCALE + CANVAS_CX,
        (y - ART_CY) * ART_SCALE + CANVAS_CY,
    )


def make_background():
    bg = Image.new("RGBA", (SIZE, SIZE))
    d = ImageDraw.Draw(bg)
    for y in range(SIZE):
        t = y / (SIZE - 1)
        d.line([(0, y), (SIZE, y)], fill=(*lerp(BG_TOP, BG_BOT, t), 255))
    # A soft shaft of light from above, blurred into the deep-water gradient.
    glow = Image.new("RGBA", (SIZE, SIZE), (0, 0, 0, 0))
    ImageDraw.Draw(glow).ellipse(
        (SIZE * 0.12, -SIZE * 0.38, SIZE * 0.88, SIZE * 0.58),
        fill=(45, 125, 195, 70),
    )
    glow = glow.filter(ImageFilter.GaussianBlur(55))
    return Image.alpha_composite(bg, glow)


def make_gradient():
    grad = Image.new("RGBA", (SIZE, SIZE))
    d = ImageDraw.Draw(grad)
    for y in range(SIZE):
        t = max(0.0, min(1.0, (y - 50) / 360.0))
        color = lerp(C_TOP, C_MID, t * 2) if t < 0.5 else lerp(C_MID, C_BOT, (t - 0.5) * 2)
        d.line([(0, y), (SIZE, y)], fill=(*color, 255))
    return grad


BACKGROUND = make_background()
GRADIENT = make_gradient()
EMPTY = Image.new("RGBA", (SIZE, SIZE), (0, 0, 0, 0))


def soft_mask(polygon, cutout=None):
    big = Image.new("L", (SIZE * SS, SIZE * SS), 0)
    ImageDraw.Draw(big).polygon(
        [(int(round(x * SS)), int(round(y * SS))) for x, y in polygon], fill=255
    )
    if cutout:
        ImageDraw.Draw(big).polygon(
            [(int(round(x * SS)), int(round(y * SS))) for x, y in cutout], fill=0
        )
    return big.resize((SIZE, SIZE), Image.LANCZOS)


# Motion trails are drawn only below the bell, so the pulse of the dome stays
# clean while the arms keep a readable sweep behind them.
TENTACLE_REGION = Image.new("L", (SIZE, SIZE), 0)
ImageDraw.Draw(TENTACLE_REGION).rectangle(
    (0, int(CANVAS_CY + 18), SIZE, SIZE), fill=255
)


def trail_layer(index):
    """Faint outlines of where the arms were a few frames ago.

    Using the same periodic deformation at earlier phases keeps the ghosts
    continuous across the loop boundary — no fade-in or jump at frame 0.
    """
    layer = Image.new("RGBA", (SIZE, SIZE), (0, 0, 0, 0))
    for back, strength in ((3, 0.30), (6, 0.18), (10, 0.10)):
        phase = 2.0 * math.pi * (index - back) / FRAMES
        past = soft_mask([to_canvas(deform(p, phase)) for p in BODY])
        edge = ImageChops.subtract(past, past.filter(ImageFilter.MinFilter(5)))
        edge = ImageChops.multiply(edge, TENTACLE_REGION)
        ghost = Image.new("RGBA", (SIZE, SIZE), (155, 230, 255, 0))
        ghost.putalpha(edge.point(lambda v: int(v * strength)))
        layer = Image.alpha_composite(layer, ghost)
    return layer


def render(index, opaque_background=True):
    phase = 2.0 * math.pi * index / FRAMES
    bell = [to_canvas(deform(p, phase)) for p in BODY]
    sheen = [to_canvas(deform(p, phase)) for p in HIGHLIGHT]
    # The brand SVG draws the specular streak in the *white tile's* colour, so
    # the source mark reads it as a cut-out. There is no white tile behind a
    # dark/transparent animation: cutting a hole there yields a dark void and
    # the highlight simply disappears. Keep the body solid and paint the
    # streak as a real white shape further down instead.
    mask = soft_mask(bell)

    frame = (BACKGROUND if opaque_background else EMPTY).copy()

    # Pulsing halo: brighter while the bell contracts (breath < 0).
    pulse = 0.62 + 0.30 * (0.5 - 0.5 * math.sin(phase))
    small = mask.resize((SIZE // 3, SIZE // 3), Image.LANCZOS)
    halo = small.filter(ImageFilter.GaussianBlur(7)).resize((SIZE, SIZE), Image.LANCZOS)
    halo_rgba = Image.new("RGBA", (SIZE, SIZE), (72, 190, 255, 0))
    halo_rgba.putalpha(halo.point(lambda v: int(v * pulse)))
    frame = Image.alpha_composite(frame, halo_rgba)

    # The arms' swept path sits behind the current silhouette.
    frame = Image.alpha_composite(frame, trail_layer(index))

    # Jelly body: brand gradient clipped to the deforming silhouette.
    frame = Image.alpha_composite(frame, Image.composite(GRADIENT, EMPTY, mask))

    # A brighter inner rim gives the bell its gelatinous thickness.
    rim = ImageChops.subtract(mask, mask.filter(ImageFilter.MinFilter(5)))
    rim_rgba = Image.new("RGBA", (SIZE, SIZE), (200, 246, 255, 0))
    rim_rgba.putalpha(rim.point(lambda v: int(v * 0.36)))
    frame = Image.alpha_composite(frame, rim_rgba)

    # Soft specular bloom across the top of the bell, following its width.
    spread = 1.0 + 0.115 * math.sin(phase)
    cx = CANVAS_CX
    cy = CANVAS_CY - 108 * ART_SCALE + 7 * math.cos(phase) * ART_SCALE
    bloom = Image.new("RGBA", (SIZE, SIZE), (0, 0, 0, 0))
    ImageDraw.Draw(bloom).ellipse(
        (
            cx - 108 * ART_SCALE * spread,
            cy - 46 * ART_SCALE,
            cx + 108 * ART_SCALE * spread,
            cy + 26 * ART_SCALE,
        ),
        fill=(235, 252, 255, 92),
    )
    bloom = bloom.filter(ImageFilter.GaussianBlur(18))
    # Keep the bloom inside the bell only.
    frame = Image.alpha_composite(frame, Image.composite(bloom, EMPTY, mask))

    # Specular highlight: a genuine white streak rather than a cut-out. It
    # follows the same deformation field as the bell, so it widens and drifts
    # with the breath. A faint cool aura keeps it from looking pasted on.
    sheen_mask = soft_mask(sheen)
    aura = Image.new("RGBA", (SIZE, SIZE), (178, 232, 255, 0))
    aura.putalpha(
        sheen_mask.filter(ImageFilter.GaussianBlur(7)).point(lambda v: int(v * 0.24))
    )
    core = Image.new("RGBA", (SIZE, SIZE), (248, 253, 255, 0))
    core.putalpha(
        sheen_mask.filter(ImageFilter.GaussianBlur(0.8)).point(lambda v: int(v * 0.96))
    )
    # Never let the glint spill outside the jellyfish silhouette.
    frame = Image.alpha_composite(frame, Image.composite(aura, EMPTY, mask))
    frame = Image.alpha_composite(frame, Image.composite(core, EMPTY, mask))

    # Plankton motes: closed sine/cosine paths, so they loop seamlessly too.
    motes = Image.new("RGBA", (SIZE, SIZE), (0, 0, 0, 0))
    md = ImageDraw.Draw(motes)
    for i in range(9):
        bx = SIZE * (0.14 + 0.09 * i) + 13 * math.cos(phase + i * 0.9)
        by = SIZE * (0.16 + 0.075 * ((i * 5) % 9)) + 26 * math.sin(2 * phase + i * 1.3)
        r = 1.6 + (i % 3) * 0.7
        md.ellipse((bx - r, by - r, bx + r, by + r), fill=(190, 235, 255, 105))
    motes = motes.filter(ImageFilter.GaussianBlur(0.6))
    frame = Image.alpha_composite(frame, motes)

    return frame.convert("RGB") if opaque_background else frame


def main():
    frames = [render(i) for i in range(FRAMES)]
    frames[9].save(POSTER)  # fully relaxed bell

    # True 8-bit alpha variant: every layer is composited onto a transparent
    # canvas, so halos, trails and anti-aliased edges survive anywhere it is
    # placed. Animated WebP is the drop-in bitmap answer to GIF transparency.
    alpha_frames = [render(i, False) for i in range(FRAMES)]
    alpha_frames[9].save(ALPHA_POSTER)
    webp = ALPHA_WEBP
    alpha_frames[0].save(
        webp,
        save_all=True,
        append_images=alpha_frames[1:],
        duration=DURATION,
        loop=0,
        format="WEBP",
        quality=92,
        method=4,
    )

    # One shared palette avoids the per-frame colour flicker an adaptive
    # palette would introduce on a dark gradient.
    picks = [frames[i].resize((SIZE // 3, SIZE // 3)) for i in range(0, FRAMES, 4)]
    sw, sh = picks[0].size
    sample = Image.new("RGB", (sw * len(picks), sh))
    for i, thumb in enumerate(picks):
        sample.paste(thumb, (i * sw, 0))
    palette = sample.quantize(colors=256, method=Image.Quantize.MEDIANCUT)
    quantized = [
        f.quantize(palette=palette, dither=Image.Dither.FLOYDSTEINBERG) for f in frames
    ]
    quantized[0].save(
        OUT,
        save_all=True,
        append_images=quantized[1:],
        duration=DURATION,
        loop=0,
        optimize=True,
    )

    # Contact sheet for a quick visual check of the whole cycle.
    cells = [frames[i].resize((240, 240)) for i in (0, 9, 18, 27)]
    sheet = Image.new("RGB", (480, 480))
    for i, cell in enumerate(cells):
        sheet.paste(cell, ((i % 2) * 240, (i // 2) * 240))
    sheet.save(SHEET)

    # Unscaled crop of the arms at four phases, to inspect the wave + trails.
    arms = [frames[i].crop((70, 215, 410, 470)) for i in (0, 9, 18, 27)]
    arms_sheet = Image.new("RGB", (680, 510))
    for i, cell in enumerate(arms):
        arms_sheet.paste(cell, ((i % 2) * 340, (i // 2) * 255))
    arms_path = SHEET.with_name("jellyfish-breathe-arms.png")
    arms_sheet.save(arms_path)

    print(f"wrote {OUT} ({OUT.stat().st_size / 1024:.0f} KiB)")
    print(f"wrote {webp} ({webp.stat().st_size / 1024:.0f} KiB, RGBA)")
    print(f"wrote {POSTER}")
    print(f"wrote {SHEET}")
    print(f"wrote {arms_path}")


if __name__ == "__main__":
    main()
