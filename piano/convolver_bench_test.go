package piano

import (
	"fmt"
	"math"
	"testing"
)

// benchmarkIR builds a deterministic decaying-noise impulse response of the
// requested length. Content only has to be non-degenerate; the cost of
// partitioned convolution depends on length and partition size, not on values.
func benchmarkIR(n int) []float32 {
	ir := make([]float32, n)
	state := uint32(0x9e3779b9)
	for i := range ir {
		v := float32(xorshift32(&state))*2.3283064e-10*2.0 - 1.0
		ir[i] = v * float32(math.Exp(-4.0*float64(i)/float64(n)))
	}
	return ir
}

func benchmarkInput(n int) []float32 {
	in := make([]float32, n)
	for i := range in {
		in[i] = float32(math.Sin(float64(i)*0.037)) * 0.5
	}
	return in
}

// irLengthCases covers the range the engine actually sees: a short body IR of a
// few milliseconds up to a four-second stereo room IR.
func irLengthCases() []struct {
	name string
	n    int
} {
	return []struct {
		name string
		n    int
	}{
		{"ir128", 128},
		{"ir1k", 1024},
		{"ir8k", 8192},
		{"ir48k_1s", 48000},
		{"ir192k_4s", 192000},
	}
}

// BenchmarkSoundboardConvolverIRLength measures the stereo room convolver at
// the production partition size (128) across IR lengths, one 128-frame block
// per iteration — the shape of a single audio callback.
func BenchmarkSoundboardConvolverIRLength(b *testing.B) {
	input := benchmarkInput(128)
	for _, tc := range irLengthCases() {
		b.Run(tc.name, func(b *testing.B) {
			c := NewSoundboardConvolver(48000)
			ir := benchmarkIR(tc.n)
			c.SetIR(ir, benchmarkIR(tc.n))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = c.Process(input)
			}
		})
	}
}

// BenchmarkBodyConvolverIRLength is the mono body-colouration equivalent.
func BenchmarkBodyConvolverIRLength(b *testing.B) {
	input := benchmarkInput(128)
	for _, tc := range irLengthCases() {
		b.Run(tc.name, func(b *testing.B) {
			c := NewBodyConvolver(48000)
			c.SetIR(benchmarkIR(tc.n))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = c.Process(input)
			}
		})
	}
}

// convolverBenchAudioFrames is the amount of audio each partition-size
// iteration renders, and convolverBenchCallbackFrames is the size of the
// individual Process calls it is delivered in. Keeping the total fixed means
// every partition size does the same acoustic work, so the numbers compare
// directly instead of measuring how much audio one call happens to consume.
//
// The audio is delivered in 128-frame calls because that is the shape the
// engine actually runs in. It matters: SoundboardConvolver.Process pads any
// call shorter than partSize up to partSize, advances a whole overlap-add
// partition, and then returns only the first len(input) samples. Handing the
// convolver all 4096 frames in one call would therefore measure an offline
// throughput number that no realtime caller can obtain, and dividing it by 32
// would understate the true per-callback cost for every partSize above 128.
const (
	convolverBenchAudioFrames    = 4096
	convolverBenchCallbackFrames = 128
)

// processInCallbacks feeds input to fn in convolverBenchCallbackFrames-sized
// slices, i.e. exactly as the audio callback would.
func processInCallbacks(fn func([]float32) []float32, input []float32) {
	for off := 0; off+convolverBenchCallbackFrames <= len(input); off += convolverBenchCallbackFrames {
		_ = fn(input[off : off+convolverBenchCallbackFrames])
	}
}

// BenchmarkSoundboardConvolverPartitionSize sweeps the partition size at a
// fixed one-second IR, driving the convolver with 128-frame calls throughout.
// Larger partitions mean fewer, bigger FFTs per unit of audio in an offline
// setting; under a fixed 128-frame callback they instead mean one full,
// mostly-discarded partition per callback.
func BenchmarkSoundboardConvolverPartitionSize(b *testing.B) {
	input := benchmarkInput(convolverBenchAudioFrames)
	ir := benchmarkIR(48000)
	for _, part := range []int{64, 128, 256, 512, 1024} {
		b.Run(fmt.Sprintf("part%d", part), func(b *testing.B) {
			c := NewSoundboardConvolver(48000)
			c.partSize = part
			c.SetIR(ir, ir)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				processInCallbacks(c.Process, input)
			}
		})
	}
}

// BenchmarkBodyConvolverPartitionSize is the mono equivalent, at the shorter IR
// length a body response actually uses.
func BenchmarkBodyConvolverPartitionSize(b *testing.B) {
	input := benchmarkInput(convolverBenchAudioFrames)
	ir := benchmarkIR(8192)
	for _, part := range []int{64, 128, 256, 512, 1024} {
		b.Run(fmt.Sprintf("part%d", part), func(b *testing.B) {
			c := NewBodyConvolver(48000)
			c.partSize = part
			c.SetIR(ir)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				processInCallbacks(c.Process, input)
			}
		})
	}
}
