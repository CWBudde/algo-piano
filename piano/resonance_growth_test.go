package piano

import (
	"math"
	"testing"
)

// A struck chord held under the sustain pedal must decay. Nothing adds energy
// after the attack: the keys are struck once, coupling is off, and the only
// thing still running is the string bank and, in the second test, the
// sympathetic loop. This file asserts that both cases decay, and pins how far.
//
// Until 2026-08-23 neither of them did, and this file recorded that as a fence
// on a known-bad number rather than as a decay assertion:
//
//	                                     before    after
//	resonance off                        1.2122x   0.1275x
//	resonance on, gain 0.00025           5.0558x   0.1338x
//
// The cause was the unison bridge coupling, which fed each string's own output
// back into itself with no subtraction and no loss - see the measurements on
// RingingStringGroup.processSample. Multi-string notes grew; single-string notes
// were bit-identical and always decayed, which is what localised it. The
// sympathetic loop never was the root cause; it was compounding a plant that was
// already above unity, and with the plant fixed it now costs 0.006 in this
// ratio rather than 4x.
const (
	// growthRenderSeconds is long enough for the ratio to clear the attack
	// transient and the first beats between the undamped strings. It is
	// deliberately far past the 45 s of TestDWGResonanceLongRenderDecays, whose
	// 1.5x wobble allowance is what let the old defect through unseen.
	growthRenderSeconds = 120
	growthWindowSeconds = 5
	// growthReferenceWindow is the 25-30 s window: past the attack, past the
	// first beats, and the point everything later is measured against.
	growthReferenceWindow = 5
)

// growthNotes is a chord spread across the register, struck once and then left
// under the pedal. It deliberately mixes single-string notes (below MIDI 40) and
// two-string ones, since only the latter have a coupling path at all. Coupling
// is off in these renders so that string-to-string coupling, which is
// block-averaged and a feedback path of its own, cannot be confused with what is
// being measured.
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

// maxBankDecayWithoutResonance is the recorded ratio plus the 8% headroom this
// repo fences with. Measured 2026-08-23: 0.1275.
const maxBankDecayWithoutResonance = 0.138

// TestDWGSustainedBankDecaysWithoutResonance is the plant on its own: six notes
// struck once, pedal held for two minutes, no sympathetic engine constructed at
// all (ResonanceEnabled is false, so Piano never builds one) and no coupling.
//
// The bound is far below 1, so this is a decay assertion and not merely a
// no-divergence one. Tighten it whenever the number genuinely improves.
func TestDWGSustainedBankDecaysWithoutResonance(t *testing.T) {
	// Both growth renders are 120 s of DWG audio and touch no package state, so
	// they are run concurrently; serially they would add a minute to the suite.
	t.Parallel()

	reference, last := measureSustainedGrowth(t, 0, growthRenderSeconds)
	ratio := last / reference
	t.Logf("dwg, pedal held, resonance off: %d-%d s peak %.6g, %d-%d s peak %.6g, ratio %.4fx",
		growthReferenceWindow*growthWindowSeconds, (growthReferenceWindow+1)*growthWindowSeconds, reference,
		growthRenderSeconds-growthWindowSeconds, growthRenderSeconds, last, ratio)
	if reference == 0 {
		t.Fatalf("reference window is silent")
	}
	if ratio > maxBankDecayWithoutResonance {
		t.Fatalf("the DWG bank decays less than it did with the pedal held: %.4fx over %d s exceeds the recorded %.3fx",
			ratio, growthRenderSeconds, maxBankDecayWithoutResonance)
	}
}

// maxDWGResonanceSustainedDecay is the same, with the sympathetic loop on.
// Measured 2026-08-23: 0.1338.
const maxDWGResonanceSustainedDecay = 0.145

// shippedResonanceGain is the resonance_gain every preset under assets/presets
// carries. The Params default is lower (DefaultResonanceGain, 0.00018), so
// fencing the default would fence a configuration nothing actually ships.
const shippedResonanceGain = 0.00025

// TestDWGResonanceSustainedDecayIsFenced pins what the sympathetic loop costs on
// top of the decay above: 0.1275 -> 0.1338, i.e. the loop slows the decay by
// about 5% of the ratio and the render still ends 17 dB below its own reference
// window. That is what an audible sympathetic path on a stable plant should look
// like.
//
// Before the unison coupling was made dissipative this same comparison read
// 1.2122 against 5.0558, and the difference was read - wrongly - as the
// resonance loop being unstable. It was not: no gain avoided it, because the
// plant it was driving was itself above unity. Lowering resonance_gain was
// measured then and did not help (over 300 s against a 5.41x baseline, 1e-6 gave
// 5.50x and 5e-5 gave 12.7x), which was already the evidence that the loop was
// not the source.
//
// The modal core is unaffected either way: the same render is at digital silence
// well before the reference window.
//
// Note that the open-loop probes in modal_resonance_test.go could not have
// caught the old defect and still cannot catch its kind. They inject a sine at
// ONE note's fundamental and measure the resonance path against a plant they
// assume is stable, so a bank that is hot on its own is invisible to them. A
// passing bound there is necessary, never sufficient - this test is the
// sufficient one.
func TestDWGResonanceSustainedDecayIsFenced(t *testing.T) {
	t.Parallel()

	reference, last := measureSustainedGrowth(t, shippedResonanceGain, growthRenderSeconds)
	ratio := last / reference
	t.Logf("dwg, pedal held, resonance_gain %.5f: %d-%d s peak %.6g, %d-%d s peak %.6g, ratio %.4fx",
		shippedResonanceGain,
		growthReferenceWindow*growthWindowSeconds, (growthReferenceWindow+1)*growthWindowSeconds, reference,
		growthRenderSeconds-growthWindowSeconds, growthRenderSeconds, last, ratio)
	if reference == 0 {
		t.Fatalf("reference window is silent")
	}
	if ratio > maxDWGResonanceSustainedDecay {
		t.Fatalf("the DWG sympathetic loop got hotter: %.4fx over %d s exceeds the recorded %.3fx",
			ratio, growthRenderSeconds, maxDWGResonanceSustainedDecay)
	}
}
