package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/gabriel-vasile/mimetype"
)

type MimeIn struct {
	Path string `json:"file_path" jsonschema:"required,path to the file to inspect"`
}
type MimeOut struct {
	Card string `json:"card"`
}

// RunMime detects the MIME type of a file by inspecting its content (magic
// bytes), then compares the detected extension against the filename extension.
// Returns a formatted identity card string.
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

	card := fmt.Sprintf(
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
	return card, nil
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
