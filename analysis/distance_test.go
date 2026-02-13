package analysis

import (
	"math"
	"testing"
)

func TestCompareIdenticalSignalsHasLowDistance(t *testing.T) {
	sr := 48000
	x := makeDecaySine(sr, 440.0, 1.5, 0.7)
	m := Compare(x, x, sr)
	if m.Score > 0.05 {
		t.Fatalf("expected very low score for identical signals, got %f", m.Score)
	}
	if m.Similarity < 0.85 {
		t.Fatalf("expected high similarity for identical signals, got %f", m.Similarity)
	}
}

func TestCompareDifferentSignalsHasHigherDistance(t *testing.T) {
	sr := 48000
	a := makeDecaySine(sr, 261.63, 1.8, 0.8)
	b := makeDecaySine(sr, 330.0, 0.8, 0.25)
	m := Compare(a, b, sr)
	if m.Score < 0.25 {
		t.Fatalf("expected higher score for different signals, got %f", m.Score)
	}
}

func makeDecaySine(sr int, freq float64, durationSec float64, decaySec float64) []float64 {
	n := int(float64(sr) * durationSec)
	if n < 1 {
		n = 1
	}
	out := make([]float64, n)
	for i := 0; i < n; i++ {
		t := float64(i) / float64(sr)
		env := math.Exp(-t / decaySec)
		out[i] = env * math.Sin(2*math.Pi*freq*t)
	}
	return out
}
