package tui

import (
	"strings"
	"testing"
)

func TestTotalCostDollars_RequiresBothPrices(t *testing.T) {
	if _, ok := totalCostDollars(1000, 1000, 0, 2.4); ok {
		t.Fatalf("expected false when input price is missing")
	}
	if _, ok := totalCostDollars(1000, 1000, 0.6, 0); ok {
		t.Fatalf("expected false when output price is missing")
	}
}

func TestTotalCostDollars_ComputesFromInOutTokens(t *testing.T) {
	got, ok := totalCostDollars(1500000, 250000, 0.6, 2.4)
	if !ok {
		t.Fatalf("expected cost to be available")
	}
	want := 1.5*0.6 + 0.25*2.4
	if got != want {
		t.Fatalf("totalCostDollars() = %v, want %v", got, want)
	}
}

func TestBuildStatusText_ContainsTokensAndCost(t *testing.T) {
	cfg := Config{
		AppName:                    "agent-toolkit",
		SessionID:                  "s1",
		UserID:                     "u1",
		InputTokenPricePerMillion:  0.6,
		OutputTokenPricePerMillion: 2.4,
	}

	text := buildStatusText(cfg, 1234, 567)
	if !strings.Contains(text, "tokens in/out") {
		t.Fatalf("status missing token section: %q", text)
	}
	if !strings.Contains(text, "$") {
		t.Fatalf("status missing dollar total: %q", text)
	}
}

func TestBuildStatusText_HidesCostWithoutPrices(t *testing.T) {
	cfg := Config{AppName: "agent-toolkit", SessionID: "s1", UserID: "u1"}
	text := buildStatusText(cfg, 1234, 567)
	if strings.Contains(text, "$") {
		t.Fatalf("status should not include dollar total when prices are missing: %q", text)
	}
}

func TestBuildTurnUsageText_ContainsTokenBreakdownAndCost(t *testing.T) {
	cfg := Config{InputTokenPricePerMillion: 0.6, OutputTokenPricePerMillion: 2.4}
	text := buildTurnUsageText(cfg, 1000, 500)
	if !strings.Contains(text, "in/out/total") {
		t.Fatalf("turn usage should include token breakdown: %q", text)
	}
	if !strings.Contains(text, "$") {
		t.Fatalf("turn usage should include dollar cost when prices are set: %q", text)
	}
}

func TestBuildTurnUsageText_HidesCostWithoutPrices(t *testing.T) {
	text := buildTurnUsageText(Config{}, 1000, 500)
	if strings.Contains(text, "$") {
		t.Fatalf("turn usage should not include dollar cost when prices are missing: %q", text)
	}
}
