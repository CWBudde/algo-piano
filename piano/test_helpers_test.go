package piano

import (
	"math"
	"os"
	"testing"

	"github.com/cwbudde/wav"
	"github.com/go-audio/audio"
)

func measureFundamentalFreq(samples []float32, sampleRate float32) float32 {
	startIdx := len(samples) / 10
	crossings := 0
	for i := startIdx + 1; i < len(samples); i++ {
		if (samples[i-1] < 0 && samples[i] >= 0) || (samples[i-1] >= 0 && samples[i] < 0) {
			crossings++
		}
	}
	if crossings == 0 {
		return 0
	}
	duration := float32(len(samples)-startIdx) / sampleRate
	return float32(crossings) / (2.0 * duration)
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
	defer f.Close()

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
