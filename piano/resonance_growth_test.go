package piano

import (
	"math"
	"testing"
)

// The DWG core grows slowly with the sustain pedal held even when the
// sympathetic loop is switched off entirely, and the resonance loop multiplies
// that growth. This file pins BOTH numbers so neither can drift unnoticed.
//
// Neither bound is a stability claim. They are regression fences on measured
// values, in the sense assets/thresholds/c4.json uses the word: a number the
// suite must not get worse than, not a number anyone is happy with. See
// TestDWGSustainedBankGrowsWithoutResonance for what is actually wrong here.
const (
	// growthRenderSeconds is long enough for the growth to clear the attack
	// transient and become unambiguous. It is deliberately far past the 45 s of
	// TestDWGResonanceLongRenderDecays: at 45 s the resonance-off growth is
	// still inside that test's 1.5x wobble allowance, which is exactly why that
	// test does not see this.
	growthRenderSeconds = 120
	growthWindowSeconds = 5
	// growthReferenceWindow is the 25-30 s window: past the attack, past the
	// first beats between the undamped strings, and before any of the growth
	// below has taken hold.
	growthReferenceWindow = 5
)

// growthNotes is a chord spread across the register, struck once and then left
// under the pedal. Coupling is off in these renders so that string-to-string
// coupling, which is block-averaged and a feedback path of its own, cannot be
// confused with the sympathetic loop.
var growthNotes = []int{33, 36, 45, 52, 60, 72}

// measureSustainedGrowth renders the DWG core through the public Piano.Process
// with the pedal held and reports the peak of the reference window and of the
// last window.
func measureSustainedGrowth(t *testing.T, gain float32, seconds int) (reference, last float64) {
	t.Helper()

	params := NewDefaultParams()
	params.StringModel = StringModelDWG
	params.ResonanceEnabled = gain > 0
	params.ResonancePerNoteFilter = true
	if gain > 0 {
		params.ResonanceGain = gain
	}
	params.CouplingEnabled = false
	params.CouplingMode = CouplingModeOff

	p := NewPiano(48000, 128, params)
	p.SetSustainPedal(true)
	for _, note := range growthNotes {
		p.NoteOn(note, 100)
	}

	blocksPerWindow := 48000 * growthWindowSeconds / 128
	peaks := make([]float64, 0, seconds/growthWindowSeconds)
	for range seconds / growthWindowSeconds {
		peak := 0.0
		for range blocksPerWindow {
			for _, v := range p.Process(128) {
				a := math.Abs(float64(v))
				if math.IsNaN(a) || math.IsInf(a, 0) {
					t.Fatalf("non-finite sample at gain %v", gain)
				}
				if a > peak {
					peak = a
				}
			}
		}
		peaks = append(peaks, peak)
	}
	if len(peaks) <= growthReferenceWindow {
		t.Fatalf("render produced %d windows, want more than %d", len(peaks), growthReferenceWindow)
	}
	return peaks[growthReferenceWindow], peaks[len(peaks)-1]
}

// maxBankGrowthWithoutResonance fences the DWG core's OWN growth with the pedal
// held and no sympathetic path at all. Measured 2026-08-22: 1.2122.
const maxBankGrowthWithoutResonance = 1.35

// TestDWGSustainedBankGrowsWithoutResonance records a defect rather than
// asserting correctness.
//
// Six notes are struck once and the pedal is held for two minutes. Nothing adds
// energy after the attack: coupling is off, the sympathetic engine is not built
// at all (ResonanceEnabled is false, so Piano never constructs one), and no key
// is pressed again. The output should decay. It does not - the 115-120 s window
// peaks 1.21x higher than the 25-30 s window, and over 300 s the same render
// reaches 5.41x. The growth is geometric, so this is a loop above unity
// somewhere in the DWG bank itself, not a slow transient.
//
// It is PRE-EXISTING and has nothing to do with the sympathetic loop: the
// numbers here are bit-identical on the code before and after resonance
// injection was interleaved with rendering (2026-08-22), which is expected,
// because with no engine attached that change cannot execute a single
// instruction.
//
// The fence is set just above the measured value so a WORSENING fails. It must
// not be read as a claim that 1.21x is acceptable. See PLAN.md 12.4.
func TestDWGSustainedBankGrowsWithoutResonance(t *testing.T) {
	// Both growth renders are 120 s of DWG audio and touch no package state, so
	// they are run concurrently; serially they would add a minute to the suite.
	t.Parallel()

	reference, last := measureSustainedGrowth(t, 0, growthRenderSeconds)
	growth := last / reference
	t.Logf("dwg, pedal held, resonance off: %d-%d s peak %.6g, %d-%d s peak %.6g, growth %.4fx",
		growthReferenceWindow*growthWindowSeconds, (growthReferenceWindow+1)*growthWindowSeconds, reference,
		growthRenderSeconds-growthWindowSeconds, growthRenderSeconds, last, growth)
	if reference == 0 {
		t.Fatalf("reference window is silent")
	}
	if growth > maxBankGrowthWithoutResonance {
		t.Fatalf("the DWG bank's own growth with the pedal held got worse: %.4fx over %d s exceeds the recorded %.2fx",
			growth, growthRenderSeconds, maxBankGrowthWithoutResonance)
	}
}

