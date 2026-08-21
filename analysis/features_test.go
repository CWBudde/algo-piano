package analysis

import (
	"math"
	"testing"
)

func makeHarmonicTone(sr int, f0 float64, durationSec float64, decaySec float64, amps []float64) []float64 {
	n := int(float64(sr) * durationSec)
	if n < 1 {
		n = 1
	}
	out := make([]float64, n)
	for i := 0; i < n; i++ {
		t := float64(i) / float64(sr)
		env := math.Exp(-t / decaySec)
		var v float64
		for k, a := range amps {
			v += a * math.Sin(2*math.Pi*f0*float64(k+1)*t)
		}
		out[i] = env * v
	}
	return out
}

func TestAnalyzePartialsFindsHarmonicSeries(t *testing.T) {
	sr := 48000
	f0 := 220.0
	x := makeHarmonicTone(sr, f0, 1.0, 1.5, []float64{1.0, 0.5, 0.25, 0.12})

	ps, ok := analyzePartials(x, sr, f0)
	if !ok {
		t.Fatalf("expected partial analysis to succeed for a 1 s harmonic tone")
	}
	for k := 1; k <= 4; k++ {
		if !ps.found[k] {
			t.Fatalf("expected partial %d to be found", k)
		}
		want := f0 * float64(k)
		cents := 1200.0 * math.Log2(ps.freq[k]/want)
		if math.Abs(cents) > 15.0 {
			t.Fatalf("expected partial %d within 15.000000 cents of %f Hz, got %f Hz", k, want, ps.freq[k])
		}
	}
	if ps.amp[1] <= ps.amp[2] {
		t.Fatalf("expected the fundamental to be the strongest partial, got %f vs %f", ps.amp[1], ps.amp[2])
	}
}

func TestAnalyzePartialsEstimatesF0WhenUnknown(t *testing.T) {
	sr := 48000
	f0 := 261.63
	x := makeHarmonicTone(sr, f0, 1.0, 1.5, []float64{0.4, 1.0, 0.6, 0.2})

	ps, ok := analyzePartials(x, sr, 0)
	if !ok {
		t.Fatalf("expected partial analysis with an estimated f0 to succeed")
	}
	cents := 1200.0 * math.Log2(ps.f0/f0)
	if math.Abs(cents) > 60.0 {
		t.Fatalf("expected the estimated f0 within 60.000000 cents of %f Hz, got %f Hz", f0, ps.f0)
	}
}

func TestAnalyzePartialsRejectsShortSignal(t *testing.T) {
	if _, ok := analyzePartials(randomSignal(512, 5), 48000, 220.0); ok {
		t.Fatalf("expected partial analysis to fail on a 512 sample signal")
	}
}

func TestPartialFreqRMSECentsDetectsDetuning(t *testing.T) {
	sr := 48000
	amps := []float64{1.0, 0.5, 0.25, 0.12}
	ref := makeHarmonicTone(sr, 220.0, 1.0, 1.5, amps)
	same := makeHarmonicTone(sr, 220.0, 1.0, 1.5, amps)
	sharp := makeHarmonicTone(sr, 226.0, 1.0, 1.5, amps)

	refPS, ok := analyzePartials(ref, sr, 220.0)
	if !ok {
		t.Fatalf("expected reference partial analysis to succeed")
	}
	samePS, _ := analyzePartials(same, sr, refPS.f0)
	sharpPS, _ := analyzePartials(sharp, sr, refPS.f0)

	matched := partialFreqRMSECents(refPS, samePS)
	detuned := partialFreqRMSECents(refPS, sharpPS)
	if matched >= detuned {
		t.Fatalf("expected the detuned tone to have the larger cents error, got %f vs %f", detuned, matched)
	}
	if matched > 5.0 {
		t.Fatalf("expected an identical tone to be within 5.000000 cents, got %f", matched)
	}
}

