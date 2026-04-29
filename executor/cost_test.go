package executor

import (
	"math"
	"testing"
)

func TestComputeGeminiCost(t *testing.T) {
	pricing := GeminiPricing{
		InputPerMTok:  0.15,
		OutputPerMTok: 0.60,
		CachedPerMTok: 0.0375,
	}

	tests := []struct {
		name     string
		session  SessionOutput
		wantCost float64
	}{
		{
			name:     "zero tokens",
			session:  SessionOutput{},
			wantCost: 0,
		},
		{
			name: "input only",
			session: SessionOutput{
				InputTokens: 1_000_000,
			},
			wantCost: 0.15,
		},
		{
			name: "output only",
			session: SessionOutput{
				OutputTokens: 1_000_000,
			},
			wantCost: 0.60,
		},
		{
			name: "mixed tokens no cache",
			session: SessionOutput{
				InputTokens:  100_000,
				OutputTokens: 10_000,
			},
			wantCost: 0.015 + 0.006,
		},
		{
			name: "with cached tokens",
			session: SessionOutput{
				InputTokens:  100_000,
				OutputTokens: 10_000,
				CachedTokens: 60_000,
			},
			// uncached input: 40_000 * 0.15/1M = 0.006
			// output:         10_000 * 0.60/1M = 0.006
			// cached:         60_000 * 0.0375/1M = 0.00225
			wantCost: 0.006 + 0.006 + 0.00225,
		},
		{
			name: "cached exceeds input clamps to zero uncached",
			session: SessionOutput{
				InputTokens:  50_000,
				OutputTokens: 10_000,
				CachedTokens: 80_000,
			},
			// uncached input: 0 (clamped)
			// output:         10_000 * 0.60/1M = 0.006
			// cached:         80_000 * 0.0375/1M = 0.003
			wantCost: 0.006 + 0.003,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := computeGeminiCost(tt.session, pricing)
			if math.Abs(got-tt.wantCost) > 1e-9 {
				t.Errorf("computeGeminiCost() = %v, want %v", got, tt.wantCost)
			}
		})
	}
}

func TestComputeGeminiCost_ZeroPricing(t *testing.T) {
	session := SessionOutput{
		InputTokens:  100_000,
		OutputTokens: 10_000,
	}
	got := computeGeminiCost(session, GeminiPricing{})
	if got != 0 {
		t.Errorf("zero pricing should produce zero cost, got %v", got)
	}
}
