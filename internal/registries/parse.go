package registries

import (
	"fmt"
	"net/url"
	"strings"
)

// ParseRepoRef parses a repository URL into a RepoRef for the given provider.
// If provider is empty, the provider is auto-detected from the URL.
func ParseRepoRef(rawURL, provider string) (RepoRef, error) {
	switch provider {
	case ProviderGitHub:
		return parseGitHubRef(rawURL)
	case ProviderGitLab:
		return parseGitLabRef(rawURL)
	case ProviderGitea:
		return parseGiteaRef(rawURL)
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}
	host := u.Hostname()
	if host == "github.com" {
		return parseGitHubRef(rawURL)
	}
	if host == "gitlab.com" || strings.Contains(u.Path, "/-/tree/") {
		return parseGitLabRef(rawURL)
	}
	if strings.Contains(u.Path, "/src/branch/") {
		return parseGiteaRef(rawURL)
	}
	return nil, fmt.Errorf("cannot auto-detect provider for %q — set provider to github, gitlab, or gitea", rawURL)
}
