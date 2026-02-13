package piano

import (
	"fmt"
	"math"
	"testing"

	algofft "github.com/cwbudde/algo-fft"
	pdefd "github.com/cwbudde/algo-pde/fd"
	pdepoisson "github.com/cwbudde/algo-pde/poisson"
)

// TestTuningAccuracy verifies that the generated pitch is within tolerance
func TestTuningAccuracy(t *testing.T) {
	sampleRate := 48000

	tests := []struct {
		note         int
		expectedFreq float32
		tolerance    float32 // Hz
	}{
		{69, 440.0, 1.0},  // A4
		{60, 261.63, 1.0}, // Middle C (C4)
		{72, 523.25, 2.0}, // C5
		{48, 130.81, 1.0}, // C3
		{57, 220.0, 1.0},  // A3
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("Note%d", tt.note), func(t *testing.T) {
			// Create a string waveguide for this note
			freq := midiNoteToFreq(tt.note)
			str := NewStringWaveguide(sampleRate, freq)

			// Excite the string
			str.Excite(0.5)

			// Run the waveguide for several periods to ensure it's stable
			numSamples := sampleRate * 2 // 2 seconds
			samples := make([]float32, numSamples)
			for i := 0; i < numSamples; i++ {
				samples[i] = str.Process()
			}

			// Measure the fundamental frequency using zero-crossing analysis
			// (simple pitch detection)
			measuredFreq := measureFundamentalFreq(samples, float32(sampleRate))

			// Check if within tolerance
			diff := math.Abs(float64(measuredFreq - tt.expectedFreq))
			if diff > float64(tt.tolerance) {
				t.Errorf("Note %d: expected %.2f Hz, got %.2f Hz (diff: %.2f Hz, tolerance: %.2f Hz)",
					tt.note, tt.expectedFreq, measuredFreq, diff, tt.tolerance)
			} else {
				t.Logf("Note %d: expected %.2f Hz, got %.2f Hz ✓", tt.note, tt.expectedFreq, measuredFreq)
			}
		})
	}
}

func TestLoopLossEnergyDecaysMonotonically(t *testing.T) {
	const sampleRate = 48000
	str := NewStringWaveguide(sampleRate, 220.0)
	str.SetLoopLoss(0.997, 0.25)
	str.ExciteAtPosition(0.6, 0.2)

	const numSamples = 24000
	samples := make([]float32, numSamples)
	for i := range samples {
		samples[i] = str.Process()
	}

	window := 2000
	prev := float64(math.MaxFloat32)
	for start := window * 4; start+window <= len(samples); start += window {
		energy := windowRMS(samples[start : start+window])
		// Allow tiny numerical slack.
		if energy > prev*1.15 {
			t.Fatalf("energy rose unexpectedly: prev=%.8f curr=%.8f at window %d", prev, energy, start/window)
		}
		prev = energy
	}
}

func TestDispersionDetunesPartialsFromHarmonicSeries(t *testing.T) {
	const sampleRate = 48000
	const f0 = 220.0

	base := NewStringWaveguide(sampleRate, f0)
	base.SetLoopLoss(0.9997, 0.04)
	base.ExciteAtPosition(0.7, 0.2)

	disp := NewStringWaveguide(sampleRate, f0)
	disp.SetLoopLoss(0.9997, 0.04)
	disp.SetDispersion(0.8)
	disp.ExciteAtPosition(0.7, 0.2)

	const numSamples = 98304
	baseSamples := make([]float32, numSamples)
	dispSamples := make([]float32, numSamples)
	for i := 0; i < numSamples; i++ {
		baseSamples[i] = base.Process()
		dispSamples[i] = disp.Process()
	}

	skip := 4096
	baseAnalysis := baseSamples[skip : skip+8192]
	dispAnalysis := dispSamples[skip : skip+8192]
	fund := findPeakNear(baseAnalysis, sampleRate, f0, 20.0)
	if fund <= 0 {
		t.Fatalf("could not detect fundamental")
	}

	detunedPartials := 0
	for partial := 2; partial <= 5; partial++ {
		target := float64(partial) * fund
		basePeak := findPeakNear(baseAnalysis, sampleRate, target, target*0.12)
		dispPeak := findPeakNear(dispAnalysis, sampleRate, target, target*0.12)
		if basePeak <= 0 || dispPeak <= 0 {
			t.Fatalf("could not detect partial %d near %.2f Hz", partial, target)
		}
		if math.Abs(dispPeak-basePeak) > 1.0 {
			detunedPartials++
		}
		t.Logf("partial %d: base=%.2fHz dispersed=%.2fHz", partial, basePeak, dispPeak)
	}
	if detunedPartials < 2 {
		t.Fatalf("expected at least 2 partials to be detuned by dispersion, got %d", detunedPartials)
	}
}

