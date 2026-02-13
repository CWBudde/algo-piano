package irsynth

import (
	"math"
	"testing"
)

func TestGenerateStereoBasic(t *testing.T) {
	cfg := DefaultConfig()
	cfg.SampleRate = 48000
	cfg.DurationS = 0.5
	cfg.Modes = 32
	cfg.Seed = 42
	cfg.NormalizePeak = 0.8

	l, r, err := GenerateStereo(cfg)
	if err != nil {
		t.Fatalf("GenerateStereo: %v", err)
	}
	if len(l) != int(0.5*48000) || len(r) != len(l) {
		t.Fatalf("unexpected output lengths: L=%d R=%d", len(l), len(r))
	}

	maxAbs := 0.0
	energy := 0.0
	for i := range l {
		if math.IsNaN(float64(l[i])) || math.IsInf(float64(l[i]), 0) || math.IsNaN(float64(r[i])) || math.IsInf(float64(r[i]), 0) {
			t.Fatalf("non-finite sample at %d", i)
		}
		la := math.Abs(float64(l[i]))
		ra := math.Abs(float64(r[i]))
		if la > maxAbs {
			maxAbs = la
		}
		if ra > maxAbs {
			maxAbs = ra
		}
		energy += float64(l[i]*l[i] + r[i]*r[i])
	}
	if energy <= 1e-8 {
		t.Fatalf("expected non-zero energy")
	}
	if maxAbs > 0.81 {
		t.Fatalf("unexpected normalization peak: %.6f", maxAbs)
	}
}

func TestGenerateStereoDeterministicForSeed(t *testing.T) {
	cfg := DefaultConfig()
	cfg.SampleRate = 32000
	cfg.DurationS = 0.2
	cfg.Modes = 24
	cfg.Seed = 99

	l1, r1, err := GenerateStereo(cfg)
	if err != nil {
		t.Fatalf("first GenerateStereo: %v", err)
	}
	l2, r2, err := GenerateStereo(cfg)
	if err != nil {
		t.Fatalf("second GenerateStereo: %v", err)
	}
	if len(l1) != len(l2) || len(r1) != len(r2) {
		t.Fatalf("length mismatch")
	}
	for i := range l1 {
		if l1[i] != l2[i] || r1[i] != r2[i] {
			t.Fatalf("non-deterministic output at index %d", i)
		}
	}
}
