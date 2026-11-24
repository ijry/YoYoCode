"""Generate src-tauri/icons/tray-icon-template.png from the new app icon.

This is the macOS menu-bar image for the jellyfish mark. AppKit treats an
`NSImage` with `isTemplate = true` as a mask: only the alpha channel matters,
and the system tints the opaque pixels to match the menu bar — white on a dark
bar, black on a light one. So the artwork is emitted as pure black RGB under
that alpha; a hard-coded white would disappear on a light menu bar.

The silhouette is derived from the already-rasterised app icon (`icon.png`,
produced by `pnpm tauri icon` from icon.svg) rather than by parsing SVG paths.
The new icon art uses cubic Bezier curves, which a simple polygon parser cannot
handle. Instead we isolate the WHITE jellyfish by colour: the deep-blue
gradient background (R <= 104) is rejected, the white body (R = 255) is kept,
and the blue arc on the head is naturally excluded — giving it the same
cut-out treatment as the app icon. Only Pillow is required.

Re-run after editing icon.svg (and regenerating icon.png):

    python3 src-tauri/icons/tray-icon-template.gen.py
"""

import sys
from pathlib import Path
from PIL import Image

ICONS_DIR = Path(__file__).resolve().parent
SOURCE_PNG = ICONS_DIR / "icon.png"
OUTPUT_PNG = ICONS_DIR / "tray-icon-template.png"

# Kept at the upstream image's dimensions: the menu-bar item keeps the same
# footprint, only its silhouette changes.
W_LOGICAL, H_LOGICAL = 52, 44

# Render at 4x then downsample with Lanczos for smooth edges.
SCALE = 4
W, H = W_LOGICAL * SCALE, H_LOGICAL * SCALE

TARGET_H = 40.0 * 0.9

# White-body detector. The gradient's maximum red channel is 104, so anything
# with R >= 140 is antialiasing toward the white jellyfish.
R_LO, R_HI = 140, 200


def _fail(message):
    raise SystemExit(f"tray-icon-template.gen.py: {message}")


def main():
    if not SOURCE_PNG.is_file():
        _fail(f"missing {SOURCE_PNG} — run `pnpm tauri icon` first")

    src = Image.open(SOURCE_PNG).convert("RGBA")
    w, h = src.size
    px = src.load()

    # Build the white-jellyfish alpha at 4x resolution.
    mask = Image.new("L", (w, h), 0)
    mpx = mask.load()
    for y in range(h):
        for x in range(w):
            r, g, b, a = px[x, y]
            if a == 0:
                continue
            t = (r - R_LO) / float(R_HI - R_LO)
            t = 0.0 if t < 0 else (1.0 if t > 1 else t)
            v = int(round(t * a))
            if v:
                mpx[x, y] = v

    bbox = mask.getbbox()
    if bbox is None:
        _fail("could not find the white jellyfish in icon.png")

    # Crop to the mark, scale to 4x the target height, centre in the big
    # canvas, then downsample ONCE to the 52x44 template. Two successive
    # downsamples wash out the thin tentacles.
    mark = mask.crop(bbox)
    target_h_big = int(round(TARGET_H * SCALE))
    scale = target_h_big / mark.height
    new_w = max(1, int(round(mark.width * scale)))
    mark = mark.resize((new_w, target_h_big), Image.LANCZOS)

    big = Image.new("L", (W, H), 0)
    ox = (W - new_w) // 2
    oy = (H - target_h_big) // 2
    big.paste(mark, (ox, oy))
    alpha = big.resize((W_LOGICAL, H_LOGICAL), Image.LANCZOS)

    histogram = alpha.histogram()
    opaque = sum(histogram[200:])
    transparent = histogram[0]
    if opaque == 0 or transparent == 0:
        _fail("the generated mask is empty or fully solid")

    zero = Image.new("L", alpha.size, 0)
    img = Image.merge("RGBA", (zero, zero, zero, alpha))
    img.save(OUTPUT_PNG)
    print(
        f"wrote {OUTPUT_PNG} {img.size} "
        f"(opaque {opaque}px, transparent {transparent}px)"
    )


if __name__ == "__main__":
    try:
        main()
    except OSError as err:
        print(f"tray-icon-template.gen.py: {err}", file=sys.stderr)
        sys.exit(1)
