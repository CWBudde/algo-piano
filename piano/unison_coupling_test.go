package piano

import (
	"math"
	"testing"
)

// couplingProbeParams isolates the unison bridge coupling: one note, pedal held,
// no sympathetic loop and no string-to-string coupling, so the only feedback
// path in the render is the one under test.
func couplingProbeParams(crossfeed float32) *Params {
	params := NewDefaultParams()
	params.StringModel = StringModelDWG
	params.ResonanceEnabled = false
	params.CouplingEnabled = false
	params.CouplingMode = CouplingModeOff
	params.UnisonCrossfeed = crossfeed
	return params
}

// windowPeaks renders one note held under the pedal and returns the peak of each
// 5-second window.
func windowPeaks(t *testing.T, note int, crossfeed float32, seconds int) []float64 {
	t.Helper()

	p := NewPiano(48000, 128, couplingProbeParams(crossfeed))
	p.SetSustainPedal(true)
	p.NoteOn(note, 100)

	const window = 5
	peaks := make([]float64, 0, seconds/window)
	for range seconds / window {
		peak := 0.0
		for range 48000 * window / 128 {
			for _, v := range p.Process(128) {
				a := math.Abs(float64(v))
				if math.IsNaN(a) || math.IsInf(a, 0) {
					t.Fatalf("note %d, crossfeed %v: non-finite sample", note, crossfeed)
				}
				if a > peak {
					peak = a
				}
			}
		}
		peaks = append(peaks, peak)
	}
	return peaks
}

// decayRatio is the last window's peak over the 25-30 s window's peak. Below 1
// means the note decayed.
func decayRatio(t *testing.T, note int, crossfeed float32, seconds int) float64 {
	t.Helper()

	peaks := windowPeaks(t, note, crossfeed, seconds)
	if peaks[growthReferenceWindow] == 0 {
		t.Fatalf("note %d, crossfeed %v: reference window is silent, the ratio would be meaningless", note, crossfeed)
	}
	return peaks[len(peaks)-1] / peaks[growthReferenceWindow]
}

// TestUnisonCouplingRemovesEnergy is the property that makes the coupling term
// correct rather than merely bounded.
//
// The force on each string is c*g_i*(mix - y_i), which for weights summing to
// one adds c*(mix^2 - sum(g_i*y_i^2)) <= 0 of energy per sample. So on a
// multi-string note the coupling must make the note decay FASTER than the same
// note with no coupling at all - never slower. Before 2026-08-23 the force was
// c*mix with no subtraction, which is a positive feedback loop, and every
// multi-string note here failed this by orders of magnitude (note 60: 1.7159x
// against 0.0003x).
func TestUnisonCouplingRemovesEnergy(t *testing.T) {
	t.Parallel()

	// Two-string notes (MIDI 40-69) and a three-string one. Below 40 the group
	// is a single string and there is no coupling path - see
	// defaultUnisonForNote.
	for _, note := range []int{45, 52, 60, 72} {
		coupled := decayRatio(t, note, DefaultUnisonCrossfeed, growthRenderSeconds)
		free := decayRatio(t, note, 0, growthRenderSeconds)
		t.Logf("note %d: coupled %.6fx, uncoupled %.6fx", note, coupled, free)
		if coupled > free {
			t.Fatalf("note %d: the unison coupling ADDED energy - decay ratio %.6fx with coupling against %.6fx without; "+
				"the force must be proportional to (mix - y_i), not to mix",
				note, coupled, free)
		}
	}
}

// TestUnisonCouplingIsInertOnSingleStringNotes is the control that attributes
// any failure above to the coupling and to nothing else in the render loop.
//
// Notes below MIDI 40 have one string, processSample skips the coupling branch
// entirely, and the render must therefore be bit-identical at any crossfeed.
func TestUnisonCouplingIsInertOnSingleStringNotes(t *testing.T) {
	t.Parallel()

	for _, note := range []int{21, 33, 36} {
		reference := windowPeaks(t, note, 0, 30)
		for _, crossfeed := range []float32{DefaultUnisonCrossfeed, 0.0025, maxUnisonCrossfeed} {
			candidate := windowPeaks(t, note, crossfeed, 30)
			for i := range reference {
				if candidate[i] != reference[i] {
					t.Fatalf("note %d, crossfeed %v: window %d peak %v, want %v - a single-string group has no coupling path",
						note, crossfeed, i, candidate[i], reference[i])
				}
			}
		}
	}
}

// TestUnisonCouplingDecaysAcrossTheKnobRange sweeps the whole range
// cmd/piano-fit can select and the clamp above it.
//
// This is the test the old topology could not have passed at any setting: at
// strike position 0.92 and c = 0.005 - the value assets/presets/fitted-c4.json
// ships - the same render grew 88x even after the difference form was in place.
// Writing the force with InjectForceNext instead of at a strike position is what
// closed that; see the table on RingingStringGroup.processSample. The bound is
// deliberately a long way under 1: what is being asserted is that the note
// decays at every setting, not merely that it fails to explode.
func TestUnisonCouplingDecaysAcrossTheKnobRange(t *testing.T) {
	t.Parallel()

	// 0.005 is the maximum cmd/piano-fit/knobs.go gives the unison_crossfeed
	// knob and the largest value any preset in assets/presets carries.
	for _, crossfeed := range []float32{0, DefaultUnisonCrossfeed, 0.0025, 0.005, maxUnisonCrossfeed} {
		ratio := decayRatio(t, 60, crossfeed, growthRenderSeconds)
		t.Logf("note 60, crossfeed %.4f: ratio %.6fx", crossfeed, ratio)
		if ratio > 0.01 {
			t.Fatalf("note 60 at crossfeed %v decayed only to %.6fx of its 25-30 s peak over %d s",
				crossfeed, ratio, growthRenderSeconds)
		}
	}
}

// TestUnisonCrossfeedIsClamped guards the bound itself.
//
// Presets are hand-editable JSON, and the coupling is stable only well inside
// the range measured on maxUnisonCrossfeed - at c = 0.5 the same render goes
// non-finite. NewStringBank must therefore clamp rather than trust.
func TestUnisonCrossfeedIsClamped(t *testing.T) {
	t.Parallel()

	params := couplingProbeParams(10 * maxUnisonCrossfeed)
	sb := NewStringBank(48000, params)
	if sb.unisonCrossfeed != maxUnisonCrossfeed {
		t.Fatalf("bank took unison crossfeed %v from the preset, want it clamped to %v",
			sb.unisonCrossfeed, maxUnisonCrossfeed)
	}

	// And the clamped render is finite and decays, which is the point of the
	// clamp rather than the assignment above.
	if ratio := decayRatio(t, 60, 10*maxUnisonCrossfeed, growthRenderSeconds); ratio > 0.01 {
		t.Fatalf("an out-of-range preset crossfeed still left note 60 at %.6fx of its 25-30 s peak", ratio)
	}
}
