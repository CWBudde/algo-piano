package piano

import dspcore "github.com/cwbudde/algo-dsp/dsp/core"

// StringWaveguide implements the digital waveguide string model.
type StringWaveguide struct {
	sampleRate  float32
	f0          float32
	delayLength float32
	delayLine   []float32
	writePos    int

	reflection       float32
	baseReflection   float32
	damperReflection float32
	damperEngaged    bool

	lowpassCoeff float32
	loopState    float32

	dispersionCoeff float32
	dispersionX1    float32
	dispersionY1    float32
	dispersionX2    float32
	dispersionY2    float32
}

// NewStringWaveguide creates a new string waveguide.
func NewStringWaveguide(sampleRate int, f0 float32) *StringWaveguide {
	s := &StringWaveguide{
		sampleRate:       float32(sampleRate),
		f0:               f0,
		reflection:       0.9999,
		baseReflection:   0.9999,
		damperReflection: 0.92,
		damperEngaged:    false,
		lowpassCoeff:     0.0,
		dispersionCoeff:  0.0,
	}

	s.delayLength = s.sampleRate / s.f0
	intDelay := int(s.delayLength)
	if intDelay < 2 {
		intDelay = 2
	}
	s.delayLine = make([]float32, intDelay+4)

	return s
}

// Process renders one sample from the string and advances the simulation.
func (s *StringWaveguide) Process() float32 {
	delayedSample := s.readDelayFractional(s.delayLength)
	dispersed := s.processDispersion(delayedSample)
	loopSample := s.processLoopLoss(dispersed)
	output := delayedSample

	s.delayLine[s.writePos] = loopSample
	s.writePos = (s.writePos + 1) % len(s.delayLine)
	return output
}

// Excite applies an excitation to the string.
func (s *StringWaveguide) Excite(force float32) {
	s.ExciteAtPosition(force, 0.2)
}

// ExciteAtPosition applies an excitation at a fractional string position [0,1].
func (s *StringWaveguide) ExciteAtPosition(force float32, strikePos float32) {
	if strikePos < 0.01 {
		strikePos = 0.01
	}
	if strikePos > 0.99 {
		strikePos = 0.99
	}

	basePos := (s.writePos + int(float32(len(s.delayLine))*strikePos)) % len(s.delayLine)
	width := int(float32(len(s.delayLine)) * (0.04 + 0.22*strikePos))
	if width < 4 {
		width = 4
	}
	if width > len(s.delayLine)-1 {
		width = len(s.delayLine) - 1
	}

	for i := 0; i < width; i++ {
		pos := (basePos + i) % len(s.delayLine)
		amp := force * (float32(i)/float32(width-1) - 0.5) * 2.0
		s.delayLine[pos] += amp
	}
}

// InjectForceAtPosition injects a single-sample force at a fractional string position.
func (s *StringWaveguide) InjectForceAtPosition(force float32, strikePos float32) {
	if strikePos < 0.01 {
		strikePos = 0.01
	}
	if strikePos > 0.99 {
		strikePos = 0.99
	}
	pos := (s.writePos + int(float32(len(s.delayLine))*strikePos)) % len(s.delayLine)
	s.delayLine[pos] += force
}

// SetLoopLoss configures loop loss.
func (s *StringWaveguide) SetLoopLoss(gain float32, highFreqDamping float32) {
	if gain <= 0 {
		gain = 0.0001
	}
	if gain > 1.0 {
		gain = 1.0
	}
	if highFreqDamping < 0.0 {
		highFreqDamping = 0.0
	}
	if highFreqDamping > 0.99 {
		highFreqDamping = 0.99
	}
	s.reflection = gain
	s.baseReflection = gain
	if s.damperEngaged {
		s.reflection = s.damperReflection
	}
	s.lowpassCoeff = highFreqDamping
}

// SetDamper toggles aggressive damping for release behavior.
func (s *StringWaveguide) SetDamper(engaged bool) {
	s.damperEngaged = engaged
	if engaged {
		s.reflection = s.damperReflection
		return
	}
	s.reflection = s.baseReflection
}

// SetDispersion maps a small inharmonicity amount [0,1] to allpass coefficient.
func (s *StringWaveguide) SetDispersion(amount float32) {
	if amount < 0.0 {
		amount = 0.0
	}
	if amount > 1.0 {
		amount = 1.0
	}
	s.dispersionCoeff = -0.85 * amount
}

func (s *StringWaveguide) processLoopLoss(input float32) float32 {
	lp := (1.0-s.lowpassCoeff)*input + s.lowpassCoeff*s.loopState
	lp = float32(dspcore.FlushDenormals(float64(lp)))
	s.loopState = lp
	return float32(dspcore.FlushDenormals(float64(lp * s.reflection)))
}

func (s *StringWaveguide) processDispersion(input float32) float32 {
	a := s.dispersionCoeff
	if a == 0.0 {
		return input
	}
	y := -a*input + s.dispersionX1 + a*s.dispersionY1
	s.dispersionX1 = input
	s.dispersionY1 = y

	z := -a*y + s.dispersionX2 + a*s.dispersionY2
	s.dispersionX2 = y
	s.dispersionY2 = z
	return z
}

func (s *StringWaveguide) readDelayFractional(delay float32) float32 {
	intDelay := int(delay)
	frac := delay - float32(intDelay)
	readPos1 := (s.writePos - intDelay + len(s.delayLine)) % len(s.delayLine)
	readPos2 := (s.writePos - intDelay - 1 + len(s.delayLine)) % len(s.delayLine)
	sample1 := s.delayLine[readPos1]
	sample2 := s.delayLine[readPos2]
	return sample1*(1.0-frac) + sample2*frac
}
