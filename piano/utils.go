package piano

import (
	"math"

	approx "github.com/cwbudde/algo-approx"
)

// midiNoteToFreq converts MIDI note number to frequency in Hz.
func midiNoteToFreq(note int) float32 {
	const a4Freq = 440.0
	const a4Note = 69
	exponent := float32(note-a4Note) / 12.0
	return a4Freq * pow2Approx(exponent)
}

func pow2Approx(x float32) float32 {
	const ln2 = 0.69314718055994530942
	return approx.FastExp(x * ln2)
}

func defaultUnisonForNote(note int) ([]float32, []float32) {
	switch {
	case note < 40:
		return []float32{0.0}, []float32{1.0}
	case note < 70:
		return []float32{-1.8, 1.8}, []float32{0.52, 0.48}
	default:
		return []float32{-3.0, 0.0, 3.0}, []float32{0.34, 0.33, 0.33}
	}
}

func centsToRatio(cents float32) float32 {
	return pow2Approx(cents / 1200.0)
}

func isFinite(x float32) bool {
	return !math.IsNaN(float64(x)) && !math.IsInf(float64(x), 0)
}

func maxf(a float32, b float32) float32 {
	if a > b {
		return a
	}
	return b
}

func minf(a float32, b float32) float32 {
	if a < b {
		return a
	}
	return b
}

func clampf(x, lo, hi float32) float32 {
	if x < lo {
		return lo
	}
	if x > hi {
		return hi
	}
	return x
}

func expf(x float32) float32 {
	return float32(math.Exp(float64(x)))
}

// expDecayPerSample returns the per-sample multiplicative factor to achieve
// the given attenuation in dB over nSamples.
func expDecayPerSample(attenuationDB float32, nSamples int) float32 {
	if nSamples <= 1 {
		return 0
	}
	// 10^(-dB/20) = target ratio, then nth root.
	return expf(-attenuationDB * 0.11512925 / float32(nSamples)) // ln(10)/20 ≈ 0.11512925
}

// xorshift32 is a fast 32-bit PRNG for audio-rate noise generation.
func xorshift32(state *uint32) uint32 {
	x := *state
	x ^= x << 13
	x ^= x >> 17
	x ^= x << 5
	*state = x
	return x
}
