package piano

import (
	"fmt"
	"math"
	"testing"
)

// measureWaveguideCents renders a bare string for two seconds and returns how
// far its measured fundamental lands from the note's nominal frequency, in
// cents.
func measureWaveguideCents(t *testing.T, note int) (measured float32, cents float64) {
	t.Helper()
	const sampleRate = 48000

	nominal := midiNoteToFreq(note)
	str := NewStringWaveguide(sampleRate, nominal)
	str.Excite(0.5)

	samples := make([]float32, sampleRate*2)
	for i := range samples {
		samples[i] = str.Process()
	}

	measured = float32(measureFundamentalNear(samples, sampleRate, float64(nominal)))
	if measured <= 0 {
		return 0, math.Inf(-1)
	}
	return measured, 1200 * math.Log2(float64(measured/nominal))
}

// TestTuningAccuracyAcrossCompass extends tuning coverage from the five
// mid-range notes of TestTuningAccuracy to the whole 21-108 piano compass.
//
// Every note is measurable. The compass used to stop at MIDI 92 because the DWG
// core lost its tonal content above that and went bit-exactly silent at 106-108;
// both causes are gone (see TestTrebleRegisterIsStableInDWGCore), so the test
// now runs to C8.
//
// Tolerances are per register and are set from the worst error measured over
// that register with roughly 40% headroom. Measured on 2026-08-21 (Go 1.26.5,
// linux/amd64) on a bare StringWaveguide, 2 s at 48 kHz:
//
//	bass    21-39   worst 0.64 cents (note 22)   -> 0.90
//	tenor   40-59   worst 0.22 cents (note 42)   -> 0.31
//	middle  60-83   worst 0.07 cents (note 60)   -> 0.10
//	treble  84-95   worst 0.06 cents (note 94)   -> 0.09
//	high    96-103  worst 0.46 cents (note 103)  -> 0.65
//	top    104-108  worst 3.48 cents (note 108)  -> 4.90
//
// The tolerances no longer widen towards the bass, because the measurement no
// longer does: measureFundamentalNear interpolates a spectral peak instead of
// counting zero crossings, and is accurate to +/-0.012 Hz (worst 0.32 cents at
// 27.5 Hz, measured against synthetic decaying sinusoids).
//
// They widen at the very top for a real, structural reason, not a defect. The
// loop of a top-octave string is only 11-14 samples long, and the loop delay is
// set by a two-tap linear interpolator whose excess phase delay is a fraction of
// one sample. That fraction is a fixed number of samples, so as a share of an
// 11-sample period it is 1000x more of the pitch than it is of C4's 187-sample
// period. The same error is present without any of this PR's changes: the
// pre-fix core, measured with the same estimator, is -2.83 cents at MIDI 108.
// Fixing it needs a higher-order fractional delay (allpass or Lagrange), which
// is a separate change.
func TestTuningAccuracyAcrossCompass(t *testing.T) {
	registers := []struct {
		name      string
		lo, hi    int
		tolerance float64 // cents
	}{
		{"bass", 21, 39, 0.90},
		{"tenor", 40, 59, 0.31},
		{"middle", 60, 83, 0.10},
		{"treble", 84, 95, 0.09},
		{"high", 96, 103, 0.65},
		{"top", 104, 108, 4.90},
	}

	for _, reg := range registers {
		t.Run(reg.name, func(t *testing.T) {
			worst := 0.0
			worstNote := 0
			for note := reg.lo; note <= reg.hi; note++ {
				t.Run(fmt.Sprintf("note%d", note), func(t *testing.T) {
					measured, cents := measureWaveguideCents(t, note)
					if math.IsInf(cents, 0) {
						t.Fatalf("note %d: no measurable fundamental (nominal %.2f Hz)",
							note, midiNoteToFreq(note))
					}
					if math.Abs(cents) > reg.tolerance {
						t.Errorf("note %d: nominal %.3f Hz, measured %.3f Hz, error %+.2f cents (tolerance %.1f)",
							note, midiNoteToFreq(note), measured, cents, reg.tolerance)
					}
					if math.Abs(cents) > worst {
						worst, worstNote = math.Abs(cents), note
					}
				})
			}
			t.Logf("%s register worst error: %.2f cents at note %d (tolerance %.1f)",
				reg.name, worst, worstNote, reg.tolerance)
		})
	}
}

