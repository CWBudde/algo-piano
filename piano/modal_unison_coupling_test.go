package piano

import (
	"math"
	"testing"
)

// modalCouplingProbeParams isolates the modal unison bridge coupling: one note,
// pedal held, no sympathetic loop and no string-to-string coupling, so the only
// feedback path in the render is the one under test. It is the modal twin of
// couplingProbeParams in unison_coupling_test.go.
func modalCouplingProbeParams(crossfeed float32) *Params {
	params := NewDefaultParams()
	params.StringModel = StringModelModal
	params.ResonanceEnabled = false
	params.CouplingEnabled = false
	params.CouplingMode = CouplingModeOff
	params.UnisonCrossfeed = crossfeed
	return params
}

// modalCouplingRenderSeconds is long enough for a struck note to decay into
// silence in every register the probes use, so the energy totals below cover a
// whole note rather than a slice of its attack.
const modalCouplingRenderSeconds = 8

// modalSecondEnergy renders one note struck under a held pedal through the modal
// core and returns the energy (sum of squares) of each one-second window.
//
// unforced is set through the bank field rather than through the preset so the
// stress probes can ask for a coupling strength past maxUnisonCrossfeed, which
// NewStringBank would otherwise clamp away.
func modalSecondEnergy(t *testing.T, note int, crossfeed float32, seconds int) []float64 {
	t.Helper()

	params := modalCouplingProbeParams(crossfeed)
	sb := NewStringBank(48000, params)
	sb.unisonCrossfeed = crossfeed
	h := NewHammerExciter(48000, params)
	sb.SetSustain(true)
	sb.SetKeyDown(note, true)
	h.Trigger(note, 100)

	out := make([]float64, 0, seconds)
	for range seconds {
		energy := 0.0
		for range 48000 / 128 {
			for _, v := range sb.Process(128, h) {
				a := float64(v)
				if math.IsNaN(a) || math.IsInf(a, 0) {
					t.Fatalf("note %d, crossfeed %v: non-finite sample", note, crossfeed)
				}
				energy += a * a
			}
		}
		out = append(out, energy)
	}
	return out
}

func totalEnergy(windows []float64) float64 {
	total := 0.0
	for _, e := range windows {
		total += e
	}
	return total
}

// modalMultiStringNotes are the notes the coupling can act on at all: two
// strings between MIDI 40 and 69, three from 70 up (defaultUnisonForNote).
var modalMultiStringNotes = []int{45, 52, 60, 72}

// TestModalUnisonCouplingDoesNotPumpTheBank is the load-bearing one: it fences
// the shape of the coupling term, not a level.
//
// The force on string si is proportional to sample - stringOut[si], so a string
// louder than the bridge is pushed back and the term cannot add energy
// unconditionally. Until 2026-08-23 the modal core added sample*c*0.08 into
// every string's first mode with no subtraction at all, which is a bare positive
// feedback loop around an already resonant bank. Whole-render energy against the
// same render at unison_crossfeed = 0, 8 s, pedal held:
//
//	                c = 0.0008   c = 0.005   c = 0.02 (the clamp)
//	note 45 before      1.1403      5.6102   non-finite
//	note 45 after       1.0000      0.9998      0.9995
//	note 52 before      1.1310      4.1267   non-finite
//	note 52 after       1.0000      1.0001      1.0002
//	note 60 before      1.1171      3.0163   non-finite
//	note 60 after       1.0001      1.0006      1.0016
//	note 72 before      1.0856      1.9726   non-finite
//	note 72 after       1.0004      1.0022      1.0073
//
// So the old form was in fact observable, and at the shipped default: it added
// 9-14% of the note's energy at c = 0.0008 and diverged at maxUnisonCrossfeed,
// a value a hand-edited preset may legally ask for.
//
// The bound is 1.02 rather than 1.0 because the corrected force lands on mode 0
// alone instead of being distributed over the string's modes, so it moves a
// little energy from the upper partials into the fundamental, which is the
// loudest and slowest-decaying mode. That is redistribution, not growth - the
// render decays either way, see TestModalUnisonCouplingDecaysAcrossTheKnobRange
// - and it is why applyCrossfeed claims a correct sign rather than a proof of
// passivity. The waveguide core can claim more because it injects into the delay
// line itself.
func TestModalUnisonCouplingDoesNotPumpTheBank(t *testing.T) {
	t.Parallel()

	const maxRatio = 1.02

	for _, note := range modalMultiStringNotes {
		free := totalEnergy(modalSecondEnergy(t, note, 0, modalCouplingRenderSeconds))
		if free == 0 {
			t.Fatalf("note %d: the uncoupled render is silent, the ratio would be meaningless", note)
		}
		for _, crossfeed := range []float32{DefaultUnisonCrossfeed, 0.005, maxUnisonCrossfeed} {
			coupled := totalEnergy(modalSecondEnergy(t, note, crossfeed, modalCouplingRenderSeconds))
			ratio := coupled / free
			t.Logf("note %d, crossfeed %v: energy %.5fx of the uncoupled render", note, crossfeed, ratio)
			if math.IsNaN(ratio) || ratio > maxRatio {
				t.Fatalf("note %d at crossfeed %v carries %.5fx the energy of the uncoupled render, want at most %.2fx - "+
					"the force must be proportional to (sample - stringOut[si]), not to sample",
					note, crossfeed, ratio, maxRatio)
			}
		}
	}
}

