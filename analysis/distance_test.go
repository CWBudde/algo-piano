package analysis

import (
	"math"
	"math/rand"
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

func TestEstimateLagFindsPositiveShift(t *testing.T) {
	const (
		n      = 8192
		shift  = 237
		maxLag = 600
	)
	ref := randomSignal(n, 7)
	cand := make([]float64, n)
	copy(cand, ref[shift:])

	got := estimateLag(ref, cand, maxLag)
	if got != shift {
		t.Fatalf("estimateLag() = %d, want %d", got, shift)
	}
}

func TestEstimateLagFindsNegativeShift(t *testing.T) {
	const (
		n      = 8192
		shift  = -191
		maxLag = 600
	)
	ref := randomSignal(n, 11)
	cand := make([]float64, n)
	copy(cand[-shift:], ref)

	got := estimateLag(ref, cand, maxLag)
	if got != shift {
		t.Fatalf("estimateLag() = %d, want %d", got, shift)
	}
}

func TestEstimateLagFFTMatchesExhaustive(t *testing.T) {
	const (
		n      = 16000
		shift  = 443
		maxLag = 1000
	)
	ref := randomSignal(n, 23)
	cand := make([]float64, n)
	copy(cand, ref[shift:])

	got := estimateLag(ref, cand, maxLag)
	want := estimateLagExhaustive(ref, cand, maxLag)
	if got != want {
		t.Fatalf("estimateLag() = %d, exhaustive = %d", got, want)
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

func randomSignal(n int, seed int64) []float64 {
	rng := rand.New(rand.NewSource(seed))
	out := make([]float64, n)
	for i := range out {
		out[i] = rng.Float64()*2 - 1
	}
	return out
}
