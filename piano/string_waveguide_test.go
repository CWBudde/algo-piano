package piano

import (
	"fmt"
	"math"
	"testing"
)

func TestTuningAccuracy(t *testing.T) {
	sampleRate := 48000

	tests := []struct {
		note         int
		expectedFreq float32
		tolerance    float32
	}{
		{69, 440.0, 1.0},
		{60, 261.63, 1.0},
		{72, 523.25, 2.0},
		{48, 130.81, 1.0},
		{57, 220.0, 1.0},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("Note%d", tt.note), func(t *testing.T) {
			freq := midiNoteToFreq(tt.note)
			str := NewStringWaveguide(sampleRate, freq)
			str.Excite(0.5)

			numSamples := sampleRate * 2
			samples := make([]float32, numSamples)
			for i := 0; i < numSamples; i++ {
				samples[i] = str.Process()
			}

			measuredFreq := measureFundamentalFreq(samples, float32(sampleRate))
			diff := math.Abs(float64(measuredFreq - tt.expectedFreq))
			if diff > float64(tt.tolerance) {
				t.Errorf("Note %d: expected %.2f Hz, got %.2f Hz (diff: %.2f Hz, tolerance: %.2f Hz)",
					tt.note, tt.expectedFreq, measuredFreq, diff, tt.tolerance)
			}
		})
	}
}

func TestLoopLossEnergyDecaysMonotonically(t *testing.T) {
	const sampleRate = 48000
	str := NewStringWaveguide(sampleRate, 220.0)
	str.SetLoopLoss(0.997, 0.25)
	str.ExciteAtPosition(0.6, 0.2)

	const numSamples = 24000
	samples := make([]float32, numSamples)
	for i := range samples {
		samples[i] = str.Process()
	}

	window := 2000
	prev := float64(math.MaxFloat32)
	for start := window * 4; start+window <= len(samples); start += window {
		energy := windowRMS(samples[start : start+window])
		if energy > prev*1.15 {
			t.Fatalf("energy rose unexpectedly: prev=%.8f curr=%.8f at window %d", prev, energy, start/window)
		}
		prev = energy
	}
}

func TestDispersionDetunesPartialsFromHarmonicSeries(t *testing.T) {
	const sampleRate = 48000
	const f0 = 220.0

	base := NewStringWaveguide(sampleRate, f0)
	base.SetLoopLoss(0.9997, 0.04)
	base.ExciteAtPosition(0.7, 0.2)

	disp := NewStringWaveguide(sampleRate, f0)
	disp.SetLoopLoss(0.9997, 0.04)
	disp.SetDispersion(0.8)
	disp.ExciteAtPosition(0.7, 0.2)

	const numSamples = 98304
	baseSamples := make([]float32, numSamples)
	dispSamples := make([]float32, numSamples)
	for i := 0; i < numSamples; i++ {
		baseSamples[i] = base.Process()
		dispSamples[i] = disp.Process()
	}

	skip := 4096
	baseAnalysis := baseSamples[skip : skip+8192]
	dispAnalysis := dispSamples[skip : skip+8192]
	fund := findPeakNear(baseAnalysis, sampleRate, f0, 20.0)
	if fund <= 0 {
		t.Fatalf("could not detect fundamental")
	}

	detunedPartials := 0
	for partial := 2; partial <= 5; partial++ {
		target := float64(partial) * fund
		basePeak := findPeakNear(baseAnalysis, sampleRate, target, target*0.12)
		dispPeak := findPeakNear(dispAnalysis, sampleRate, target, target*0.12)
		if basePeak <= 0 || dispPeak <= 0 {
			t.Fatalf("could not detect partial %d near %.2f Hz", partial, target)
		}
		if math.Abs(dispPeak-basePeak) > 1.0 {
			detunedPartials++
		}
	}
	if detunedPartials < 2 {
		t.Fatalf("expected at least 2 partials to be detuned by dispersion, got %d", detunedPartials)
	}
}

