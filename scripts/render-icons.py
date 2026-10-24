#!/usr/bin/env python3
"""Draw the SSH Proxy icon and render every icon the apps embed at its exact size.

The design lives here: a white paper windmill on a green-to-blue disc. Its four
vanes are one vane turned about the center; sizes up to 64 px put the center on
a pixel corner and round the vanes to whole pixels with hard edges, so tray
icons stay crisp at 16-24 px. Larger sizes use the same design unrounded and
smoothed.

Writes:
- src/ui/icon.svg: the design as a vector, for reference and large renders.
- src/ui/icons/icon-N.png: Windows small icons for 100-350% scaling (16-56 px,
  the tray picks the size Windows reports), the macOS menu bar's 22 pt at 2x
  (44 px), and the larger Windows app icon views (64, 256 px).
- src/ui/icon.icns: the macOS app icon.

Run after changing the design (packages from requirements.txt):
    python scripts/render-icons.py
"""

import io
from pathlib import Path
import sys

try:
    from PIL import Image
    import resvg_py
except ImportError:
    sys.exit("Install the icon tools first: python -m pip install -r requirements.txt")

UI = Path(__file__).resolve().parents[1] / "src" / "ui"
PNG_SIZES = (16, 20, 24, 28, 32, 36, 40, 44, 48, 56, 64, 256)
ICNS_SIZES = (32, 64, 128, 256, 512, 1024)
PIXEL_SNAP_MAX = 64
# Both keep at least 3.7:1 contrast on dark and light taskbars and behind the white windmill.
GREEN, BLUE, VANE = "#3F8F00", "#2477DB", "#FFFFFF"


def number(value):
    return f"{value:.3f}".rstrip("0").rstrip(".")


def svg(size, snap):
    """The icon on a size x size canvas: four triangular vanes swirling around
    the center, separated by a channel of disc color. Each vane is the previous
    one turned 90 degrees; with snap, every straight edge lies on whole pixels."""
    center = size / 2
    if snap:
        gap = max(1, round(size / 32))
        margin = max(2, round(size / 10))
        reach = int(center - gap - margin)
        side = round(0.6 * reach)
    else:
        gap, margin = size / 32, size / 10
        reach = center - gap - margin
        side = 0.6 * reach
    # The upright vane: its long edge runs up from the center, its short edge to the right.
    vane = [(0, -gap), (0, -gap - reach), (side, -gap)]
    paths = []
    for _ in range(4):
        paths.append("M" + " ".join(f"{number(center + dx)} {number(center + dy)}" for dx, dy in vane) + "Z")
        vane = [(-dy, dx) for dx, dy in vane]  # 90 degrees clockwise on screen.
    # Small sizes draw the vanes as hard pixels: smoothing would blur their tips into the disc.
    edges = ' shape-rendering="crispEdges"' if snap else ""
    vanes = "\n".join(f'  <path d="{d}" fill="{VANE}"{edges}/>' for d in paths)
    half = number(center)
    return f"""<svg xmlns="http://www.w3.org/2000/svg" width="{size}" height="{size}" viewBox="0 0 {size} {size}">
  <title>SSH Proxy</title>
  <desc>A white paper windmill on a green-to-blue disc. Drawn by scripts/render-icons.py.</desc>
  <defs>
    <linearGradient id="disc" x1="0" y1="0" x2="{size}" y2="{size}" gradientUnits="userSpaceOnUse">
      <stop offset="0.15" stop-color="{GREEN}"/>
      <stop offset="0.85" stop-color="{BLUE}"/>
    </linearGradient>
  </defs>
  <circle cx="{half}" cy="{half}" r="{half}" fill="url(#disc)"/>
{vanes}
</svg>
"""


def render(size):
    text = svg(size, snap=True) if size <= PIXEL_SNAP_MAX else svg(64, snap=False)
    data = resvg_py.svg_to_bytes(svg_string=text, width=size, height=size, skip_system_fonts=True)
    return Image.open(io.BytesIO(bytes(data))).convert("RGBA")


def main():
    (UI / "icon.svg").write_text(svg(64, snap=False).replace('width="64" height="64"', 'width="512" height="512"'), encoding="utf-8", newline="\n")
    folder = UI / "icons"
    folder.mkdir(exist_ok=True)
    for old in folder.glob("icon-*.png"):
        old.unlink()
    for size in PNG_SIZES:
        render(size).save(folder / f"icon-{size}.png", optimize=True)
    images = [render(size) for size in ICNS_SIZES]
    images[-1].save(UI / "icon.icns", format="ICNS", append_images=images[:-1])
    print(f"Wrote {UI / 'icon.svg'}, {len(PNG_SIZES)} PNGs in {folder} and {UI / 'icon.icns'}")


if __name__ == "__main__":
    main()
