package piano

import (
	"math"
	"math/cmplx"
	"os"
	"testing"

	algofft "github.com/cwbudde/algo-fft"
	"github.com/cwbudde/wav"
	"github.com/go-audio/audio"
)

// measureFundamentalNear estimates the fundamental of a plucked/struck string
// signal by locating the spectral peak nearest a known nominal frequency and
// refining it with parabolic interpolation over the log magnitudes of the two
// neighbouring bins.
//
// It replaced a bare zero-crossing counter on 2026-08-21. Counting sign changes
// has no noise margin: it reports the right answer only while the waveform
// happens to cross zero exactly twice per period, and any low-level ripple
// riding on it doubles or triples the count. That was masked for the DWG core
// because its output used to be exactly 0.0 between excitation pulses; once the
// loop DC blocker leaves a small decaying residue there instead, a sign counter
// reads 2x the true pitch at several mid-range notes while the spectrum is
// still exactly where it should be. Measured on note 69: sign counting says
// 779.4 Hz, the spectrum says 440.5 Hz against a nominal 440.0 Hz.
//
// Searching near a nominal rather than globally is deliberate: the peak of a
// piano partial series is not always the fundamental. The +/-35% window is a
// little over five semitones wide, far beyond any tuning error worth calling
// accurate, so it constrains nothing the test cares about.
func measureFundamentalNear(samples []float32, sampleRate int, nominalHz float64) float64 {
	const fftLen = 65536 // 1.365 s at 48 kHz; bin spacing 0.73 Hz before interpolation

	// Skip the attack, which is broadband and would smear the peak.
	start := len(samples) / 10
	if len(samples)-start < fftLen {
		start = 0
	}
	if len(samples)-start < fftLen {
		return 0
	}

	plan, err := algofft.NewPlanReal64(fftLen)
	if err != nil {
		return 0
	}

	in := make([]float64, fftLen)
	for i := range in {
		// Hann window: without it the spectral leakage of a decaying tone is
		// wide enough to move the interpolated peak by several cents.
		w := 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(fftLen))
		in[i] = float64(samples[start+i]) * w
	}

	spec := make([]complex128, fftLen/2+1)
	if err := plan.Forward(spec, in); err != nil {
		return 0
	}

	binHz := float64(sampleRate) / float64(fftLen)
	lo := int((nominalHz * 0.65) / binHz)
	hi := int((nominalHz*1.35)/binHz) + 1
	if lo < 1 {
		lo = 1
	}
	if hi > len(spec)-2 {
		hi = len(spec) - 2
	}
	if lo >= hi {
		return 0
	}

	best, bestMag := lo, 0.0
	for k := lo; k <= hi; k++ {
		if m := cmplx.Abs(spec[k]); m > bestMag {
			best, bestMag = k, m
		}
	}
	if bestMag <= 0 {
		return 0
	}

	// Parabolic interpolation on log magnitudes, the standard refinement for a
	// Hann-windowed peak. Guard against a zero neighbour, whose log is -Inf.
	l, c, r := cmplx.Abs(spec[best-1]), bestMag, cmplx.Abs(spec[best+1])
	offset := 0.0
	if l > 0 && r > 0 {
		ll, lc, lr := math.Log(l), math.Log(c), math.Log(r)
		if denom := ll - 2*lc + lr; denom != 0 {
			offset = 0.5 * (ll - lr) / denom
		}
		if offset > 0.5 || offset < -0.5 {
			offset = 0
		}
	}
	return (float64(best) + offset) * binHz
}

func windowRMS(samples []float32) float64 {
	var sum float64
	for _, s := range samples {
		v := float64(s)
		sum += v * v
	}
	return math.Sqrt(sum / float64(len(samples)))
}

func findPeakNear(samples []float32, sampleRate int, centerHz float64, spanHz float64) float64 {
	n := len(samples)
	minBin := int((centerHz - spanHz) * float64(n) / float64(sampleRate))
	maxBin := int((centerHz + spanHz) * float64(n) / float64(sampleRate))
	if minBin < 1 {
		minBin = 1
	}
	nyquist := n / 2
	if maxBin > nyquist-1 {
		maxBin = nyquist - 1
	}
	if minBin >= maxBin {
		return 0
	}

	bestBin := minBin
	bestMag := 0.0
	for k := minBin; k <= maxBin; k++ {
		mag := dftBinMagnitude(samples, k)
		if mag > bestMag {
			bestMag = mag
			bestBin = k
		}
	}
	return float64(bestBin) * float64(sampleRate) / float64(n)
}

func spectralCentroid(samples []float32, sampleRate int, fftSize int) float64 {
	if len(samples) < fftSize {
		return 0
	}
	segment := samples[:fftSize]

	var weightedSum float64
	var magSum float64
	for k := 1; k < fftSize/2; k++ {
		mag := dftBinMagnitude(segment, k)
		freq := float64(k) * float64(sampleRate) / float64(fftSize)
		weightedSum += freq * mag
		magSum += mag
	}
	if magSum == 0 {
		return 0
	}
	return weightedSum / magSum
}