// TestModalUnisonCouplingStaysFiniteBeyondTheClamp is what the item in PLAN.md
// 14.1 was actually about: with the old form the bound was an accident of the
// 0.08 scale rather than a property of the term, and maxUnisonCrossfeed - the
// value NewStringBank clamps to, i.e. the largest a preset can reach - already
// sat past it.
//
// This probe writes the bank field directly to get 5x and 25x past that clamp.
// The old form went non-finite at every one of these on every multi-string note;
// the difference form stays finite and keeps decaying, so the clamp is now a
// policy about level rather than the only thing between the modal core and NaN.
func TestModalUnisonCouplingStaysFiniteBeyondTheClamp(t *testing.T) {
	t.Parallel()

	for _, note := range modalMultiStringNotes {
		for _, crossfeed := range []float32{5 * maxUnisonCrossfeed, 25 * maxUnisonCrossfeed} {
			// modalSecondEnergy fails the test on the first non-finite sample.
			windows := modalSecondEnergy(t, note, crossfeed, modalCouplingRenderSeconds)
			last, first := windows[len(windows)-1], windows[0]
			t.Logf("note %d, crossfeed %v: last window %.4e against first %.4e", note, crossfeed, last, first)
			if last >= first {
				t.Fatalf("note %d at crossfeed %v ended louder than it started: %.4e against %.4e",
					note, crossfeed, last, first)
			}
		}
	}
}

// TestModalUnisonCouplingIsInertOnSingleStringNotes is the control that
// attributes any failure above to the coupling and to nothing else in the modal
// render loop.
//
// Notes below MIDI 40 have one string, applyCrossfeed skips the branch entirely,
// and the render must therefore be bit-identical at any crossfeed.
func TestModalUnisonCouplingIsInertOnSingleStringNotes(t *testing.T) {
	t.Parallel()

	for _, note := range []int{21, 33, 36} {
		reference := modalSecondEnergy(t, note, 0, 4)
		for _, crossfeed := range []float32{DefaultUnisonCrossfeed, 0.005, maxUnisonCrossfeed} {
			candidate := modalSecondEnergy(t, note, crossfeed, 4)
			for i := range reference {
				if candidate[i] != reference[i] {
					t.Fatalf("note %d, crossfeed %v: window %d energy %v, want %v - a single-string group has no coupling path",
						note, crossfeed, i, candidate[i], reference[i])
				}
			}
		}
	}
}

// TestModalUnisonCouplingDecaysAcrossTheKnobRange sweeps the whole range
// cmd/piano-fit can select plus the clamp above it, and requires the note to
// decay at every setting rather than merely to stay finite.
func TestModalUnisonCouplingDecaysAcrossTheKnobRange(t *testing.T) {
	t.Parallel()

	// 0.005 is the maximum cmd/piano-fit/knobs.go gives the unison_crossfeed
	// knob and the largest value any preset in assets/presets carries.
	for _, crossfeed := range []float32{0, DefaultUnisonCrossfeed, 0.0025, 0.005, maxUnisonCrossfeed} {
		windows := modalSecondEnergy(t, 60, crossfeed, modalCouplingRenderSeconds)
		ratio := windows[len(windows)-1] / windows[0]
		t.Logf("note 60, crossfeed %.4f: last window %.3e of the first", crossfeed, ratio)
		if math.IsNaN(ratio) || ratio > 1e-6 {
			t.Fatalf("note 60 at crossfeed %v still held %.3e of its first-second energy after %d s",
				crossfeed, ratio, modalCouplingRenderSeconds)
		}
	}
}
