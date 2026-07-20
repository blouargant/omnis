---
name: liteparse
description: Preferred local parser for extracting text from PDFs, images, and scanned/layout-heavy documents (OCR, screenshots, complex PDFs) — far better than pdftotext. Use this FIRST for any PDF, image, or OCR text-extraction or conversion task. NOTE for Office documents (DOCX, PPTX, XLSX, ODT) — for plain text extraction prefer the `pandoc` skill, which needs no LibreOffice; only use LiteParse for Office docs when you need its layout/OCR/screenshot output. The `pdf` skill (pdftotext) is only a fallback for when LiteParse cannot be installed.
compatibility: Requires Python 3.10+ and the `lit` CLI from `liteparse`, installed with pipx (`pipx install liteparse`) — pipx isolates the app and puts `lit` on PATH, and works on PEP 668 / externally-managed Python where `pip install` is blocked
license: MIT
metadata:
  author: LlamaIndex
  version: "0.1.0"
  tags: "file, pdf, docx, pptx, xlsx, image, ocr, text extraction"
---

# LiteParse Skill

Parse unstructured documents (PDF, DOCX, PPTX, XLSX, images, and more) locally with LiteParse: fast, lightweight, no cloud dependencies or LLM required.

**LiteParse is the preferred PDF/document parser** — it produces much better
results than `pdftotext`, especially on scanned, multi-column, or
layout-heavy PDFs, and it also handles formats `pdftotext` cannot. Reach for
the `pdf` skill (pdftotext) **only as a fallback** when `lit` is unavailable and
cannot be installed (see Step 1).

## Step 0 — Install discipline (READ FIRST)

Apply these rules before installing anything — and *especially* before **re-**trying
an install. They prevent the common failure of re-installing tools that are
already present and thrashing on installs when the real problem is elsewhere:

- **Check before you install.** Run `command -v <tool>` (e.g. `command -v lit`,
  `command -v libreoffice`, `command -v pandoc`, `command -v convert`). **If it
  prints a path, the tool is ALREADY INSTALLED — do NOT install or re-install
  it.**
- **Install each tool at most once.** Never run the same install command twice in
  a task. If you already ran it, treat that dependency as handled.
- **A conversion failure is NOT proof of a missing package.** If the required
  binary is already on PATH and a parse still fails, the cause is *not* a missing
  dependency, and installing more packages will not fix it. **Stop, read the
  actual error, and diagnose or fall back** (see the fallbacks in Step 1). In
  particular, a **snap-packaged LibreOffice** is sandboxed: it cannot read files
  in hidden directories (e.g. uploads under `~/.omnis/…`) nor write its temp PDF,
  so `lit` will report `source file could not be loaded` or `output PDF not
  found` no matter how many times you reinstall it — use **pandoc** for Office
  documents instead (see the "Optional system dependencies" note under Step 1).

## Step 1 — Check that `lit` is installed

Before doing anything else, verify the `lit` CLI is available on the host:

```bash
command -v lit
```

- **If it prints a path** → LiteParse is installed; continue to Step 2.
- **If it prints nothing (exit status 1)** → LiteParse is **not installed**.

  > ⚠️ **Do NOT fall back to `pdftotext` here.** "Not installed" is **not** the
  > same as "unavailable". Your **very next action must be to ask the user
  > whether to install LiteParse** — never silently switch to pdftotext. The
  > fallback is only reachable *after* the user has explicitly declined the
  > install, or the install has actually failed.

  Ask (via the `ask_user` tool when available, otherwise in chat):

  > LiteParse (`lit`) isn't installed. It gives much better PDF results than
  > pdftotext. Install it now with `pipx install liteparse`?

  Then, **based on the user's answer**:
  - **User agrees** → install and verify:
    ```bash
    pipx install liteparse
    lit --version
    ```
    Use **pipx**, not `pip` — `liteparse` is a CLI app, and on PEP 668 /
    externally-managed Python (modern Debian/Ubuntu) `pip install` is blocked
    with `error: externally-managed-environment`. If `pipx` itself is missing,
    install it first (`apt install pipx` / `brew install pipx`, then
    `pipx ensurepath`). If `lit --version` still fails after a successful
    install, `lit` landed in a dir that isn't on `PATH` (pipx uses
    `~/.local/bin`) — run `pipx ensurepath` and restart the shell, then treat
    LiteParse as unavailable and use the fallback below.
  - **User declines, or the install/verify genuinely fails** → *now* fall back:
    - For a **plain PDF**, hand off to the **`pdf` skill** (`pdftotext`) — it
      needs no install and covers the common case. Note the result may be lower
      quality on scanned or layout-heavy PDFs.
    - For **Office documents** (DOCX, PPTX, XLSX, ODT), hand off to the
      **`pandoc` skill** for text extraction — it needs no LibreOffice, is
      usually already installed, and is the **preferred path** for these formats
      (see the optional-dependencies note below).
    - For **images**, LiteParse (or ImageMagick + OCR) is required, so stop and
      explain that.

### Optional system dependencies (and when to skip LiteParse)

LiteParse needs extra tools only for certain inputs — check/install these **only
for the formats actually being parsed** (plain PDF parsing needs neither). Always
`command -v` first (Step 0):

