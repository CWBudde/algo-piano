package piano

import (
	"math"

	dspcore "github.com/cwbudde/algo-dsp/dsp/core"
)

const (
	// delayHeadroom is how many slots the delay line is allocated beyond the
	// integer part of the loop delay. readDelayFractional taps at
	// writePos-intDelay and writePos-intDelay-1, which modulo the buffer length
	// are the slots delayHeadroom and delayHeadroom-1 *ahead* of the write
	// pointer, so the headroom is exactly the window in which freshly injected
	// energy is still readable. It must be at least 2 for the two taps to fit.
	delayHeadroom = 4

	// minIntDelay is the smallest integer delay the buffer is sized for. It
	// keeps a few injectable slots above delayHeadroom even for a fundamental
	// far above anything the keyboard can produce (MIDI 108 is 11.5 samples at
	// 48 kHz, so this guard is unreachable in practice and exists so that an
	// out-of-range f0 degrades instead of collapsing).
	minIntDelay = 4

	// dcBlockCutoffHz is the -3 dB corner of the DC blocker that sits in the
	// string loop. The loop filter of processLoopLoss has unity DC gain, so
	// without this the loop is a leaky integrator for DC: the per-sample
	// crossfeed injection of RingingStringGroup.processSample outruns the
	// per-round-trip loss and the offset diverges (measured +108 at MIDI 108
	// once the injection fix let energy into those strings at all).
	// A real string cannot sustain DC, so blocking it in the loop is the
	// physical model, not a patch. The corner sits well below A0 (27.5 Hz), and
	// the phase lead it costs is compensated in dcBlockPhaseDelay, so it leaves
	// neither an audible tilt nor a tuning error.
	dcBlockCutoffHz = 2.0
)

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
	damperAmount     float32

	lowpassCoeff float32
	loopState    float32

	dispersionCoeff float32
	dispersionX1    float32
	dispersionY1    float32
	dispersionX2    float32
	dispersionY2    float32

	dcBlockPole float32
	dcBlockGain float32
	dcBlockX1   float32
	dcBlockY1   float32
}

// NewStringWaveguide creates a new string waveguide.
func NewStringWaveguide(sampleRate int, f0 float32) *StringWaveguide {
	s := &StringWaveguide{
		sampleRate:       float32(sampleRate),
		f0:               f0,
		reflection:       0.9999,
		baseReflection:   0.9999,
		damperReflection: 0.92,
		damperAmount:     0,
		lowpassCoeff:     0.0,
		dispersionCoeff:  0.0,
	}

	// Standard normalised DC blocker: H(z) = g*(1 - z^-1) / (1 - p*z^-1) with
	// g = (1+p)/2. The normalisation matters inside a feedback loop: the raw
	// (1 - z^-1)/(1 - p*z^-1) form peaks at 2/(1+p) > 1 at Nyquist, which would
	// push the loop gain above the 0.9999 reflection and make the string blow
	// up at half the sample rate. With g the magnitude is exactly 1 at Nyquist
	// and below 1 everywhere else, so the loop can only ever lose energy.
	s.dcBlockPole = 1 - 2*math.Pi*dcBlockCutoffHz/s.sampleRate
	s.dcBlockGain = (1 + s.dcBlockPole) / 2

	s.delayLength = s.sampleRate/s.f0 + s.dcBlockPhaseDelay()
	intDelay := int(s.delayLength)
	if intDelay < minIntDelay {
		intDelay = minIntDelay
	}
	s.delayLine = make([]float32, intDelay+delayHeadroom)

	return s
}

// dcBlockPhaseDelay returns how many samples the delay line has to grow to
// cancel the phase lead the DC blocker adds to the loop, evaluated at f0.
//
// The blocker leads by roughly fc/f radians at f >> fc, which shortens the loop
// round trip by fc/(2*pi*f^2) seconds and therefore sharpens every string by a
// flat fc/(2*pi) Hz - independent of pitch, so it is nearly inaudible at the top
// and badly wrong at the bottom. Measured before this compensation existed, with
// fc = 2 Hz: every note from MIDI 21 to 108 came out +0.31 Hz sharp, which is
// +19.6 cents at A0 and +0.2 cents at C8. Adding the blocker's own phase delay
// back into the delay line removes the tilt entirely: measured 2026-08-21 the
// compensated core is within 0.64 cents everywhere below MIDI 104 and matches
// the pre-DC-blocker core to better than 0.01 cents note for note.
func (s *StringWaveguide) dcBlockPhaseDelay() float32 {
	w := 2 * math.Pi * float64(s.f0) / float64(s.sampleRate)
	if w <= 0 || w >= math.Pi {
		return 0
	}
	p := float64(s.dcBlockPole)
	// angle of (1 - e^-jw) is (pi - w)/2; the gain g is real and positive and
	// so contributes no phase.
	phase := (math.Pi-w)/2 - math.Atan2(p*math.Sin(w), 1-p*math.Cos(w))
	return float32(phase / w)
}

