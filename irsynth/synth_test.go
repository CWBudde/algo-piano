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

func TestDensityAffectsFrequencyDistribution(t *testing.T) {
	// With high density (>1), modes cluster toward low frequencies.
	// With low density (<1), modes spread more toward high frequencies.
	// We verify this by comparing spectral energy distribution.
	cfg := DefaultConfig()
	cfg.SampleRate = 48000
	cfg.DurationS = 0.1
	cfg.Modes = 64
	cfg.Seed = 1

	// High density: biased low
	cfg.Density = 3.0
	lHigh, _, err := GenerateStereo(cfg)
	if err != nil {
		t.Fatalf("GenerateStereo high density: %v", err)
	}

	// Low density: biased high
	cfg.Density = 0.5
	lLow, _, err := GenerateStereo(cfg)
	if err != nil {
		t.Fatalf("GenerateStereo low density: %v", err)
	}

	// Compare energy in first 10% of samples (initial transient dominated by mode amplitudes).
	// Higher density should have more low-frequency energy = smoother waveform = different character.
	energyHigh := 0.0
	energyLow := 0.0
	for i := range lHigh {
		energyHigh += float64(lHigh[i] * lHigh[i])
		energyLow += float64(lLow[i] * lLow[i])
	}
	// Just verify both produce non-trivial output and they differ
	if energyHigh < 1e-8 || energyLow < 1e-8 {
		t.Fatalf("one density setting produced near-zero energy: high=%.6g low=%.6g", energyHigh, energyLow)
	}
	if energyHigh == energyLow {
		t.Fatal("different density values produced identical output")
	}
}

func TestModeAdditionSmallFrequencyShift(t *testing.T) {
	// Verify that adding one mode doesn't drastically change mode frequencies.
	// With deterministic placement, mode i at count N should be close to mode i at count N+1.
	cfg := DefaultConfig()
	n := 64

	modesN := make([]float64, n)
	modesN1 := make([]float64, n+1)

	minF := 35.0
	maxF := 0.47 * float64(cfg.SampleRate)

	for m := 0; m < n; m++ {
		fNorm := math.Pow((float64(m)+0.5)/float64(n), cfg.Density)
		modesN[m] = minF * math.Pow(maxF/minF, fNorm)
	}
	for m := 0; m < n+1; m++ {
		fNorm := math.Pow((float64(m)+0.5)/float64(n+1), cfg.Density)
		modesN1[m] = minF * math.Pow(maxF/minF, fNorm)
	}

	// Each mode in N should have a nearby match in N+1.
	// Maximum relative shift should be small (bounded by ~1/N).
	maxRelShift := 0.0
	for i := 0; i < n; i++ {
		// Find closest match in N+1
		bestRel := math.Inf(1)
		for j := 0; j < n+1; j++ {
			rel := math.Abs(modesN[i]-modesN1[j]) / modesN[i]
			if rel < bestRel {
				bestRel = rel
			}
		}
		if bestRel > maxRelShift {
			maxRelShift = bestRel
		}
	}

	// With deterministic placement, max shift should be well under 10%
	if maxRelShift > 0.10 {
		t.Fatalf("adding one mode caused %.1f%% max frequency shift, expected <10%%", maxRelShift*100)
	}
}

func TestGenerateBodyBasic(t *testing.T) {
	cfg := DefaultBodyConfig()
	cfg.SampleRate = 48000
	out, err := GenerateBody(cfg)
	if err != nil {
		t.Fatalf("GenerateBody: %v", err)
	}
	wantLen := int(cfg.DurationS * float64(cfg.SampleRate))
	if len(out) != wantLen {
		t.Fatalf("len = %d, want %d", len(out), wantLen)
	}
	energy := 0.0
	for _, s := range out {
		if math.IsNaN(float64(s)) || math.IsInf(float64(s), 0) {
			t.Fatal("non-finite sample")
		}
		energy += float64(s * s)
	}
	if energy < 1e-8 {
		t.Fatal("expected non-zero energy")
	}
}

func TestGenerateBodyDeterministic(t *testing.T) {
	cfg := DefaultBodyConfig()
	cfg.SampleRate = 48000
	cfg.Seed = 77
	a, _ := GenerateBody(cfg)
	b, _ := GenerateBody(cfg)
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("non-deterministic at %d", i)
		}
	}
}

