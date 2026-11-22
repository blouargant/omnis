package tools

import (
	"log"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/blouargant/omnis/core/adk"
	"github.com/blouargant/omnis/core/permissions"
)

// ShellWrite is one file a Bash call wrote, as checked on disk.
type ShellWrite struct {
	Path    string // absolute, as the parser returned it
	Created bool   // the file did not exist before the command
}

// Bounds of the shell-write store.
const (
	shellWriteMaxEntries = 1024
	shellWriteTTL        = 10 * time.Minute
)

var shellWriteTracking atomic.Bool

type shellWriteEntry struct {
	writes []ShellWrite
	at     time.Time
	seq    int // record order, oldest lowest
}

// shellWriteSweepEvery: expired entries are swept once every this many
// records (and whenever the store is full).
const shellWriteSweepEvery = 64

// shellWriteNow is the store's clock (tests replace it).
var shellWriteNow = time.Now

var shellWriteStore = struct {
	sync.Mutex
	m       map[string]shellWriteEntry
	records int
}{m: map[string]shellWriteEntry{}}

// EnableShellWriteTracking turns the Bash tool's write tracking on or off. It
// is off by default; the server turns it on. While off the Bash tool neither
// parses the command nor stats any file.
func EnableShellWriteTracking(on bool) { shellWriteTracking.Store(on) }

// RecordShellWrites stores the files a call wrote. A no-op when tracking is
// off, the call id is empty or nothing was written.
func RecordShellWrites(callID string, ws []ShellWrite) {
	if !shellWriteTracking.Load() || callID == "" || len(ws) == 0 {
		return
	}
	now := shellWriteNow()
	s := &shellWriteStore
	s.Lock()
	defer s.Unlock()
	s.records++
	s.m[callID] = shellWriteEntry{writes: append([]ShellWrite(nil), ws...), at: now, seq: s.records}
	full := len(s.m) > shellWriteMaxEntries
	if full || s.records%shellWriteSweepEvery == 0 {
		for id, e := range s.m {
			if now.Sub(e.at) >= shellWriteTTL {
				delete(s.m, id)
			}
		}
	}
	// Still over the cap: drop the oldest (one scan per record, at most
	// shellWriteMaxEntries entries).
	for len(s.m) > shellWriteMaxEntries {
		oldest, seq := "", 0
		for id, e := range s.m {
			if oldest == "" || e.seq < seq {
				oldest, seq = id, e.seq
			}
		}
		delete(s.m, oldest)
	}
}

// ShellWrites returns the files a call wrote and keeps the entry.
func ShellWrites(callID string) []ShellWrite {
	return readShellWrites(callID, false)
}

// TakeShellWrites returns the files a call wrote and removes the entry.
func TakeShellWrites(callID string) []ShellWrite {
	return readShellWrites(callID, true)
}

func readShellWrites(callID string, take bool) []ShellWrite {
	if callID == "" {
		return nil
	}
	s := &shellWriteStore
	s.Lock()
	defer s.Unlock()
	e, ok := s.m[callID]
	if !ok {
		return nil
	}
	if shellWriteNow().Sub(e.at) >= shellWriteTTL {
		delete(s.m, callID)
		return nil
	}
	if take {
		delete(s.m, callID)
	}
	return append([]ShellWrite(nil), e.writes...)
}

// shellWriteCount is the number of stored entries (tests).
func shellWriteCount() int {
	shellWriteStore.Lock()
	defer shellWriteStore.Unlock()
	return len(shellWriteStore.m)
}

type fileState struct {
	exists  bool
	regular bool
	mod     time.Time
	size    int64
}

func statFile(p string) fileState {
	fi, err := os.Stat(p) // follows a symlink
	if err != nil {
		return fileState{}
	}
	return fileState{exists: true, regular: fi.Mode().IsRegular(), mod: fi.ModTime(), size: fi.Size()}
}

// trackBashWrites is trackShellWrites for a Bash handler: it reads the call
// id from the tool context only when tracking is on, so a context that cannot
// give one (a test mock) is never asked while tracking is off.
func trackBashWrites(ctx adk.ToolContext, command, cwd string) (finish func()) {
	if !shellWriteTracking.Load() || ctx == nil {
		return func() {}
	}
	id := ""
	func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("tools: shell write tracking: no call id: %v", r)
			}
		}()
		id = ctx.FunctionCallID()
	}()
	return trackShellWrites(id, command, cwd)
}

// trackShellWrites stats the files command may write before it runs and
// returns a finish func that stats them again and records the ones written
// (now a regular file that did not exist before, or whose mtime or size
// changed). A no-op when tracking is off or the call id is empty. A panic in
// either half is recovered and logged; the command runs normally.
func trackShellWrites(callID, command, cwd string) (finish func()) {
	noop := func() {}
	if !shellWriteTracking.Load() || callID == "" {
		return noop
	}
	defer func() {
		if r := recover(); r != nil {
			log.Printf("tools: shell write tracking: %v", r)
			finish = noop
		}
	}()
	dir := cwd
	if dir == "" {
		if wd, err := os.Getwd(); err == nil {
			dir = wd
		}
	}
	cands := permissions.ShellWriteTargets(command, dir)
	if len(cands) == 0 {
		return noop
	}
	before := make([]fileState, len(cands))
	for i, p := range cands {
		before[i] = statFile(p)
	}
	return func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("tools: shell write tracking: %v", r)
			}
		}()
		var ws []ShellWrite
		for i, p := range cands {
			after := statFile(p)
			if !after.exists || !after.regular {
				continue
			}
			b := before[i]
			switch {
			case !b.exists:
				ws = append(ws, ShellWrite{Path: p, Created: true})
			case !b.mod.Equal(after.mod) || b.size != after.size:
				ws = append(ws, ShellWrite{Path: p})
			}
		}
		RecordShellWrites(callID, ws)
	}
}
