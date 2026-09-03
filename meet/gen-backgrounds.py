#!/usr/bin/env python3
"""Generate Honco Meet virtual-background templates.

Drawn rather than downloaded on purpose: stock photography carries licence
terms and these ship inside a product, so everything here is generated maths
that is unambiguously ours. Real office photos can be dropped into the same
directory later -- the Jitsi config lists whatever files are present.

Design constraints, because a call background is not wallpaper:
  * low saturation, so it does not compete with a face
  * a dark vignette, so the centre stays the brightest part of the frame
  * soft, large-scale shapes only -- fine detail turns to mush at webcam
    bitrates and confuses the segmentation model at the hair line
"""
import math
import os
import random

from PIL import Image, ImageChops, ImageDraw, ImageFilter

OUT = os.path.expanduser("~/.jitsi-meet-cfg/web-backgrounds")
W, H = 1920, 1080

# (name, top-left colour, bottom-right colour)
PALETTES = [
    ("slate",  (0x1e, 0x29, 0x3b), (0x3d, 0x4f, 0x66)),
    ("ink",    (0x14, 0x18, 0x24), (0x2c, 0x35, 0x4d)),
    ("teal",   (0x10, 0x2f, 0x33), (0x24, 0x5d, 0x63)),
    ("moss",   (0x1c, 0x2b, 0x22), (0x3a, 0x55, 0x44)),
    ("clay",   (0x3a, 0x2a, 0x24), (0x6b, 0x4c, 0x3d)),
    ("plum",   (0x27, 0x1c, 0x33), (0x4b, 0x37, 0x5e)),
    ("sand",   (0xe8, 0xe0, 0xd4), (0xc0, 0xb0, 0x9a)),
    ("paper",  (0xf2, 0xf0, 0xec), (0xd2, 0xce, 0xc6)),
    ("steel",  (0x2b, 0x30, 0x34), (0x55, 0x5f, 0x66)),
    ("indigo", (0x18, 0x1f, 0x3a), (0x33, 0x40, 0x70)),
    ("rose",   (0x3a, 0x24, 0x2c), (0x70, 0x4a, 0x55)),
    ("forest", (0x12, 0x24, 0x1c), (0x28, 0x47, 0x38)),
]


def gradient(c1, c2, angle_deg):
    """Linear gradient across an arbitrary angle, built from a 1-D ramp."""
    a = math.radians(angle_deg)
    dx, dy = math.cos(a), math.sin(a)
    lo = min(0.0, W * dx) + min(0.0, H * dy)
    span = (max(0.0, W * dx) + max(0.0, H * dy)) - lo or 1.0

    img = Image.new("RGB", (W, H))
    px = img.load()
    xs = [x * dx - lo for x in range(W)]
    for y in range(H):
        yy = y * dy
        for x in range(W):
            t = (xs[x] + yy) / span
            px[x, y] = (
                int(c1[0] + (c2[0] - c1[0]) * t),
                int(c1[1] + (c2[1] - c1[1]) * t),
                int(c1[2] + (c2[2] - c1[2]) * t),
            )
    return img


def bokeh_layer(seed, count=26):
    """Soft out-of-focus discs, like a shallow depth of field behind someone."""
    rnd = random.Random(seed)
    layer = Image.new("RGB", (W, H), (0, 0, 0))
    d = ImageDraw.Draw(layer)
    for _ in range(count):
        r = rnd.randint(70, 280)
        cx = rnd.randint(-120, W + 120)
        cy = rnd.randint(-120, H + 120)
        v = rnd.randint(14, 46)
        d.ellipse([cx - r, cy - r, cx + r, cy + r], fill=(v, v, v))
    return layer.filter(ImageFilter.GaussianBlur(80))


def vignette(img, strength=0.34):
    """Darken the edges so a face in the centre stays the brightest thing."""
    mask = Image.new("L", (W, H), 0)
    ImageDraw.Draw(mask).ellipse(
        [-W * 0.25, -H * 0.35, W * 1.25, H * 1.35], fill=255
    )
    mask = mask.filter(ImageFilter.GaussianBlur(240))
    darkened = Image.blend(img, Image.new("RGB", (W, H), (0, 0, 0)), strength)
    return Image.composite(img, darkened, mask)


def main():
    os.makedirs(OUT, exist_ok=True)
    made = []
    for i, (name, c1, c2) in enumerate(PALETTES, start=1):
        img = gradient(c1, c2, 20 + (i * 27) % 140)
        img = ImageChops.add(img, bokeh_layer(seed=i * 101))
        img = vignette(img)
        img = img.filter(ImageFilter.GaussianBlur(1.2))

        fn = os.path.join(OUT, "honco-%02d-%s.jpg" % (i, name))
        img.save(fn, "JPEG", quality=86, optimize=True)
        made.append(fn)
        print("  %-34s %6.0f KB" % (os.path.basename(fn),
                                    os.path.getsize(fn) / 1024))

    print("\n%d backgrounds in %s" % (len(made), OUT))


if __name__ == "__main__":
    main()
