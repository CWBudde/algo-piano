package piano

import (
	"fmt"
	"os"

	dspconv "github.com/cwbudde/algo-dsp/dsp/conv"
	dspresample "github.com/cwbudde/algo-dsp/dsp/resample"
	"github.com/cwbudde/wav"
)

const DefaultIRWavPath = "assets/ir/default_96k.wav"

// SoundboardConvolver implements partitioned convolution for the soundboard/body.
type SoundboardConvolver struct {
	sampleRate int
	partSize   int
	irLen      int

	leftOLA  *dspconv.StreamingOverlapAddT[float32, complex64]
	rightOLA *dspconv.StreamingOverlapAddT[float32, complex64]

	// Pre-allocated buffers for zero-allocation processing
	leftOut  []float32
	rightOut []float32
}

// NewSoundboardConvolver creates a new soundboard convolver.
func NewSoundboardConvolver(sampleRate int) *SoundboardConvolver {
	c := &SoundboardConvolver{
		sampleRate: sampleRate,
		partSize:   128,
	}
	c.SetIR([]float32{1.0}, []float32{1.0})
	return c
}

// Process convolves mono input with IR and returns stereo output.
func (c *SoundboardConvolver) Process(input []float32) []float32 {
	output := make([]float32, len(input)*2)
	if len(input) == 0 {
		return output
	}

	// Handle arbitrary input lengths by processing in partSize blocks
	processed := 0

	for processed < len(input) {
		blockEnd := processed + c.partSize
		if blockEnd > len(input) {
			blockEnd = len(input)
		}
		blockLen := blockEnd - processed
		block := input[processed:blockEnd]

		// Pad to partSize if needed (for last block)
		if blockLen < c.partSize {
			padded := make([]float32, c.partSize)
			copy(padded, block)
			block = padded
		}

		// Process block with zero-allocation streaming convolvers
		errL := c.leftOLA.ProcessBlockTo(c.leftOut, block)
		errR := c.rightOLA.ProcessBlockTo(c.rightOut, block)
		if errL != nil || errR != nil {
			// Fallback: pass through for this block
			for i := 0; i < blockLen; i++ {
				output[(processed+i)*2] = input[processed+i]
				output[(processed+i)*2+1] = input[processed+i]
			}
			processed = blockEnd
			continue
		}

		// Interleave stereo output for this block
		for i := 0; i < blockLen; i++ {
			output[(processed+i)*2] = c.leftOut[i]
			output[(processed+i)*2+1] = c.rightOut[i]
		}

		processed = blockEnd
	}

	return output
}

// SetIR configures left/right impulse responses.
func (c *SoundboardConvolver) SetIR(leftIR []float32, rightIR []float32) {
	if len(leftIR) == 0 {
		leftIR = []float32{1.0}
	}
	if len(rightIR) == 0 {
		rightIR = []float32{1.0}
	}

	leftOLA, errL := dspconv.NewStreamingOverlapAdd32(leftIR, c.partSize)
	rightOLA, errR := dspconv.NewStreamingOverlapAdd32(rightIR, c.partSize)
	if errL != nil || errR != nil {
		return
	}
	c.leftOLA = leftOLA
	c.rightOLA = rightOLA
	c.irLen = len(leftIR)
	if len(rightIR) > c.irLen {
		c.irLen = len(rightIR)
	}
	if c.irLen < 1 {
		c.irLen = 1
	}

	// Allocate output buffers
	c.leftOut = make([]float32, c.partSize)
	c.rightOut = make([]float32, c.partSize)

	c.Reset()
}

// SetIRFromWAV loads a mono/stereo IR from WAV.
func (c *SoundboardConvolver) SetIRFromWAV(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	dec := wav.NewDecoder(f)
	if !dec.IsValidFile() {
		return fmt.Errorf("invalid wav file: %s", path)
	}
	buf, err := dec.FullPCMBuffer()
	if err != nil {
		return err
	}
	if buf == nil || buf.Format == nil || buf.Format.NumChannels < 1 {
		return fmt.Errorf("invalid wav buffer: %s", path)
	}

	numCh := buf.Format.NumChannels
	srcRate := buf.Format.SampleRate
	if srcRate <= 0 {
		return fmt.Errorf("invalid wav sample-rate: %d", srcRate)
	}
	frames := len(buf.Data) / numCh
	if frames == 0 {
		return fmt.Errorf("empty wav data: %s", path)
	}

	left := make([]float32, frames)
	right := make([]float32, frames)
	if numCh == 1 {
		for i := range frames {
			v := buf.Data[i]
			left[i] = v
			right[i] = v
		}
	} else {
		for i := range frames {
			left[i] = buf.Data[i*numCh]
			right[i] = buf.Data[i*numCh+1]
		}
	}

	left, err = c.resampleIfNeeded(left, srcRate)
	if err != nil {
		return err
	}
	right, err = c.resampleIfNeeded(right, srcRate)
	if err != nil {
		return err
	}
	c.SetIR(left, right)
	return nil
}

// Reset clears convolver history and overlap buffers.
func (c *SoundboardConvolver) Reset() {
	if c.leftOLA != nil {
		c.leftOLA.Reset()
	}
	if c.rightOLA != nil {
		c.rightOLA.Reset()
	}
}

func (c *SoundboardConvolver) resampleIfNeeded(in []float32, inRate int) ([]float32, error) {
	if inRate == c.sampleRate {
		return in, nil
	}
	r, err := dspresample.NewForRates(
		float64(inRate),
		float64(c.sampleRate),
		dspresample.WithQuality(dspresample.QualityBest),
	)
	if err != nil {
		return nil, err
	}

	in64 := make([]float64, len(in))
	for i, v := range in {
		in64[i] = float64(v)
	}
	out64 := r.Process(in64)
	out := make([]float32, len(out64))
	for i, v := range out64 {
		out[i] = float32(v)
	}
	return out, nil
}
