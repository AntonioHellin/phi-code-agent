package provider

import (
	"math"
	"testing"

	"phi-agent-code/internal/api"
)

func almostEqualFloat(a, b, tol float64) bool {
	return math.Abs(a-b) <= tol
}

// TestLookupPricingAndEstimateCostEUR verifies EUR rate lookup across cloud models,
// prompt caching multipliers, local/mock zero-cost models, and unknown identifiers.
func TestLookupPricingAndEstimateCostEUR(t *testing.T) {
	// 1. Anthropic Sonnet: input=3*0.92, output=15*0.92, cacheCreate=3.75*0.92, cacheRead=0.30*0.92 per 1M.
	usage := api.Usage{
		InputTokens:         1_000_000,
		OutputTokens:        1_000_000,
		CacheCreationTokens: 1_000_000,
		CacheReadTokens:     1_000_000,
	}
	sonnetCost, ok := EstimateCostEUR("  claude-sonnet-4-5-20250929 ", usage)
	if !ok {
		t.Fatalf("expected known pricing for Claude Sonnet")
	}
	expectedSonnet := (3.00 + 15.00 + 3.75 + 0.30) * usdToEUR
	if !almostEqualFloat(sonnetCost, expectedSonnet, 1e-6) {
		t.Errorf("expected Sonnet EUR cost %.6f, got %.6f", expectedSonnet, sonnetCost)
	}

	// 2. Gemini 2.5 Pro vs Flash pricing tiers.
	proCost, ok := EstimateCostEUR("gemini-2.5-pro", api.Usage{InputTokens: 500_000, OutputTokens: 100_000})
	if !ok {
		t.Fatalf("expected known pricing for gemini-2.5-pro")
	}
	expectedPro := (0.5*1.25 + 0.1*10.00) * usdToEUR
	if !almostEqualFloat(proCost, expectedPro, 1e-6) {
		t.Errorf("expected Gemini Pro EUR cost %.6f, got %.6f", expectedPro, proCost)
	}

	flashCost, ok := EstimateCostEUR("gemini-2.5-flash", api.Usage{InputTokens: 1_000_000})
	if !ok || !almostEqualFloat(flashCost, 0.15*usdToEUR, 1e-6) {
		t.Errorf("expected Gemini Flash input EUR %.6f, got %.6f (ok=%v)", 0.15*usdToEUR, flashCost, ok)
	}

	// 3. Local Ollama models (with :tag suffix) and mock provider incur 0 EUR cost.
	for _, localModel := range []string{"llama3.2:latest", "qwen3-coder", "deepseek-r1:8b", "mock", "mock-fast"} {
		cost, known := EstimateCostEUR(localModel, api.Usage{InputTokens: 50_000, OutputTokens: 10_000})
		if !known || cost != 0 {
			t.Errorf("expected local/mock model %q to have known 0 EUR cost, got cost=%v known=%v", localModel, cost, known)
		}
	}

	// 4. Unknown or empty models return (0, false) safely.
	for _, unknown := range []string{"", "   ", "custom-finetune-v9"} {
		cost, known := EstimateCostEUR(unknown, api.Usage{InputTokens: 1000})
		if known || cost != 0 {
			t.Errorf("expected unknown model %q to return (0, false), got (%v, %v)", unknown, cost, known)
		}
	}
}
