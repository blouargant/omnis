package tui

import (
	"strings"
	"testing"
)

func TestTotalCostDollars_RequiresBothPrices(t *testing.T) {
	if _, ok := totalCostDollars(1000, 0, 0, 1000, Config{InputTokenPricePerMillion: 0, OutputTokenPricePerMillion: 2.4}); ok {
		t.Fatalf("expected false when input price is missing")
	}
	if _, ok := totalCostDollars(1000, 0, 0, 1000, Config{InputTokenPricePerMillion: 0.6, OutputTokenPricePerMillion: 0}); ok {
		t.Fatalf("expected false when output price is missing")
	}
}

func TestTotalCostDollars_ComputesFromInOutTokens(t *testing.T) {
	cfg := Config{InputTokenPricePerMillion: 0.6, OutputTokenPricePerMillion: 2.4}
	got, ok := totalCostDollars(1_500_000, 0, 0, 250_000, cfg)
	if !ok {
		t.Fatalf("expected cost to be available")
	}
	want := 1.5*0.6 + 0.25*2.4
	if got != want {
		t.Fatalf("totalCostDollars() = %v, want %v", got, want)
	}
}

func TestTotalCostDollars_AppliesCachedAndCreationDiscounts(t *testing.T) {
	cfg := Config{
		InputTokenPricePerMillion:         3.0,
		OutputTokenPricePerMillion:        15.0,
		CachedInputTokenPricePerMillion:   0.30, // 0.1× input
		CacheCreationTokenPricePerMillion: 3.75, // 1.25× input
	}
	// 1M total prompt = 200k fresh + 700k cache_read + 100k cache_creation
	got, ok := totalCostDollars(1_000_000, 700_000, 100_000, 50_000, cfg)
	if !ok {
		t.Fatalf("expected cost to be available")
	}
	want := 0.2*3.0 + 0.7*0.30 + 0.1*3.75 + 0.05*15.0
	if got < want-1e-9 || got > want+1e-9 {
		t.Fatalf("totalCostDollars() = %v, want %v", got, want)
	}
}

func TestTotalCostDollars_DefaultsCachedToInputPrice(t *testing.T) {
	cfg := Config{InputTokenPricePerMillion: 1.0, OutputTokenPricePerMillion: 2.0}
	got, ok := totalCostDollars(1_000_000, 800_000, 0, 0, cfg)
	if !ok {
		t.Fatalf("expected cost to be available")
	}
	// Without cached price configured, cached tokens cost the full input rate.
	want := 1.0 * 1.0
	if got < want-1e-9 || got > want+1e-9 {
		t.Fatalf("totalCostDollars() = %v, want %v", got, want)
	}
}

func TestBuildStatusText_ContainsTokensAndCost(t *testing.T) {
	cfg := Config{
		AppName:                    "yoke",
		SessionID:                  "s1",
		UserID:                     "u1",
		InputTokenPricePerMillion:  0.6,
		OutputTokenPricePerMillion: 2.4,
	}

	text := buildStatusText(cfg, 1234, 0, 0, 567)
	if !strings.Contains(text, "tokens in/out") {
		t.Fatalf("status missing token section: %q", text)
	}
	if !strings.Contains(text, "$") {
		t.Fatalf("status missing dollar total: %q", text)
	}
}

func TestBuildStatusText_HidesCostWithoutPrices(t *testing.T) {
	cfg := Config{AppName: "yoke", SessionID: "s1", UserID: "u1"}
	text := buildStatusText(cfg, 1234, 0, 0, 567)
	if strings.Contains(text, "$") {
		t.Fatalf("status should not include dollar total when prices are missing: %q", text)
	}
}

func TestBuildStatusText_ShowsCacheBreakdownWhenPresent(t *testing.T) {
	cfg := Config{AppName: "yoke", SessionID: "s1", UserID: "u1"}
	text := buildStatusText(cfg, 1234, 800, 100, 567)
	if !strings.Contains(text, "cache r/w: 800/100") {
		t.Fatalf("status should expose cache read/write counts: %q", text)
	}
}

func TestBuildTurnUsageText_ContainsTokenBreakdownAndCost(t *testing.T) {
	cfg := Config{InputTokenPricePerMillion: 0.6, OutputTokenPricePerMillion: 2.4}
	text := buildTurnUsageText(cfg, 1000, 0, 0, 500)
	if !strings.Contains(text, "in/out/total") {
		t.Fatalf("turn usage should include token breakdown: %q", text)
	}
	if !strings.Contains(text, "$") {
		t.Fatalf("turn usage should include dollar cost when prices are set: %q", text)
	}
}

func TestBuildTurnUsageText_HidesCostWithoutPrices(t *testing.T) {
	text := buildTurnUsageText(Config{}, 1000, 0, 0, 500)
	if strings.Contains(text, "$") {
		t.Fatalf("turn usage should not include dollar cost when prices are missing: %q", text)
	}
}
