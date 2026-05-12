---
name: pdf
description: Extract or summarise text from a local PDF file.
metadata:
  author: blouargant@chapsvision.com
  tags: [ "file", "pdf", "text extraction" ]
---

# PDF

If `pdftotext` is available on the host:

1. Use `bash` to run `pdftotext -layout <input> -`.
2. Read the captured stdout.
3. Summarise per-section.

If `pdftotext` is missing, suggest `brew install poppler` (macOS) or
`apt-get install poppler-utils` (Debian/Ubuntu) and stop.