// maxDWGResonanceSustainedGrowth fences the same render with the sympathetic
// loop on at the gain every shipped preset pins. Measured 2026-08-22: 5.0558.
const maxDWGResonanceSustainedGrowth = 5.6

// shippedResonanceGain is the resonance_gain every preset under assets/presets
// carries. The Params default is lower (DefaultResonanceGain, 0.00018), so
// fencing the default would fence a configuration nothing actually ships.
const shippedResonanceGain = 0.00025

// TestDWGResonanceSustainedGrowthIsFenced pins how much the sympathetic loop
// multiplies the growth above.
//
// THIS IS A FENCE ON A KNOWN-BAD NUMBER. 5.06x over two minutes is not stable
// and is not being called stable. What the fence buys is that the number cannot
// get worse without the suite saying so.
//
// The history, measured on the same render, same notes, same gain:
//
//	                                 resonance off  resonance on (0.00025)
//	block-deposit loop (before)      1.2122         1.1432
//	interleaved loop (after)         1.2122         5.0558
//
// The resonance-off column is bit-identical, so the whole move is the loop. The
// block-deposit loop scored BELOW the resonance-off baseline - it was damping
// the bank, not driving it - because depositing a whole block into frozen state
// delivers |sum x[i]|, the block's DC content, rather than the drive at each
// partial. It fed the strings almost no energy at their own frequencies, and
// what it did feed them arrived with the wrong phase. Interleaving makes the
// loop do what it was always meant to do, and the honest consequence is that a
// bank which was already growing on its own now has real positive feedback on
// top of it.
//
// Reducing the gain does not rescue this. Measured over 300 s against a 5.41x
// resonance-off baseline: 1e-6 gives 5.50x, 1e-5 gives 6.41x, 5e-5 gives 12.7x.
// Even a gain 250x under the shipped one is already above the baseline, so
// there is no gain at which an audible corrected loop sits inside it. The
// resonance loop is not the root cause; the bank's own growth is, and that is
// what has to be fixed. Both are PLAN.md 12.4 follow-ups.
//
// The modal core is unaffected: the same render decays to digital silence well
// before the reference window, with or without resonance.
//
// Note that the open-loop probes in modal_resonance_test.go do NOT see this.
// They report 0.174 (default) and 0.241 (modal-calibrated) against a bound of
// 0.5. They inject a sine at ONE note's fundamental, so a loop that is hot at a
// frequency the probe does not drive is invisible to them, and they measure the
// resonance path against a plant that is assumed stable. Here it is not.
func TestDWGResonanceSustainedGrowthIsFenced(t *testing.T) {
	t.Parallel()

	reference, last := measureSustainedGrowth(t, shippedResonanceGain, growthRenderSeconds)
	growth := last / reference
	t.Logf("dwg, pedal held, resonance_gain %.5f: %d-%d s peak %.6g, %d-%d s peak %.6g, growth %.4fx",
		shippedResonanceGain,
		growthReferenceWindow*growthWindowSeconds, (growthReferenceWindow+1)*growthWindowSeconds, reference,
		growthRenderSeconds-growthWindowSeconds, growthRenderSeconds, last, growth)
	if reference == 0 {
		t.Fatalf("reference window is silent")
	}
	if growth > maxDWGResonanceSustainedGrowth {
		t.Fatalf("the DWG sympathetic loop got hotter: %.4fx over %d s exceeds the recorded %.2fx",
			growth, growthRenderSeconds, maxDWGResonanceSustainedGrowth)
	}
}
