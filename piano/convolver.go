package piano

import (
	"bytes"
	"fmt"
	"io"
	"os"

	dspconv "github.com/cwbudde/algo-dsp/dsp/conv"
	dspresample "github.com/cwbudde/algo-dsp/dsp/resample"
	"github.com/cwbudde/wav"
)

const DefaultIRWavPath = "assets/ir/default_96k.wav"

// ApplyDefaultRoomIR installs the shipped IR as the *room* stage of the linear
// radiation path when a preset names no impulse response at all.
//
// The locked chain is `string-bank bridge mix -> body IR -> room IR`, so the
// only sensible slot for a single shipped reverberant IR is the room stage.
// Command-line tools used to default the deprecated Params.IRWavPath instead,
// which made the legacy single-IR path the *default* rather than a fallback for
// presets that still spell it that way.
//
// The remap of the deprecated mix fields below is what keeps the swap
// behaviour-preserving: filling IRWavPath used to trigger exactly this mapping
// inside the render path (see resolveRadiationMix), so applying it here yields
// bit-identical output while leaving the engine reading dual-IR semantics only.
//
// Presets that set any IR path themselves — legacy or dual — are left untouched,
// so an explicitly configured IRWavPath is still honoured as a fallback.
func ApplyDefaultRoomIR(params *Params) {
	if params == nil {
		return
	}
	if params.BodyIRWavPath != "" || params.RoomIRWavPath != "" || params.IRWavPath != "" {
		return
	}
	params.RoomIRWavPath = DefaultIRWavPath
	params.BodyDryMix = params.IRDryMix
	params.RoomWetMix = params.IRWetMix
	params.RoomGain = params.IRGain
	params.BodyIRGain = 1.0
}

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

// SetIRFromWAV loads a mono/stereo IR from a WAV file.
func (c *SoundboardConvolver) SetIRFromWAV(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	return c.SetIRFromReader(f, path)
}

// SetIRFromBytes loads a mono/stereo IR from in-memory WAV bytes.
func (c *SoundboardConvolver) SetIRFromBytes(data []byte) error {
	return c.SetIRFromReader(bytes.NewReader(data), "<bytes>")
}

// SetIRFromReader loads a mono/stereo IR from a WAV stream.
// name is only used to describe the source in error messages.
func (c *SoundboardConvolver) SetIRFromReader(r io.ReadSeeker, name string) error {
	dec := wav.NewDecoder(r)
	if !dec.IsValidFile() {
		return fmt.Errorf("invalid wav file: %s", name)
	}
	buf, err := dec.FullPCMBuffer()
	if err != nil {
		return err
	}
	if buf == nil || buf.Format == nil || buf.Format.NumChannels < 1 {
		return fmt.Errorf("invalid wav buffer: %s", name)
	}

	numCh := buf.Format.NumChannels
	srcRate := buf.Format.SampleRate
	if srcRate <= 0 {
		return fmt.Errorf("invalid wav sample-rate: %d", srcRate)
	}
	frames := len(buf.Data) / numCh
	if frames == 0 {
		return fmt.Errorf("empty wav data: %s", name)
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

// BodyConvolver implements mono-to-mono partitioned convolution for body coloration.
type BodyConvolver struct {
	sampleRate int
	partSize   int
	ola        *dspconv.StreamingOverlapAddT[float32, complex64]
	out        []float32
}

// NewBodyConvolver creates a new mono body convolver with a passthrough IR.
func NewBodyConvolver(sampleRate int) *BodyConvolver {
	c := &BodyConvolver{
		sampleRate: sampleRate,
		partSize:   128,
	}
	c.SetIR([]float32{1.0})
	return c
}

// Process convolves mono input with the body IR and returns mono output.
func (c *BodyConvolver) Process(input []float32) []float32 {
	output := make([]float32, len(input))
	if len(input) == 0 {
		return output
	}

	processed := 0
	for processed < len(input) {
		blockEnd := processed + c.partSize
		if blockEnd > len(input) {
			blockEnd = len(input)
		}
		blockLen := blockEnd - processed
		block := input[processed:blockEnd]

		if blockLen < c.partSize {
			padded := make([]float32, c.partSize)
			copy(padded, block)
			block = padded
		}

		if err := c.ola.ProcessBlockTo(c.out, block); err != nil {
			copy(output[processed:blockEnd], input[processed:blockEnd])
			processed = blockEnd
			continue
		}

		copy(output[processed:blockEnd], c.out[:blockLen])
		processed = blockEnd
	}
	return output
}

// SetIR sets the mono body impulse response.
func (c *BodyConvolver) SetIR(ir []float32) {
	if len(ir) == 0 {
		ir = []float32{1.0}
	}
	ola, err := dspconv.NewStreamingOverlapAdd32(ir, c.partSize)
	if err != nil {
		return
	}
	c.ola = ola
	c.out = make([]float32, c.partSize)
	c.Reset()
}

// SetIRFromWAV loads a mono IR from a WAV file, resampling if needed.
func (c *BodyConvolver) SetIRFromWAV(path string, targetRate int) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	return c.SetIRFromReader(f, path, targetRate)
}

// SetIRFromBytes loads a mono IR from in-memory WAV bytes, resampling if needed.
func (c *BodyConvolver) SetIRFromBytes(data []byte, targetRate int) error {
	return c.SetIRFromReader(bytes.NewReader(data), "<bytes>", targetRate)
}

// SetIRFromReader loads a mono IR from a WAV stream, resampling if needed.
// name is only used to describe the source in error messages.
func (c *BodyConvolver) SetIRFromReader(r io.ReadSeeker, name string, targetRate int) error {
	dec := wav.NewDecoder(r)
	if !dec.IsValidFile() {
		return fmt.Errorf("invalid wav file: %s", name)
	}
	buf, err := dec.FullPCMBuffer()
	if err != nil {
		return err
	}
	if buf == nil || buf.Format == nil || buf.Format.NumChannels < 1 {
		return fmt.Errorf("invalid wav buffer: %s", name)
	}

	srcRate := buf.Format.SampleRate
	numCh := buf.Format.NumChannels
	frames := len(buf.Data) / numCh
	if frames == 0 {
		return fmt.Errorf("empty wav data: %s", name)
	}

	// Mix to mono.
	mono := make([]float32, frames)
	for i := range frames {
		var sum float32
		for ch := 0; ch < numCh; ch++ {
			sum += buf.Data[i*numCh+ch]
		}
		mono[i] = sum / float32(numCh)
	}

	if srcRate != targetRate {
		r, err := dspresample.NewForRates(
			float64(srcRate),
			float64(targetRate),
			dspresample.WithQuality(dspresample.QualityBest),
		)
		if err != nil {
			return err
		}
		in64 := make([]float64, len(mono))
		for i, v := range mono {
			in64[i] = float64(v)
		}
		out64 := r.Process(in64)
		mono = make([]float32, len(out64))
		for i, v := range out64 {
			mono[i] = float32(v)
		}
	}

	c.SetIR(mono)
	return nil
}

// Reset clears convolver history.
func (c *BodyConvolver) Reset() {
	if c.ola != nil {
		c.ola.Reset()
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
