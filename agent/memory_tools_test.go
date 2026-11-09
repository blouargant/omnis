package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blouargant/omnis/internal/collmem"
	"github.com/blouargant/omnis/internal/paths"
)

func TestMemorySaveReadDeleteRoundTrip(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	SetCollectionResolver(nil)
	SetCollectionRootResolver(nil)
	var seen []string
	SetMemoryChangeHook(func(sid, col, name, op string) { seen = append(seen, sid+"|"+col+"|"+name+"|"+op) })
	t.Cleanup(func() { SetMemoryChangeHook(nil) })
	out := memorySave(context.Background(), "s1", memorySaveIn{Name: "No Force Push", Description: "Never force-push", Type: "feedback", Content: "Rebase."})
	if !out.OK || out.Name != "no-force-push" || out.Status != "created" {
		t.Fatalf("%+v", out)
	}
	out = memorySave(context.Background(), "s1", memorySaveIn{Name: "no-force-push", Description: "Never force-push", Type: "feedback", Content: "Rebase, then ask."})
	if out.Status != "updated" {
		t.Fatalf("%+v", out)
	}
	if r := memoryRead("s1", "no-force-push"); !r.Found || r.Content != "Rebase, then ask." {
		t.Fatalf("%+v", r)
	}
	if d := memoryDelete(context.Background(), "s1", "no-force-push"); !d.OK {
		t.Fatalf("%+v", d)
	}
	want := "s1||no-force-push|created,s1||no-force-push|updated,s1||no-force-push|deleted"
	if got := strings.Join(seen, ","); got != want {
		t.Fatalf("hook calls = %s", got)
	}
}

func TestMemorySaveErrorsAreMessagesNotPanics(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	for _, in := range []memorySaveIn{
		{Name: "../x", Description: "d", Type: "feedback", Content: "c"},
		{Name: "a", Description: "d", Type: "note", Content: "c"},
		{Name: "a", Description: "d", Type: "feedback", Content: ""},
	} {
		out := memorySave(context.Background(), "s", in)
		if out.OK || out.Error == "" {
			t.Errorf("%+v ⇒ %+v", in, out)
		}
	}
	if r := memoryRead("s", "missing"); r.Found || r.Error == "" {
		t.Errorf("%+v", r)
	}
}

func TestMemoryWritesRefusedInUnattendedRun(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	memorySave(context.Background(), "s", memorySaveIn{Name: "a", Description: "d", Type: "user", Content: "c"})
	ctx := WithMemoryReadOnly(context.Background())
	if out := memorySave(ctx, "s", memorySaveIn{Name: "b", Description: "d", Type: "user", Content: "c"}); out.OK || !strings.Contains(out.Error, "did not start") {
		t.Fatalf("%+v", out)
	}
	if out := memoryDelete(ctx, "s", "a"); out.OK {
		t.Fatalf("%+v", out)
	}
	if r := memoryRead("s", "a"); !r.Found {
		t.Fatal("reading must still work")
	}
}

func TestMemoryToolsUseTheSessionsCollection(t *testing.T) {
	t.Setenv("OMNIS_HOME", t.TempDir())
	SetCollectionResolver(func(sid string) string {
		if sid == "user-facing" {
			return "Proxy"
		}
		return ""
	})
	t.Cleanup(func() { SetCollectionResolver(nil) })
	memorySave(context.Background(), "user-facing", memorySaveIn{Name: "a", Description: "d", Type: "user", Content: "c"})
	if _, ok := collmem.In(paths.ConfigWriteDir()).Get("Proxy", "a"); !ok {
		t.Fatal("must land in the Proxy collection")
	}
}

// shippedConfigLayer builds a system config layer from the repo's shipped
// config/ files and registry/, so a test can build the real fleet.
func shippedConfigLayer(t *testing.T) string {
	t.Helper()
	layer := t.TempDir()
	entries, err := os.ReadDir(filepath.Join("..", "config"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join("..", "config", e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(layer, e.Name()), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	reg, err := filepath.Abs(filepath.Join("..", "registry"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(reg, filepath.Join(layer, "registry")); err != nil {
		t.Fatal(err)
	}
	return layer
}

func TestMemoryToolsMountedOnAnsweringRootsOnly(t *testing.T) {
	layer := shippedConfigLayer(t)
	t.Setenv("OMNIS_HOME", t.TempDir())
	t.Setenv("OMNIS_SYSTEM_CONFIG_DIR", layer)
	t.Setenv("OMNIS_CONFIG_DIRS", layer)
	t.Setenv("OMNIS_CONFIG_PATH", "")
	t.Setenv("OMNIS_AGENTSKILLS_DIR", "")

	ctx := context.Background()
	opts := Options{DeferModelErrors: true}
	infra, err := BuildInfrastructure(ctx, opts)
	if err != nil {
		t.Fatalf("build infra: %v", err)
	}
	defer infra.Close()
	inst, err := BuildInstance(ctx, infra, opts, 1)
	if err != nil {
		t.Fatalf("build instance: %v", err)
	}
	if inst.RouterName == "" {
		t.Fatal("shipped fleet has no router squad")
	}
	if len(inst.Squads) < 3 {
		t.Fatalf("shipped fleet built only %d squads", len(inst.Squads))
	}
	memNames := []string{"memory_save", "memory_read", "memory_delete"}
	for name, sq := range inst.Squads {
		root := agentToolNames(t, sq.Leader)
		for _, m := range memNames {
			if name == inst.RouterName {
				if has(root, m) {
					t.Errorf("router root %q mounts %s", name, m)
				}
			} else if !has(root, m) {
				t.Errorf("answering root of squad %q lacks %s: %v", name, m, root)
			}
		}
		for subName, sub := range sq.SubAgents {
			got := agentToolNames(t, sub)
			for _, m := range memNames {
				if has(got, m) {
					t.Errorf("sub-agent %q of squad %q mounts %s", subName, name, m)
				}
			}
		}
	}
}
