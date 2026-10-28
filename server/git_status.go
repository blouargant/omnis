package main

import (
	"bufio"
	"bytes"
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// gitStatusTimeout bounds every git command run for the environment panel so
// a huge repository (or a hung filesystem) can never stall the request.
const gitStatusTimeout = 5 * time.Second

// maxUntrackedLines caps the line count of an untracked file (its "+N").
const maxUntrackedLines = 10000

// GitStatus is the read-only git view of a session's working folder shown in
// the environment panel (branch + Modifications card).
type GitStatus struct {
	Repo    bool      `json:"repo"`
	Root    string    `json:"root,omitempty"`
	Branch  string    `json:"branch,omitempty"`
	Files   []GitFile `json:"files"`
	Added   int       `json:"added"`
	Removed int       `json:"removed"`
}

// GitFile is one changed file versus HEAD (working tree + index). Status is
// the porcelain letter: M modified, A added, D deleted, R renamed, ? untracked.
type GitFile struct {
	Path    string `json:"path"`
	Abs     string `json:"abs"`
	Status  string `json:"status"`
	Added   int    `json:"added"`
	Removed int    `json:"removed"`
}

func gitOut(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "LC_ALL=C")
	return cmd.Output()
}

// gitStatus reports the git state of dir. A folder outside a repository, or a
// host without git, is not an error: it returns Repo=false.
func gitStatus(ctx context.Context, dir string) (GitStatus, error) {
	st := GitStatus{Files: []GitFile{}}
	if dir == "" {
		return st, nil
	}
	ctx, cancel := context.WithTimeout(ctx, gitStatusTimeout)
	defer cancel()

	top, err := gitOut(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return st, nil // not a repository (or git missing)
	}
	st.Repo = true
	st.Root = strings.TrimSpace(string(top))

	if b, err := gitOut(ctx, dir, "branch", "--show-current"); err == nil {
		st.Branch = strings.TrimSpace(string(b))
	}
	if st.Branch == "" {
		if b, err := gitOut(ctx, dir, "rev-parse", "--short", "HEAD"); err == nil {
			st.Branch = strings.TrimSpace(string(b)) // detached HEAD
		}
	}

	// Status letters (porcelain v1, -z: "XY path\0", renames add "\0orig").
	status := map[string]string{}
	var order []string
	if out, err := gitOut(ctx, st.Root, "status", "--porcelain", "-z", "--untracked-files=all"); err == nil {
		parts := bytes.Split(out, []byte{0})
		for i := 0; i < len(parts); i++ {
			p := parts[i]
			if len(p) < 4 {
				continue
			}
			x, y, path := p[0], p[1], string(p[3:])
			letter := string(y)
			if y == ' ' {
				letter = string(x)
			}
			if x == '?' {
				letter = "?"
			}
			if x == 'R' || y == 'R' {
				letter = "R"
				i++ // skip the original path
			}
			if _, seen := status[path]; !seen {
				order = append(order, path)
			}
			status[path] = letter
		}
	}

	// Line counts for tracked changes versus HEAD (the empty tree before the
	// first commit).
	counts := map[string][2]int{}
	base := "HEAD"
	if _, err := gitOut(ctx, st.Root, "rev-parse", "--verify", "-q", "HEAD"); err != nil {
		base = "4b825dc642cb6eb9a060e54bf8d69288fbee4904" // git's empty tree
	}
	if out, err := gitOut(ctx, st.Root, "diff", "--numstat", "-z", base); err == nil {
		for _, rec := range bytes.Split(out, []byte{0}) {
			f := strings.SplitN(string(rec), "\t", 3)
			if len(f) != 3 || f[2] == "" {
				continue
			}
			a, _ := strconv.Atoi(f[0]) // "-" for binary ⇒ 0
			r, _ := strconv.Atoi(f[1])
			counts[f[2]] = [2]int{a, r}
		}
	}

	for _, path := range order {
		f := GitFile{Path: path, Abs: filepath.Join(st.Root, path), Status: status[path]}
		if f.Status == "?" {
			f.Added = countLines(f.Abs)
		} else if c, ok := counts[path]; ok {
			f.Added, f.Removed = c[0], c[1]
		}
		st.Added += f.Added
		st.Removed += f.Removed
		st.Files = append(st.Files, f)
	}
	return st, nil
}

// countLines counts the lines of a text file, capped; a binary file counts 0.
func countLines(path string) int {
	fh, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer fh.Close()
	head := make([]byte, 8000)
	n, _ := fh.Read(head)
	if bytes.IndexByte(head[:n], 0) >= 0 {
		return 0
	}
	if _, err := fh.Seek(0, 0); err != nil {
		return 0
	}
	sc := bufio.NewScanner(fh)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	lines := 0
	for sc.Scan() {
		lines++
		if lines >= maxUntrackedLines {
			break
		}
	}
	return lines
}

// handleSessionGit serves GET /api/sessions/:id/git — the git state of the
// session's working folder (branch + changes versus HEAD).
func handleSessionGit(d serverDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		if _, ok := d.Registry.Get(id); !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "session not found"})
			return
		}
		st, _ := gitStatus(c.Request.Context(), bashCwd.get(id))
		c.JSON(http.StatusOK, st)
	}
}
