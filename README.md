# xt-zim-imager

**xt-zim-imager** is a high-performance desktop and command-line tool that converts Kiwix `.zim` archives (such as `wikipedia_en_all_maxi`, `wikipedia_en_all_nopic`, `wikipedia_en_simple_all_maxi`, or Wiktionary dumps) into a single, self-contained, 512-byte sector-aligned `.img` archive.

It is purpose-built for **CPR-vCodex / CrossInk Wikipedia** on ESP32-C3 / ESP32-S3 e-ink readers (such as the Xteink X4 and X3), enabling instant prefix search, zero-heap-fragmentation article streaming, on-device Infobox and 2D Table viewers, inline e-ink images, flattened MathJax formulas, and full multilingual glyph fallback (`FBF1`).

---

## Table of Contents

1. [Key Features](#key-features)
2. [System Architecture & Component Breakdown](#system-architecture--component-breakdown)
   - [1. Wails Desktop GUI & App Coordinator (`main.go`, `app.go`, `frontend/`)](#1-wails-desktop-gui--app-coordinator-maingo-appgo-frontend)
   - [2. Command-Line Interface (`cli.go`)](#2-command-line-interface-cligo)
   - [3. Three-Phase ZIM Processing Engine (`zim_processor.go`)](#3-three-phase-zim-processing-engine-zim_processorgo)
   - [4. Concurrent Local ZIM Reader (`local_gozim/`)](#4-concurrent-local-zim-reader-local_gozim)
   - [5. MathJax SVG Flattener (`MathJaxFlatten/`)](#5-mathjax-svg-flattener-mathjaxflatten)
   - [6. Multilingual Fallback Font Pipeline (`font-production/`)](#6-multilingual-fallback-font-pipeline-font-production)
   - [7. Built-In `.img` Inspector & Test Suite (`test_suite.go`, `TestImg.tsx`)](#7-built-in-img-inspector--test-suite-test_suitego-testimgtsx)
   - [8. Interactive E-Ink Image Optimiser Sandbox (`image-optimiser/`)](#8-interactive-e-ink-image-optimiser-sandbox-image-optimiser)
3. [Binary `.img` Layout & `CPR1` Footer Specification](#binary-img-layout--cpr1-footer-specification)
4. [Article Payload Format Specification](#article-payload-format-specification)
5. [Building from Source](#building-from-source)
6. [Usage Guide (GUI & CLI)](#usage-guide-gui--cli)
7. [Rebuilding the Fallback Font (`fallback_noto.bin`)](#rebuilding-the-fallback-font-fallback_notobin)
8. [License](#license)

---

## Key Features

- **Parsoid HTML & MediaWiki Extraction:**
  - Strips navigation boxes, message boxes, citation clutter, and layout tables while preserving article structure, headings, lists, internal links (`[Label|Target]`), infoboxes (`===INFOBOX===`), data tables (`===TABLE:N===`), and biological cladograms (`===CLADE:N===`).
  - Merges vertical navigation sidebars (`.sidebar`, `.vertical-navbox`) into the article's infobox stream and restores whitespace across inline `<span class="nowrap">` elements.
  - Pre-shapes Right-to-Left (RTL) scripts (Arabic, Hebrew) into visual order using Unicode BiDi analysis while preserving combining marks, and strips zero-width/invisible formatting codepoints (`U+200B`–`U+200F`, `U+202A`–`U+202E`, `U+2060`–`U+206F`, `U+FEFF`).
- **Math & Chemical Formula Rendering:**
  - Converts inline LaTeX `<math>` annotations into readable plain-text Unicode math notation and flattens complex/block `<svg>` MathJax equations into crisp rasterized images.
- **E-Ink Image Optimisation:**
  - Resizes, sharpens (7-tap Gaussian unsharp mask), and tone-maps embedded article images into either compact Grayscale JPEGs (for on-device 4-level e-ink tone mapping) or pre-dithered 1-bit / 2-bit PNGs (Floyd-Steinberg, Atkinson, Sierra, Bayer 8x8, or Threshold).
- **GC-Friendly Static 512-Byte B-Tree Index:**
  - Builds a pointer-free (`FlatBTreeNode`) 512-byte sector-aligned B-Tree index in breadth-first order for instant prefix search, exact/case-insensitive title lookup, and random article traversal directly from SD storage.
- **Bundled Multilingual Fallback Font (`FBF1`):**
  - Embeds [`font-production/fallback_noto.bin`](font-production/fallback_noto.bin) (`FBF1` 16 MB glyph binary covering Basic Multilingual Plane `U+0000`–`U+FFFF` + SMP Mathematical Alphanumeric Symbols `U+1D400`–`U+1D7FF`, including CJK Unified Ideographs, Hiragana, Katakana, Hangul, Cyrillic, Greek, IPA, Arabic, Hebrew, Devanagari, Thai, and Box Drawing).
- **Self-Describing `CPR1` Footer:**
  - Appends a 512-byte `CPR1` trailer sector at the end of the `.img` containing the exact sector offsets for the B-Tree root, article/image chunks, `FBF1` fallback font, and image flags—allowing the reader firmware to configure itself automatically with zero manual config files.

---

## System Architecture & Component Breakdown

```
┌────────────────────────────────────────────────────────────────────────────┐
│                              xt-zim-imager                                 │
├───────────────────────────┬───────────────────────────┬────────────────────┤
│   Wails Desktop GUI       │   CLI Runner (cli.go)     │  Image Optimiser   │
│  (frontend/ + app.go)     │  process-image /          │  (image-optimiser) │
│  • Convert ZIM to .img    │  append-font              │                    │
│  • Copy .img Contiguous   └─────────────┬─────────────┴────────────────────┤
│  • Test .img Inspector                  │                                  │
│                                         ▼                                  │
├────────────────────────────────────────────────────────────────────────────┤
│                Core Processing Engine (zim_processor.go)                   │
│  ┌─────────────────────┐  ┌──────────────────────┐  ┌───────────────────┐  │
│  │ Phase 1: Parallel   │  │ Phase 2: Sequential  │  │ Phase 3: Redirect │  │
│  │ Metadata & Redirect │─▶│ Cluster Extraction,  │─▶│ Resolution, BFS   │  │
│  │ Scanner             │  │ HTML/Math/Image Pipe │  │ B-Tree & CPR1     │  │
│  └─────────────────────┘  └──────────────────────┘  └───────────────────┘  │
├───────────────────────────┬───────────────────────────┬────────────────────┤
│     local_gozim/          │     MathJaxFlatten/       │  font-production/  │
│  Thread-safe ZIM cluster  │  SVG <use>/<defs> &       │  FBF1 16 MB Noto + │
│  & directory readers      │  matrix transform engine  │  Unifont generator │
└───────────────────────────┴───────────────────────────┴────────────────────┘
```

### 1. Wails Desktop GUI & App Coordinator (`main.go`, `app.go`, `frontend/`)
- **[`main.go`](main.go)**: Entry point that detects whether CLI arguments (`process-image`, `append-font`) were passed; if invoked with no arguments, launches the Wails v2 desktop application.
- **[`app.go`](app.go)**: Exposes Go backend methods to the React/TypeScript frontend (`SelectZIM`, `SelectImgOut`, `StartProcessingImg`, `SelectImgIn`, `SelectTargetDir`, `CopyImgContiguous`, `StartAppendingFont`) and streams real-time events (`log`, `progress`, `eta`, `worker_stats`, `done`, `error`).
- **[`frontend/src/App.tsx`](frontend/src/App.tsx)**: High-contrast e-ink-styled interface providing three operational modes:
  1. **Convert ZIM to `.img`**: Builds a standalone `.img` file on your computer.
  2. **Copy `.img` to SD Card (Contiguous)**: Preallocates the exact file size via `os.File.Truncate(size)` on the target FAT32/exFAT SD card before copying in 4 MB chunks. This guarantees contiguous sector allocation on the SD card, which is critical for fast raw SD sector reads on the ESP32.
  3. **Test `.img` File**: Interactive `.img` verifier and article previewer.

### 2. Command-Line Interface (`cli.go`)
- **[`cli.go`](cli.go)**: Headless CLI runner for automated builds and batch conversions:
  - `xt-zim-imager process-image <input.zim> <output.img>`: Runs the full 3-phase ZIM-to-`.img` pipeline with console progress logging.
  - `xt-zim-imager append-font <existing.img>`: Appends or updates `fallback_noto.bin` in-place on an existing `.img` archive and updates its `CPR1` footer without re-running ZIM extraction.

### 3. Three-Phase ZIM Processing Engine (`zim_processor.go`)
The heart of the tool is [`ProcessZIM`](zim_processor.go), which processes millions of articles and images with minimal memory overhead:

- **Phase 1 — Parallel Metadata & Redirect Scan:**
  - Partitions the ZIM URL index across `runtime.NumCPU()` worker goroutines.
  - Filters entries to keep articles (`A/` or `C/` text/html) and optionally images (`I/` or `image/*`), skipping stylesheets, scripts, and special namespaces.
  - Records redirect entries (`Redirect{Index, Title, URL, TargetIndex}`) and groups content entry indices by their ZIM cluster ID (`map[uint32][]uint32`).

- **Phase 2 — Sequential Cluster Extraction, HTML Cleaning & Image Re-encoding:**
  - Sorts all ZIM cluster IDs in ascending order so disk reads occur sequentially, feeding `ClusterJob` items to a pool of `NumCPU()` worker goroutines.
  - **HTML & Parsoid Transformation (`extractHTML`)**:
    - Parses article DOM via `golang.org/x/net/html`.
    - Strips `<script>`, `<style>`, `<nav>`, `.navbox`, `.ambox`, `.mbox-small`, `.reflist`, `.mw-editsection`, and hidden elements.
    - Extracts `<table class="infobox">` and `.sidebar` / `.vertical-navbox` into structured `===INFOBOX===` payloads (merging sidebar navigation tables onto the end of the infobox so both are accessible from the firmware's Infobox viewer) and places an `[INFOBOX]` marker in the article body.
    - Extracts data tables into `===TABLE:N===` payloads (`[TABLE:N]` markers in the body) and nested biological cladograms (`table.clade`) into ASCII/Unicode tree diagrams (`===CLADE:N===`).
    - Converts hyperlinks `<a href="...">` pointing to internal Wikipedia articles into `[Display Text|Target_URL]` syntax (or `[Text]` when label and target match).
    - Converts `<math>` LaTeX annotations via `cleanLatexMath` into Unicode symbols (`α`, `β`, `≥`, `≠`, `∑`, `∫`, superscripts/subscripts) and rasterizes complex block MathJax SVGs into inline `[IMG:hash]` entries.
  - **Image Processing (`processImageBytes`)**:
    - Decodes JPEG, PNG, GIF, WebP, and SVG (including flattened MathJax SVGs).
    - Converts images to grayscale (`toGrayImage`) with white-background alpha compositing (`invA`), resizes via fixed-point bilinear scaling (`scaleGrayBiLinear`) to fit `440×550` (or dynamically upscales tiny inline equations/diagrams by 2×), and applies a 7-tap Gaussian unsharp mask + contrast/clipping tone LUT (`filterGrayUnsharpAndLUT`).
    - Encodes to Grayscale JPEG (quality 75) or dithered 1-bit/2-bit PNG.
  - **32 KB Gzip Article Clustering (`flushCluster`)**:
    - Buffers extracted articles up to `32 KB` uncompressed, prepending each article with a 4-byte little-endian uncompressed length header, then compresses the cluster with Gzip (`32-byte` aligned on disk).
    - Stores a packed 64-bit value in the B-Tree: `(uint64(offset32) << 32) | uint64(uncompressedOffset)`, where `offset32 = byteOffset / 32`.
    - Writes processed images directly to disk aligned to 32-byte boundaries with the sentinel lower 32 bits `0xFFFFFFFF`: `(uint64(offset32) << 32) | 0xFFFFFFFF`.

- **Phase 3 — Redirect Resolution, Static B-Tree Serialization & `CPR1` Footer:**
  - Resolves every collected redirect against the `sectorSlice` lookup table and inserts original, URL-formatted, and lowercase keys into the in-memory `BTree`.
  - **GC-Noscan B-Tree (`FlatBTreeNode`)**: Allocates nodes in 65,536-node slabs containing zero Go pointers so the Go garbage collector marks the entire B-Tree backing store as `noscan`, preventing GC pauses even on multi-million-entry English Wikipedia dumps.
  - Serializes the B-Tree in **Breadth-First Search (BFS)** order (`SerializeTo`) into 512-byte sectors so sector `0` of the index is always the root node.
  - Appends `fallback_noto.bin` (if found) aligned to 512-byte sectors, and writes the final 512-byte `CPR1` metadata footer.

### 4. Concurrent Local ZIM Reader (`local_gozim/`)
- A customized local package based on `gozim` providing lock-free, per-worker `DirReader` and `ClusterReader` instances (`local_gozim/zim.go`, `zstd_reader.go`, `xz_reader.go`) so all CPU cores can decompress Zstandard and XZ ZIM clusters in parallel without contention.

### 5. MathJax SVG Flattener (`MathJaxFlatten/`)
- Wikipedia ZIM dumps store mathematical equations as SVG `<defs>` glyph libraries referenced by `<use xlink:href="#...">` nodes with deeply nested `matrix(...)`, `translate(...)`, and `scale(1, -1)` transforms that standard Go SVG rasterizers (`oksvg`) cannot render directly.
- [`MathJaxFlatten/flatten.go`](MathJaxFlatten/flatten.go), [`xml_parser.go`](MathJaxFlatten/xml_parser.go), and [`path_parser.go`](MathJaxFlatten/path_parser.go) parse the SVG AST, inline all `<use>` references, multiply affine transformation matrices down to absolute SVG path coordinates, and emit standard `<path d="...">` geometry that `oksvg` + `rasterx` rasterize cleanly into e-ink bitmaps.

### 6. Multilingual Fallback Font Pipeline (`font-production/`)
- **[`font-production/merge_all_noto.py`](font-production/merge_all_noto.py)** & **[`font-production/SKILL.md`](font-production/SKILL.md)**:
  - Generates the 16 MB `fallback_noto.bin` (`FBF1` magic header) embedded at the end of `.img` files.
  - Renders 65,536 BMP codepoints (`U+0000`–`U+FFFF`) + SMP mathematical symbols (`U+1D400`–`U+1D7FF`) using a prioritized stack of 29 Google Noto Sans fonts (CJK, Arabic, Hebrew, Devanagari, Thai, Bengali, Tamil, Cyrillic, Greek, IPA, Math, Symbols) with GNU Unifont as ultimate fallback.
  - Uses 2-bit anti-aliased rendering (`18px`) for non-CJK scripts and crisp 1-bit rendering (`16px`) for CJK ideographs.

### 7. Built-In `.img` Inspector & Test Suite (`test_suite.go`, `TestImg.tsx`)
- **[`test_suite.go`](test_suite.go)** & **[`frontend/src/TestImg.tsx`](frontend/src/TestImg.tsx)**: Allows you to open any generated `.img` file inside the GUI, read its `CPR1` footer, search articles via the B-Tree index, pick random articles, click internal links, inspect parsed `===INFOBOX===` and `===TABLE:N===` payloads, and view embedded e-ink images before copying the file to an SD card.

### 8. Interactive E-Ink Image Optimiser Sandbox (`image-optimiser/`)
- **[`image-optimiser/main.go`](image-optimiser/main.go)** & **[`image-optimiser/index.html`](image-optimiser/index.html)**: Standalone localhost web tool (`go run ./image-optimiser <file.zim>`) for visually comparing image resize filters, Gaussian unsharp mask parameters, contrast/clipping curves, and 1-bit/2-bit dithering algorithms on live images pulled from a ZIM archive.

---

## Binary `.img` Layout & `CPR1` Footer Specification

Every `.img` file produced by `xt-zim-imager` is 512-byte sector-aligned and follows this layout:

```
Sector 0                                                    indexStartSector
┌──────────────────────────────────────────────────────────┬────────────────────────┐
│  Compressed Article Clusters (Gzip, 32-byte aligned)     │  Static B-Tree Index   │
│  & Raw E-Ink Images (JPEG/PNG, 32-byte aligned)          │  (512 B per node, BFS) │
└──────────────────────────────────────────────────────────┴────────────────────────┘
                                                                                    │
┌──────────────────────────────────────────────────────────┬────────────────────────┘
│  FBF1 Fallback Font (fallback_noto.bin, 16 MB, optional) │  CPR1 Footer (512 B)   │
└──────────────────────────────────────────────────────────┴────────────────────────┘
fontSector                                                  Final Sector (EOF - 512)
```

### 64-Bit B-Tree Value Encoding
Each key in the 512-byte `FlatBTreeNode` maps a 64-byte null-padded key to a 64-bit unsigned integer (`uint64`):
- **Upper 32 bits (`val64 >> 32`)**: `offset32` — the starting byte offset of the Gzip cluster (or raw image) divided by `32` (supporting archives up to **128 GB**).
- **Lower 32 bits (`val64 & 0xFFFFFFFF`)**:
  - For **text articles**: Byte offset of the article's 4-byte length header inside the uncompressed Gzip cluster.
  - For **images**: `0xFFFFFFFF` sentinel value (the image starts with its 4-byte length header directly at `offset32 * 32`).

### `CPR1` 512-Byte Footer Structure (Final Sector)
| Byte Offset | Type | Field | Description |
|---|---|---|---|
| `0..3` | `char[4]` | Magic | ASCII `"CPR1"` (`0x43 0x50 0x52 0x31`) |
| `4..7` | `uint32` (LE) | `indexStartSector` | 512-byte sector offset of the B-Tree root node |
| `8..11` | `uint32` (LE) | `chunksStartSector` | 512-byte sector offset of the data chunks (`0` for `.img` files) |
| `12..15` | `uint32` (LE) | `fontSector` | 512-byte sector offset of the `FBF1` fallback font (`0` if omitted) |
| `16..19` | `uint32` (LE) | `flags` | Bit `0`: `1` = Images enabled (`Maxi`), `0` = Text-only (`NoPic`) |
| `20..511` | `uint8[492]` | Reserved | Zero-padded to 512 bytes |

---

## Article Payload Format Specification

Each uncompressed article in a Gzip cluster begins with a `uint32` (LE) byte length followed by UTF-8 text formatted for single-pass streaming on the firmware:

```text
Article Title
Lead paragraph text with [internal links|Target_Article] and [INFOBOX] marker.
[IMG:d41d8cd98f00b204e9800998ecf8427e]
== Section Heading ==
Body text referencing a table [TABLE:1] and cladogram [CLADE:1].
===END===
===INFOBOX===
[H] Article Title
[I] a1b2c3d4e5f60718293a4b5c6d7e8f90
Born | 7 April 1772 [Besançon|Besançon], France
Known for | [Fourierism|Fourierism]
===END===
===TABLE:1===
Header 1 | Header 2 | Header 3
Row 1A | Row 1B | Row 1C
===END===
===CLADE:1===
┌── Taxon A
└──┬── Taxon B
   └── Taxon C
===END===
```

---

## Building from Source

### Prerequisites
- **Go** `1.22+`
- **Node.js** `18+` & `npm`
- **Wails CLI** `v2.6+` (`go install github.com/wailsapp/wails/v2/cmd/wails@latest`)

### Development Mode (Live Reload GUI)
```bash
wails dev
```

### Production Build (GUI + CLI Binary)
```powershell
# Windows
$env:GO111MODULE="on"
wails build
```
```bash
# macOS Universal (Apple Silicon + Intel)
wails build -platform darwin/universal
```
The compiled application is written to `build/bin/xt-zim-imager.exe` (Windows) or `build/bin/xt-zim-imager.app` (macOS).

> **macOS Gatekeeper Note:** When downloading `xt-zim-imager-macos-universal.zip` from GitHub Releases, macOS Gatekeeper may flag the unsigned `.app` bundle on first launch. Clear the quarantine attribute once before opening:
> ```bash
> xattr -cr xt-zim-imager.app
> ```

---

## Usage Guide (GUI & CLI)

### 1. Desktop GUI Workflow (Recommended)
Launch `xt-zim-imager` with no arguments to open the desktop application:

1. **Convert ZIM to `.img`:**
   - Select **Convert ZIM to .img**.
   - Click **Browse** under **1. Select Source ZIM** and pick your `.zim` file (if the filename contains `_nopic_`, **Extract and Process Images** is automatically unchecked).
   - If extracting images, choose between:
     - **JPEG (Grayscale)** *(Recommended)*: Compact archive size; the firmware tone-maps and dithers images on the fly.
     - **PNG (Dithered)**: Pre-dithered `1-bit` or `2-bit (4 grey scale)` PNGs with selectable dithering (`Floyd-Steinberg`, `Atkinson`, `Sierra`, `Bayer 8x8`, or `None`).
   - Select your output `.img` path under **2. Target** and click **Create Image**.
   - Wait for the **`✓ JOB COMPLETE`** banner to appear.

2. **Copy `.img` to SD Card (Contiguous):**
   - Insert your microSD card (formatted as FAT32 or exFAT).
   - Select **Copy .img to SD Card (Contiguous)**.
   - Choose your newly created `.img` file and target folder on the SD card (e.g., `E:\` or `E:\wiki`), then click **Copy Contiguously**.
   - This preallocates contiguous sectors on the SD card before copying so the ESP32 can seek across multi-gigabyte archives without FAT cluster-chain fragmentation.

3. **Test `.img` File (Optional):**
   - Select **Test .img File**, browse to your `.img`, and test title lookups, random articles, infoboxes, tables, and images directly on your PC.

### 2. Command-Line Interface (CLI)
Build a complete `.img` archive from a `.zim` file (with images enabled in Grayscale JPEG mode):
```bash
xt-zim-imager process-image <input.zim> <output.img>
```

Append or update the `FBF1` fallback font (`font-production/fallback_noto.bin`) in-place on an existing `.img` file:
```bash
xt-zim-imager append-font <existing.img>
```

---

## Rebuilding the Fallback Font (`fallback_noto.bin`)

The prebuilt 16 MB [`font-production/fallback_noto.bin`](font-production/fallback_noto.bin) is included in the repository and automatically embedded by `xt-zim-imager` whenever present.

To customize or rebuild `fallback_noto.bin` from Google Noto Sans / Noto Sans CJK / GNU Unifont sources, see [`font-production/SKILL.md`](font-production/SKILL.md) and run:
```bash
cd font-production
python merge_all_noto.py
```

---

## License

Released under the [MIT License](LICENSE).
Includes a modified local copy of `gozim` under [`local_gozim/`](local_gozim/LICENSE) and bitmap glyphs rendered from SIL Open Font License (OFL) Google Noto fonts and GNU Unifont.
