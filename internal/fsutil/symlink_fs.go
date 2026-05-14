// Package fsutil provides small fs.FS helpers shared by the skills and
// softskills toolsets.
package fsutil

import (
	"io/fs"
	"os"
	"path/filepath"
)

// NewSymlinkDirFS returns an fs.FS rooted at root that follows symlinks to
// directories in ReadDir. Plain os.DirFS treats directory-symlinks as
// fs.ModeSymlink (IsDir==false), which causes the ADK FileSystemSource to
// silently skip them. This wrapper stats each symlink entry and promotes it
// to a directory entry when the target is a directory.
func NewSymlinkDirFS(root string) fs.FS {
	abs, err := filepath.Abs(root)
	if err != nil {
		abs = root
	}
	return &symlinkFS{root: abs, FS: os.DirFS(root)}
}

type symlinkFS struct {
	root string
	fs.FS
}

type symlinkDirEntry struct {
	fs.DirEntry
}

func (s *symlinkDirEntry) IsDir() bool       { return true }
func (s *symlinkDirEntry) Type() fs.FileMode { return fs.ModeDir }

// ReadDir overrides the embedded FS to follow directory symlinks.
func (s *symlinkFS) ReadDir(name string) ([]fs.DirEntry, error) {
	entries, err := fs.ReadDir(s.FS, name)
	if err != nil {
		return nil, err
	}
	out := make([]fs.DirEntry, 0, len(entries))
	for _, e := range entries {
		if e.Type()&fs.ModeSymlink != 0 {
			target := filepath.Join(s.root, filepath.FromSlash(name), e.Name())
			if fi, err := os.Stat(target); err == nil && fi.IsDir() {
				out = append(out, &symlinkDirEntry{DirEntry: e})
				continue
			}
		}
		out = append(out, e)
	}
	return out, nil
}
