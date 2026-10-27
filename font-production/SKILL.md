---
name: build-fallback-font
description: Use this skill when the user asks to rebuild, maintain, or update the fallback_noto.bin font used by the e-reader to render non-English Wikipedia text.
---

# Build Fallback Font (fallback_noto.bin)

This skill provides step-by-step instructions for producing the `fallback_noto.bin` file from Google's Noto font collection. This file acts as the universal fallback font for the ESP32 e-reader, containing glyphs for global languages like Arabic, Hebrew, Thai, Cyrillic, and CJK.

## 1. Prerequisites

Before running the generation script, ensure you have the complete Google Noto font family and the required Python libraries installed on the host system.

Run the following commands to install the system fonts:
```bash
sudo apt-get update
sudo apt-get install -y fonts-noto-core fonts-noto-extra fonts-noto-ui-core fonts-noto-cjk
```

Install the required Python packages:
```bash
pip install fonttools pillow
```

## 2. Running the Font Generator

The generator script is located in this directory: `merge_all_noto.py`.

Run it directly from this directory:
```bash
python3 merge_all_noto.py
```

### What the script does:
1. **Initializes the Binary:** If `fallback_noto.bin` does not exist, it creates a blank 16.7MB file initialized with the custom `FBF1` header. This pre-allocates space for 131,072 characters (Unicode Planes 0 and 1) at 128 bytes per glyph.
2. **Scans System Fonts:** It globs `/usr/share/fonts/truetype/noto/*-Regular.ttf` as well as `/usr/share/fonts/opentype/noto/Noto*CJK-Regular.ttc` (where Debian/Ubuntu installs `fonts-noto-cjk`) and sorts them so `NotoSans-Regular.ttf` takes highest priority for Latin/Greek/Cyrillic while `NotoSansCJK-Regular.ttc` provides CJK coverage.
3. **Extracts and Renders:** It opens each font, scans its `cmap` for supported codepoints, and uses Python `Pillow` to draw each character.
4. **Patches the Binary:** Valid glyph bitmaps are written into the `fallback_noto.bin` file using an exact byte offset calculation.

## 3. Font Parameters Explained

If you need to tweak the font in the future, these are the critical parameters hardcoded inside `merge_all_noto.py` that dictate how the font renders on the ESP32's 1-bit E-ink display:

* **Font Size (`29`):** The Pillow font size is set to 29 (recorded in `header[8]`). This matches the 14pt reader font scale and allows enough resolution for complex glyphs (like CJK, Thai, or Arabic) to be readable.
* **Canvas Box (`32x32`):** Each glyph is rendered onto a 32x32 pixel canvas. At 1-bit monochrome, this is exactly 128 bytes per glyph.
* **Anchor & Baseline (`(16, 25)` / `anchor="ms"`):** Glyphs are anchored at the middle-baseline at Y=25. This ensures tall ascenders and deep descenders (e.g., in Thai scripts) do not get clipped out of the 32x32 box.
* **Stroke Thickness Threshold (`pixels[col, row] > 64`):** E-ink screens suffer from single-pixel line fading. Pillow uses anti-aliasing (greyscale values 0-255). By capturing any pixel with a value greater than `64` (rather than the standard 127), we artificially thicken the strokes, acting as a "Semi-Bold" weight filter to guarantee legibility on the device.
