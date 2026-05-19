package main

import (
	"bytes"
	"image"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
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

// maxMediaBytes caps how large an image we are willing to serve back to the
// browser. Generated images can be larger than the API inlining limit, but a
// hard ceiling still protects against accidental gigabyte files.
const maxMediaBytes int64 = 25 * 1024 * 1024 // 25 MB

// handleMedia serves an image file referenced by absolute or relative path
// (passed as the `path` query parameter) back to the browser. It is the
// counterpart of the markdown-image rewriting on the client: tool outputs and
// assistant replies may reference local image files; this endpoint streams
// those bytes after enforcing two safety rules:
//
//  1. The resolved path must live under the server's working directory
//     (where logs/uploads/<session>/ lives) OR under the OS temp directory.
//     Any other prefix is rejected as out-of-policy.
//  2. The file extension must be a recognised image type. Non-image MIME
//     types are refused; this endpoint is not a generic file server.
func handleMedia(d serverDeps) gin.HandlerFunc {
	// Resolve allowed roots once at handler construction; both are absolute
	// and cleaned so prefix-match below is well-defined.
	roots := mediaAllowedRoots()
	return func(c *gin.Context) {
		id := c.Param("id")
		if _, ok := d.Registry.Get(id); !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
			return
		}
		raw := strings.TrimSpace(c.Query("path"))
		if raw == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "missing path"})
			return
		}
		// Strip any file:// prefix the model may have included.
		raw = strings.TrimPrefix(raw, "file://")

		abs, err := filepath.Abs(raw)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid path"})
			return
		}
		abs = filepath.Clean(abs)

		if !pathUnderAnyRoot(abs, roots) {
			c.JSON(http.StatusForbidden, gin.H{"error": "path outside allowed roots"})
			return
		}
		mime := imageMIME(abs)
		if mime == "" {
			c.JSON(http.StatusUnsupportedMediaType, gin.H{"error": "not a recognised image type"})
			return
		}
		f, err := os.Open(abs)
		if err != nil {
			if os.IsNotExist(err) {
				c.JSON(http.StatusNotFound, gin.H{"error": "file not found"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "open failed"})
			return
		}
		defer f.Close()
		st, err := f.Stat()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "stat failed"})
			return
		}
		if st.Size() > maxMediaBytes {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "file exceeds media size limit"})
			return
		}
		c.Header("Content-Type", mime)
		c.Header("Cache-Control", "private, max-age=300")
		c.Status(http.StatusOK)
		_, _ = io.Copy(c.Writer, f)
	}
}

// mediaAllowedRoots returns the absolute, cleaned directories from which the
// media endpoint will serve files: the server's working directory (which
// contains logs/uploads/) plus the OS temp directory (where many MCP image
// generators write their output).
func mediaAllowedRoots() []string {
	var roots []string
	if cwd, err := os.Getwd(); err == nil {
		if abs, err := filepath.Abs(cwd); err == nil {
			roots = append(roots, filepath.Clean(abs))
		}
	}
	if tmp := os.TempDir(); tmp != "" {
		if abs, err := filepath.Abs(tmp); err == nil {
			roots = append(roots, filepath.Clean(abs))
		}
	}
	return roots
}

// pathUnderAnyRoot reports whether `abs` resolves to a location inside any of
// `roots`. Both `abs` and each root are expected to be absolute + cleaned.
// Comparison is done on path components (not raw string prefix) to avoid
// false positives like "/tmpfoo" matching "/tmp".
func pathUnderAnyRoot(abs string, roots []string) bool {
	for _, root := range roots {
		rel, err := filepath.Rel(root, abs)
		if err != nil {
			continue
		}
		if rel == "." || (!strings.HasPrefix(rel, "..") && !strings.HasPrefix(rel, string(filepath.Separator))) {
			return true
		}
	}
	return false
}
