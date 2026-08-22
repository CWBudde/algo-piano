package piano

import (
	"fmt"
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

// The tests below fence the convolvers' streaming contract: Process may be
// called with any input length, and the output for a given sample must not
// depend on how the caller chopped the stream up.
//
// They exist because that used to be false. Both Process implementations
// zero-padded a trailing short block up to partSize and pushed it through the
// fixed-block overlap-add stage, which advanced the stage by a whole partition
// and spliced the padding zeros into the stream. Streaming a 1000-sample sine
// through a 512-tap IR in fixed chunks and comparing against directConvolve
// measured a max absolute error of 5.56 at chunk 100, 1.93 at chunk 64 and 1.40
// at chunk 1, against a signal peaking at 15.9 — while chunk 128 was correct at
// 1.19e-06. Every renderer in the tree happens to use 128, which is why nothing
// caught it.

// convolverRelTolerance bounds the relative error of the FFT convolution
// against a direct time-domain convolution, as a fraction of the reference
// signal's peak.
//
// The error here is float32 round-off accumulated through the overlap-add FFT,
// not an approximation: with a float32 mantissa (eps = 1.19e-07) and an FFT of
// size N, the relative error of a forward/inverse pair grows roughly as
// eps·log2(N), i.e. about 1.7e-06 for the N=16384 the 8192-tap case below uses.
// 1e-05 leaves an order of magnitude of headroom for the accumulation across
// partitions while staying far below anything audible, and — the point of the
// bound — far below the errors the block-size defect produced, which were
// fractions of the signal peak rather than parts per million.
const convolverRelTolerance = 1e-5

// decayingIR builds a deterministic, broadband, exponentially decaying impulse
// response of n taps. Unlike a handful of hand-written coefficients it spans
// several partitions, so the overlap-add tail state actually has to be right.
func decayingIR(n int) []float32 {
	ir := make([]float32, n)
	for i := range ir {
		ir[i] = float32(math.Sin(float64(i)*0.11) * math.Exp(-float64(i)/(float64(n)/4)))
	}
	return ir
}

// convolverTestSine is the input signal used throughout: long enough to cross
// many partition boundaries and not a multiple of any chunk size below.
func convolverTestSine(n int) []float32 {
	x := make([]float32, n)
	for i := range x {
		x[i] = float32(math.Sin(float64(i)*0.07)) * 0.8
	}
	return x
}

func peakAbs(x []float32) float64 {
	peak := 0.0
	for _, v := range x {
		if a := math.Abs(float64(v)); a > peak {
			peak = a
		}
	}
	return peak
}

// relDiff is maxAbsDiff normalised by the reference peak, so the bound above is
// independent of the input amplitude and of the IR gain.
func relDiff(got, ref []float32) float64 {
	peak := peakAbs(ref)
	if peak == 0 {
		return maxAbsDiff(got, ref)
	}
	return maxAbsDiff(got, ref) / peak
}

// streamStereo drives a SoundboardConvolver in fixed chunks and returns the two
// deinterleaved channels.
func streamStereo(c *SoundboardConvolver, input []float32, chunk int) (left, right []float32) {
	left = make([]float32, 0, len(input))
	right = make([]float32, 0, len(input))
	for off := 0; off < len(input); off += chunk {
		end := min(off+chunk, len(input))
		out := c.Process(input[off:end])
		for i := range end - off {
			left = append(left, out[i*2])
			right = append(right, out[i*2+1])
		}
	}
	return left, right
}

// streamMono drives a BodyConvolver in fixed chunks.
func streamMono(c *BodyConvolver, input []float32, chunk int) []float32 {
	out := make([]float32, 0, len(input))
	for off := 0; off < len(input); off += chunk {
		end := min(off+chunk, len(input))
		out = append(out, c.Process(input[off:end])...)
	}
	return out
}

// TestConvolverMatchesDirectConvolutionLongIR validates the multi-partition
// overlap-add itself. The pre-existing direct-convolution test used IRs of 5 and
// 3 taps, shorter than one partition, so the tail state that carries a partition
// boundary was never exercised at all — and BodyConvolver had no
// direct-convolution test whatsoever.
func TestConvolverMatchesDirectConvolutionLongIR(t *testing.T) {
	input := convolverTestSine(4096)

	for _, irLen := range []int{512, 8192} {
		t.Run(fmt.Sprintf("taps%d", irLen), func(t *testing.T) {
			leftIR := decayingIR(irLen)
			rightIR := decayingIR(irLen / 2)
			wantL := directConvolve(input, leftIR)[:len(input)]
			wantR := directConvolve(input, rightIR)[:len(input)]

			c := NewSoundboardConvolver(48000)
			c.SetIR(leftIR, rightIR)
			gotL, gotR := streamStereo(c, input, 128)
			if d := relDiff(gotL, wantL); d > convolverRelTolerance {
				t.Errorf("soundboard left relative error %g exceeds %g", d, convolverRelTolerance)
			}
			if d := relDiff(gotR, wantR); d > convolverRelTolerance {
				t.Errorf("soundboard right relative error %g exceeds %g", d, convolverRelTolerance)
			}

			b := NewBodyConvolver(48000)
			b.SetIR(leftIR)
			if d := relDiff(streamMono(b, input, 128), wantL); d > convolverRelTolerance {
				t.Errorf("body relative error %g exceeds %g", d, convolverRelTolerance)
			}
		})
	}
}

// TestConvolverBlockSizeContinuity is the regression test for the defect
// described above: the same stream, chopped every way, must convolve the same.
//
// The chunk sizes cover sub-partition calls (1, 63, 64, 100), the render quantum
// (128), whole multiples of it (256) and a size that is neither a divisor nor a
// multiple, so the partial partition lands at a different offset on every call
// (333).
func TestConvolverBlockSizeContinuity(t *testing.T) {
	input := convolverTestSine(3000)
	leftIR := decayingIR(512)
	rightIR := decayingIR(1024)
	wantL := directConvolve(input, leftIR)[:len(input)]
	wantR := directConvolve(input, rightIR)[:len(input)]

	for _, chunk := range []int{1, 63, 64, 100, 128, 256, 333} {
		t.Run(fmt.Sprintf("chunk%d", chunk), func(t *testing.T) {
			c := NewSoundboardConvolver(48000)
			c.SetIR(leftIR, rightIR)
			gotL, gotR := streamStereo(c, input, chunk)
			if d := relDiff(gotL, wantL); d > convolverRelTolerance {
				t.Errorf("soundboard left relative error %g exceeds %g at chunk %d", d, convolverRelTolerance, chunk)
			}
			if d := relDiff(gotR, wantR); d > convolverRelTolerance {
				t.Errorf("soundboard right relative error %g exceeds %g at chunk %d", d, convolverRelTolerance, chunk)
			}

			b := NewBodyConvolver(48000)
			b.SetIR(leftIR)
			if d := relDiff(streamMono(b, input, chunk), wantL); d > convolverRelTolerance {
				t.Errorf("body relative error %g exceeds %g at chunk %d", d, convolverRelTolerance, chunk)
			}
		})
	}
}

// TestConvolverImpulseRecoversIR pins the zero-latency alignment: a unit impulse
// at sample 0 must reproduce the IR from sample 0 on, whatever the chunking.
// Process(n) returns n samples with no added latency and radiation_test.go
// depends on that, so a buffered implementation that delayed the stream by a
// partition would show up here first.
func TestConvolverImpulseRecoversIR(t *testing.T) {
	ir := decayingIR(300)
	input := make([]float32, 1024)
	input[0] = 1.0

	for _, chunk := range []int{1, 100, 128} {
		t.Run(fmt.Sprintf("chunk%d", chunk), func(t *testing.T) {
			c := NewSoundboardConvolver(48000)
			c.SetIR(ir, ir)
			gotL, gotR := streamStereo(c, input, chunk)
			if d := relDiff(gotL[:len(ir)], ir); d > convolverRelTolerance {
				t.Errorf("left impulse response relative error %g exceeds %g", d, convolverRelTolerance)
			}
			if d := relDiff(gotR[:len(ir)], ir); d > convolverRelTolerance {
				t.Errorf("right impulse response relative error %g exceeds %g", d, convolverRelTolerance)
			}

			b := NewBodyConvolver(48000)
			b.SetIR(ir)
			if d := relDiff(streamMono(b, input, chunk)[:len(ir)], ir); d > convolverRelTolerance {
				t.Errorf("body impulse response relative error %g exceeds %g", d, convolverRelTolerance)
			}
		})
	}
}

// TestConvolverResetMidStreamStartsFresh checks that Reset really drops all of
// the streaming state — the overlap tail, the partial partition and the input
// history the off-grid path reads — and not only the parts the old
// implementation had. A convolver reset mid-stream must behave exactly like a
// newly constructed one, including when the reset lands off a partition
// boundary.
func TestConvolverResetMidStreamStartsFresh(t *testing.T) {
	ir := decayingIR(512)
	first := convolverTestSine(700)
	second := convolverTestSine(1000)
	want := directConvolve(second, ir)[:len(second)]

	for _, chunk := range []int{100, 128} {
		t.Run(fmt.Sprintf("chunk%d", chunk), func(t *testing.T) {
			c := NewSoundboardConvolver(48000)
			c.SetIR(ir, ir)
			_, _ = streamStereo(c, first, chunk)
			c.Reset()
			gotL, _ := streamStereo(c, second, chunk)
			if d := relDiff(gotL, want); d > convolverRelTolerance {
				t.Errorf("soundboard relative error %g after mid-stream reset exceeds %g", d, convolverRelTolerance)
			}

			b := NewBodyConvolver(48000)
			b.SetIR(ir)
			_ = streamMono(b, first, chunk)
			b.Reset()
			if d := relDiff(streamMono(b, second, chunk), want); d > convolverRelTolerance {
				t.Errorf("body relative error %g after mid-stream reset exceeds %g", d, convolverRelTolerance)
			}
		})
	}
}

// decayingCosineIR is decayingIR with a non-zero first tap, so that the
// degenerate one-tap case below is a real filter rather than silence.
func decayingCosineIR(n int) []float32 {
	ir := make([]float32, n)
	for i := range ir {
		ir[i] = float32(math.Cos(float64(i)*0.11) * math.Exp(-float64(i)/(float64(n)/4)))
	}
	return ir
}

// TestConvolverShortIRBlockSizeContinuity is the same continuity contract as
// above, swept over IRs that fit inside a single partition.
//
// Those were the blind spot of the sweep above, which only uses 512 and 1024
// taps: the input history was sized from the tail stage's priming window, and
// an IR of at most partSize taps needs no tail stage at all, so the history
// collapsed to one partition. emitDirect reaches back len(ir)-1 samples from
// the end of a partial block that is itself up to partSize-1 samples long, so
// anything past one partition was silently read as the zero fill. With a
// 128-tap IR that was wrong even for a single 1200-sample Process call
// (max abs diff 0.0417 against directConvolve), and worst at chunk 255, where
// a full partition is followed by a 127-sample partial one (2.08).
//
// The IR lengths straddle the partition size in both directions and include the
// degenerate single tap; the chunk sizes add 200 and 255 to the sweep above,
// both of which leave a large partial partition after a complete one.
func TestConvolverShortIRBlockSizeContinuity(t *testing.T) {
	input := convolverTestSine(1200)

	for _, irLen := range []int{1, 32, 64, 127, 128, 129} {
		for _, chunk := range []int{1, 63, 64, 100, 128, 200, 255, 256, 333, 1200} {
			t.Run(fmt.Sprintf("taps%d/chunk%d", irLen, chunk), func(t *testing.T) {
				leftIR := decayingCosineIR(irLen)
				rightIR := decayingIR(irLen + 1)
				wantL := directConvolve(input, leftIR)[:len(input)]
				wantR := directConvolve(input, rightIR)[:len(input)]

				c := NewSoundboardConvolver(48000)
				c.SetIR(leftIR, rightIR)
				gotL, gotR := streamStereo(c, input, chunk)
				if d := relDiff(gotL, wantL); d > convolverRelTolerance {
					t.Errorf("soundboard left relative error %g exceeds %g", d, convolverRelTolerance)
				}
				if d := relDiff(gotR, wantR); d > convolverRelTolerance {
					t.Errorf("soundboard right relative error %g exceeds %g", d, convolverRelTolerance)
				}

				b := NewBodyConvolver(48000)
				b.SetIR(leftIR)
				if d := relDiff(streamMono(b, input, chunk), wantL); d > convolverRelTolerance {
					t.Errorf("body relative error %g exceeds %g", d, convolverRelTolerance)
				}
			})
		}
	}
}
