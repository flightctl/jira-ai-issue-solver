package executor

// GeminiPricing holds per-million-token prices for Gemini cost estimation.
type GeminiPricing struct {
	InputPerMTok  float64
	OutputPerMTok float64
	CachedPerMTok float64
}

// computeGeminiCost estimates session cost from token counts and pricing.
// Returns 0 if no tokens were consumed.
func computeGeminiCost(session SessionOutput, pricing GeminiPricing) float64 {
	if session.InputTokens == 0 && session.OutputTokens == 0 {
		return 0
	}

	uncachedInput := max(session.InputTokens-session.CachedTokens, 0)

	cost := float64(uncachedInput) * pricing.InputPerMTok / 1_000_000
	cost += float64(session.OutputTokens) * pricing.OutputPerMTok / 1_000_000
	cost += float64(session.CachedTokens) * pricing.CachedPerMTok / 1_000_000

	return cost
}

// applyCostEstimate fills in CostUSD from token counts for Gemini
// sessions where the CLI does not report cost directly.
func (p *Pipeline) applyCostEstimate(session *SessionOutput) {
	if session.CostUSD > 0 {
		return
	}
	session.CostUSD = computeGeminiCost(*session, p.cfg.GeminiPricing)
}
