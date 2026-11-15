"""Export the breathing jellyfish as a Lottie/Bodymovin JSON.

Reuses the exact deformation field from jellyfish-breathe.gen.py, but writes
vector shape keyframes instead of raster frames: real 8-bit alpha, infinitely
scalable, restyleable, and a fraction of the GIF's size.

    python3 scripts/jellyfish-lottie.gen.py

Produces jellyfish-breathe.json and a self-contained offline preview page.
"""

import importlib.util
import json
import math
from pathlib import Path

SCRIPT_DIR = Path(__file__).resolve().parent
ASSETS = SCRIPT_DIR.parent / "public/logo/animation"
spec = importlib.util.spec_from_file_location(
    "jelly_breathe", SCRIPT_DIR / "jellyfish-breathe.gen.py"
)
jelly = importlib.util.module_from_spec(spec)
spec.loader.exec_module(jelly)

OUT_JSON = ASSETS / "jellyfish-breathe.json"
OUT_HTML = ASSETS / "jellyfish-breathe-lottie.html"
# Offline preview runtime, vendored so the self-contained HTML can be
# regenerated without adding lottie-web to the app's dependencies.
LOTTIE_JS = SCRIPT_DIR / "lottie-web-5.12.2.min.js"

SIZE = jelly.SIZE
FPS = 25
OP = 72  # 2.88 s at 25 fps, matching the 36 x 80 ms GIF
KEY_STEP = 4  # 19 path keyframes; lottie-web interpolates the in-betweens

BRAND = [0.188, 0.867, 0.992, 0.408, 0.765, 0.988, 0.624, 0.663, 0.988]


def phase_at(t, back=0):
    """One full breathing cycle over OP frames, optionally shifted backwards
    for the motion-trail layers. Periodic, so frame 0 and OP match exactly."""
    return 2.0 * math.pi * (t - 2 * back) / OP


def shape_obj(points):
    n = len(points)
    return {
        "i": [[0, 0] for _ in range(n)],  # straight segments: zero tangents
        "o": [[0, 0] for _ in range(n)],
        "v": [[round(x, 2), round(y, 2)] for x, y in points],
        "c": True,
    }


def body_shape(t, back=0):
    ph = phase_at(t, back)
    return shape_obj([jelly.to_canvas(jelly.deform(p, ph)) for p in jelly.BODY])


def highlight_shape(t):
    ph = phase_at(t)
    return shape_obj([jelly.to_canvas(jelly.deform(p, ph)) for p in jelly.HIGHLIGHT])


def path_keyframes(shape_fn):
    times = list(range(0, OP + 1, KEY_STEP))
    keys = []
    for j, t in enumerate(times):
        kf = {"t": t, "s": [shape_fn(t)]}
        if j < len(times) - 1:
            kf["e"] = [shape_fn(times[j + 1])]
            kf["i"] = {"x": 1, "y": 1}
            kf["o"] = {"x": 0, "y": 0}
        keys.append(kf)
    return {"a": 1, "k": keys}


def stroke(color, opacity, width, name):
    return {
        "ty": "st",
        "nm": name,
        "c": {"a": 0, "k": color},
        "o": {"a": 0, "k": opacity},
        "w": {"a": 0, "k": width},
        "lc": 2,
        "lj": 2,
        "bm": 0,
        "hd": False,
    }


def solid_fill(color, opacity, name):
    return {
        "ty": "fl",
        "nm": name,
        "c": {"a": 0, "k": color},
        "o": {"a": 0, "k": opacity},
        "r": 1,
        "bm": 0,
        "hd": False,
    }


def gradient_fill():
    # 3 colour stops + 3 opacity stops, all normalised to 0..1.
    return {
        "ty": "gf",
        "nm": "brand cyan-to-violet",
        "o": {"a": 0, "k": 100},
        "r": 1,
        "bm": 0,
        "g": {
            "p": 3,
            "k": {
                "a": 0,
                "k": [
                    0, *BRAND[0:3],
                    0.5, *BRAND[3:6],
                    1, *BRAND[6:9],
                    0, 1,
                    0.5, 1,
                    1, 1,
                ],
            },
        },
        "s": {"a": 0, "k": [0, 50]},
        "e": {"a": 0, "k": [0, 410]},
        "t": 1,
        "hd": False,
    }