- **Office documents** (DOCX, PPTX, XLSX, ODT, …): LiteParse converts these to
  PDF via **LibreOffice** first. For plain **text extraction, prefer the
  `pandoc` skill instead of LiteParse** — it is lightweight, usually already
  installed, needs no conversion step, and is immune to the LibreOffice-as-snap
  problem (Step 0). Example: `pandoc "file.docx" -t plain`. Only use
  LiteParse+LibreOffice for an Office doc when you specifically need LiteParse's
  layout/OCR/screenshot output, **and** LibreOffice is a real (non-snap) install:
  ```bash
  brew install --cask libreoffice   # macOS
  apt-get install libreoffice       # Ubuntu/Debian — avoid the snap: it is
                                    # sandboxed and cannot read ~/.omnis uploads
                                    # or write its temp PDF, so conversion fails
  ```
- **Images** (PNG, JPG, TIFF, …) require **ImageMagick** (`command -v convert`
  first):
  ```bash
  brew install imagemagick          # macOS
  apt-get install imagemagick       # Ubuntu/Debian
  ```

---

## Step 2 — Confirm the request

Once `lit` is available, make sure you have (ask the user for anything missing):

1. One or more files to parse (PDF, DOCX, PPTX, XLSX, images, etc.).
2. Any specific options: output format (json/text), page ranges, OCR
   preferences, DPI, etc.
3. What to do with the parsed content.

Then produce the appropriate `lit` CLI command (or a short Python script using
the `liteparse` package) and, once it's clear, run it and report the results.

---

## Step 3 — Produce the CLI Command or Script

### Parse a Single File

```bash
# Basic text extraction
lit parse document.pdf

# JSON output saved to a file
lit parse document.pdf --format json -o output.json

# Specific page range
lit parse document.pdf --target-pages "1-5,10,15-20"

# Disable OCR (faster, text-only PDFs)
lit parse document.pdf --no-ocr

# Use an external HTTP OCR server for higher accuracy
lit parse document.pdf --ocr-server-url http://localhost:8828/ocr

# Higher DPI for better quality
lit parse document.pdf --dpi 300
```

### Batch Parse a Directory

```bash
lit batch-parse ./input-directory ./output-directory

# Only process PDFs, recursively
lit batch-parse ./input ./output --extension .pdf --recursive
```

### Generate Page Screenshots

Screenshots are useful for LLM agents that need to see visual layout.

```bash
# All pages
lit screenshot document.pdf -o ./screenshots

# Specific pages
lit screenshot document.pdf --pages "1,3,5" -o ./screenshots

# High-DPI PNG
lit screenshot document.pdf --dpi 300 --format png -o ./screenshots

# Page range
lit screenshot document.pdf --pages "1-10" -o ./screenshots
```

---

## Step 4 — Key Options Reference

### OCR Options

| Option | Description |
|--------|-------------|
| (default) | Tesseract — zero setup, bundled with the library |
| `--ocr-language fra` | Set OCR language (ISO code) |
| `--ocr-server-url <url>` | Use external HTTP OCR server (EasyOCR, PaddleOCR, custom) |
| `--no-ocr` | Disable OCR entirely |

### Output Options

| Option | Description |
|--------|-------------|
| `--format json` | Structured JSON with bounding boxes |
| `--format text` | Plain text (default) |
| `-o <file>` | Save output to file |

### Performance / Quality Options

| Option | Description |
|--------|-------------|
| `--dpi <n>` | Rendering DPI (default: 150; use 300 for high quality) |
| `--max-pages <n>` | Limit pages parsed |
| `--target-pages <pages>` | Parse specific pages (e.g. `"1-5,10"`) |
| `--no-precise-bbox` | Disable precise bounding boxes (faster) |
| `--skip-diagonal-text` | Ignore rotated/diagonal text |
| `--preserve-small-text` | Keep very small text that would otherwise be dropped |

---

## Step 5 — Using a Config File

For repeated use with consistent options, generate a `liteparse.config.json`:

```json
{
  "ocrLanguage": "en",
  "ocrEnabled": true,
  "maxPages": 1000,
  "dpi": 150,
  "outputFormat": "json",
  "preciseBoundingBox": true,
  "skipDiagonalText": false,
  "preserveVerySmallText": false
}
```

For an HTTP OCR server:

```json
{
  "ocrServerUrl": "http://localhost:8828/ocr",
  "ocrLanguage": "en",
  "outputFormat": "json"
}
```

Use with:

```bash
lit parse document.pdf --config liteparse.config.json
```

---

## Step 6 — HTTP OCR Server API (Advanced)

If the user wants to plug in a custom OCR backend, the server must implement:

- **Endpoint**: `POST /ocr`
- **Accepts**: `file` (multipart) and `language` (string) parameters
- **Returns**:
```json
{
  "results": [
    { "text": "Hello", "bbox": [x1, y1, x2, y2], "confidence": 0.98 }
  ]
}
```

Ready-to-use wrappers exist for EasyOCR and PaddleOCR in the LiteParse repo.

---

## Supported Input Formats

| Category | Formats |
|----------|---------|
| PDF | `.pdf` |
| Word | `.doc`, `.docx`, `.docm`, `.odt`, `.rtf` |
| PowerPoint | `.ppt`, `.pptx`, `.pptm`, `.odp` |
| Spreadsheets | `.xls`, `.xlsx`, `.xlsm`, `.ods`, `.csv`, `.tsv` |
| Images | `.jpg`, `.jpeg`, `.png`, `.gif`, `.bmp`, `.tiff`, `.webp`, `.svg` |

Office documents require LibreOffice; images require ImageMagick. LiteParse auto-converts these formats to PDF before parsing.
