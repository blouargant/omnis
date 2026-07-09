package registries

import "testing"

func TestParseGiteaRef(t *testing.T) {
	cases := []struct {
		name    string
		url     string
		wantErr bool
		host    string
		base    string
		owner   string
		repo    string
		branch  string
		path    string
	}{
		{
			// The reported case: a Gitea served under a "/git" base path,
			// with a dotted owner name.
			name:   "subpath-hosted tree url",
			url:    "https://llm-gateway.ai.chapsvision.com/git/Chapsvision.RD/cc-chapsvision-marketplace/src/branch/main/plugins",
			host:   "llm-gateway.ai.chapsvision.com",
			base:   "/git",
			owner:  "Chapsvision.RD",
			repo:   "cc-chapsvision-marketplace",
			branch: "main",
			path:   "plugins",
		},
		{
			name:   "root-hosted tree url",
			url:    "https://gitea.example.com/owner/repo/src/branch/main/skills",
			host:   "gitea.example.com",
			base:   "",
			owner:  "owner",
			repo:   "repo",
			branch: "main",
			path:   "skills",
		},
		{
			name:   "root-hosted tree url no path",
			url:    "https://gitea.example.com/owner/repo/src/branch/main",
			host:   "gitea.example.com",
			owner:  "owner",
			repo:   "repo",
			branch: "main",
			path:   "",
		},
		{
			name:   "trailing slash trimmed",
			url:    "https://gitea.example.com/owner/repo/src/branch/main/plugins/",
			host:   "gitea.example.com",
			owner:  "owner",
			repo:   "repo",
			branch: "main",
			path:   "plugins",
		},
		{
			name:   "multi-segment base path with nested tree path",
			url:    "https://host.example/a/b/owner/repo/src/branch/dev/x/y",
			host:   "host.example",
			base:   "/a/b",
			owner:  "owner",
			repo:   "repo",
			branch: "dev",
			path:   "x/y",
		},
		{
			name:   "repo-only root",
			url:    "https://gitea.example.com/owner/repo",
			host:   "gitea.example.com",
			base:   "",
			owner:  "owner",
			repo:   "repo",
			branch: "main",
		},
		{
			name:   "repo-only subpath",
			url:    "https://host.example/git/owner/repo",
			host:   "host.example",
			base:   "/git",
			owner:  "owner",
			repo:   "repo",
			branch: "main",
		},
		{
			name:    "too few segments",
			url:     "https://host.example/onlyone",
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ref, err := parseGiteaRef(tc.url)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %+v", ref)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if ref.Host != tc.host || ref.BasePath != tc.base || ref.Owner != tc.owner ||
				ref.Repo != tc.repo || ref.Branch != tc.branch || ref.Path != tc.path {
				t.Fatalf("got %+v", ref)
			}
		})
	}
}

func TestGiteaAPIURLHonoursBasePath(t *testing.T) {
	sub := GiteaRef{Host: "host.example", BasePath: "/git", Owner: "o", Repo: "r"}
	if got := sub.apiURL("/repos/%s/%s/branches/main", sub.Owner, sub.Repo); got != "https://host.example/git/api/v1/repos/o/r/branches/main" {
		t.Fatalf("subpath apiURL = %q", got)
	}
	root := GiteaRef{Host: "host.example", Owner: "o", Repo: "r"}
	if got := root.apiURL("/repos/%s/%s/branches/main", root.Owner, root.Repo); got != "https://host.example/api/v1/repos/o/r/branches/main" {
		t.Fatalf("root apiURL = %q", got)
	}
}

// Auto-detection (empty provider) must still route a subpath-hosted Gitea tree
// URL to the Gitea parser via the "/src/branch/" marker.
func TestParseRepoRefAutodetectSubpathGitea(t *testing.T) {
	ref, err := ParseRepoRef("https://host.example/git/owner/repo/src/branch/main/plugins", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	g, ok := ref.(GiteaRef)
	if !ok {
		t.Fatalf("expected GiteaRef, got %T", ref)
	}
	if g.BasePath != "/git" || g.Owner != "owner" || g.Repo != "repo" {
		t.Fatalf("got %+v", g)
	}
}
