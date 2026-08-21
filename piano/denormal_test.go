package piano

import (
	"math"
	"testing"
)

// float32SmallestNormal is the smallest positive normal float32 (2^-126).
// Anything strictly between zero and this value is a denormal, which most x86
// FPUs handle in microcode at a large and variable cost.
const float32SmallestNormal = 1.1754944e-38

// modalStateDenormalFraction reports what share of a bank's live modal state
// (re and im of every mode the bank actually advances each sample) sits in the
// float32 denormal range. Only notes in the bank's active set are counted: a
// held sustain pedal marks every group "active" but the process loop touches
// only the excited ones, and including the untouched groups' exact zeros would
// dilute the metric into meaninglessness.
func modalStateDenormalFraction(sb *StringBank) (fraction float64, total int) {
	denormal := 0
	for _, note := range sb.activeNotes {
		g := sb.modalGroups[note]
		if g == nil {
			continue
		}
		for i := range g.re {
			for _, v := range [2]float32{g.re[i], g.im[i]} {
				total++
				a := float32(math.Abs(float64(v)))
				if a > 0 && a < float32SmallestNormal {
					denormal++
				}
			}
		}
	}
	if total == 0 {
		return 0, 0
	}
	return float64(denormal) / float64(total), total
}

// TestModalSustainedDecayDoesNotStallInDenormals is the regression guard for
// the denormal-storm fix recorded in BENCHMARKS.md ("Denormal flush").
//
// High modal partials decay below the smallest normal float32 long before the
// fundamental becomes inaudible. A sustained group never deactivates, so
// without ModalStringGroup.flushDenormalModes those modes stall in the denormal
// range indefinitely, and denormal arithmetic is microcoded.
//
// Re-measured on this exact scenario by deleting the flushDenormalModes call
// from endBlock: 25.56% of live mode state denormal after 500 blocks, 95.37%
// after 2000, and the test itself took 13.2 s instead of 0.5 s. With the flush
// in place both checkpoints measure exactly 0%. The 1% thresholds below sit far
// under the "before" numbers but above zero, so the test guards the behaviour
// rather than the specific flush threshold constant.
func TestModalSustainedDecayDoesNotStallInDenormals(t *testing.T) {
	params := NewDefaultParams()
	params.StringModel = StringModelModal
	params.ResonanceEnabled = false
	params.CouplingMode = CouplingModePhysical

	p := NewPiano(48000, 16, params)
	p.SetSustainPedal(true)
	p.NoteOn(60, 110)

	checkpoints := map[int]float64{
		500:  0.01, // measured 25.56% with the flush removed
		2000: 0.01, // measured 95.37% with the flush removed
	}

	for block := 1; block <= 2000; block++ {
		_ = p.Process(128)
		limit, ok := checkpoints[block]
		if !ok {
			continue
		}
		fraction, total := modalStateDenormalFraction(p.ringing.bank)
		if total == 0 {
			t.Fatalf("block %d: no live modal state to inspect", block)
		}
		t.Logf("block %d: %.2f%% of %d modal state values are denormal", block, fraction*100, total)
		if fraction > limit {
			t.Fatalf("block %d: %.2f%% of modal state is denormal, want <= %.2f%% "+
				"(denormal flush regressed; see BENCHMARKS.md)",
				block, fraction*100, limit*100)
		}
	}
}

// TestModalFlushThresholdStaysAboveDenormalRange pins the invariant that makes
// the flush work: the cutoff must sit above the denormal range, or modes reach
// denormal magnitudes before the block-rate check can catch them.
func TestModalFlushThresholdStaysAboveDenormalRange(t *testing.T) {
	if modalFlushThreshold <= float32SmallestNormal {
		t.Fatalf("modalFlushThreshold %g must exceed the smallest normal float32 %g",
			modalFlushThreshold, float32SmallestNormal)
	}
	// Slowest per-sample decay is 0.86 (see modalDecay's damped floor). Over one
	// 128-frame block a mode sitting just above the threshold must not fall into
	// the denormal range before the next check.
	worstBlockShrink := math.Pow(0.86, 128)
	if modalFlushThreshold*worstBlockShrink <= float32SmallestNormal {
		t.Fatalf("a mode at the flush threshold can reach denormal range within one block: %g",
			modalFlushThreshold*worstBlockShrink)
	}
}
