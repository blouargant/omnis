package main

import (
	"bytes"
	"image"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"math"
	"path/filepath"
	"strings"
)

// imageMIME returns the MIME type for recognized image extensions, or "" for
// anything else. Only the types accepted by both Anthropic and OpenAI vision
// APIs are included.
func imageMIME(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	default:
		return ""
	}
}

// maxImageBytes is Anthropic's hard limit for inline image data.
const maxImageBytes = 4_900_000 // 4.9 MB — safely under the 5 MB API limit

// shrinkIfNeeded returns the image data unchanged if it is already within
// maxImageBytes. Otherwise it decodes the image, scales it down proportionally
// (nearest-neighbour, pure stdlib), and re-encodes as JPEG. If decoding fails
// (e.g. unsupported format) the original bytes are returned as-is so the
// caller can forward them and let the API return a useful error.
func shrinkIfNeeded(data []byte, mime string) ([]byte, string) {
	if len(data) <= maxImageBytes {
		return data, mime
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		// Unsupported format (e.g. webp without a decoder registered) — pass
		// through and let the downstream API surface the error.
		return data, mime
	}

	current := data
	currentImg := img
	outMime := "image/jpeg"

	// Iteratively shrink until we're under the limit (usually one pass).
	for range 5 {
		if len(current) <= maxImageBytes {
			break
		}
		ratio := math.Sqrt(float64(maxImageBytes) / float64(len(current)))
		b := currentImg.Bounds()
		w := max(1, int(float64(b.Dx())*ratio))
		h := max(1, int(float64(b.Dy())*ratio))

		dst := image.NewRGBA(image.Rect(0, 0, w, h))
		for y := range h {
			sy := b.Min.Y + int(float64(y)*float64(b.Dy())/float64(h))
			for x := range w {
				sx := b.Min.X + int(float64(x)*float64(b.Dx())/float64(w))
				dst.Set(x, y, currentImg.At(sx, sy))
			}
		}

		var buf bytes.Buffer
		if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 85}); err != nil {
			break
		}
		current = buf.Bytes()
		currentImg = dst
	}

	return current, outMime
}
