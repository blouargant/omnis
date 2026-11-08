package permissions

import "testing"

// TestReadOnlyCommandRejectsWriteRedirects: the read-only allowlist judges a
// command by its name, so an output redirection must disqualify it — `echo x >
// ~/.bashrc` used to be auto-allowed as a "read-only command". Harmless targets
// (/dev/null, /dev/stdout, /dev/stderr, fd duplication, /tmp/) keep it
// read-only, and a `>` inside quotes is not a redirect.
func TestReadOnlyCommandRejectsWriteRedirects(t *testing.T) {
	cases := []struct {
		cmd  string
		want bool
	}{
		// writes
		{`echo x > ~/.bashrc`, false},
		{`echo x >~/.bashrc`, false},
		{`cat a > /etc/x`, false},
		{`cat a >> notes.txt`, false},
		{`echo x >| f`, false},
		{`ls &> out.log`, false},
		{`ls &>> out.log`, false},
		{`ls 2> err.log`, false},
		{`grep a f 1>out`, false},
		{`echo x >&out.txt`, false},
		{`cat a <> f`, false},
		{`echo x > "$HOME/.profile"`, false},
		{`echo x > $(mktemp -p ~)`, false},
		{"echo x > `pwd`/f", false},
		{`echo x > "/tmp/../etc/passwd"`, false},
		{`echo x > /tmp`, false},
		{`echo x >`, false},
		{`cat a > >(cat)`, false},
		{`awk '{print $1}' f > out.txt`, false},
		{`find . -name x > list.txt`, false},
		{`git log > log.txt`, false},
		{`echo a > /dev/null > f`, false},
		// harmless redirects
		{`ls 2>/dev/null`, true},
		{`ls > /dev/null 2>&1`, true},
		{`ls &>/dev/null`, true},
		{`echo err >&2`, true},
		{`echo err 1>&2`, true},
		{`ls 2>&-`, true},
		{`echo x > /dev/stdout`, true},
		{`echo x 2> /dev/stderr`, true},
		{`echo x > /tmp/out.txt`, true},
		{`cat a >> "/tmp/review-base/x"`, true},
		{`cat a > '/tmp/x y'`, true},
		// not redirects at all
		{`echo "a > b"`, true},
		{`echo 'x >> ~/.bashrc'`, true},
		{`echo a \> b`, true},
		{`grep -E "->|=>" f`, true},
		{`awk 'NR>10' f`, true},
		{`awk '$1 > 5 {print}' f`, true},
		{`cat < input.txt`, true},
		{`grep x <<< "a > b"`, true},
	}
	for _, c := range cases {
		if got := isReadOnlyCommand(c.cmd); got != c.want {
			t.Errorf("isReadOnlyCommand(%q) = %v, want %v", c.cmd, got, c.want)
		}
		if got := allReadOnly(c.cmd); got != c.want {
			t.Errorf("allReadOnly(%q) = %v, want %v", c.cmd, got, c.want)
		}
	}
}

// TestRedirectWritesAskEndToEnd checks the decision path: a redirected
// read-only command falls through to the default ask (step 5 no longer allows
// it, and bashAllowed no longer counts it as covered), while plan mode — which
// trusts allReadOnly — denies it.
func TestRedirectWritesAskEndToEnd(t *testing.T) {
	cfg := cfgFromJSON(t, `{"permissions":{"allow":["Bash(go test *)"]}}`)
	cases := []struct {
		cmd  string
		want Decision
	}{
		{`echo x > ~/.bashrc`, DecisionAsk},
		{`cat a > /etc/x`, DecisionAsk},
		{`go test ./... && echo done > ~/.bashrc`, DecisionAsk},
		{`go test ./... 2>&1 | tail -5`, DecisionAllow},
		{`ls -la 2>/dev/null`, DecisionAllow},
		{`echo x > /tmp/scratch.txt`, DecisionAllow},
	}
	for _, c := range cases {
		if d, r := cfg.CheckArgs("Bash", bash(c.cmd), "/proj"); d != c.want {
			t.Errorf("%q: want %v got %v (%s)", c.cmd, c.want, d, r)
		}
	}
	plan := cfgFromJSON(t, `{"permissions":{"defaultMode":"plan"}}`)
	if d, _ := plan.CheckArgs("Bash", bash(`echo x > ~/.bashrc`), "/proj"); d != DecisionDeny {
		t.Errorf("plan mode: redirect to ~/.bashrc want deny, got %v", d)
	}
	if d, _ := plan.CheckArgs("Bash", bash(`ls 2>/dev/null`), "/proj"); d == DecisionDeny {
		t.Errorf("plan mode: ls 2>/dev/null must not be denied")
	}
}
