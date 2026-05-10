package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pngBytes is the minimal valid PNG magic header (8 bytes signature + IHDR).
var pngMagic = []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}

// pdfMagic is the PDF file signature.
var pdfMagic = []byte{'%', 'P', 'D', 'F', '-', '1', '.', '4'}

func TestRunMimeMatchingExtension(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "image.png")
	if err := os.WriteFile(path, pngMagic, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	out, err := RunMime(context.Background(), MimeIn{Path: path})
	if err != nil {
		t.Fatalf("RunMime() error = %v", err)
	}
	if !strings.Contains(out, "image/png") {
		t.Errorf("RunMime() missing MIME type: %q", out)
	}
	if !strings.Contains(out, "Match     : YES") {
		t.Errorf("RunMime() expected match=YES: %q", out)
	}
}

func TestRunMimeMismatchedExtension(t *testing.T) {
	t.Parallel()

	// PDF content saved with a .jpg extension.
	path := filepath.Join(t.TempDir(), "disguised.jpg")
	if err := os.WriteFile(path, pdfMagic, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	out, err := RunMime(context.Background(), MimeIn{Path: path})
	if err != nil {
		t.Fatalf("RunMime() error = %v", err)
	}
	if !strings.Contains(out, "NO") {
		t.Errorf("RunMime() expected mismatch: %q", out)
	}
	if !strings.Contains(out, "application/pdf") {
		t.Errorf("RunMime() missing detected MIME: %q", out)
	}
	if !strings.Contains(out, ".jpg") {
		t.Errorf("RunMime() missing claimed extension: %q", out)
	}
}

func TestRunMimePlainText(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(path, []byte("hello world\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	out, err := RunMime(context.Background(), MimeIn{Path: path})
	if err != nil {
		t.Fatalf("RunMime() error = %v", err)
	}
	if !strings.Contains(out, "text/plain") {
		t.Errorf("RunMime() expected text/plain: %q", out)
	}
	if !strings.Contains(out, "Match     : YES") {
		t.Errorf("RunMime() expected match=YES: %q", out)
	}
}

func TestRunMimeNoExtension(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "Makefile")
	if err := os.WriteFile(path, []byte("all:\n\techo hi\n"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	out, err := RunMime(context.Background(), MimeIn{Path: path})
	if err != nil {
		t.Fatalf("RunMime() error = %v", err)
	}
	if !strings.Contains(out, "UNKNOWN") {
		t.Errorf("RunMime() expected UNKNOWN for no extension: %q", out)
	}
}

func TestRunMimeNonexistent(t *testing.T) {
	t.Parallel()

	out, err := RunMime(context.Background(), MimeIn{Path: "/nonexistent/path/file.png"})
	if err != nil {
		t.Fatalf("RunMime() unexpected error = %v", err)
	}
	if !strings.Contains(out, "Error") {
		t.Errorf("RunMime(nonexistent) = %q, want Error message", out)
	}
}

func TestRunMimeDirectory(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	out, err := RunMime(context.Background(), MimeIn{Path: dir})
	if err != nil {
		t.Fatalf("RunMime() unexpected error = %v", err)
	}
	if !strings.Contains(out, "Error") {
		t.Errorf("RunMime(dir) = %q, want Error message", out)
	}
}

func TestRunMimeCardFields(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "sample.png")
	if err := os.WriteFile(path, pngMagic, 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	out, err := RunMime(context.Background(), MimeIn{Path: path})
	if err != nil {
		t.Fatalf("RunMime() error = %v", err)
	}

	for _, field := range []string{"File", "Path", "Size", "MIME type", "MIME ext", "File ext", "Match"} {
		if !strings.Contains(out, field) {
			t.Errorf("RunMime() card missing field %q:\n%s", field, out)
		}
	}
}

func TestNewIncludesMimeTool(t *testing.T) {
	t.Parallel()

	tools := New()
	for _, tool := range tools {
		if tool.Name() == "mime" {
			return
		}
	}
	t.Fatal("New() does not include the mime tool")
}
