package tools

import (
	"context"
	"strings"
	"testing"
)

func TestShellEnvContext(t *testing.T) {
	if ShellEnvFrom(context.Background()) != nil {
		t.Fatal("empty ctx must carry no env")
	}
	ctx := WithShellEnv(context.Background(), []string{"OMNIS_T=abc"})
	if got := ShellEnvFrom(ctx); len(got) != 1 || got[0] != "OMNIS_T=abc" {
		t.Fatalf("got %q", got)
	}
	if WithShellEnv(ctx, nil) != ctx {
		t.Fatal("nil env must be a no-op")
	}
}

func TestRunBashAppliesEnv(t *testing.T) {
	out, _ := RunBash(context.Background(), BashIn{Command: `echo "v=$OMNIS_T"`, Env: []string{"OMNIS_T=abc"}})
	if !strings.Contains(out, "v=abc") {
		t.Fatalf("got %q", out)
	}
	out, _ = RunBash(context.Background(), BashIn{Command: `echo "v=$OMNIS_T"`})
	if !strings.Contains(out, "v=") || strings.Contains(out, "abc") {
		t.Fatalf("no env must leave the var unset: %q", out)
	}
}

func TestRunBashInteractiveReadsEnvFromContext(t *testing.T) {
	ctx := WithShellEnv(context.Background(), []string{"OMNIS_T=xyz"})
	out, _, _ := RunBashInteractive(ctx, `echo "v=$OMNIS_T"`, "", 0)
	if !strings.Contains(out, "v=xyz") {
		t.Fatalf("got %q", out)
	}
}

func TestRunShellCapturedNeverReadsContextEnv(t *testing.T) {
	ctx := WithShellEnv(context.Background(), []string{"OMNIS_T=leak"})
	res := RunShellCaptured(ctx, `echo "v=$OMNIS_T"`, "", nil, 0)
	if strings.Contains(res.Stdout, "leak") {
		t.Fatalf("hooks/run_tests path must not receive the planted env: %q", res.Stdout)
	}
}
