// Package lessons stores the one-line lessons an agent learns from its tool
// errors: <root>/lessons/<agent>/<id>.md, one Markdown file per lesson, a small
// front matter followed by the cue, the text and two fenced excerpts (what
// failed, what worked).
//
// It imports only the standard library (and internal/paths when a caller needs
// the root), so the agent package can use it without an import cycle.
package lessons

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	// MaxPerAgent caps the lessons stored for one agent.
	MaxPerAgent = 200
	// PromptBudgetChars is the size the lessons injected in a prompt may take.
	PromptBudgetChars = 1500
	// PromoteSeen and PromoteSessions: a hint becomes a prompt lesson once seen
	// this many times in at least this many distinct sessions.
	PromoteSeen     = 3
	PromoteSessions = 2
	// RetireRepeated failures within the last RetireWindow hint outcomes retire
	// a lesson.
	RetireRepeated = 3
	RetireWindow   = 5
	// A prompt lesson is demoted after DemoteSeenInPrompt appearances in a prompt
	// without the failure recurring, or DemoteIdleDays days unseen.
	DemoteSeenInPrompt = 3
	DemoteIdleDays     = 60
	// MaxLineRunes caps Cue and Text; MaxWorkedChars caps the Worked (and
	// Failed) excerpt.
	MaxLineRunes   = 300
	MaxWorkedChars = 500
	// SignatureTextChars is how much of the normalised error text is hashed.
	SignatureTextChars = 200

	maxSessionIDs = 8
)

var (
	ErrInvalid = errors.New("lessons: invalid lesson")
	ErrFull    = errors.New("lessons: the agent's lessons are all in use")
)

// State is where a lesson stands: shown as a hint after the failing call, in
// the agent's system prompt, or retired.
type State string

const (
	StateHint    State = "hint"
	StatePrompt  State = "prompt"
	StateRetired State = "retired"
)

func (s State) valid() bool { return s == StateHint || s == StatePrompt || s == StateRetired }

// Lesson is one stored lesson.
type Lesson struct {
	ID, Agent, Tool, Signature                      string // ID = first 12 hex chars of Signature
	Cue, Text                                       string // one line each, <= MaxLineRunes
	Failed, Worked                                  string // excerpts, <= MaxWorkedChars
	State                                           State
	Created, LastSeen                               time.Time
	Seen, Hinted, Recovered, Repeated, SeenInPrompt int
	SessionIDs                                      []string // distinct, at most 8 kept
	Outcomes                                        []bool   // last RetireWindow hint outcomes, true = recovered
	PromptFailed                                    bool     // demoted because the prompt did not prevent the failure; never promoted again
}

// Store reads and writes lesson files under one root directory.
type Store struct{ root string }

// In returns a store rooted at root (files live in <root>/lessons/<agent>/).
func In(root string) *Store { return &Store{root: root} }

var (
	agentRE = regexp.MustCompile(`^[a-z0-9][a-z0-9_]{0,63}$`)
	idRE    = regexp.MustCompile(`^[0-9a-f]{12}$`)
	sigRE   = regexp.MustCompile(`^[0-9a-f]{12,64}$`)
)

func (s *Store) dir(agent string) string {
	if !agentRE.MatchString(agent) {
		return ""
	}
	return filepath.Join(s.root, "lessons", agent)
}

var (
	locksMu sync.Mutex
	locks   = map[string]*sync.Mutex{}
)

func dirLock(dir string) *sync.Mutex {
	locksMu.Lock()
	defer locksMu.Unlock()
	l, ok := locks[dir]
	if !ok {
		l = &sync.Mutex{}
		locks[dir] = l
	}
	return l
}

// prepare validates l and returns the lesson to write.
func prepare(l Lesson) (Lesson, error) {
	if !agentRE.MatchString(l.Agent) {
		return Lesson{}, fmt.Errorf("%w: agent name", ErrInvalid)
	}
	if !sigRE.MatchString(l.Signature) {
		return Lesson{}, fmt.Errorf("%w: signature", ErrInvalid)
	}
	id := l.Signature[:12]
	if l.ID != "" && l.ID != id {
		return Lesson{}, fmt.Errorf("%w: id does not match signature", ErrInvalid)
	}
	l.ID = id
	if !l.State.valid() {
		return Lesson{}, fmt.Errorf("%w: state %q", ErrInvalid, l.State)
	}
	for name, v := range map[string]string{"cue": l.Cue, "text": l.Text} {
		v = strings.TrimSpace(v)
		if v == "" || strings.ContainsAny(v, "\r\n") || utf8.RuneCountInString(v) > MaxLineRunes {
			return Lesson{}, fmt.Errorf("%w: %s must be one non-empty line of at most %d characters", ErrInvalid, name, MaxLineRunes)
		}
	}
	l.Cue, l.Text = strings.TrimSpace(l.Cue), strings.TrimSpace(l.Text)
	if strings.ContainsAny(l.Tool, "\r\n") {
		return Lesson{}, fmt.Errorf("%w: tool", ErrInvalid)
	}
	l.Tool = strings.TrimSpace(l.Tool)
	l.Failed = truncateRunes(strings.TrimSpace(l.Failed), MaxWorkedChars)
	l.Worked = truncateRunes(strings.TrimSpace(l.Worked), MaxWorkedChars)
	l.Seen, l.Hinted, l.Recovered = max(l.Seen, 0), max(l.Hinted, 0), max(l.Recovered, 0)
	l.Repeated, l.SeenInPrompt = max(l.Repeated, 0), max(l.SeenInPrompt, 0)
	seen := map[string]bool{}
	var ids []string
	for _, sid := range l.SessionIDs {
		if sid == "" || strings.ContainsAny(sid, ", \r\n\t") || seen[sid] || len(ids) >= maxSessionIDs {
			continue
		}
		seen[sid] = true
		ids = append(ids, sid)
	}
	l.SessionIDs = ids
	if len(l.Outcomes) > RetireWindow {
		l.Outcomes = l.Outcomes[len(l.Outcomes)-RetireWindow:]
	}
	if l.Created.IsZero() {
		l.Created = time.Now()
	}
	if l.LastSeen.IsZero() {
		l.LastSeen = l.Created
	}
	return l, nil
}