// TestTrebleRegisterIsStableInDWGCore is the regression test for the two
// independent DWG defects recorded on 2026-08-21 and fixed in this change. It
// was written as a skipped test documenting the defects; it now asserts the
// fix.
//
// What was wrong:
//
//  1. Silent top notes. MIDI 106, 107 and 108 rendered exactly zero - not
//     quiet, bit-exact silence - for a whole render. The delay line is
//     allocated delayHeadroom slots longer than the integer delay, and the two
//     interpolating taps sit at the far end of that headroom, so a slot written
//     closer than delayHeadroom-1 ahead of the write pointer is overwritten
//     before any tap reads it. Those three notes have 16, 16 and 15 slot delay
//     lines, and the default strike position of 0.18 lands on offset 2. All of
//     the hammer energy was discarded. StringWaveguide.injectionOffset now
//     clamps every injection into the observable range.
//
//  2. DC runaway. From roughly MIDI 96 up the output was essentially pure DC and
//     the offset grew with pitch and with time: note 100 +5.3, note 103 +13.0,
//     note 105 +38.5, the last about 32x full scale, and holding note 96 for 800
//     blocks took it from +4.8 to +59.2 without converging. The loop filter has
//     unity DC gain, so the loop is a leaky integrator for DC, and unison
//     crossfeed injects into every string of the group on every sample. A short
//     loop recirculates about 4000 times a second, so the injection outruns the
//     per-round-trip loss. StringWaveguide.processDCBlock now removes DC inside
//     the loop, which is what a real string does.
//
// Measured after the fix on 2026-08-21 (Go 1.26.5, linux/amd64), same
// conditions as the original measurement - default parameters, sustain held,
// resonance and coupling disabled, left channel over the second second of a
// 400-block render:
//
//	note  96: dc +4.01e-02  ac 2.06e-01
//	note 100: dc -3.04e-03  ac 3.24e-01
//	note 103: dc -2.83e-02  ac 4.49e-01
//	note 105: dc -7.16e-02  ac 5.68e-01
//	note 106: dc -9.57e-02  ac 6.39e-01
//	note 108: dc -1.35e-01  ac 8.07e-01
//
// Two things to read out of that. Every note, including the three that were
// bit-exactly silent, now carries tonal content, and the AC level rises smoothly
// with pitch instead of collapsing. And the residual DC is now smaller than the
// AC it rides on at every note (worst ratio 0.17 at MIDI 108) and it decays with
// the note instead of growing: holding note 108 for 1600 blocks gives
// dc/ac = -1.35e-01/8.07e-01 at 1 s, +2.07e-02/1.18e-01 at 2 s,
// -1.85e-03/1.73e-02 at 3 s and -1.87e-05/2.54e-03 at 4 s. That is the tail of a
// decaying note seen through a 2 Hz corner, not an integrator winding up.
//
// The bounds below are the original ones from the skipped version, deliberately
// unchanged: they are the "is this broken" bounds, and the measured margin
// against them is a factor of 7 on DC and five orders of magnitude on AC.
func TestTrebleRegisterIsStableInDWGCore(t *testing.T) {
	const blocks = 400

	worstDC := 0.0
	worstDCNote := 0
	weakestAC := math.Inf(1)
	weakestACNote := 0

	for note := 96; note <= 108; note++ {
		mono := renderNoteMonoFloat32(StringModelDWG, note, blocks)
		tail := mono[48000:]

		var mean float64
		for _, v := range tail {
			mean += float64(v)
		}
		mean /= float64(len(tail))

		ac := make([]float32, len(tail))
		for i, v := range tail {
			ac[i] = v - float32(mean)
		}
		acRMS := windowRMS(ac)

		t.Logf("note %d: dc=%+.4e ac=%.4e", note, mean, acRMS)
		if math.Abs(mean) > 1.0 {
			t.Errorf("note %d: DC offset %+.4f exceeds full scale", note, mean)
		}
		if acRMS <= 1e-6 {
			t.Errorf("note %d: no tonal content in the tail (ac RMS %.4e)", note, acRMS)
		}

		if math.Abs(mean) > worstDC {
			worstDC, worstDCNote = math.Abs(mean), note
		}
		if acRMS < weakestAC {
			weakestAC, weakestACNote = acRMS, note
		}
	}

	t.Logf("worst DC offset %.4e at note %d (bound 1.0); weakest tonal content %.4e at note %d (bound 1e-6)",
		worstDC, worstDCNote, weakestAC, weakestACNote)
}

// renderNoteMonoFloat32 renders one held note through a full Piano and returns
// the left channel.
func renderNoteMonoFloat32(model StringModel, note int, blocks int) []float32 {
	params := NewDefaultParams()
	params.StringModel = model
	params.ResonanceEnabled = false
	params.CouplingEnabled = false

	p := NewPiano(48000, 16, params)
	p.SetSustainPedal(true)
	p.NoteOn(note, 100)

	out := make([]float32, 0, blocks*128)
	for i := 0; i < blocks; i++ {
		block := p.Process(128)
		for j := 0; j < 128; j++ {
			out = append(out, block[j*2])
		}
	}
	return out
}
