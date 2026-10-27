#!/usr/bin/env python3
import sys
import os
import glob
from fontTools.ttLib import TTFont
from PIL import Image, ImageDraw, ImageFont

def render_glyph(codepoint, font):
    img = Image.new("L", (32, 32), 0)
    draw = ImageDraw.Draw(img)
    char = chr(codepoint)
    try:
        draw.text((16, 25), char, font=font, fill=255, anchor="ms")
    except Exception:
        return bytearray(128)
        
    raw = img.tobytes()
    if max(raw) <= 64:
        return bytearray(128)

    bitmap = bytearray(128)
    for idx, val in enumerate(raw):
        if val > 64:
            bitmap[idx >> 3] |= (1 << (7 - (idx & 7)))
    return bitmap

def main():
    bin_path = "fallback_noto.bin"
    
    ttf_fonts = glob.glob("/usr/share/fonts/truetype/noto/*-Regular.ttf")
    ttc_fonts = [
        p for p in [
            "/usr/share/fonts/opentype/noto/NotoSerifCJK-Regular.ttc",
            "/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc",
        ]
        if os.path.exists(p)
    ]
    if not ttf_fonts and not ttc_fonts:
        print("Error: No Noto fonts found in /usr/share/fonts/truetype/noto/ or /usr/share/fonts/opentype/noto/.")
        print("Please install them using 'sudo apt install fonts-noto-core fonts-noto-extra fonts-noto-cjk'.")
        sys.exit(1)
        
    # Exclude high-contrast Display faces whose hairlines drop out at 1-bit e-ink sizes,
    # place CJK TTC collections first, and place NotoSans-Regular.ttf last so core
    # Latin/Greek/IPA/Cyrillic use clean sans-serif strokes.
    ttf_fonts = [p for p in ttf_fonts if "Display" not in os.path.basename(p)]
    ttf_fonts.sort(key=lambda p: (os.path.basename(p) == "NotoSans-Regular.ttf", p))
    fonts = ttc_fonts + ttf_fonts
    
    print(f"Initializing {bin_path}...")
    data = bytearray(16 + 131072 * 128)
    data[0:4] = b'FBF1'
    data[4] = 32
    data[5] = 32
    data[6] = 128 & 0xFF
    data[7] = (128 >> 8) & 0xFF
    data[8] = 29

    patched_cps = set()
    total_patched = 0
    # Iterate from highest-priority font (last) to lowest-priority font (first)
    # so each codepoint is rendered once by its highest-priority font.
    for font_path in reversed(fonts):
        is_ttc = font_path.endswith(".ttc")
        try:
            tt = TTFont(font_path, fontNumber=0) if is_ttc else TTFont(font_path)
            cmap = tt['cmap'].getBestCmap()
            codepoints = [cp for cp in sorted(cmap.keys()) if cp < 131072 and cp not in patched_cps]
        except Exception as e:
            print(f"Skipping {font_path} due to cmap error: {e}")
            continue

        if not codepoints:
            continue

        print(f"Merging {font_path} ({len(codepoints)} new codepoints)...")
        try:
            if is_ttc:
                font = ImageFont.truetype(font_path, 29, index=0, layout_engine=ImageFont.Layout.BASIC)
            else:
                font = ImageFont.truetype(font_path, 29, layout_engine=ImageFont.Layout.BASIC)
        except AttributeError:
            font = ImageFont.truetype(font_path, 29, index=0) if is_ttc else ImageFont.truetype(font_path, 29)
        except Exception as e:
            print(f"Failed to load font {font_path}: {e}")
            continue
            
        for cp in codepoints:
            isControl = (0x200B <= cp <= 0x200F) or (0x202A <= cp <= 0x202E) or (0x2060 <= cp <= 0x206F) or (cp == 0xFEFF) or (0x0000 <= cp <= 0x001F) or (0x007F <= cp <= 0x00A0)
            if isControl:
                continue
            
            bitmap = render_glyph(cp, font)
            if any(b != 0 for b in bitmap):
                offset = 16 + cp * 128
                data[offset:offset + 128] = bitmap
                patched_cps.add(cp)
                total_patched += 1

    with open(bin_path, "wb") as f:
        f.write(data)
        
    print(f"Total unique glyphs patched across all fonts: {total_patched}")

if __name__ == '__main__':
    main()
