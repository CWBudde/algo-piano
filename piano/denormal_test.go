package piano

import (
	"fmt"
	"math"
	"testing"
)

// float32SmallestNormal is the smallest positive normal float32 (2^-126).
// Anything strictly between zero and this value is a denormal, which most x86
// FPUs handle in microcode at a large and variable cost.
const float32SmallestNormal = 1.1754944e-38

// modalStateDenormalFraction reports what share of a bank's live modal state
// (re and im of every mode the bank actually advances each sample) sits in the
// float32 denormal range. Only notes in the bank's active set are counted,
// because the active set is exactly what StringBank.Process advances each
// sample, and denormals only cost anything in state that is being advanced.
// Groups outside it sit at exact zero and are never touched, so counting them
// would dilute the metric into meaninglessness. Note that holding the sustain
// pedal does not by itself put a group in the active set — SetSustain forwards
// damper state only — so in the scenarios below the set contains just the
// struck note and whatever coupling has excited.
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
// Piano.Process accepts arbitrary block lengths and real callers use very
// different ones — 128 in the audio callback, 4800 and 48000 in the offline
// tests (piano/pedals_test.go) — so every supported size is exercised here.
// Note what this can and cannot prove: flushDenormalModes runs from endBlock,
// i.e. once per Process call, so these assertions describe the state a caller
// can observe between calls. What happens *inside* a single long call is a
// separate matter, documented in TestModalFlushThresholdStaysAboveDenormalRange.
func TestModalSustainedDecayDoesNotStallInDenormals(t *testing.T) {
	// Total audio rendered per case. 256000 frames at 48 kHz is 5.3 s, well past
	// the point where the unflushed run was almost entirely denormal.
	const totalFrames = 256000
	const limit = 0.01

	for _, blockSize := range []int{128, 4800, 48000} {
		t.Run(fmt.Sprintf("block%d", blockSize), func(t *testing.T) {
			params := NewDefaultParams()
			params.StringModel = StringModelModal
			params.ResonanceEnabled = false
			params.CouplingMode = CouplingModePhysical

			p := NewPiano(48000, 16, params)
			p.SetSustainPedal(true)
			p.NoteOn(60, 110)

			// Checkpoints in frames, snapped to this case's block boundaries, so
			// every block size is inspected at the same two points in the decay.
			checkpoints := map[int]bool{
				(500 * 128 / blockSize) * blockSize:  true,
				(2000 * 128 / blockSize) * blockSize: true,
			}

			rendered := 0
			for rendered < totalFrames {
				_ = p.Process(blockSize)
				rendered += blockSize
				if !checkpoints[rendered] && rendered < totalFrames {
					continue
				}
				fraction, total := modalStateDenormalFraction(p.ringing.bank)
				if total == 0 {
					t.Fatalf("frame %d: no live modal state to inspect", rendered)
				}
				t.Logf("frame %d: %.2f%% of %d modal state values are denormal",
					rendered, fraction*100, total)
				if fraction > limit {
					t.Fatalf("frame %d: %.2f%% of modal state is denormal, want <= %.2f%% "+
						"(denormal flush regressed; see BENCHMARKS.md)",
						rendered, fraction*100, limit*100)
				}
			}
		})
	}
}

// TestModalFlushThresholdStaysAboveDenormalRange pins the invariant that makes
// the flush work at the callback block size: the cutoff must sit above the
// denormal range, or modes reach denormal magnitudes before the block-rate check
// can catch them.
//
// This covers 128 frames only, which is what the audio callback uses. Longer
// calls are a different story — see TestModalFlushSurvivesLongProcessCalls.
func TestModalFlushThresholdStaysAboveDenormalRange(t *testing.T) {
	if modalFlushThreshold <= float32SmallestNormal {
		t.Fatalf("modalFlushThreshold %g must exceed the smallest normal float32 %g",
			modalFlushThreshold, float32SmallestNormal)
	}
	// Slowest per-sample decay is 0.86 (see modalDecay's damped floor). Over one
	// 128-frame block a mode sitting just above the threshold must not fall into
	// the denormal range before the next check.
	worstBlockShrink := math.Pow(0.86, callbackFrames)
	if modalFlushThreshold*worstBlockShrink <= float32SmallestNormal {
		t.Fatalf("a mode at the flush threshold can reach denormal range within one block: %g",
			modalFlushThreshold*worstBlockShrink)
	}
}

// callbackFrames is the block size the realtime audio callback uses, and the
// only one the block-rate denormal flush is actually sized for.
const callbackFrames = 128

// TestModalFlushSurvivesLongProcessCalls documents a real production gap. It is
// skipped: the defect is in the engine, not in the test, and this PR is
// test-only. Un-skip it as the regression test once the flush is fixed.
//
// ModalStringGroup.flushDenormalModes runs from endBlock, i.e. exactly once per
// StringBank.Process call, never inside the per-sample loop. Piano.Process
// accepts any block length and callers do use large ones (piano/pedals_test.go
// renders 4800 and 48000 frames in a single call), so the flush rate is set by
// the caller rather than by the engine. With the slowest per-sample decay of
// 0.86, a mode sitting just above modalFlushThreshold = 1e-25 falls below the
// smallest normal float32 after 197.4 samples. That is comfortably longer than a
// 128-frame callback, which is why the invariant above holds — but it means such
// a mode spends 95.9% of a 4800-frame call and 99.6% of a 48000-frame call in
// the denormal range, where the arithmetic is microcoded.
//
// Measured on 2026-08-21 (sustain held, note 60 at velocity 110, modal core,
// resonance off, physical coupling, ~256k frames rendered per case, four runs):
//
//	block   128 frames:  870-943 ns/frame
//	block  4800 frames:  813-845 ns/frame
//	block 48000 frames: 1712-1813 ns/frame
//
// So a 48000-frame call costs about 2x per frame what the callback-sized call
// does, reproducibly. 4800 is fine, which is the expected shape: the penalty
// only appears once a single call is long enough for a large population of modes
// to cross the threshold inside it. TestModalSustainedDecayDoesNotStallInDenormals
// cannot see any of this, because it can only inspect the state a caller sees
// *between* calls, and by then the end-of-call flush has already run.
//
// The fix is in the engine: run flushDenormalModes on a fixed sample stride
// instead of once per call, or bound the supported block length. The assertion
// below is the "fixed stride" version of the existing threshold invariant.
func TestModalFlushSurvivesLongProcessCalls(t *testing.T) {
	t.Skip("known defect: flushDenormalModes runs once per Process call, so calls much longer than 128 frames spend most of their samples in denormals (~2x per-frame cost at 48000 frames)")

	for _, frames := range []int{callbackFrames, 4800, 48000} {
		shrink := math.Pow(0.86, float64(frames))
		if modalFlushThreshold*shrink <= float32SmallestNormal {
			t.Errorf("block of %d frames: a mode at the flush threshold reaches %g, "+
				"inside the denormal range (< %g), before the next flush",
				frames, modalFlushThreshold*shrink, float32SmallestNormal)
		}
	}
}
