package piano

import (
	"math"

	"github.com/cwbudde/algo-approx"
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

func toFloat64(in []float32) []float64 {
	out := make([]float64, len(in))
	for i, v := range in {
		out[i] = float64(v)
	}
	return out
}

func overlapAddBlock(convOut []float64, tail []float64, blockLen int) ([]float64, []float64) {
	if len(convOut) < blockLen {
		out := make([]float64, blockLen)
		copy(out, convOut)
		return out, nil
	}

	full := make([]float64, len(convOut))
	copy(full, convOut)
	n := len(tail)
	if n > len(full) {
		n = len(full)
	}
	for i := 0; i < n; i++ {
		full[i] += tail[i]
	}

	out := make([]float64, blockLen)
	copy(out, full[:blockLen])
	newTail := make([]float64, len(full)-blockLen)
	copy(newTail, full[blockLen:])
	return out, newTail
}