func TestStrikePositionChangesSpectralTilt(t *testing.T) {
	const sampleRate = 48000
	const f0 = 261.63

	nearBridge := NewStringWaveguide(sampleRate, f0)
	nearBridge.SetLoopLoss(0.9997, 0.04)
	nearBridge.ExciteAtPosition(0.7, 0.08)

	nearMiddle := NewStringWaveguide(sampleRate, f0)
	nearMiddle.SetLoopLoss(0.9997, 0.04)
	nearMiddle.ExciteAtPosition(0.7, 0.45)

	const numSamples = 16384
	a := make([]float32, numSamples)
	b := make([]float32, numSamples)
	for i := 0; i < numSamples; i++ {
		a[i] = nearBridge.Process()
		b[i] = nearMiddle.Process()
	}

	aCentroid := spectralCentroid(a[2048:], sampleRate, 2048)
	bCentroid := spectralCentroid(b[2048:], sampleRate, 2048)
	if aCentroid <= bCentroid {
		t.Fatalf("expected near-bridge strike to be brighter: bridge=%.2fHz middle=%.2fHz", aCentroid, bCentroid)
	}
}

func TestHighFreqDampingReducesHighPartialEnergy(t *testing.T) {
	const sampleRate = 48000
	const f0 = 220.0
	const numSamples = 48000 // 1 second

	// Low damping: high partials sustain.
	lowDamp := NewStringWaveguide(sampleRate, f0)
	lowDamp.SetLoopLoss(0.9997, 0.02)
	lowDamp.ExciteAtPosition(0.7, 0.12) // near bridge for strong harmonics

	// High damping: high partials decay faster.
	highDamp := NewStringWaveguide(sampleRate, f0)
	highDamp.SetLoopLoss(0.9997, 0.45)
	highDamp.ExciteAtPosition(0.7, 0.12)

	lowSamples := make([]float32, numSamples)
	highSamples := make([]float32, numSamples)
	for i := 0; i < numSamples; i++ {
		lowSamples[i] = lowDamp.Process()
		highSamples[i] = highDamp.Process()
	}

	// Measure spectral centroid in the tail (last 0.5s). Higher damping
	// should produce a lower centroid (less high-frequency energy).
	tailStart := numSamples / 2
	lowCentroid := spectralCentroid(lowSamples[tailStart:], sampleRate, 2048)
	highCentroid := spectralCentroid(highSamples[tailStart:], sampleRate, 2048)

	if highCentroid >= lowCentroid {
		t.Fatalf("expected higher damping to lower spectral centroid in tail: low=%.1fHz high=%.1fHz",
			lowCentroid, highCentroid)
	}
	t.Logf("tail centroid: low_damp=%.1fHz high_damp=%.1fHz (ratio=%.2f)",
		lowCentroid, highCentroid, highCentroid/lowCentroid)
}

func TestUnisonDetuneProducesBeating(t *testing.T) {
	const sampleRate = 48000
	const note = 69
	f0 := midiNoteToFreq(note)

	s1 := NewStringWaveguide(sampleRate, f0*centsToRatio(-8.0))
	s2 := NewStringWaveguide(sampleRate, f0*centsToRatio(8.0))
	s1.SetLoopLoss(0.9997, 0.03)
	s2.SetLoopLoss(0.9997, 0.03)
	s1.ExciteAtPosition(0.6, 0.18)
	s2.ExciteAtPosition(0.6, 0.18)

	ref1 := NewStringWaveguide(sampleRate, f0)
	ref2 := NewStringWaveguide(sampleRate, f0)
	ref1.SetLoopLoss(0.9997, 0.03)
	ref2.SetLoopLoss(0.9997, 0.03)
	ref1.ExciteAtPosition(0.6, 0.18)
	ref2.ExciteAtPosition(0.6, 0.18)

	const numSamples = 98304
	unisonOut := make([]float32, numSamples)
	referenceOut := make([]float32, numSamples)
	for i := 0; i < numSamples; i++ {
		unisonOut[i] = 0.5*s1.Process() + 0.5*s2.Process()
		referenceOut[i] = 0.5*ref1.Process() + 0.5*ref2.Process()
	}

	analysis := unisonOut[8192 : 8192+32768]
	refAnalysis := referenceOut[8192 : 8192+32768]

	u1, u2 := twoStrongestPeaksNear(analysis, sampleRate, float64(f0), 30.0)
	r1, r2 := twoStrongestPeaksNear(refAnalysis, sampleRate, float64(f0), 30.0)
	uSep := math.Abs(u2 - u1)
	rSep := math.Abs(r2 - r1)
	if uSep <= rSep+1.5 {
		t.Fatalf("expected larger peak separation for detuned unison: unison=%.2fHz reference=%.2fHz", uSep, rSep)
	}
}
