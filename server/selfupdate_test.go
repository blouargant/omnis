package main

import "testing"

func boolPtr(b bool) *bool { return &b }

// TestUpdateCheckEnabled locks in the on/off precedence:
// dev-build guard, then OMNIS_UPDATE_CHECK env, then server.yaml update_check,
// then the enabled-by-default fallback.
func TestUpdateCheckEnabled(t *testing.T) {
	tests := []struct {
		name    string
		version string
		env     string // "" means unset
		cfg     *bool
		want    bool
	}{
		{name: "dev build is always off", version: "dev", env: "", cfg: boolPtr(true), want: false},
		{name: "empty version is always off", version: "", env: "", cfg: boolPtr(true), want: false},
		{name: "default enabled when nothing set", version: "1.2.3", env: "", cfg: nil, want: true},
		{name: "config false disables", version: "1.2.3", env: "", cfg: boolPtr(false), want: false},
		{name: "config true enables", version: "1.2.3", env: "", cfg: boolPtr(true), want: true},
		{name: "env true overrides config false", version: "1.2.3", env: "true", cfg: boolPtr(false), want: true},
		{name: "env false overrides config true", version: "1.2.3", env: "false", cfg: boolPtr(true), want: false},
		{name: "env false overrides default", version: "1.2.3", env: "false", cfg: nil, want: false},
		{name: "unparseable env falls back to config", version: "1.2.3", env: "nonsense", cfg: boolPtr(false), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.env == "" {
				t.Setenv("OMNIS_UPDATE_CHECK", "")
			} else {
				t.Setenv("OMNIS_UPDATE_CHECK", tt.env)
			}
			if got := updateCheckEnabled(tt.version, tt.cfg); got != tt.want {
				t.Errorf("updateCheckEnabled(%q, %v) = %v, want %v", tt.version, tt.cfg, got, tt.want)
			}
		})
	}
}
