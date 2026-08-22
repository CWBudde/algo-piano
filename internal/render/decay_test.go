package render

import (
	"math"
	"testing"
)

// blockAt builds a constant-amplitude interleaved stereo block, so its RMS is
// exactly the amplitude and the test can reason in dB without a crest factor.
func blockAt(amp float64, frames int) []float32 {
	b := make([]float32, frames*2)
	for i := range b {
		b[i] = float32(amp)
	}
	return b
}

// runDecay feeds a geometric decay to a detector and reports the 0-based index
// of the block that stopped it, or -1 when it never stopped.
func runDecay(d *DecayDetector, peak float64, perBlockDB float64, blocks int) int {
	for i := 0; i < blocks; i++ {
		amp := peak * math.Pow(10.0, perBlockDB*float64(i)/20.0)
		if d.Update(blockAt(amp, 64)) {
			return i
		}
	}
	return -1
}

// The same decay shape at two very different absolute levels must stop at the
// same block in relative mode, and at different blocks in absolute mode. That
// contrast is the whole point of the change.
func TestDecayDetectorRelativeIsLevelInvariant(t *testing.T) {
	const perBlockDB = -1.7
	const blocks = 400

	loudRel := runDecay(NewDecayDetector(-60, 1, true), 0.8, perBlockDB, blocks)
	quietRel := runDecay(NewDecayDetector(-60, 1, true), 0.08, perBlockDB, blocks)

	// Block 0 sets the peak, block i is 1.7*i dB below it, so the stop block is
	// the same index for both levels: the first one more than 60 dB down.
	wantRel := firstBelow(1.0, perBlockDB, 1e-3)
	if loudRel != wantRel || quietRel != wantRel {
		t.Fatalf("relative mode stopped at blocks %d and %d, want %d for both", loudRel, quietRel, wantRel)
	}

	loudAbs := runDecay(NewDecayDetector(-60, 1, false), 0.8, perBlockDB, blocks)
	quietAbs := runDecay(NewDecayDetector(-60, 1, false), 0.08, perBlockDB, blocks)
	if loudAbs == quietAbs {
		t.Fatalf("absolute mode stopped at the same block %d for both levels; "+
			"the level dependence this change removes should still be visible with --decay-relative=false", loudAbs)
	}

	// -60 dBFS absolute is 0.001, and where that lands depends on the level the
	// decay started from.
	wantLoud := firstBelow(0.8, perBlockDB, 0.001)
	wantQuiet := firstBelow(0.08, perBlockDB, 0.001)
	if loudAbs != wantLoud || quietAbs != wantQuiet {
		t.Fatalf("absolute mode stopped at %d/%d, want %d/%d", loudAbs, quietAbs, wantLoud, wantQuiet)
	}
}

func firstBelow(peak, perBlockDB, threshold float64) int {
	for i := 0; i < 100000; i++ {
		if peak*math.Pow(10.0, perBlockDB*float64(i)/20.0) < threshold {
			return i
		}
	}
	return -1
}

func TestDecayDetectorHonoursHoldBlocks(t *testing.T) {
	base := firstBelow(1.0, -1.7, 1e-3)
	for _, hold := range []int{1, 3, 6} {
		d := NewDecayDetector(-60, hold, true)
		got := runDecay(d, 0.5, -1.7, 400)
		want := base + hold - 1
		if got != want {
			t.Fatalf("hold=%d stopped at block %d, want %d", hold, got, want)
		}
	}
}

// A hold requirement below 1 is meaningless; every call site used to clamp it
// locally, and the detector must keep doing so.
func TestDecayDetectorClampsHoldBlocks(t *testing.T) {
	d := NewDecayDetector(-60, 0, true)
	want := firstBelow(1.0, -1.7, 1e-3)
	if got := runDecay(d, 0.5, -1.7, 400); got != want {
		t.Fatalf("hold=0 stopped at block %d, want it clamped to hold=1 and stop at %d", got, want)
	}
}

// A decay that dips below the threshold and comes back must serve the full
// hold requirement again from scratch.
func TestDecayDetectorResetsRunOnRecovery(t *testing.T) {
	d := NewDecayDetector(-60, 3, true)

	d.Update(blockAt(1.0, 64)) // sets the peak
	for i := 0; i < 2; i++ {
		if d.Update(blockAt(1e-4, 64)) {
			t.Fatalf("stopped after %d below-threshold blocks, want 3", i+1)
		}
	}
	if d.Update(blockAt(0.5, 64)) {
		t.Fatal("a block back above the threshold must not stop the render")
	}
	// The run restarted, so two quiet blocks are no longer enough.
	for i := 0; i < 2; i++ {
		if d.Update(blockAt(1e-4, 64)) {
			t.Fatalf("stopped after %d below-threshold blocks post-recovery, want 3", i+1)
		}
	}
	if !d.Update(blockAt(1e-4, 64)) {
		t.Fatal("the third below-threshold block after recovery must stop the render")
	}
}

