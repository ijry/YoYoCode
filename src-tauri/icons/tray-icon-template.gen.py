"""Generate src-tauri/icons/tray-icon-template.png from the YoYoCode icon.

This is the macOS menu-bar image for the jellyfish mark. AppKit treats an
`NSImage` with `isTemplate = true` as a mask: only the alpha channel matters,
and the system tints the opaque pixels to match the menu bar — white on a dark
bar, black on a light one. So the artwork is emitted as pure black RGB under
that alpha; a hard-coded white would disappear on a light menu bar.

The silhouette is derived from `icon.svg`, the same artwork the Dock, favicon
and window icons use (`be3004562`). Before this, the menu bar still showed the
upstream bracket-and-dots mark because that commit covered every platform
asset except this one.

Re-run after editing icon.svg:

    python3 src-tauri/icons/tray-icon-template.gen.py

Requires Pillow (pip install Pillow). No SVG renderer is needed: the traced
jellyfish outline is made only of straight M/L/Z segments, so its polygons can
be drawn directly as a mask. (The gradient id in icon.svg is still called
`octo` upstream; only the shape is read here, never its colours.)
"""

import re
import sys
from pathlib import Path
from PIL import Image, ImageDraw, ImageChops

ICONS_DIR = Path(__file__).resolve().parent
SOURCE_SVG = ICONS_DIR / "icon.svg"
OUTPUT_PNG = ICONS_DIR / "tray-icon-template.png"

# Kept at the upstream image's dimensions: the menu-bar item keeps the same
# footprint, only its silhouette changes.
W_LOGICAL, H_LOGICAL = 52, 44

# Render at 4x then downsample with Lanczos: PIL's drawing primitives are not
# anti-aliased, so supersampling is what produces smooth diagonals and the
# rounded tentacle tips.
SCALE = 4
W, H = W_LOGICAL * SCALE, H_LOGICAL * SCALE

# Leave a small transparent margin on the top and bottom; the artwork keeps
# its own aspect ratio and is centred horizontally.
TARGET_H = 40.0

_PATH_TAG = re.compile(r"<path\b([^>]*)>", re.IGNORECASE | re.DOTALL)
_FILL = re.compile(r'\bfill="([^"]+)"', re.IGNORECASE)
_D = re.compile(r'\bd="([^"]+)"', re.IGNORECASE | re.DOTALL)
_TOKEN = re.compile(
    r"[A-Za-z]|[-+]?(?:\d*\.\d+|\d+)(?:[eE][-+]?\d+)?"
)
_COMMAND = re.compile(r"[A-Za-z]")
_ALLOWED_COMMANDS = frozenset("MLZmlz")


def _fail(message):
    raise SystemExit(f"tray-icon-template.gen.py: {message}")


def _parse_points(d):
    """Return the polygon of a straight-line-only SVG path."""
    tokens = _TOKEN.findall(d)
    commands = [t for t in tokens if _COMMAND.fullmatch(t)]
    unsupported = [c for c in commands if c not in _ALLOWED_COMMANDS]
    if unsupported:
        _fail(
            "icon.svg now uses curved/relative SVG commands "
            f"({''.join(sorted(set(unsupported)))}); this mask needs an SVG renderer"
        )
    if sum(c in "Mm" for c in commands) != 1:
        _fail("expected a single jellyfish subpath in icon.svg")

    points = []
    i = 0
    while i < len(tokens):
        if _COMMAND.fullmatch(tokens[i]):
            i += 1
            continue
        if i + 1 >= len(tokens):
            _fail("odd number of coordinates in an icon.svg path")
        points.append((float(tokens[i]), float(tokens[i + 1])))
        i += 2
    if len(points) < 3:
        _fail("jellyfish path has fewer than three points")
    return points


def _read_artwork():
    if not SOURCE_SVG.is_file():
        _fail(f"missing {SOURCE_SVG}")
    svg = SOURCE_SVG.read_text(encoding="utf-8")
    paths = []
    for attrs in _PATH_TAG.findall(svg):
        d = _D.search(attrs)
        if not d:
            continue
        fill = _FILL.search(attrs)
        paths.append((fill.group(1) if fill else "", _parse_points(d.group(1))))

    # The gradient id is `octo` in the source SVG, but the shape is the
    # jellyfish. Match by fill so a gradient rename cannot break the lookup.
    body = next((pts for fill, pts in paths if "url(" in fill), None)
    highlight = next((pts for fill, pts in paths if fill == "#ffffff"), None)
    if body is None or highlight is None:
        _fail("could not find the jellyfish body and its highlight in icon.svg")
    return body, highlight


def _placement(body):
    xs = [p[0] for p in body]
    ys = [p[1] for p in body]
    x0, x1, y0, y1 = min(xs), max(xs), min(ys), max(ys)
    scale = TARGET_H / (y1 - y0)
    offset_x = (W_LOGICAL - (x1 - x0) * scale) / 2 - x0 * scale
    offset_y = (H_LOGICAL - (y1 - y0) * scale) / 2 - y0 * scale
    return scale, offset_x, offset_y


def main():
    body, highlight = _read_artwork()
    scale, offset_x, offset_y = _placement(body)

    def projected(points):
        return [
            (
                int(round((x * scale + offset_x) * SCALE)),
                int(round((y * scale + offset_y) * SCALE)),
            )
            for x, y in points
        ]

    silhouette = Image.new("L", (W, H), 0)
    ImageDraw.Draw(silhouette).polygon(projected(body), fill=255)

    # The specular highlight is a cut-out in the app icon. Keep it transparent
    # here too, so the mark stays legible at menu-bar size.
    cut = Image.new("L", (W, H), 0)
    ImageDraw.Draw(cut).polygon(projected(highlight), fill=255)

    alpha = ImageChops.subtract(silhouette, cut).resize(
        (W_LOGICAL, H_LOGICAL), Image.LANCZOS
    )

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