def group_transform():
    return {
        "ty": "tr",
        "p": {"a": 0, "k": [0, 0]},
        "a": {"a": 0, "k": [0, 0]},
        "s": {"a": 0, "k": [100, 100]},
        "r": {"a": 0, "k": 0},
        "o": {"a": 0, "k": 100},
        "sk": {"a": 0, "k": 0},
        "sa": {"a": 0, "k": 0},
        "nm": "Transform",
    }


def layer_transform():
    return {
        "o": {"a": 0, "k": 100},
        "r": {"a": 0, "k": 0},
        "p": {"a": 0, "k": [0, 0, 0]},
        "a": {"a": 0, "k": [0, 0, 0]},
        "s": {"a": 0, "k": [100, 100, 100]},
    }


def make_layer(ind, name, shape_fn, items, masks=None):
    layer = {
        "ddd": 0,
        "ind": ind,
        "ty": 4,
        "nm": name,
        "sr": 1,
        "ks": layer_transform(),
        "ao": 0,
        "shapes": [
            {
                "ty": "gr",
                "nm": name,
                "it": [
                    {
                        "ty": "sh",
                        "ind": 0,
                        "nm": "silhouette",
                        "ks": path_keyframes(shape_fn),
                    },
                    *items,
                    group_transform(),
                ],
            }
        ],
        "ip": 0,
        "op": OP,
        "st": 0,
        "bm": 0,
    }
    if masks:
        layer["masksProperties"] = masks
    return layer


def build():
    body_items = [
        stroke([0.28, 0.75, 1, 1], 14, 11, "outer glow"),
        gradient_fill(),
        stroke([0.80, 0.96, 1, 1], 34, 2.5, "gelatinous rim"),
    ]
    # No subtract mask. The source brand SVG knocks the highlight out of the
    # gradient because a *white tile* sits behind it; on a transparent canvas
    # that becomes a dark hole and the glint vanishes. It is a real top layer.
    body = make_layer(1, "Jellyfish body", body_shape, body_items)

    highlight = make_layer(
        4,
        "Specular highlight",
        highlight_shape,
        [
            stroke([0.72, 0.93, 1, 1], 24, 9, "specular halo"),
            solid_fill([0.973, 0.992, 1, 1], 96, "specular white"),
        ],
    )

    near = make_layer(
        2,
        "Motion trail · near",
        lambda t: body_shape(t, 3),
        [stroke([0.61, 0.90, 1, 1], 26, 2.5, "trail")],
    )
    far = make_layer(
        3,
        "Motion trail · far",
        lambda t: body_shape(t, 8),
        [stroke([0.61, 0.90, 1, 1], 13, 2, "trail")],
    )

    return {
        "v": "5.9.6",
        "fr": FPS,
        "ip": 0,
        "op": OP,
        "w": SIZE,
        "h": SIZE,
        "nm": "YoYoCode Jellyfish - Breathing",
        "ddd": 0,
        "assets": [],
        # layers[0] is topmost: glint, then body, then its own motion trails.
        "layers": [highlight, body, near, far],
        "markers": [],
    }