func TestTristimulusDistanceSeparatesBrightAndDark(t *testing.T) {
	sr := 48000
	dark := makeHarmonicTone(sr, 220.0, 1.0, 1.5, []float64{1.0, 0.1, 0.05, 0.02})
	bright := makeHarmonicTone(sr, 220.0, 1.0, 1.5, []float64{0.2, 0.8, 0.9, 0.7})

	darkPS, ok := analyzePartials(dark, sr, 220.0)
	if !ok {
		t.Fatalf("expected dark tone analysis to succeed")
	}
	brightPS, ok := analyzePartials(bright, sr, 220.0)
	if !ok {
		t.Fatalf("expected bright tone analysis to succeed")
	}

	if d := tristimulusDistance(darkPS, darkPS); d > 1e-12 {
		t.Fatalf("expected a self distance of 0.000000, got %f", d)
	}
	d := tristimulusDistance(darkPS, brightPS)
	if d < 0.2 {
		t.Fatalf("expected a clear tristimulus separation, got %f", d)
	}
}

func TestDecaySegmentRMSEDBPerSDetectsDecayMismatch(t *testing.T) {
	sr := 48000
	hop := 128.0 / float64(sr)
	ref := rmsEnvelope(makeDecaySine(sr, 220.0, 2.0, 1.0), 256, 128)
	same := rmsEnvelope(makeDecaySine(sr, 220.0, 2.0, 1.0), 256, 128)
	fast := rmsEnvelope(makeDecaySine(sr, 220.0, 2.0, 0.3), 256, 128)

	matched := decaySegmentRMSEDBPerS(ref, same, hop)
	mismatched := decaySegmentRMSEDBPerS(ref, fast, hop)
	if math.IsNaN(matched) || math.IsNaN(mismatched) {
		t.Fatalf("expected both segment comparisons to be defined, got %f and %f", matched, mismatched)
	}
	if matched > 1e-9 {
		t.Fatalf("expected 0.000000 for identical decays, got %f", matched)
	}
	if mismatched < 10.0 {
		t.Fatalf("expected a large segment slope error for a much faster decay, got %f", mismatched)
	}
}

func TestDecaySegmentRMSEDBPerSUndefinedForShortEnvelope(t *testing.T) {
	env := make([]float64, 10)
	for i := range env {
		env[i] = 0.1
	}
	if v := decaySegmentRMSEDBPerS(env, env, 128.0/48000.0); !math.IsNaN(v) {
		t.Fatalf("expected NaN for a 10 frame envelope, got %f", v)
	}
}

func TestCompareWithOptionsUsesMIDINoteForF0(t *testing.T) {
	sr := 48000
	amps := []float64{1.0, 0.5, 0.25}
	ref := makeHarmonicTone(sr, MIDIToHz(57), 1.5, 1.0, amps)
	cand := makeHarmonicTone(sr, MIDIToHz(57), 1.5, 1.0, amps)

	m := CompareWithOptions(ref, cand, sr, Options{MIDINote: 57})
	if math.Abs(m.F0Hz-220.0) > 5.0 {
		t.Fatalf("expected an f0 near 220.000000 Hz, got %f", m.F0Hz)
	}
	if m.PartialFreqRMSECents > 5.0 {
		t.Fatalf("expected an identical tone to match within 5.000000 cents, got %f", m.PartialFreqRMSECents)
	}
}

// TestCompareWithOptionsPinsExplicitF0 covers the difference between the two
// ways of supplying a fundamental: Options.F0Hz is a pin and is used exactly,
// while Options.MIDINote is only a starting point.
func TestCompareWithOptionsPinsExplicitF0(t *testing.T) {
	sr := 48000
	amps := []float64{1.0, 0.5, 0.25}
	ref := makeHarmonicTone(sr, 220.0, 1.5, 1.0, amps)
	cand := makeHarmonicTone(sr, 220.0, 1.5, 1.0, amps)

	// A deliberately off pin must survive untouched: nothing may re-estimate it.
	const pinned = 211.0
	m := CompareWithOptions(ref, cand, sr, Options{F0Hz: pinned})
	if m.F0Hz != pinned {
		t.Fatalf("expected the pinned f0 %.17g to be used verbatim, got %.17g", pinned, m.F0Hz)
	}
}