// injectionOffset maps a fractional string position [0,1] onto a delay-line
// offset measured forward from the write pointer.
//
// The buffer is delayHeadroom slots longer than the integer delay, so the two
// interpolating taps of readDelayFractional sit at writePos+delayHeadroom and
// writePos+delayHeadroom-1 (modulo the buffer length). Energy written at offset
// k is therefore first read k-delayHeadroom samples later and destroyed by the
// write pointer k samples later. At k == delayHeadroom-1 only the fractional
// tap ever sees it, so the string receives a frac-weighted fraction of the
// force; at k <= delayHeadroom-2 it is overwritten before either tap reads it
// and the injection is silently discarded. That was the cause of
// the bit-exact silence at MIDI 106-108, whose delay lines are only 16, 16 and
// 15 slots long: a strike position of 0.18 lands on offset 2.
//
// The observable slots are exactly [delayHeadroom, len-1], and there are
// len-delayHeadroom of them - one full loop round trip. So the strike position
// is mapped affinely onto that window rather than onto the raw buffer length:
// the whole of [0,1] stays resolvable, at every pitch, and a position near 0
// still means "close to the near end of the round trip". Clamping instead would
// fold every position below delayHeadroom/len onto the first observable slot,
// which in the top register is most of the useful range - a 15-slot MIDI 108
// string would render every position below 0.27 bit-identically, so the soft
// pedal's strike movement and the fitted strike_position would stop doing
// anything up there.
func (s *StringWaveguide) injectionOffset(strikePos float32) int {
	if strikePos < 0.01 {
		strikePos = 0.01
	}
	if strikePos > 0.99 {
		strikePos = 0.99
	}
	span := len(s.delayLine) - delayHeadroom
	off := delayHeadroom + int(float32(span)*strikePos)
	if off > len(s.delayLine)-1 {
		off = len(s.delayLine) - 1
	}
	return off
}

// Process renders one sample from the string and advances the simulation.
func (s *StringWaveguide) Process() float32 {
	delayedSample := s.readDelayFractional(s.delayLength)
	dispersed := s.processDispersion(delayedSample)
	loopSample := s.processDCBlock(s.processLoopLoss(dispersed))
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
	basePos := (s.writePos + s.injectionOffset(strikePos)) % len(s.delayLine)
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
	pos := (s.writePos + s.injectionOffset(strikePos)) % len(s.delayLine)
	s.delayLine[pos] += force
}

// InjectForceNext writes a force into the slot the interpolating taps read on
// the NEXT call to Process, with no strike-position mapping in between.
//
// readDelayFractional taps at writePos+delayHeadroom and writePos+delayHeadroom-1
// (modulo the buffer length), so delayHeadroom is the freshest offset both taps
// still see - one slot lower and only the fractional tap reads it, two lower and
// the write pointer destroys it first. That makes this the shortest feedback
// path into the string that exists.
//
// It deliberately does NOT go through injectionOffset. That function maps a
// strike position affinely onto the observable window, so its smallest input
// (0.01, after clamping) is 1% of the ROUND TRIP, not one sample: about 1 slot
// at MIDI 60 but 5 at MIDI 40 and 17 at MIDI 21. A caller that needs a force to
// come back immediately - the unison bridge coupling in
// RingingStringGroup.processSample, whose dissipativity argument only holds
// while the delay is negligible in phase - cannot express that as a position,
// at any pitch-independent value. Hence a separate entry point.
func (s *StringWaveguide) InjectForceNext(force float32) {
	pos := (s.writePos + delayHeadroom) % len(s.delayLine)
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
	s.baseReflection = gain
	s.applyDamper()
	s.lowpassCoeff = highFreqDamping
}

// SetDamper toggles aggressive damping for release behavior.
// It is the fully-off / fully-on case of SetDamperAmount.
func (s *StringWaveguide) SetDamper(engaged bool) {
	if engaged {
		s.SetDamperAmount(1)
		return
	}
	s.SetDamperAmount(0)
}

// SetDamperAmount sets continuous damper contact in [0,1], where 0 leaves the
// string undamped and 1 is a fully seated damper. Intermediate values model a
// half-pedalled damper resting partly on the string and interpolate the loop
// reflection coefficient accordingly.
func (s *StringWaveguide) SetDamperAmount(amount float32) {
	if amount < 0 {
		amount = 0
	}
	if amount > 1 {
		amount = 1
	}
	s.damperAmount = amount
	s.applyDamper()
}

// applyDamper recomputes the loop reflection from the current base reflection
// and damper amount. It is called both when the damper moves and when loop loss
// changes, so a partial damper survives a loss-parameter update.
func (s *StringWaveguide) applyDamper() {
	switch s.damperAmount {
	case 0:
		s.reflection = s.baseReflection
	case 1:
		s.reflection = s.damperReflection
	default:
		s.reflection = s.baseReflection + (s.damperReflection-s.baseReflection)*s.damperAmount
	}
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

// processDCBlock removes the DC component from the loop signal. All state is a
// pair of float32 fields on the struct, so it allocates nothing per block.
func (s *StringWaveguide) processDCBlock(input float32) float32 {
	y := s.dcBlockGain*(input-s.dcBlockX1) + s.dcBlockPole*s.dcBlockY1
	y = float32(dspcore.FlushDenormals(float64(y)))
	s.dcBlockX1 = input
	s.dcBlockY1 = y
	return y
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