def make_html(data):
    lib = LOTTIE_JS.read_text(encoding="utf-8")
    if "</script" in lib.lower():
        lib = lib.replace("</script", "<\\/script")
    payload = json.dumps(data, separators=(",", ":"))
    return f"""<!doctype html>
<html lang="zh-CN" style="background:#0b1220"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<meta name="color-scheme" content="dark">
<meta name="theme-color" content="#0b1220">
<title>水母呼吸 · Lottie</title>
<style>
:root{{font-family:system-ui,-apple-system,"Segoe UI",sans-serif;color-scheme:dark}}
html,body{{background:#0b1220}}
*{{box-sizing:border-box}}
body{{margin:0;background:#0b1220;color:#e8eef7;min-height:100vh}}
header{{display:flex;gap:8px;align-items:center;flex-wrap:wrap;padding:14px 18px;border-bottom:1px solid #1d2a3d}}
header strong{{font-size:14px;font-weight:500;margin-right:8px}}
header span{{font-size:12px;color:#8fa3bd;margin-right:auto}}
button{{font:inherit;font-size:12px;padding:6px 12px;border-radius:8px;border:1px solid #2b3d55;background:#152238;color:#dbe7f5;cursor:pointer}}
button:hover{{background:#1d3050}}
button.on{{border-color:#4aa8e0;color:#8fdcff}}
main{{display:grid;place-items:center;padding:24px}}
#stage{{width:480px;height:480px;max-width:100%;border-radius:16px;overflow:hidden;position:relative;border:1px solid #1d2a3d}}
#stage.dark{{background:linear-gradient(#051020,#092a44)}}
#stage.light{{background:#f2f6fb}}
#stage.checker{{background-color:#eef3f9;background-image:linear-gradient(45deg,#d9e0e9 25%,transparent 25%),linear-gradient(-45deg,#d9e0e9 25%,transparent 25%),linear-gradient(45deg,transparent 75%,#d9e0e9 75%),linear-gradient(-45deg,transparent 75%,#d9e0e9 75%);background-size:24px 24px;background-position:0 0,0 12px,12px -12px,-12px 0}}
#lottie{{width:100%;height:100%;opacity:0;transition:opacity .2s ease}}
#lottie.ready{{opacity:1}}
footer{{padding:0 18px 20px;font-size:12px;color:#8fa3bd;text-align:center}}
</style></head><body style="background:#0b1220">
<header><strong>水母呼吸 · Lottie</strong><span>透明矢量 · 25 fps · 2.88 s 循环</span>
<button data-bg="dark" class="on">深色底</button>
<button data-bg="light">浅色底</button>
<button data-bg="checker">棋盘（验透明）</button>
<button id="toggle">暂停</button></header>
<main><div id="stage" class="dark"><div id="lottie"></div></div></main>
<footer>同一套形变函数导出的 Bodymovin 路径关键帧；切换背景可以看到没有硬边白框。</footer>
<script>{lib}</script>
<script>
const DATA={payload};
const anim=lottie.loadAnimation({{container:document.getElementById('lottie'),renderer:'svg',loop:true,autoplay:true,animationData:DATA}});
// Reveal only once the first SVG frame exists, so there is never an empty
// (or default-white) stage during startup or at the loop seam.
anim.addEventListener('DOMLoaded',()=>document.getElementById('lottie').classList.add('ready'));
const stage=document.getElementById('stage');
document.querySelectorAll('button[data-bg]').forEach(b=>b.addEventListener('click',()=>{{
  document.querySelectorAll('button[data-bg]').forEach(x=>x.classList.remove('on'));
  b.classList.add('on');stage.className=b.dataset.bg;
}}));
const toggle=document.getElementById('toggle');
toggle.addEventListener('click',()=>{{if(anim.isPaused){{anim.play();toggle.textContent='暂停'}}else{{anim.pause();toggle.textContent='播放'}}}});
</script></body></html>"""


def main():
    data = build()
    payload = json.dumps(data, separators=(",", ":"))
    OUT_JSON.write_text(payload, encoding="utf-8")
    OUT_HTML.write_text(make_html(data), encoding="utf-8")
    print(f"wrote {OUT_JSON} ({OUT_JSON.stat().st_size / 1024:.0f} KiB)")
    print(f"wrote {OUT_HTML} ({OUT_HTML.stat().st_size / 1024:.0f} KiB, self-contained)")
    print(f"layers {len(data['layers'])}, keyframes/layer {len(data['layers'][0]['shapes'][0]['it'][0]['ks']['k'])}")


if __name__ == "__main__":
    main()
