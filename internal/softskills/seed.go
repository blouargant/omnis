package softskills

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/blouargant/omnis/internal/paths"
)

// seedMarkerName records, per top-level entry of the built-in soft-skill
// tree, the content hash of the copy last seeded into the user's softskills
// directory. It is what lets seeding tell three cases apart:
//   - never seeded            → copy it in;
//   - seeded, then deleted    → the user removed it on purpose, leave it gone;
//   - seeded, still untouched → safe to refresh when the shipped copy changes
//     (a user- or curator-edited copy is never overwritten).
const seedMarkerName = ".builtin_seeded.json"

// BuiltinDir returns where the packaged built-in soft-skills live: the
// `softskills/` directory of the system config layer (/etc/omnis/softskills
// by default, relocated by OMNIS_SYSTEM_CONFIG_DIR for the brew/MSI/pip
// wrappers).
func BuiltinDir() string { return filepath.Join(paths.SystemDir(), "softskills") }

// SeedBuiltins copies the built-in soft-skills shipped under src (one
// `<name>/SKILL.md` directory per soft-skill, plus loose files such as
// INDEX.md) into the per-user soft-skills directory dst, where
// `load_softskill` reads them. Existing user files are never overwritten and
// an entry the user deleted is not brought back; an untouched seeded copy is
// refreshed when the shipped version changes. A missing src is a no-op.
func SeedBuiltins(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("softskills seed: read %s: %w", src, err)
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return fmt.Errorf("softskills seed: %w", err)
	}

	markerPath := filepath.Join(dst, seedMarkerName)
	seeded := map[string]string{}
	if b, err := os.ReadFile(markerPath); err == nil {
		_ = json.Unmarshal(b, &seeded)
	}

	changed := false
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") {
			continue
		}
		from := filepath.Join(src, name)
		to := filepath.Join(dst, name)
		srcHash, err := treeHash(from)
		if err != nil {
			return fmt.Errorf("softskills seed: %w", err)
		}
		prev, wasSeeded := seeded[name]
		dstHash, err := treeHash(to)
		switch {
		case os.IsNotExist(err):
			if wasSeeded {
				continue // deleted by the user after seeding: respect it
			}
		case err != nil:
			return fmt.Errorf("softskills seed: %w", err)
		case !wasSeeded || dstHash != prev || srcHash == prev:
			// Pre-existing user copy, a modified seeded copy, or already
			// up to date: leave it alone.
			if !wasSeeded && dstHash == srcHash {
				seeded[name] = srcHash
				changed = true
			}
			continue
		default:
			// Untouched seeded copy and the shipped version moved on.
			if err := os.RemoveAll(to); err != nil {
				return fmt.Errorf("softskills seed: %w", err)
			}
		}
		if err := copyTree(from, to); err != nil {
			return fmt.Errorf("softskills seed: %w", err)
		}
		seeded[name] = srcHash
		changed = true
	}

	if !changed {
		return nil
	}
	b, err := json.MarshalIndent(seeded, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(markerPath, append(b, '\n'), 0o644)
}

// treeHash hashes a file, or every regular file under a directory (relative
// path + content, in sorted order). A missing path returns an os.IsNotExist
// error.
func treeHash(root string) (string, error) {
	info, err := os.Stat(root)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	if !info.IsDir() {
		if err := hashFile(h, root); err != nil {
			return "", err
		}
		return hex.EncodeToString(h.Sum(nil)), nil
	}
	var files []string
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type().IsRegular() {
			files = append(files, p)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(files)
	for _, f := range files {
		rel, _ := filepath.Rel(root, f)
		io.WriteString(h, filepath.ToSlash(rel)+"\x00")
		if err := hashFile(h, f); err != nil {
			return "", err
		}
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func hashFile(w io.Writer, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(w, f)
	return err
}

// copyTree copies a file or a directory tree (regular files and directories
// only) from src to dst.
func copyTree(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return copyFile(src, dst)
	}
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		switch {
		case d.IsDir():
			return os.MkdirAll(target, 0o755)
		case d.Type().IsRegular():
			return copyFile(p, target)
		}
		return nil
	})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
