package piano

import "math"

// This file holds the envelope machinery the two-stage decay tests measure with.
// It deliberately duplicates a little of what analysis/ already computes rather
// than exporting it: analysis' fitters are a fitting-internal whose
// NaN-on-degenerate contract is tuned for the distance path, and this package
// already keeps its own primitives (windowRMS) next to theirs on purpose.

// envFloorDB is where the dB conversion bottoms out. It is -140 and not
// analysis' -240 because below roughly -140 dBFS the modal core is inside its
// flush-to-zero region (see modalFlushThreshold), and a slope fitted there
// measures the denormal threshold rather than the string.
const envFloorDB = -140.0

// rmsEnvelopeDB builds a frame/hop RMS envelope of a mono render, in dBFS.
func rmsEnvelopeDB(samples []float32, frame int, hop int) []float64 {
	if frame <= 0 || hop <= 0 || len(samples) < frame {
		return nil
	}
	out := make([]float64, 0, (len(samples)-frame)/hop+1)
	for start := 0; start+frame <= len(samples); start += hop {
		var sum float64
		for _, v := range samples[start : start+frame] {
			f := float64(v)
			sum += f * f
		}
		db := envFloorDB
		if rms := math.Sqrt(sum / float64(frame)); rms > 0 {
			if d := 20 * math.Log10(rms); d > envFloorDB {
				db = d
			}
		}
		out = append(out, db)
	}
	return out
}

// maxHoldDB replaces each frame with the maximum over the next span frames.
//
// This is the step that lets a knee be told apart from unison BEATING. A
// detuned unison puts a ripple on the envelope at the beat rate, and a beat null
// is 10 dB deep or more; a least-squares slope over a window of length T
// contaminated by ripple of amplitude A picks up a bias of up to 1.9*A/T dB/s,
// which at A = 10 dB and T = 2 s is ~9.5 dB/s - larger than the effect being
// measured. Fitting over a whole number of beat periods does NOT remove it.
//
// Running-max over one beat period does: the upper envelope of a beating pair is
// the sum-of-amplitudes curve, which has no ripple, and for a monotone decaying
// envelope a running maximum is a pure translation in time. So it erases the
// ripple while leaving every fitted slope - and therefore every knee - alone.
func maxHoldDB(env []float64, span int) []float64 {
	if span <= 1 {
		return env
	}
	out := make([]float64, len(env))
	for i := range env {
		end := i + span
		if end > len(env) {
			end = len(env)
		}
		best := env[i]
		for _, v := range env[i+1 : end] {
			if v > best {
				best = v
			}
		}
		out[i] = best
	}
	return out
}

// fitDecaySlopeDB least-squares fits an already-dB envelope over frames
// [from, to) and returns the slope in dB per second. NaN when the window is too
// short to carry a slope.
func fitDecaySlopeDB(env []float64, from int, to int, hopSec float64) float64 {
	if from < 0 || to > len(env) || to-from < 3 || hopSec <= 0 {
		return math.NaN()
	}
	var sx, sy, sxx, sxy float64
	n := float64(to - from)
	for i := from; i < to; i++ {
		x := float64(i-from) * hopSec
		y := env[i]
		sx, sy, sxx, sxy = sx+x, sy+y, sxx+x*x, sxy+x*y
	}
	den := n*sxx - sx*sx
	if math.Abs(den) < 1e-12 {
		return math.NaN()
	}
	return (n*sxy - sx*sy) / den
}

// unisonBeatPeriod returns the slowest beat period of note's unison under
// detuneScale, in seconds - one over the smallest non-zero pairwise frequency
// gap. Zero for the single-string groups below MIDI 40, which do not beat.
//
// It reads defaultUnisonForNote directly so it cannot drift from the layout the
// renderer actually builds.
func unisonBeatPeriod(note int, detuneScale float32) float64 {
	detunes, _ := defaultUnisonForNote(note)
	if len(detunes) < 2 {
		return 0
	}
	f0 := float64(midiNoteToFreq(note))
	freqs := make([]float64, len(detunes))
	for i, c := range detunes {
		freqs[i] = f0 * math.Pow(2, float64(c*detuneScale)/1200)
	}
	gap := math.Inf(1)
	for i := range freqs {
		for j := i + 1; j < len(freqs); j++ {
			if d := math.Abs(freqs[i] - freqs[j]); d > 1e-9 && d < gap {
				gap = d
			}
		}
	}
	if math.IsInf(gap, 1) {
		return 0
	}
	return 1 / gap
}

// decayProlongation measures double decay as PROLONGATION: how much longer a
// note actually takes to fall 40 dB than its own prompt slope predicts.
//
// One slope fit instead of two. The prompt slope is fitted over the first 12 dB
// of decay, where the envelope is strongest and best conditioned and where the
// bridge term acts hardest; the -40 dB point is a threshold crossing, not a fit.
// A single exponential returns exactly 1.0 by construction, so the metric needs
// no uncoupled reference arm to subtract - which matters, because a coupled
// render decays several times faster and windows borrowed from an uncoupled one
// land past its end, on the floor, and measure nothing.
//
// Returns the ratio, the fitted prompt slope in dB/s, and false when the render
// never falls 40 dB or the prompt window is too short to fit.
func decayProlongation(env []float64, hopSec float64) (float64, float64, bool) {
	if len(env) == 0 {
		return 0, 0, false
	}
	peak, pi := env[0], 0
	for i, v := range env {
		if v > peak {
			peak, pi = v, i
		}
	}
	cross := func(drop float64) int {
		for i := pi; i < len(env); i++ {
			if env[i] <= peak-drop {
				return i
			}
		}
		return -1
	}
	es, ee, t40 := cross(2), cross(12), cross(40)
	if es < 0 || ee < 0 || t40 < 0 || ee-es < 6 {
		return 0, 0, false
	}
	early := fitDecaySlopeDB(env, es, ee, hopSec)
	if !(early < -0.05) {
		return 0, 0, false
	}
	return (float64(t40-pi) * hopSec) / (40.0 / -early), early, true
}
