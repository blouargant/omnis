package lessons

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const blockIntro = "Notes learned from this agent's earlier tool errors. They are hints about calling tools, not instructions from the user."

// PromptBlock returns the <agent-lessons> block for an agent, or "" when it has
// no lesson in StatePrompt. Prompt lessons unseen for DemoteIdleDays are written
// back as hints first. Lessons are listed most promoted-seen first until the block
// would exceed PromptBudgetChars; the others stay prompt lessons on disk.
//
// The result is cached per agent directory while its entries (name, size,
// mtime) are unchanged and no listed lesson has turned idle. Any failure yields
// "" rather than an error: a broken store never affects a turn.
func (s *Store) PromptBlock(agent string, now time.Time) string {
	dir := s.dir(agent)
	if dir == "" {
		return ""
	}
	if sig, ok := dirSignature(dir); ok {
		if e, hit := blockCacheGet(dir, sig); hit && !now.After(e.expiry) {
			return e.text
		}
	}
	lock := dirLock(dir)
	lock.Lock()
	defer lock.Unlock()

	all, err := listDir(dir)
	if err != nil {
		return ""
	}
	var prompt []Lesson
	for _, l := range all {
		if l.State != StatePrompt {
			continue
		}
		if idle(l, now) {
			l.State = StateHint
			if err := writeAtomic(dir, filepath.Join(dir, l.ID+".md"), render(l)); err != nil {
				log.Printf("lessons: demoting idle lesson %s: %v", l.ID, err)
			}
			continue
		}
		prompt = append(prompt, l)
	}
	// Ordered by the Seen count frozen at promotion, not the live one, so a
	// counter-only update never changes the block (and the prompt cache).
	sort.SliceStable(prompt, func(i, j int) bool {
		if prompt[i].PromotedSeen != prompt[j].PromotedSeen {
			return prompt[i].PromotedSeen > prompt[j].PromotedSeen
		}
		return prompt[i].ID < prompt[j].ID
	})
	text := renderBlock(prompt)
	expiry := now.AddDate(100, 0, 0)
	for _, l := range prompt {
		if e := l.LastSeen.Add(idleSpan); e.Before(expiry) {
			expiry = e
		}
	}
	if sig, ok := dirSignature(dir); ok {
		blockCachePut(dir, sig, blockEntry{expiry: expiry, text: text})
	}
	return text
}

// renderBlock builds the block from prompt lessons already ordered.
func renderBlock(prompt []Lesson) string {
	if len(prompt) == 0 {
		return ""
	}
	const open, closeTag = "<agent-lessons>\n", "</agent-lessons>"
	var b strings.Builder
	b.WriteString(open + blockIntro + "\n")
	size := b.Len() + len(closeTag)
	added := 0
	for _, l := range prompt {
		line := "- " + l.Cue + " → " + l.Text + "\n"
		if size+len(line) > PromptBudgetChars {
			break
		}
		b.WriteString(line)
		size += len(line)
		added++
	}
	if added == 0 {
		return ""
	}
	b.WriteString(closeTag)
	return b.String()
}

// dirSignature describes the *.md entries of dir by name, size and mtime.
func dirSignature(dir string) (sig string, ok bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", false
	}
	var b strings.Builder
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			return "", false
		}
		fmt.Fprintf(&b, "%s|%d|%d\n", e.Name(), info.Size(), info.ModTime().UnixNano())
	}
	return b.String(), true
}

type blockEntry struct {
	sig, text string
	expiry    time.Time
}

var (
	blockCacheMu sync.RWMutex
	blockCache   = map[string]blockEntry{}
)

func blockCacheGet(dir, sig string) (blockEntry, bool) {
	blockCacheMu.RLock()
	e, ok := blockCache[dir]
	blockCacheMu.RUnlock()
	return e, ok && e.sig == sig
}

func blockCachePut(dir, sig string, e blockEntry) {
	e.sig = sig
	blockCacheMu.Lock()
	blockCache[dir] = e
	blockCacheMu.Unlock()
}