// Digital silence has no peak, so a naive `rms < peak * ratio` never fires and
// the render would run to its max duration. Silence is the most decayed a
// signal can be and must stop.
func TestDecayDetectorTerminatesOnSilentRender(t *testing.T) {
	d := NewDecayDetector(-90, 4, true)
	for i := 0; i < 3; i++ {
		if d.Update(blockAt(0, 64)) {
			t.Fatalf("silent render stopped after %d blocks, want the full hold of 4", i+1)
		}
	}
	if !d.Update(blockAt(0, 64)) {
		t.Fatal("a silent render must stop once the hold requirement is met")
	}
	if d.Peak() != 0 {
		t.Fatalf("silent render peak = %v, want 0", d.Peak())
	}
}

// While the running peak is still climbing the threshold climbs with it, so a
// rising attack can never look like a finished decay.
func TestDecayDetectorDoesNotFireDuringAttack(t *testing.T) {
	d := NewDecayDetector(-60, 1, true)
	for i := 1; i <= 50; i++ {
		amp := 1e-5 * math.Pow(1.5, float64(i)) // rising by ~3.5 dB per block
		if d.Update(blockAt(amp, 64)) {
			t.Fatalf("detector fired at rising block %d (amp %g)", i, amp)
		}
	}
}

// Track must feed the running peak without ever stopping the render, and must
// clear any below-threshold run so the hold is only ever served by blocks the
// caller considered eligible.
func TestDecayDetectorTrackFeedsPeakWithoutArming(t *testing.T) {
	d := NewDecayDetector(-60, 2, true)
	d.Track(blockAt(1.0, 64))
	if d.Peak() != 1.0 {
		t.Fatalf("Track did not record the peak: got %v, want 1", d.Peak())
	}
	d.Track(blockAt(1e-6, 64)) // below threshold, but must not count
	if d.Update(blockAt(1e-6, 64)) {
		t.Fatal("a Track'd quiet block must not count towards the hold requirement")
	}
	if !d.Update(blockAt(1e-6, 64)) {
		t.Fatal("two Update'd quiet blocks should satisfy hold=2")
	}
}

// In absolute mode the threshold is fixed and the peak is irrelevant, which is
// exactly the pre-change behaviour --decay-relative=false has to reproduce.
func TestDecayDetectorAbsoluteThresholdIsFixed(t *testing.T) {
	d := NewDecayDetector(-90, 1, false)
	if got, want := d.Threshold(), math.Pow(10, -90.0/20.0); math.Abs(got-want) > 1e-18 {
		t.Fatalf("absolute threshold = %g, want %g", got, want)
	}
	d.Update(blockAt(0.9, 64))
	if got, want := d.Threshold(), math.Pow(10, -90.0/20.0); math.Abs(got-want) > 1e-18 {
		t.Fatalf("absolute threshold moved with the peak: %g, want %g", got, want)
	}
}

// A non-finite sample must never become the running peak. NaN would poison
// every subsequent threshold comparison; +/-Inf would pin the relative
// threshold at infinity, so the very next finite block would compare as
// decayed and the render would be silently truncated.
func TestDecayDetectorIgnoresNonFiniteSamples(t *testing.T) {
	for _, tc := range []struct {
		name   string
		sample float64
	}{
		{"NaN", math.NaN()},
		{"PosInf", math.Inf(1)},
		{"NegInf", math.Inf(-1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := NewDecayDetector(-60, 1, true)
			block := blockAt(0.5, 8)
			block[3] = float32(tc.sample)
			d.Update(block)
			if d.Peak() != 0.5 {
				t.Fatalf("peak = %v, want 0.5 with the %s ignored", d.Peak(), tc.name)
			}
			if math.IsInf(d.Threshold(), 0) || math.IsNaN(d.Threshold()) {
				t.Fatalf("threshold went non-finite: %v", d.Threshold())
			}
			// The threshold stayed finite, so the render carries on exactly as
			// it would have without the poisoned sample.
			if d.Update(blockAt(0.5, 8)) {
				t.Fatal("a full-level block must not stop the render")
			}
			if !d.Update(blockAt(1e-9, 8)) {
				t.Fatal("a decayed block must still stop the render")
			}
		})
	}
}

// A render that has diverged must not be reported as decayed: an infinite or
// NaN block RMS never satisfies the stop condition, so the render runs out to
// its max duration instead of producing a short, plausible-looking file.
func TestDecayDetectorNeverStopsOnDivergedBlock(t *testing.T) {
	for _, tc := range []struct {
		name   string
		sample float64
	}{
		{"NaN", math.NaN()},
		{"PosInf", math.Inf(1)},
		{"NegInf", math.Inf(-1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, relative := range []bool{true, false} {
				// A relative detector whose peak is still zero would otherwise
				// take the silent-render shortcut on an all-non-finite block.
				d := NewDecayDetector(-60, 1, relative)
				block := blockAt(tc.sample, 8)
				for i := 0; i < 4; i++ {
					if d.Update(block) {
						t.Fatalf("relative=%v: diverged block reported as decayed", relative)
					}
				}
			}
		})
	}
}
