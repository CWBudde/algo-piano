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

	leftOLA  *dspconv.OverlapAdd
	rightOLA *dspconv.OverlapAdd

	tailLeft  []float64
	tailRight []float64
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

	in64 := toFloat64(input)

	leftFull, errL := c.leftOLA.Process(in64)
	rightFull, errR := c.rightOLA.Process(in64)
	if errL != nil || errR != nil {
		for i, s := range input {
			output[i*2] = s
			output[i*2+1] = s
		}
		return output
	}

	outL, newTailL := overlapAddBlock(leftFull, c.tailLeft, len(input))
	outR, newTailR := overlapAddBlock(rightFull, c.tailRight, len(input))
	c.tailLeft = newTailL
	c.tailRight = newTailR

	for i := 0; i < len(input); i++ {
		output[i*2] = float32(outL[i])
		output[i*2+1] = float32(outR[i])
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

	left64 := toFloat64(leftIR)
	right64 := toFloat64(rightIR)

	leftOLA, errL := dspconv.NewOverlapAdd(left64, c.partSize)
	rightOLA, errR := dspconv.NewOverlapAdd(right64, c.partSize)
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
		for i := 0; i < frames; i++ {
			v := buf.Data[i]
			left[i] = v
			right[i] = v
		}
	} else {
		for i := 0; i < frames; i++ {
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
	tailLen := c.irLen - 1
	if tailLen < 0 {
		tailLen = 0
	}
	c.tailLeft = make([]float64, tailLen)
	c.tailRight = make([]float64, tailLen)
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