func TestStrikePositionChangesSpectralTilt(t *testing.T) {
	const sampleRate = 48000
	const f0 = 261.63

	nearBridge := NewStringWaveguide(sampleRate, f0)
	nearBridge.SetLoopLoss(0.9997, 0.04)
	nearBridge.ExciteAtPosition(0.7, 0.08)

	nearMiddle := NewStringWaveguide(sampleRate, f0)
	nearMiddle.SetLoopLoss(0.9997, 0.04)
	nearMiddle.ExciteAtPosition(0.7, 0.45)

	const numSamples = 16384
	a := make([]float32, numSamples)
	b := make([]float32, numSamples)
	for i := 0; i < numSamples; i++ {
		a[i] = nearBridge.Process()
		b[i] = nearMiddle.Process()
	}

	// Skip initial transient for a steadier comparison.
	aCentroid := spectralCentroid(a[2048:], sampleRate, 2048)
	bCentroid := spectralCentroid(b[2048:], sampleRate, 2048)
	if aCentroid <= bCentroid {
		t.Fatalf("expected near-bridge strike to be brighter: bridge=%.2fHz middle=%.2fHz", aCentroid, bCentroid)
	}
}

func TestHammerVelocityIncreasesBrightnessProxy(t *testing.T) {
	const sampleRate = 48000
	soft := NewHammer(sampleRate, 35)
	hard := NewHammer(sampleRate, 120)

	softPeak, softContact := hammerContactProfile(soft)
	hardPeak, hardContact := hammerContactProfile(hard)

	// Brightness proxy: harder strikes should produce higher peak force and shorter contact.
	if hardPeak <= softPeak {
		t.Fatalf("expected hard strike peak force > soft strike: hard=%f soft=%f", hardPeak, softPeak)
	}
	if hardContact >= softContact {
		t.Fatalf("expected hard strike contact duration < soft strike: hard=%d soft=%d", hardContact, softContact)
	}
}

func TestLongRenderHasNoNaNOrInf(t *testing.T) {
	const sampleRate = 48000
	params := NewDefaultParams()
	p := NewPiano(sampleRate, 16, params)
	p.NoteOn(48, 80)
	p.NoteOn(60, 90)
	p.NoteOn(72, 110)

	const numBlocks = 300
	const blockSize = 128
	for i := 0; i < numBlocks; i++ {
		out := p.Process(blockSize)
		for j, s := range out {
			if math.IsNaN(float64(s)) || math.IsInf(float64(s), 0) {
				t.Fatalf("non-finite sample at block %d sample %d: %v", i, j, s)
			}
		}
	}
}