// TestRefineF0HintTracksActualTuning covers the MIDI-note path: the nominal
// pitch is a starting point, and the refined value must follow the instrument
// rather than equal temperament. Real pianos are stretched and detuned; the
// tracked C4 reference sits about 6 cents above 261.63 Hz.
func TestRefineF0HintTracksActualTuning(t *testing.T) {
	sr := 48000
	// One cent under six cents sharp of C4, i.e. inside the +/-50 cent window
	// but well away from the nominal pitch.
	trueF0 := MIDIToHz(60) * math.Pow(2, 5.7/1200.0)
	x := makeHarmonicTone(sr, trueF0, 1.5, 1.0, []float64{0.5, 1.0, 0.7, 0.3})

	ps, ok := analyzePartialsHint(x, sr, MIDIToHz(60))
	if !ok {
		t.Fatalf("expected hint-refined partial analysis to succeed")
	}
	cents := 1200.0 * math.Log2(ps.f0/trueF0)
	if math.Abs(cents) > 5.0 {
		t.Fatalf("expected the refined f0 within 5.000000 cents of %f Hz, got %f Hz (%f cents)", trueF0, ps.f0, cents)
	}

	// The nominal pitch must not have been used as-is.
	nominal := 1200.0 * math.Log2(ps.f0/MIDIToHz(60))
	if math.Abs(nominal) < 1.0 {
		t.Fatalf("expected the refined f0 to move off the nominal pitch, got %f cents", nominal)
	}

	// A hint that is hopelessly far out (a semitone) must not drag the analysis
	// across to a neighbouring note: it stays inside its window.
	far, ok := analyzePartialsHint(x, sr, MIDIToHz(59))
	if !ok {
		t.Fatalf("expected hint-refined partial analysis to succeed for a distant hint")
	}
	if off := math.Abs(1200.0 * math.Log2(far.f0/MIDIToHz(59))); off > f0HintHalfCents+1e-9 {
		t.Fatalf("expected the refinement to stay within %f cents of its hint, got %f", f0HintHalfCents, off)
	}
}

// TestEstimateF0OnSyntheticC4 pins the accuracy of the unaided estimator. A
// raw bin centre at the 4096-point analysis window is 11.7 Hz wide at 48 kHz,
// which is 77 cents at C4 - far too coarse to track partial ratios against.
// The estimate must land within 0.1% of the true fundamental.
func TestEstimateF0OnSyntheticC4(t *testing.T) {
	const (
		trueF0 = 262.48  // the tracked C4 reference, ~6 cents sharp of A440 C4
		bCoeff = 1.35e-4 // a representative C4 inharmonicity coefficient
		tolFra = 0.001   // 0.1%
	)
	for _, sr := range []int{44100, 48000} {
		x := makeInharmonicTone(sr, trueF0, bCoeff, 1.5, 1.0,
			[]float64{0.55, 1.0, 0.8, 0.45, 0.3, 0.2, 0.12, 0.08})

		ps, ok := analyzePartials(x, sr, 0)
		if !ok {
			t.Fatalf("sr=%d: expected partial analysis with an estimated f0 to succeed", sr)
		}
		rel := math.Abs(ps.f0-trueF0) / trueF0
		if rel > tolFra {
			t.Fatalf("sr=%d: expected the estimated f0 within %f%% of %f Hz, got %f Hz (%f%% off)",
				sr, tolFra*100, trueF0, ps.f0, rel*100)
		}

		// Guard the specific defect: the estimate must not be a raw bin centre.
		binHz := float64(sr) / float64(partialWindow)
		if math.Mod(ps.f0, binHz) == 0 {
			t.Fatalf("sr=%d: expected a sub-bin estimate, got the raw bin centre %f Hz", sr, ps.f0)
		}
	}
}

// makeInharmonicTone builds a tone whose partial k sits at
// k*f0*sqrt(1+B*k^2), the standard stiff-string model, so that partial
// tracking is exercised against something a piano actually produces.
func makeInharmonicTone(sr int, f0 float64, b float64, durationSec float64, decaySec float64, amps []float64) []float64 {
	n := int(float64(sr) * durationSec)
	if n < 1 {
		n = 1
	}
	out := make([]float64, n)
	for i := 0; i < n; i++ {
		t := float64(i) / float64(sr)
		env := math.Exp(-t / decaySec)
		var v float64
		for k, a := range amps {
			kk := float64(k + 1)
			f := f0 * kk * math.Sqrt(1+b*kk*kk)
			v += a * math.Sin(2*math.Pi*f*t)
		}
		out[i] = env * v
	}
	return out
}
