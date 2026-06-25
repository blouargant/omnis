// reindex_precedents.go — `omnis reindex-precedents` subcommand. Walks every
// $OMNIS_HOME/logs/agent_statelog_*.json and (re)builds the cross-session
// precedent index in one pass. Idempotent: ids are derived from
// (session_key, kind, index), so re-running does not duplicate entries.
//
// Requires an embedder (models.json embed_model_ref or OMNIS_EMBED_*); without
// one there is nothing to embed and the command errors out.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/blouargant/omnis/agent"
	"github.com/blouargant/omnis/internal/compress"
	"github.com/blouargant/omnis/internal/paths"
	"github.com/blouargant/omnis/internal/precedents"
)

func runReindexPrecedents(ctx context.Context, opts options, _ []string) error {
	runtime, err := agent.ResolveRuntimeSettings(agent.Options{
		SoftSkillsDir:    opts.softSkillsDir,
		AppName:          opts.appName,
		ConfigPath:       opts.configPath,
		ConfigPathStrict: opts.configPath != "",
	})
	if err != nil {
		return err
	}

	emb, err := agent.ResolveEmbedder(ctx, runtime)
	if err != nil {
		return fmt.Errorf("embedder: %w", err)
	}
	if emb == nil {
		return fmt.Errorf("no embedder configured: set an embedding model_ref in models.json (embed_model_ref) or the OMNIS_EMBED_* environment")
	}

	store, err := precedents.Open(emb)
	if err != nil {
		return fmt.Errorf("open precedents index: %w", err)
	}

	logsDir := paths.LogsDir()
	matches, err := filepath.Glob(filepath.Join(logsDir, "agent_statelog_*.json"))
	if err != nil {
		return fmt.Errorf("glob statelogs: %w", err)
	}
	sort.Strings(matches)
	if len(matches) == 0 {
		fmt.Printf("no statelog files found under %s\n", logsDir)
		return nil
	}

	indexed := 0
	for _, path := range matches {
		base := filepath.Base(path)
		key := strings.TrimSuffix(strings.TrimPrefix(base, "agent_statelog_"), ".json")
		sl := readStateLogFile(path)
		if sl == nil {
			continue
		}
		ts := time.Now()
		if st, serr := os.Stat(path); serr == nil {
			ts = st.ModTime()
		}
		if err := store.Add(ctx, key, sl, ts); err != nil {
			fmt.Fprintf(os.Stderr, "warn: index %s: %v\n", base, err)
			continue
		}
		indexed++
	}
	if err := store.Save(); err != nil {
		return fmt.Errorf("save index: %w", err)
	}
	fmt.Printf("indexed %d session(s) into the precedent index (%d total items)\n", indexed, store.Len())
	return nil
}

// readStateLogFile parses a statelog JSON file, returning nil on any error.
func readStateLogFile(path string) *compress.StateLog {
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return nil
	}
	var sl compress.StateLog
	if err := json.Unmarshal(data, &sl); err != nil {
		return nil
	}
	return &sl
}