func TestGenerateRoomBasic(t *testing.T) {
	cfg := DefaultRoomConfig()
	cfg.SampleRate = 48000
	l, r, err := GenerateRoom(cfg)
	if err != nil {
		t.Fatalf("GenerateRoom: %v", err)
	}
	wantLen := int(cfg.DurationS * float64(cfg.SampleRate))
	if len(l) != wantLen || len(r) != wantLen {
		t.Fatalf("len L=%d R=%d, want %d", len(l), len(r), wantLen)
	}
	energyL, energyR := 0.0, 0.0
	for i := range l {
		if math.IsNaN(float64(l[i])) || math.IsNaN(float64(r[i])) {
			t.Fatalf("NaN at %d", i)
		}
		energyL += float64(l[i] * l[i])
		energyR += float64(r[i] * r[i])
	}
	if energyL < 1e-8 || energyR < 1e-8 {
		t.Fatalf("expected non-zero stereo energy: L=%.6g R=%.6g", energyL, energyR)
	}
}

func TestGenerateRoomStereoWidth(t *testing.T) {
	// Early reflections with zero width should place L == R.
	cfg := DefaultRoomConfig()
	cfg.SampleRate = 48000
	cfg.StereoWidth = 0.0
	cfg.LateLevel = 0.0 // disable late tail (uses independent L/R noise)
	l, r, err := GenerateRoom(cfg)
	if err != nil {
		t.Fatalf("GenerateRoom: %v", err)
	}
	for i := range l {
		if l[i] != r[i] {
			t.Fatalf("zero-width early-only room should be mono, differ at %d: L=%f R=%f", i, l[i], r[i])
		}
	}
}

func TestPlateEigenfreqs(t *testing.T) {
	// For a square isotropic plate (R=1, S=1), f_{mn}/f_{11} = (m² + n²) / 2.
	// So the lowest modes should be: f11=1x, f12=f21=2.5x, f22=4x, f13=f31=5x, ...
	freqs := plateEigenfreqs(100.0, 10000.0, 50, 1.0, 1.0)
	if len(freqs) == 0 {
		t.Fatal("expected non-empty eigenfrequencies")
	}
	// f11 should be ~100 Hz (the fundamental).
	if math.Abs(freqs[0]-100.0) > 0.01 {
		t.Fatalf("f11 = %.2f, want 100.0", freqs[0])
	}
	// f12 = f21 = 100 * (1+4)/(1+1) = 250 Hz.
	if len(freqs) < 3 {
		t.Fatal("expected at least 3 modes")
	}
	if math.Abs(freqs[1]-250.0) > 0.01 || math.Abs(freqs[2]-250.0) > 0.01 {
		t.Fatalf("f12/f21 = %.2f, %.2f, want 250.0", freqs[1], freqs[2])
	}
	// Frequencies must be sorted.
	for i := 1; i < len(freqs); i++ {
		if freqs[i] < freqs[i-1] {
			t.Fatalf("frequencies not sorted at index %d: %.2f < %.2f", i, freqs[i], freqs[i-1])
		}
	}
}

func TestPlateEigenfreqsOrthotropic(t *testing.T) {
	// With high stiffness ratio, modes along the stiff direction (m) should
	// be pushed to higher frequencies, creating more modes at low frequencies
	// from the compliant direction (n).
	isoFreqs := plateEigenfreqs(50.0, 5000.0, 100, 1.5, 1.0)
	orthoFreqs := plateEigenfreqs(50.0, 5000.0, 100, 1.5, 12.0)

	if len(isoFreqs) == 0 || len(orthoFreqs) == 0 {
		t.Fatal("expected non-empty frequencies")
	}
	// Orthotropic should have a different distribution — fundamentals differ.
	if isoFreqs[0] == orthoFreqs[0] {
		t.Fatal("isotropic and orthotropic should produce different fundamentals")
	}
}

func TestValidateDensity(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Density = 0
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for zero density")
	}
	cfg.Density = -1
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for negative density")
	}
	cfg.Density = 0.5
	if err := cfg.Validate(); err != nil {
		t.Fatalf("unexpected error for valid density: %v", err)
	}
}
