package main

import (
	"math"
	"testing"

	"github.com/cwbudde/algo-piano/analysis"
)

func TestSanitizeMetricsReplacesNonFiniteValues(t *testing.T) {
	in := analysis.Metrics{
		TimeRMSE:        math.NaN(),
		EnvelopeRMSEDB:  math.Inf(1),
		SpectralRMSEDB:  math.Inf(-1),
		RefDecayDBPerS:  math.NaN(),
		CandDecayDBPerS: math.NaN(),
		DecayDiffDBPerS: math.NaN(),
		Score:           math.NaN(),
		Similarity:      math.NaN(),
	}
	out := sanitizeMetrics(in)
	if !isFiniteFloat(out.TimeRMSE) ||
		!isFiniteFloat(out.EnvelopeRMSEDB) ||
		!isFiniteFloat(out.SpectralRMSEDB) ||
		!isFiniteFloat(out.DecayDiffDBPerS) ||
		!isFiniteFloat(out.Score) ||
		!isFiniteFloat(out.Similarity) {
		t.Fatalf("expected sanitized finite metrics: %+v", out)
	}
	if out.Score < 0 || out.Score > 1 {
		t.Fatalf("expected score in [0,1], got=%f", out.Score)
	}
	if out.Similarity < 0 || out.Similarity > 1 {
		t.Fatalf("expected similarity in [0,1], got=%f", out.Similarity)
	}
}

func TestWeightedScoreHandlesNaN(t *testing.T) {
	score := weightedScore(
		[]analysis.Metrics{
			{Score: math.NaN()},
			{Score: 0.2},
		},
		[]float64{0.5, 0.5},
	)
	if !isFiniteFloat(score) {
		t.Fatalf("expected finite weighted score")
	}
	if score < 0 || score > 1 {
		t.Fatalf("expected weighted score in [0,1], got=%f", score)
	}
}
