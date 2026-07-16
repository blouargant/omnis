package features

import "testing"

const sampleMD = `# Omnis — What's New

## 1.8 (in development) — Future stuff

- **Not shipped** — should never appear for a 1.7 build.

## 1.7 — Newest release

- **A** — one.
- **B** — two.

## 1.6 — Prior release

- **C** — three.

## 1.5 — Older

- **D** — four.
- **E** — five.
- **F** — six.
- **G** — seven.

## 1.4 — Older still

- **H** — eight.

## 1.2 — Ancient

- **I** — nine.

## 1.1 — Oldest

- **J** — ten.
- **K** — eleven.
`

func parseSample() []Section { return parse(sampleMD) }

func TestParseSkipsH1AndSortsDesc(t *testing.T) {
	secs := parseSample()
	if len(secs) != 7 {
		t.Fatalf("want 7 version sections, got %d", len(secs))
	}
	if secs[0].Version != "1.8" || secs[len(secs)-1].Version != "1.1" {
		t.Fatalf("sections not newest-first: %s..%s", secs[0].Version, secs[len(secs)-1].Version)
	}
	if secs[1].Summary != "Newest release" {
		t.Fatalf("summary parse: got %q", secs[1].Summary)
	}
	// The "1.8 (in development)" label must still yield major.minor.
	if secs[0].Major != 1 || secs[0].Minor != 8 {
		t.Fatalf("in-development header not parsed: %+v", secs[0])
	}
	// secs[3] is 1.5, which lists four bullets.
	if len(secs[3].Items) != 4 {
		t.Fatalf("want 4 items for 1.5, got %d", len(secs[3].Items))
	}
}

// swap the embedded doc for the deterministic sample.
func withSample(t *testing.T) func() {
	t.Helper()
	orig := featuresMD
	featuresMD = sampleMD
	return func() { featuresMD = orig }
}

func TestWhatsNewFiltersRangeAndDropsUnreleased(t *testing.T) {
	defer withSample(t)()
	p := WhatsNew("v1.7.0-14-g7e0989f", "1.6.3")
	if !p.Show {
		t.Fatal("expected Show=true for 1.6→1.7")
	}
	if len(p.Sections) != 1 || p.Sections[0].Version != "1.7" {
		t.Fatalf("want just [1.7], got %+v", p.Sections)
	}
	if p.Current != "1.7.0" {
		t.Fatalf("current not cleaned: %q", p.Current)
	}
	// 1.8 (in development) is above the running build → never shown.
	for _, s := range p.Sections {
		if s.Version == "1.8" {
			t.Fatal("unreleased 1.8 leaked into a 1.7 build")
		}
	}
}

func TestWhatsNewFreshInstallCompacts(t *testing.T) {
	defer withSample(t)()
	// No recorded version ⇒ assume 1.0.0 ⇒ everything up to 1.7.
	p := WhatsNew("1.7.0", "")
	if !p.Show {
		t.Fatal("expected Show=true for a fresh install")
	}
	if p.From != "" && p.From != "1.0.0" {
		// From echoes the raw lastSeen (empty here); the assume-1.0 is internal.
	}
	// Shown newest-first: 1.7,1.6,1.5,1.4,1.2,1.1 (1.8 filtered out).
	want := []string{"1.7", "1.6", "1.5", "1.4", "1.2", "1.1"}
	if len(p.Sections) != len(want) {
		t.Fatalf("want %d sections, got %d: %+v", len(want), len(p.Sections), p.Sections)
	}
	for i, w := range want {
		if p.Sections[i].Version != w {
			t.Fatalf("section %d: want %s got %s", i, w, p.Sections[i].Version)
		}
	}
	// Tiers: first 2 full, next 3 condensed, rest headline.
	if p.Sections[0].Display != "full" || p.Sections[1].Display != "full" {
		t.Fatalf("newest two should be full: %s,%s", p.Sections[0].Display, p.Sections[1].Display)
	}
	if p.Sections[2].Display != "condensed" || p.Sections[4].Display != "condensed" {
		t.Fatalf("indices 2..4 should be condensed")
	}
	if p.Sections[5].Display != "headline" {
		t.Fatalf("oldest should be headline, got %s", p.Sections[5].Display)
	}
	// 1.5 (index 2, condensed) has 4 items → keep 3, More=1.
	s15 := p.Sections[2]
	if len(s15.Items) != condensedItems || s15.More != 1 {
		t.Fatalf("condensed 1.5: items=%d more=%d", len(s15.Items), s15.More)
	}
	// 1.1 (headline) carries no items, only a More count.
	s11 := p.Sections[5]
	if len(s11.Items) != 0 || s11.More != 2 {
		t.Fatalf("headline 1.1: items=%d more=%d", len(s11.Items), s11.More)
	}
}

func TestWhatsNewCaughtUpAndDev(t *testing.T) {
	defer withSample(t)()
	if WhatsNew("1.7.0", "1.7.0").Show {
		t.Fatal("same version should not prompt")
	}
	if WhatsNew("1.7.0", "1.7.3").Show {
		t.Fatal("patch-only difference should not prompt")
	}
	if WhatsNew("1.6.0", "1.7.0").Show {
		t.Fatal("older current should not prompt")
	}
	if WhatsNew("dev", "1.0.0").Show {
		t.Fatal("dev build should never prompt")
	}
}

// The real embedded FEATURES.md must parse to at least one section, so a
// malformed edit is caught in CI rather than silently disabling the modal.
func TestEmbeddedDocParses(t *testing.T) {
	if len(Parse()) == 0 {
		t.Fatal("embedded FEATURES.md parsed to zero sections")
	}
}
