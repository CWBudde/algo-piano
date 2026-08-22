package main

import (
	"math"
	"testing"

	"github.com/cwbudde/algo-piano/analysis"
	"github.com/cwbudde/algo-piano/piano"
)

// gainInvarianceSettings renders long enough for the auto-stop to actually
// fire. That matters: the older TestOutputGainIsScoreInvariant renders 0.3 s
// with a 0.3 s max duration, so both of its renders are cut off by the max
// duration and the auto-stop never runs. It therefore could not see the defect
// this file exists for.
func gainInvarianceSettings() (sampleRate int, minDuration, maxDuration, releaseAfter float64, decayDBFS float64, hold, blockSize int) {
	return 8000, 0.05, 6.0, 0.1, -70, 6, 64
}

func renderAtGain(t *testing.T, gain float32, relative bool) ([]float64, int) {
	t.Helper()
	sr, minD, maxD, rel, dbfs, hold, blk := gainInvarianceSettings()
	params := piano.NewDefaultParams()
	params.OutputGain = gain
	mono, _, err := renderCandidateFromParams(
		params, 60, 100, sr,
		dbfs, hold, relative,
		minD, maxD,
		blk, rel,
	)
	if err != nil {
		t.Fatalf("render at gain %v (relative=%v): %v", gain, relative, err)
	}
	return mono, len(mono)
}

// TestOutputGainDoesNotMoveTheScoreThroughRenderLength is the load-bearing
// test behind `piano-fit --match-output-gain` and behind the claim in
// docs/optimization-workflow.md that output_gain is score-invariant.
//
// analysis.Compare RMS-normalises both signals, so a pure level change cannot
// move the score through the analysis stage. It used to move it anyway,
// through the render LENGTH: the auto-stop compared the block RMS against an
// absolute -90 dBFS, so a louder render crossed that threshold later, produced
// a longer candidate and was scored over a different window. Measured on a
// fitted C4 preset before this test existed, same knobs otherwise:
// output_gain 7.096 scored 0.5208 and output_gain 1.357 scored 0.5061.
//
// With the stop threshold taken relative to the render's own running peak the
// two renders have identical length and the claim finally holds.
func TestOutputGainDoesNotMoveTheScoreThroughRenderLength(t *testing.T) {
	sr, _, _, _, _, _, _ := gainInvarianceSettings()
	ref := syntheticReference(t, 60, sr, 1.0)

	quiet, quietLen := renderAtGain(t, 0.5, true)
	loud, loudLen := renderAtGain(t, 5.0, true)

	// Guard the premise: if the auto-stop never fires, both renders are simply
	// truncated at max-duration and the test proves nothing.
	_, _, maxD, _, _, _, _ := gainInvarianceSettings()
	if quietLen >= int(float64(sr)*maxD) {
		t.Fatalf("the auto-stop never fired (%d samples == max duration); this test would be vacuous", quietLen)
	}

	if quietLen != loudLen {
		t.Fatalf("a 10x output_gain change moved the render length: %d vs %d samples (%.3f s vs %.3f s)",
			quietLen, loudLen, float64(quietLen)/float64(sr), float64(loudLen)/float64(sr))
	}

	a := analysis.Compare(ref, quiet, sr)
	b := analysis.Compare(ref, loud, sr)
	if delta := math.Abs(a.Score - b.Score); delta > 1e-7 {
		t.Fatalf("output_gain moved the score by %g: %.17g at gain 0.5 vs %.17g at gain 5.0", delta, a.Score, b.Score)
	}
	if delta := math.Abs(a.Similarity - b.Similarity); delta > 1e-7 {
		t.Fatalf("output_gain moved the similarity by %g: %v vs %v", delta, a.Similarity, b.Similarity)
	}
}

// The absolute threshold is exactly what makes the score gain-dependent. This
// test pins that down from the other side: with --decay-relative=false the two
// renders must still differ in length, so the escape hatch really does restore
// the old behaviour rather than quietly doing the new thing.
func TestAbsoluteDecayThresholdStillDependsOnOutputGain(t *testing.T) {
	_, quietLen := renderAtGain(t, 0.5, false)
	_, loudLen := renderAtGain(t, 5.0, false)
	if quietLen == loudLen {
		t.Fatalf("--decay-relative=false produced equal render lengths (%d); "+
			"the absolute threshold is supposed to be level-dependent", quietLen)
	}
	if loudLen <= quietLen {
		t.Fatalf("the louder render should cross an absolute threshold later: %d samples vs %d", loudLen, quietLen)
	}
}

// TestAbsoluteDecayThresholdReproducesPreChangeLength renders through the
// current code path with --decay-relative=false and against a local copy of
// the pre-change loop, and requires the two to agree to the sample. That is
// what makes any number measured before 2026-08-22 reproducible on demand.
func TestAbsoluteDecayThresholdReproducesPreChangeLength(t *testing.T) {
	sr, minD, maxD, rel, dbfs, hold, blk := gainInvarianceSettings()

	for _, gain := range []float32{0.5, 1.0, 5.0} {
		_, got := renderAtGain(t, gain, false)

		params := piano.NewDefaultParams()
		params.OutputGain = gain
		want := len(legacyRenderPreRelativeStop(t, params, 60, 100, sr, dbfs, hold, minD, maxD, blk, rel))

		if got != want {
			t.Fatalf("gain %v: --decay-relative=false rendered %d samples, pre-change loop rendered %d", gain, got, want)
		}
	}
}

// legacyRenderPreRelativeStop is the auto-stop loop exactly as it stood in
// cmd/piano-fit/optimize.go before the shared detector landed, kept here so
// the escape hatch is checked against the real old code rather than against a
// restatement of the new one.
func legacyRenderPreRelativeStop(
	t *testing.T,
	params *piano.Params,
	note, velocity, sampleRate int,
	decayDBFS float64,
	decayHoldBlocks int,
	minDuration, maxDuration float64,
	blockSize int,
	releaseAfter float64,
) []float32 {
	t.Helper()
	p := piano.NewPiano(sampleRate, 16, params)
	p.NoteOn(note, velocity)

	if decayHoldBlocks < 1 {
		decayHoldBlocks = 1
	}
	minFrames := int(float64(sampleRate) * minDuration)
	maxFrames := int(float64(sampleRate) * maxDuration)
	releaseAtFrame := int(float64(sampleRate) * releaseAfter)

	threshold := math.Pow(10.0, decayDBFS/20.0)
	if blockSize < 16 {
		blockSize = 16
	}
	framesRendered := 0
	belowCount := 0
	noteReleased := false
	stereo := make([]float32, 0, maxFrames*2)

	for framesRendered < maxFrames {
		framesToRender := blockSize
		if framesRendered+framesToRender > maxFrames {
			framesToRender = maxFrames - framesRendered
		}
		if !noteReleased && framesRendered >= releaseAtFrame {
			p.NoteOff(note)
			noteReleased = true
		}
		block := p.Process(framesToRender)
		stereo = append(stereo, block...)
		framesRendered += framesToRender

		if framesRendered >= minFrames {
			var sum float64
			for _, s := range block {
				v := float64(s)
				sum += v * v
			}
			rms := 0.0
			if len(block) > 0 {
				rms = math.Sqrt(sum / float64(len(block)))
			}
			if rms < threshold {
				belowCount++
				if belowCount >= decayHoldBlocks {
					break
				}
			} else {
				belowCount = 0
			}
		}
	}
	// The caller compares mono sample counts.
	return stereo[:len(stereo)/2]
}