func dftBinMagnitude(samples []float32, bin int) float64 {
	n := len(samples)
	var re float64
	var im float64
	for i := 0; i < n; i++ {
		phase := -2.0 * math.Pi * float64(bin*i) / float64(n)
		x := float64(samples[i])
		re += x * math.Cos(phase)
		im += x * math.Sin(phase)
	}
	return math.Hypot(re, im)
}

func twoStrongestPeaksNear(samples []float32, sampleRate int, centerHz float64, spanHz float64) (float64, float64) {
	n := len(samples)
	minBin := int((centerHz - spanHz) * float64(n) / float64(sampleRate))
	maxBin := int((centerHz + spanHz) * float64(n) / float64(sampleRate))
	if minBin < 1 {
		minBin = 1
	}
	if maxBin > n/2-1 {
		maxBin = n/2 - 1
	}

	bestBin1, bestBin2 := minBin, minBin
	bestMag1, bestMag2 := 0.0, 0.0
	for k := minBin; k <= maxBin; k++ {
		mag := dftBinMagnitude(samples, k)
		if mag > bestMag1 {
			bestMag2, bestBin2 = bestMag1, bestBin1
			bestMag1, bestBin1 = mag, k
		} else if mag > bestMag2 {
			bestMag2, bestBin2 = mag, k
		}
	}

	f1 := float64(bestBin1) * float64(sampleRate) / float64(n)
	f2 := float64(bestBin2) * float64(sampleRate) / float64(n)
	if f1 > f2 {
		return f2, f1
	}
	return f1, f2
}

func hammerContactProfile(h *Hammer) (peakForce float32, contactSamples int) {
	for h.InContact() {
		f := h.Step(0)
		if f > peakForce {
			peakForce = f
		}
		contactSamples++
		if contactSamples > int(h.sampleRate)*2 {
			break
		}
	}
	return peakForce, contactSamples
}

func directConvolve(x []float32, h []float32) []float32 {
	y := make([]float32, len(x)+len(h)-1)
	for i := 0; i < len(x); i++ {
		for j := 0; j < len(h); j++ {
			y[i+j] += x[i] * h[j]
		}
	}
	return y
}

func maxAbsDiff(a []float32, b []float32) float64 {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	max := 0.0
	for i := 0; i < n; i++ {
		d := math.Abs(float64(a[i] - b[i]))
		if d > max {
			max = d
		}
	}
	return max
}

func stereoRMS(interleaved []float32) float64 {
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

func setupSympatheticScenario(params *Params) (*Piano, *RingingStringGroup) {
	p := NewPiano(48000, 16, params)
	p.SetSustainPedal(true)

	p.ringing.SetKeyDown(67, true)
	held := p.ringing.bank.Group(67)

	p.NoteOn(60, 115)
	return p, held
}

func filteredDriveRMS(g *RingingStringGroup, inputHz float64, n int) float64 {
	if g == nil || n <= 0 {
		return 0
	}
	const sampleRate = 48000.0
	var sum float64
	for i := 0; i < n; i++ {
		x := float32(math.Sin(2.0 * math.Pi * inputHz * float64(i) / sampleRate))
		y := g.filterResonanceDrive(x)
		f := float64(y)
		sum += f * f
	}
	return math.Sqrt(sum / float64(n))
}

func voiceInternalEnergy(g *RingingStringGroup) float64 {
	if g == nil {
		return 0
	}
	var sum float64
	for _, s := range g.strings {
		for _, x := range s.delayLine {
			f := float64(x)
			sum += f * f
		}
	}
	return sum
}

func writeTempIRWav(t *testing.T, left []float32, right []float32, sampleRate int) string {
	t.Helper()
	f, err := os.CreateTemp("", "ir-*.wav")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	defer func() { _ = f.Close() }()

	numCh := 1
	data := make([]float32, len(left))
	copy(data, left)
	if right != nil {
		numCh = 2
		if len(right) != len(left) {
			t.Fatalf("left/right length mismatch")
		}
		data = make([]float32, len(left)*2)
		for i := range left {
			data[i*2] = left[i]
			data[i*2+1] = right[i]
		}
	}

	enc := wav.NewEncoder(f, sampleRate, 16, numCh, 1)
	buf := &audio.Float32Buffer{
		Format: &audio.Format{
			SampleRate:  sampleRate,
			NumChannels: numCh,
		},
		Data:           data,
		SourceBitDepth: 16,
	}
	if err := enc.Write(buf); err != nil {
		t.Fatalf("wav write: %v", err)
	}
	if err := enc.Close(); err != nil {
		t.Fatalf("wav close: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(f.Name()) })
	return f.Name()
}