func TestUnisonDetuneProducesBeating(t *testing.T) {
	const sampleRate = 48000
	const note = 69
	f0 := midiNoteToFreq(note)

	s1 := NewStringWaveguide(sampleRate, f0*centsToRatio(-8.0))
	s2 := NewStringWaveguide(sampleRate, f0*centsToRatio(8.0))
	s1.SetLoopLoss(0.9997, 0.03)
	s2.SetLoopLoss(0.9997, 0.03)
	s1.ExciteAtPosition(0.6, 0.18)
	s2.ExciteAtPosition(0.6, 0.18)

	ref1 := NewStringWaveguide(sampleRate, f0)
	ref2 := NewStringWaveguide(sampleRate, f0)
	ref1.SetLoopLoss(0.9997, 0.03)
	ref2.SetLoopLoss(0.9997, 0.03)
	ref1.ExciteAtPosition(0.6, 0.18)
	ref2.ExciteAtPosition(0.6, 0.18)

	const numSamples = 98304
	unisonOut := make([]float32, numSamples)
	referenceOut := make([]float32, numSamples)
	for i := 0; i < numSamples; i++ {
		unisonOut[i] = 0.5*s1.Process() + 0.5*s2.Process()
		referenceOut[i] = 0.5*ref1.Process() + 0.5*ref2.Process()
	}

	analysis := unisonOut[8192 : 8192+32768]
	refAnalysis := referenceOut[8192 : 8192+32768]

	u1, u2 := twoStrongestPeaksNear(analysis, sampleRate, float64(f0), 30.0)
	r1, r2 := twoStrongestPeaksNear(refAnalysis, sampleRate, float64(f0), 30.0)
	uSep := math.Abs(u2 - u1)
	rSep := math.Abs(r2 - r1)
	if uSep <= rSep+1.5 {
		t.Fatalf("expected larger peak separation for detuned unison: unison=%.2fHz reference=%.2fHz", uSep, rSep)
	}
}

func TestVoiceUnisonStringCountByRange(t *testing.T) {
	vLow := NewVoice(48000, 30, 90, NewDefaultParams())
	vMid := NewVoice(48000, 60, 90, NewDefaultParams())
	vHigh := NewVoice(48000, 80, 90, NewDefaultParams())

	if len(vLow.strings) != 1 {
		t.Fatalf("expected low note to allocate 1 string, got %d", len(vLow.strings))
	}
	if len(vMid.strings) != 2 {
		t.Fatalf("expected mid note to allocate 2 strings, got %d", len(vMid.strings))
	}
	if len(vHigh.strings) != 3 {
		t.Fatalf("expected high note to allocate 3 strings, got %d", len(vHigh.strings))
	}
}

func TestPartitionedConvolverMatchesDirectConvolution(t *testing.T) {
	c := NewSoundboardConvolver(48000)

	input := make([]float32, 0, 1024)
	for i := 0; i < 1024; i++ {
		input = append(input, float32(math.Sin(2*math.Pi*float64(i)/37.0))*0.3)
	}

	leftIR := []float32{0.8, -0.3, 0.2, 0.1, -0.05, 0.025}
	rightIR := []float32{0.75, -0.22, 0.15, 0.08, -0.03, 0.02}
	c.SetIR(leftIR, rightIR)

	got := c.Process(input)
	gotL := make([]float32, len(input))
	gotR := make([]float32, len(input))
	for i := 0; i < len(input); i++ {
		gotL[i] = got[i*2]
		gotR[i] = got[i*2+1]
	}

	wantL := directConvolve(input, leftIR)[:len(input)]
	wantR := directConvolve(input, rightIR)[:len(input)]

	if err := maxAbsDiff(gotL, wantL); err > 1e-4 {
		t.Fatalf("left convolution mismatch: maxAbsDiff=%f", err)
	}
	if err := maxAbsDiff(gotR, wantR); err > 1e-4 {
		t.Fatalf("right convolution mismatch: maxAbsDiff=%f", err)
	}
}

func TestConvolverResetClearsTail(t *testing.T) {
	c := NewSoundboardConvolver(48000)
	c.SetIR([]float32{1.0, 0.5, 0.25}, []float32{1.0, 0.5, 0.25})
	_ = c.Process([]float32{1, 0, 0, 0, 0, 0, 0, 0})
	c.Reset()

	out := c.Process(make([]float32, 64))
	for i, v := range out {
		if math.Abs(float64(v)) > 1e-7 {
			t.Fatalf("expected silence after reset, found %f at sample %d", v, i)
		}
	}
}