// Save validates and writes a lesson atomically. A new lesson of an agent that
// holds MaxPerAgent lessons replaces the retired one with the lowest Seen, else
// the hint with the lowest Seen; prompt lessons are never removed (ErrFull when
// nothing can go).
func (s *Store) Save(l Lesson) error {
	l, err := prepare(l)
	if err != nil {
		return err
	}
	dir := s.dir(l.Agent)
	lock := dirLock(dir)
	lock.Lock()
	defer lock.Unlock()

	path := filepath.Join(dir, l.ID+".md")
	if _, statErr := os.Stat(path); os.IsNotExist(statErr) {
		all, _ := listDir(dir)
		if len(all) >= MaxPerAgent {
			victim, ok := evictable(all)
			if !ok {
				return ErrFull
			}
			if err := os.Remove(filepath.Join(dir, victim.ID+".md")); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	} else if statErr != nil {
		return statErr
	}
	return writeAtomic(dir, path, render(l))
}

// evictable picks the lesson to drop: lowest-Seen retired, else lowest-Seen hint.
func evictable(all []Lesson) (Lesson, bool) {
	var best Lesson
	rank := 3
	for _, l := range all {
		r := 3
		switch l.State {
		case StateRetired:
			r = 0
		case StateHint:
			r = 1
		}
		if r == 3 {
			continue
		}
		if r < rank || (r == rank && (l.Seen < best.Seen || (l.Seen == best.Seen && l.ID < best.ID))) {
			best, rank = l, r
		}
	}
	return best, rank != 3
}

// Find returns the lesson stored for a signature, or false.
func (s *Store) Find(agent, signature string) (Lesson, bool) {
	dir := s.dir(agent)
	if dir == "" || !sigRE.MatchString(signature) {
		return Lesson{}, false
	}
	id := signature[:12]
	l, ok := readLesson(filepath.Join(dir, id+".md"), id)
	if !ok || l.Agent != agent || l.Signature != signature {
		return Lesson{}, false
	}
	return l, true
}

// List returns every valid lesson of an agent, most seen first, then by ID.
// Files that do not parse, whose id differs from their file stem or whose state
// is unknown are skipped.
func (s *Store) List(agent string) ([]Lesson, error) {
	dir := s.dir(agent)
	if dir == "" {
		return nil, fmt.Errorf("%w: agent name", ErrInvalid)
	}
	out, err := listDir(dir)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	return out, nil
}

func listDir(dir string) ([]Lesson, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []Lesson
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		stem := strings.TrimSuffix(e.Name(), ".md")
		if l, ok := readLesson(filepath.Join(dir, e.Name()), stem); ok {
			out = append(out, l)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Seen != out[j].Seen {
			return out[i].Seen > out[j].Seen
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// Delete removes a lesson; deleting an absent one is not an error.
func (s *Store) Delete(agent, id string) error {
	dir := s.dir(agent)
	if dir == "" || !idRE.MatchString(id) {
		return fmt.Errorf("%w: agent or id", ErrInvalid)
	}
	lock := dirLock(dir)
	lock.Lock()
	defer lock.Unlock()
	if err := os.Remove(filepath.Join(dir, id+".md")); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func readLesson(path, stem string) (Lesson, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Lesson{}, false
	}
	l, ok := parse(string(data))
	if !ok || l.ID != stem || !idRE.MatchString(l.ID) || !agentRE.MatchString(l.Agent) ||
		!sigRE.MatchString(l.Signature) || l.Signature[:12] != l.ID || !l.State.valid() ||
		l.Cue == "" || l.Text == "" {
		return Lesson{}, false
	}
	return l, true
}

const timeLayout = time.RFC3339Nano

func render(l Lesson) string {
	var b strings.Builder
	fmt.Fprintf(&b, "---\nid: %s\nagent: %s\ntool: %s\nsignature: %s\nstate: %s\ncreated: %s\nlast_seen: %s\n",
		l.ID, l.Agent, l.Tool, l.Signature, l.State, l.Created.UTC().Format(timeLayout), l.LastSeen.UTC().Format(timeLayout))
	fmt.Fprintf(&b, "seen: %d\nhinted: %d\nrecovered: %d\nrepeated: %d\nseen_in_prompt: %d\n",
		l.Seen, l.Hinted, l.Recovered, l.Repeated, l.SeenInPrompt)
	if len(l.SessionIDs) > 0 {
		fmt.Fprintf(&b, "sessions: %s\n", strings.Join(l.SessionIDs, ","))
	}
	if len(l.Outcomes) > 0 {
		var o strings.Builder
		for _, ok := range l.Outcomes {
			if ok {
				o.WriteByte('1')
			} else {
				o.WriteByte('0')
			}
		}
		fmt.Fprintf(&b, "outcomes: %s\n", o.String())
	}
	if l.PromptFailed {
		b.WriteString("prompt_failed: true\n")
	}
	fmt.Fprintf(&b, "---\n\nCue: %s\nText: %s\n", l.Cue, l.Text)
	for _, blk := range []struct{ name, body string }{{"Failed", l.Failed}, {"Worked", l.Worked}} {
		if blk.body == "" {
			continue
		}
		fence := strings.Repeat("`", max(3, longestBacktickRun(blk.body)+1))
		fmt.Fprintf(&b, "\n%s:\n%s\n%s\n%s\n", blk.name, fence, blk.body, fence)
	}
	return b.String()
}

func longestBacktickRun(s string) int {
	best, cur := 0, 0
	for _, r := range s {
		if r == '`' {
			cur++
			best = max(best, cur)
		} else {
			cur = 0
		}
	}
	return best
}

// parse reads the front matter by hand (like collmem), then the body.
func parse(text string) (Lesson, bool) {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	if len(lines) == 0 || lines[0] != "---" {
		return Lesson{}, false
	}
	end := -1
	for i := 1; i < len(lines); i++ {
		if lines[i] == "---" {
			end = i
			break
		}
	}
	if end < 0 {
		return Lesson{}, false
	}
	var l Lesson
	atoi := func(v string) int {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return 0
		}
		return n
	}
	for _, line := range lines[1:end] {
		k, v, ok := strings.Cut(line, ": ")
		if !ok {
			k, v, ok = strings.Cut(line, ":") // an empty value ("tool:")
			if !ok {
				continue
			}
		}
		v = strings.TrimSpace(v)
		switch strings.TrimSpace(k) {
		case "id":
			l.ID = v
		case "agent":
			l.Agent = v
		case "tool":
			l.Tool = v
		case "signature":
			l.Signature = v
		case "state":
			l.State = State(v)
		case "created":
			l.Created, _ = time.Parse(timeLayout, v)
		case "last_seen":
			l.LastSeen, _ = time.Parse(timeLayout, v)
		case "seen":
			l.Seen = atoi(v)
		case "hinted":
			l.Hinted = atoi(v)
		case "recovered":
			l.Recovered = atoi(v)
		case "repeated":
			l.Repeated = atoi(v)
		case "seen_in_prompt":
			l.SeenInPrompt = atoi(v)
		case "sessions":
			for _, sid := range strings.Split(v, ",") {
				if sid = strings.TrimSpace(sid); sid != "" {
					l.SessionIDs = append(l.SessionIDs, sid)
				}
			}
		case "prompt_failed":
			l.PromptFailed = v == "true"
		case "outcomes":
			for _, r := range v {
				l.Outcomes = append(l.Outcomes, r == '1')
			}
		}
	}
	body := lines[end+1:]
	for i := 0; i < len(body); i++ {
		line := body[i]
		switch {
		case strings.HasPrefix(line, "Cue: "):
			l.Cue = strings.TrimSpace(line[len("Cue: "):])
		case strings.HasPrefix(line, "Text: "):
			l.Text = strings.TrimSpace(line[len("Text: "):])
		case line == "Failed:" || line == "Worked:":
			if i+1 >= len(body) || !strings.HasPrefix(body[i+1], "```") {
				return Lesson{}, false
			}
			fence := body[i+1]
			j := i + 2
			for j < len(body) && body[j] != fence {
				j++
			}
			if j >= len(body) {
				return Lesson{}, false
			}
			content := strings.Join(body[i+2:j], "\n")
			if line == "Failed:" {
				l.Failed = content
			} else {
				l.Worked = content
			}
			i = j
		}
	}
	return l, true
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

func writeAtomic(dir, path, text string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".lswrite_*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.WriteString(text); err != nil {
		_ = tmp.Close()
		_ = os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		_ = os.Remove(name)
		return err
	}
	return nil
}
