package lsp

import "testing"

// TestServerForFileFilenames confirms extensionless files route via Filenames
// globs while extension matching keeps working.
func TestServerForFileFilenames(t *testing.T) {
	cfg := &Config{Servers: map[string]Server{
		"dockerfile": {
			Extensions: []string{".dockerfile"},
			Filenames:  []string{"Dockerfile", "Dockerfile.*", "*.Dockerfile", "Containerfile"},
		},
		"go": {Extensions: []string{".go"}},
	}}
	cases := []struct {
		path string
		want string // server key, "" = no match
	}{
		{"/proj/Dockerfile", "dockerfile"},       // exact basename, no extension
		{"/proj/Dockerfile.prod", "dockerfile"},  // glob Dockerfile.*
		{"/proj/api.Dockerfile", "dockerfile"},   // glob *.Dockerfile
		{"/proj/Containerfile", "dockerfile"},    // exact alt name
		{"/proj/build.dockerfile", "dockerfile"}, // extension branch
		{"/proj/main.go", "go"},                  // ordinary extension
		{"/proj/Makefile", ""},                   // nothing claims it
		{"/proj/README.md", ""},                  // unhandled extension
	}
	for _, tc := range cases {
		s, ok := cfg.ServerForFile(tc.path)
		if tc.want == "" {
			if ok {
				t.Errorf("%s: expected no server, got %q", tc.path, s.Name)
			}
			continue
		}
		if !ok || s.Name != tc.want {
			t.Errorf("%s: got (%q, %v), want %q", tc.path, s.Name, ok, tc.want)
		}
	}
}

// TestLangIDForPath confirms the per-extension languageId override with fallback
// to the server default (LanguageID, else the map key).
func TestLangIDForPath(t *testing.T) {
	cpp := Server{
		Name:        "cpp",
		LanguageID:  "cpp",
		LanguageIDs: map[string]string{".c": "c"},
	}
	if got := cpp.langIDForPath("/x/a.c"); got != "c" {
		t.Errorf("a.c languageId = %q, want c", got)
	}
	if got := cpp.langIDForPath("/x/a.cpp"); got != "cpp" {
		t.Errorf("a.cpp languageId = %q, want cpp (default)", got)
	}
	if got := cpp.langIDForPath("/x/a.h"); got != "cpp" {
		t.Errorf("a.h languageId = %q, want cpp (default)", got)
	}

	// No LanguageID and no override → falls back to the map key (Name).
	plain := Server{Name: "ruby"}
	if got := plain.langIDForPath("/x/a.rb"); got != "ruby" {
		t.Errorf("plain languageId = %q, want ruby", got)
	}
}
