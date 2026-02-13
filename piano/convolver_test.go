package piano

import (
	"math"
	"testing"
)

func TestPartitionedConvolverMatchesDirectConvolution(t *testing.T) {
	c := NewSoundboardConvolver(48000)

	input := make([]float32, 0, 1024)
	for i := 0; i < 1024; i++ {
		input = append(input, float32(math.Sin(float64(i)*0.07))*0.8)
	}
	leftIR := []float32{1.0, 0.3, -0.2, 0.1, 0.05}
	rightIR := []float32{0.8, -0.1, 0.05}
	c.SetIR(leftIR, rightIR)

	stereo := c.Process(input)
	outL := make([]float32, len(input))
	outR := make([]float32, len(input))
	for i := 0; i < len(input); i++ {
		outL[i] = stereo[i*2]
		outR[i] = stereo[i*2+1]
	}

	directL := directConvolve(input, leftIR)[:len(input)]
	directR := directConvolve(input, rightIR)[:len(input)]

	if d := maxAbsDiff(outL, directL); d > 1e-4 {
		t.Fatalf("left channel mismatch too high: max diff=%g", d)
	}
	if d := maxAbsDiff(outR, directR); d > 1e-4 {
		t.Fatalf("right channel mismatch too high: max diff=%g", d)
	}
}

func TestConvolverResetClearsTail(t *testing.T) {
	c := NewSoundboardConvolver(48000)
	c.SetIR([]float32{1, 0.5, 0.25}, []float32{1, 0.5, 0.25})

	_ = c.Process([]float32{1, 0, 0, 0})
	c.Reset()
	after := c.Process([]float32{0, 0, 0, 0})
	if rms := stereoRMS(after); rms > 1e-7 {
		t.Fatalf("expected near-silence after reset, got rms=%g", rms)
	}
}

func TestConvolverLoads96kWavAndResamples(t *testing.T) {
	left := []float32{1.0, 0.2, 0.1, 0.0}
	right := []float32{0.5, 0.1, 0.05, 0.0}
	path := writeTempIRWav(t, left, right, 96000)

	c := NewSoundboardConvolver(48000)
	if err := c.SetIRFromWAV(path); err != nil {
		t.Fatalf("SetIRFromWAV failed: %v", err)
	}

	input := make([]float32, 512)
	input[0] = 1.0
	out := c.Process(input)
	if len(out) != len(input)*2 {
		t.Fatalf("unexpected stereo length: %d", len(out))
	}

	leftPeak := float32(0)
	rightPeak := float32(0)
	peakFrames := len(out) / 2
	for i := 0; i < peakFrames && i < len(out)/2; i++ {
		lv := float32(math.Abs(float64(out[i*2])))
		rv := float32(math.Abs(float64(out[i*2+1])))
		if lv > leftPeak {
			leftPeak = lv
		}
		if rv > rightPeak {
			rightPeak = rv
		}
	}
	if leftPeak < 1e-7 {
		t.Fatalf("unexpectedly weak left response after load/resample: peak=%f", leftPeak)
	}
	if rightPeak < 1e-7 {
		t.Fatalf("unexpectedly weak right response after load/resample: peak=%f", rightPeak)
	}
}

func TestConvolverLoadsMonoWavAsDualMono(t *testing.T) {
	mono := []float32{1.0, 0.4, 0.2, 0.1}
	path := writeTempIRWav(t, mono, nil, 48000)

	c := NewSoundboardConvolver(48000)
	if err := c.SetIRFromWAV(path); err != nil {
		t.Fatalf("SetIRFromWAV mono failed: %v", err)
	}

	out := c.Process([]float32{1, 0, 0, 0, 0, 0})
	if len(out) != 12 {
		t.Fatalf("unexpected stereo length: %d", len(out))
	}

	for i := 0; i < len(out); i += 2 {
		if math.Abs(float64(out[i]-out[i+1])) > 1e-6 {
			t.Fatalf("expected dual-mono output at frame %d: L=%f R=%f", i/2, out[i], out[i+1])
		}
	}
}
