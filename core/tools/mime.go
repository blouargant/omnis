package tools

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/gabriel-vasile/mimetype"
	"github.com/rwcarlsen/goexif/exif"
	"github.com/rwcarlsen/goexif/tiff"
	_ "golang.org/x/image/webp"
)

type MimeIn struct {
	Path string `json:"file_path" jsonschema:"required,path to the file to inspect"`
}
type MimeOut struct {
	Card string `json:"result"`
}

// RunMime detects the MIME type of a file by inspecting its content (magic
// bytes), compares the detected extension against the filename extension, then
// enriches the card with the system `file` description, image dimensions (for
// image types), and EXIF metadata when present.
func RunMime(_ context.Context, in MimeIn) (string, error) {
	info, err := os.Stat(in.Path)
	if err != nil {
		return fmt.Sprintf("Error: %v", err), nil
	}
	if info.IsDir() {
		return fmt.Sprintf("Error: %s is a directory, not a file", in.Path), nil
	}

	mt, err := mimetype.DetectFile(in.Path)
	if err != nil {
		return fmt.Sprintf("Error detecting MIME type: %v", err), nil
	}

	fileExt := strings.ToLower(filepath.Ext(in.Path))
	mimeExt := strings.ToLower(mt.Extension())

	var matchLine string
	if fileExt == "" {
		matchLine = "UNKNOWN — file has no extension"
	} else if fileExt == mimeExt {
		matchLine = "YES"
	} else {
		matchLine = fmt.Sprintf("NO — content is %s (%s) but filename claims %s",
			mt.String(), mimeExt, fileExt)
	}

	var sb strings.Builder
	fmt.Fprintf(&sb,
		"File      : %s\n"+
			"Path      : %s\n"+
			"Size      : %s\n"+
			"MIME type : %s\n"+
			"MIME ext  : %s\n"+
			"File ext  : %s\n"+
			"Match     : %s",
		info.Name(),
		in.Path,
		formatSize(info.Size()),
		mt.String(),
		mimeExt,
		fileExt,
		matchLine,
	)

	// `file` command description — available on most Unix systems.
	if desc := fileCommandDesc(in.Path); desc != "" {
		fmt.Fprintf(&sb, "\nfile -b   : %s", desc)
	}

	// Image-specific enrichment.
	if strings.HasPrefix(mt.String(), "image/") {
		if w, h, mode := imageConfig(in.Path); w > 0 {
			fmt.Fprintf(&sb, "\nDimensions: %d × %d px", w, h)
			if mode != "" {
				fmt.Fprintf(&sb, "\nColor mode: %s", mode)
			}
		}
		if exifCard := exifData(in.Path); exifCard != "" {
			fmt.Fprintf(&sb, "\n\nEXIF\n----\n%s", exifCard)
		}
	}

	return sb.String(), nil
}

// fileCommandDesc runs `file -b <path>` and returns its trimmed output, or ""
// if the binary is not found or the command fails.
func fileCommandDesc(path string) string {
	out, err := exec.Command("file", "-b", path).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// imageConfig returns the pixel dimensions and color model name of an image
// file using Go's stdlib decoders. Returns zeros on failure.
func imageConfig(path string) (w, h int, mode string) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, ""
	}
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return 0, 0, ""
	}
	return cfg.Width, cfg.Height, colorModelName(cfg.ColorModel)
}

func colorModelName(m color.Model) string {
	if m == nil {
		return ""
	}
	switch m {
	case color.RGBAModel:
		return "RGBA"
	case color.NRGBAModel:
		return "NRGBA"
	case color.RGBA64Model:
		return "RGBA64"
	case color.GrayModel:
		return "Gray"
	case color.Gray16Model:
		return "Gray16"
	case color.CMYKModel:
		return "CMYK"
	case color.YCbCrModel:
		return "YCbCr"
	default:
		return fmt.Sprintf("%T", m)
	}
}

// exifWalker implements exif.Walker for collecting EXIF fields.
type exifWalker struct {
	seen map[exif.FieldName]bool
	buf  *bytes.Buffer
}

func (w *exifWalker) Walk(name exif.FieldName, tag *tiff.Tag) error {
	if w.seen[name] {
		return nil
	}
	s := tag.String()
	// Skip large binary blobs (maker notes, thumbnails, embedded images).
	if len(s) > 200 {
		return nil
	}
	fmt.Fprintf(w.buf, "%-24s: %s\n", name, s)
	return nil
}

// exifData reads EXIF tags from an image and returns a formatted multi-line
// string, or "" when EXIF is absent or unreadable.
func exifData(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	x, err := exif.Decode(f)
	if err != nil {
		return ""
	}

	// High-priority fields shown first.
	priority := []exif.FieldName{
		exif.Make, exif.Model, exif.Software,
		exif.DateTime, exif.DateTimeOriginal, exif.DateTimeDigitized,
		exif.ImageWidth, exif.ImageLength,
		exif.Orientation,
		exif.XResolution, exif.YResolution, exif.ResolutionUnit,
		exif.ExposureTime, exif.FNumber, exif.ISOSpeedRatings,
		exif.FocalLength, exif.Flash,
		exif.GPSLatitude, exif.GPSLongitude, exif.GPSAltitude,
		exif.PixelXDimension, exif.PixelYDimension,
		exif.ColorSpace,
	}

	var buf bytes.Buffer
	seen := map[exif.FieldName]bool{}
	for _, name := range priority {
		tag, err := x.Get(name)
		if err != nil {
			continue
		}
		s := tag.String()
		if len(s) > 200 {
			continue
		}
		fmt.Fprintf(&buf, "%-24s: %s\n", name, s)
		seen[name] = true
	}

	// Walk remaining tags not already shown.
	walker := &exifWalker{seen: seen, buf: &buf}
	_ = x.Walk(walker)

	return strings.TrimRight(buf.String(), "\n")
}

func formatSize(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.2f GB (%d bytes)", float64(n)/float64(1<<30), n)
	case n >= 1<<20:
		return fmt.Sprintf("%.2f MB (%d bytes)", float64(n)/float64(1<<20), n)
	case n >= 1<<10:
		return fmt.Sprintf("%.2f KB (%d bytes)", float64(n)/float64(1<<10), n)
	default:
		return fmt.Sprintf("%d bytes", n)
	}
}
