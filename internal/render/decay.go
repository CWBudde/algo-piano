// Package render holds the pieces of the offline render loop that must behave
// identically in every command that renders a note. Today that is the
// auto-stop decay detector, which used to be copy-pasted into four separate
// `package main`s and therefore had four opportunities to drift.
package render

import "math"

// DecayDetector decides when an offline render has decayed far enough to stop.
//
// It is fed one interleaved stereo block at a time and answers a single
// question: has the block RMS stayed below the stop threshold for
// `holdBlocks` consecutive blocks?
//
// Two threshold modes exist:
//
//   - RELATIVE (the default everywhere that scores a render). The threshold is
//     `decayDBFS` below the render's own running peak sample magnitude. This is
//     what makes the render length independent of the render's absolute level,
//     and therefore what makes `output_gain` genuinely score-invariant: scaling
//     every sample by k scales the peak by k, scales the threshold by k, and
//     leaves the stop block index unchanged. See
//     cmd/piano-fit/gain_invariance_test.go, which asserts exactly that on the
//     scoring path.
//
//   - ABSOLUTE (`--decay-relative=false`). The threshold is `decayDBFS` below
//     full scale, which is the pre-2026-08-22 behaviour. It is kept so any
//     number measured before the change can be reproduced on demand.
//
// Two edge cases deserve their reasoning written down.
//
// THE FIRST BLOCKS, where the running peak is still climbing. The peak is
// updated from the block BEFORE that block is tested, so the threshold a block
// is judged against always already includes that block's own contribution.
// Because the running peak is monotone non-decreasing, the threshold is too:
// it can only ever rise, never fall back and re-arm a stop that had been
// ruled out. During the attack the peak grows at least as fast as the block
// RMS, so a rising block cannot look like a decayed one, and the detector
// cannot fire early on a note that has not finished attacking. For a struck
// piano note the peak settles within the first few milliseconds of the attack
// and is constant for the rest of the render, so no second pass over the
// signal is needed to know it.
//
// A GENUINELY SILENT RENDER, where the running peak stays at zero. The
// relative threshold is then zero as well, and `rms < 0` is never true, so a
// naive implementation would run every silent render out to its max duration.
// Digital silence is the most decayed a signal can be, so a zero peak counts
// as below threshold and the detector fires after `holdBlocks` blocks.
type DecayDetector struct {
	// ratio is 10^(decayDBFS/20): the linear threshold in absolute mode, and
	// the multiplier applied to the running peak in relative mode.
	ratio      float64
	relative   bool
	holdBlocks int

	peak  float64
	below int
}

// NewDecayDetector builds a detector stopping after holdBlocks consecutive
// blocks whose RMS is decayDBFS below full scale (relative == false) or below
// the render's own running peak (relative == true). A holdBlocks below 1 is
// clamped to 1, matching every call site's own clamping.
func NewDecayDetector(decayDBFS float64, holdBlocks int, relative bool) *DecayDetector {
	if holdBlocks < 1 {
		holdBlocks = 1
	}
	return &DecayDetector{
		ratio:      math.Pow(10.0, decayDBFS/20.0),
		relative:   relative,
		holdBlocks: holdBlocks,
	}
}

// Track feeds a block to the running peak without arming the stop. Call sites
// use it for blocks rendered before their `--min-duration` floor: the peak of
// the attack must be observed even though no stop may be taken yet, and the
// below-threshold run is reset so the hold requirement is always satisfied by
// blocks that were actually eligible to stop the render.
func (d *DecayDetector) Track(block []float32) {
	d.trackPeak(block)
	d.below = 0
}

// Update feeds a block to the detector and reports whether the render should
// stop after it. A block whose RMS is at or above the threshold resets the
// below-threshold run, so a decay that dips and recovers must serve the full
// hold requirement again.
func (d *DecayDetector) Update(block []float32) bool {
	d.trackPeak(block)

	if !d.isBelow(block) {
		d.below = 0
		return false
	}
	d.below++
	return d.below >= d.holdBlocks
}

// Peak reports the largest sample magnitude seen so far.
func (d *DecayDetector) Peak() float64 { return d.peak }

// Threshold reports the linear block-RMS threshold currently in force.
func (d *DecayDetector) Threshold() float64 {
	if !d.relative {
		return d.ratio
	}
	return d.peak * d.ratio
}

func (d *DecayDetector) isBelow(block []float32) bool {
	rms := BlockRMS(block)
	// A diverged block (an infinite or NaN RMS) is the opposite of decayed, so
	// it must never satisfy the stop condition -- not even through the silent
	// render shortcut below, which an all-NaN block would otherwise take while
	// the running peak is still zero. See trackPeak.
	if math.IsInf(rms, 0) || math.IsNaN(rms) {
		return false
	}
	// A silent render never rises above a zero relative threshold; treat it as
	// fully decayed rather than letting it run to the max duration.
	if d.relative && d.peak <= 0 {
		return true
	}
	return rms < d.Threshold()
}

func (d *DecayDetector) trackPeak(block []float32) {
	for _, s := range block {
		v := math.Abs(float64(s))
		// Non-finite samples are excluded from the running peak. NaN would be
		// rejected by the comparison anyway, but +/-Inf would not: it would
		// pin the peak at infinity, make the relative threshold infinite, and
		// every later finite block would compare as decayed, so the render
		// would stop one hold window later and be silently truncated.
		//
		// A render that has produced an infinity has diverged, and the right
		// answer for a diverged render is NOT a short, plausible-looking file.
		// Leaving the peak finite means the infinite (or NaN) block RMS never
		// compares below the threshold, so the detector refuses to stop and
		// the render runs to its max duration, where the damage is visible to
		// whoever inspects the output. See PLAN.md Phase 9.6 for why silently
		// swallowing divergence is the failure mode to avoid here.
		if math.IsInf(v, 0) || math.IsNaN(v) {
			continue
		}
		if v > d.peak {
			d.peak = v
		}
	}
}

// BlockRMS is the RMS of an interleaved stereo block, taken over all samples
// of both channels. It is the quantity the detector compares against its
// threshold.
func BlockRMS(interleaved []float32) float64 {
	if len(interleaved) == 0 {
		return 0
	}
	var sum float64
	for _, s := range interleaved {
		v := float64(s)
		sum += v * v
	}
	return math.Sqrt(sum / float64(len(interleaved)))
}
