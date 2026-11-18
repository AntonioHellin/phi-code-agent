package provider

import (
	"strings"

	"phi-agent-code/internal/api"
)

// usdToEUR defines the static reference exchange factor used to express published
// per-million-token USD API tiers in EUR estimates.
const usdToEUR = 0.92

// ModelPricing defines per-million-token billing rates in EUR for cost estimation.
type ModelPricing struct {
	InputPerMillion         float64
	OutputPerMillion        float64
	CacheCreationPerMillion float64
	CacheReadPerMillion     float64
}

type pricingRule struct {
	prefix  string
	pricing ModelPricing
}

func usdRates(input, output, cacheCreate, cacheRead float64) ModelPricing {
	return ModelPricing{
		InputPerMillion:         input * usdToEUR,
		OutputPerMillion:        output * usdToEUR,
		CacheCreationPerMillion: cacheCreate * usdToEUR,
		CacheReadPerMillion:     cacheRead * usdToEUR,
	}
}

// pricingCatalog maps normalized model ID prefixes (ordered most-specific first) to EUR rates.
var pricingCatalog = []pricingRule{
	// Anthropic Claude families
	{prefix: "claude-opus", pricing: usdRates(15.00, 75.00, 18.75, 1.50)},
	{prefix: "claude-sonnet", pricing: usdRates(3.00, 15.00, 3.75, 0.30)},
	{prefix: "claude-haiku", pricing: usdRates(0.80, 4.00, 1.00, 0.08)},

	// Google Gemini families
	{prefix: "gemini-2.5-pro", pricing: usdRates(1.25, 10.00, 1.25, 0.3125)},
	{prefix: "gemini-1.5-pro", pricing: usdRates(1.25, 5.00, 1.25, 0.3125)},
	{prefix: "gemini-4-carbon", pricing: usdRates(1.25, 10.00, 1.25, 0.3125)},
	{prefix: "gemini-4-argon", pricing: usdRates(1.25, 10.00, 1.25, 0.3125)},
	{prefix: "gemini-2.5-flash", pricing: usdRates(0.15, 0.60, 0.15, 0.0375)},
	{prefix: "gemini-2.0-flash", pricing: usdRates(0.10, 0.40, 0.10, 0.025)},
	{prefix: "gemini-3", pricing: usdRates(0.15, 0.60, 0.15, 0.0375)},
	{prefix: "gemini-4-hydrogen", pricing: usdRates(0.15, 0.60, 0.15, 0.0375)},
	{prefix: "gemini-", pricing: usdRates(0.15, 0.60, 0.15, 0.0375)},

	// Local / offline providers (zero API cost)
	{prefix: "mock", pricing: ModelPricing{}},
	{prefix: "llama", pricing: ModelPricing{}},
	{prefix: "qwen", pricing: ModelPricing{}},
	{prefix: "deepseek", pricing: ModelPricing{}},
	{prefix: "mistral", pricing: ModelPricing{}},
	{prefix: "gemma", pricing: ModelPricing{}},
	{prefix: "phi", pricing: ModelPricing{}},
	{prefix: "codellama", pricing: ModelPricing{}},
}

// LookupPricing resolves the estimated EUR per-million-token rates for a model identifier.
// Returns (ModelPricing{}, false) when the model family is unrecognized.
func LookupPricing(model string) (ModelPricing, bool) {
	normalized := strings.ToLower(strings.TrimSpace(model))
	if normalized == "" {
		return ModelPricing{}, false
	}
	// Strip optional Ollama tag suffix when matching local model families (e.g. "llama3.2:latest").
	base := strings.SplitN(normalized, ":", 2)[0]
	for _, rule := range pricingCatalog {
		if strings.HasPrefix(base, rule.prefix) || strings.HasPrefix(normalized, rule.prefix) {
			return rule.pricing, true
		}
	}
	return ModelPricing{}, false
}

// EstimateCostEUR computes the estimated session cost in EUR for the given model and token usage.
// Returns (0, false) if no pricing rule matches the model name.
func EstimateCostEUR(model string, usage api.Usage) (float64, bool) {
	rates, ok := LookupPricing(model)
	if !ok {
		return 0, false
	}

	cost := (float64(usage.InputTokens)*rates.InputPerMillion +
		float64(usage.CacheCreationTokens)*rates.CacheCreationPerMillion +
		float64(usage.CacheReadTokens)*rates.CacheReadPerMillion +
		float64(usage.OutputTokens)*rates.OutputPerMillion) / 1_000_000.0
	return cost, true
}