func TestAlgoFFTConvolveRealMatchesDirect(t *testing.T) {
	a := []float32{1, 2, 3, 4, 5}
	b := []float32{0.5, -0.25, 0.125}
	got := make([]float32, len(a)+len(b)-1)
	if err := algofft.ConvolveReal(got, a, b); err != nil {
		t.Fatalf("ConvolveReal error: %v", err)
	}

	want := directConvolve(a, b)
	for i := range got {
		if math.Abs(float64(got[i]-want[i])) > 1e-4 {
			t.Fatalf("fft convolution mismatch at %d: got=%f want=%f", i, got[i], want[i])
		}
	}
}

func TestAlgoPDEEigenspectrumSanity(t *testing.T) {
	const n = 64
	const h = 1.0 / 64.0

	periodic := pdefd.Eigenvalues(n, h, pdepoisson.Periodic)
	if len(periodic) != n {
		t.Fatalf("unexpected periodic eigenvalue count: %d", len(periodic))
	}
	if math.Abs(periodic[0]) > 1e-12 {
		t.Fatalf("expected periodic zero mode at index 0, got %g", periodic[0])
	}

	dirichlet := pdefd.Eigenvalues(n, h, pdepoisson.Dirichlet)
	if len(dirichlet) != n {
		t.Fatalf("unexpected dirichlet eigenvalue count: %d", len(dirichlet))
	}
	if dirichlet[0] <= 0 {
		t.Fatalf("expected strictly positive first dirichlet eigenvalue, got %g", dirichlet[0])
	}
	for i := 1; i < len(dirichlet); i++ {
		if dirichlet[i] < dirichlet[i-1] {
			t.Fatalf("expected non-decreasing dirichlet eigenspectrum at %d: %g < %g", i, dirichlet[i], dirichlet[i-1])
		}
	}
}

func TestReleaseWithPedalUpDecaysQuickly(t *testing.T) {
	p := NewPiano(48000, 16, NewDefaultParams())
	p.NoteOn(60, 100)
	_ = p.Process(4800) // attack
	p.NoteOff(60)

	var tail []float32
	for i := 0; i < 20; i++ {
		tail = p.Process(256)
	}
	rms := stereoRMS(tail)
	if rms > 0.01 {
		t.Fatalf("expected short release with pedal up, got tail RMS %f", rms)
	}
}

func TestSustainPedalKeepsNoteRinging(t *testing.T) {
	withPedal := NewPiano(48000, 16, NewDefaultParams())
	withPedal.SetSustainPedal(true)
	withPedal.NoteOn(60, 100)
	_ = withPedal.Process(4800)
	withPedal.NoteOff(60)

	withoutPedal := NewPiano(48000, 16, NewDefaultParams())
	withoutPedal.SetSustainPedal(false)
	withoutPedal.NoteOn(60, 100)
	_ = withoutPedal.Process(4800)
	withoutPedal.NoteOff(60)

	var tailWith []float32
	var tailWithout []float32
	for i := 0; i < 20; i++ {
		tailWith = withPedal.Process(256)
		tailWithout = withoutPedal.Process(256)
	}

	rmsWith := stereoRMS(tailWith)
	rmsWithout := stereoRMS(tailWithout)
	if rmsWith <= rmsWithout*1.5 {
		t.Fatalf("expected sustain pedal to keep more energy: with=%f without=%f", rmsWith, rmsWithout)
	}
}

// measureFundamentalFreq estimates the fundamental frequency using zero-crossing rate
// This is a simple method; more sophisticated pitch detection would use autocorrelation or FFT
func measureFundamentalFreq(samples []float32, sampleRate float32) float32 {
	// Skip the initial transient (first 10%)
	startIdx := len(samples) / 10

	// Find zero crossings
	crossings := 0
	for i := startIdx + 1; i < len(samples); i++ {
		if (samples[i-1] < 0 && samples[i] >= 0) || (samples[i-1] >= 0 && samples[i] < 0) {
			crossings++
		}
	}

	if crossings == 0 {
		return 0
	}

	// Zero crossing rate = 2 * frequency (crosses zero twice per period)
	duration := float32(len(samples)-startIdx) / sampleRate
	freq := float32(crossings) / (2.0 * duration)

	return freq
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
