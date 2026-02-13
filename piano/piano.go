package piano

import (
	"fmt"
	"math"
	"os"

	"github.com/cwbudde/algo-approx"
	dspconv "github.com/cwbudde/algo-dsp/dsp/conv"
	dspresample "github.com/cwbudde/algo-dsp/dsp/resample"
	"github.com/cwbudde/wav"
)

const DefaultIRWavPath = "assets/ir/default_96k.wav"

// Piano is the global engine managing voice allocation and polyphony
type Piano struct {
	sampleRate   int
	voices       []*Voice
	params       *Params
	convolver    *SoundboardConvolver
	sustainPedal bool
}

// NewPiano creates a new piano engine
func NewPiano(sampleRate int, maxPolyphony int, params *Params) *Piano {
	p := &Piano{
		sampleRate: sampleRate,
		voices:     make([]*Voice, 0, maxPolyphony),
		params:     params,
		convolver:  NewSoundboardConvolver(sampleRate),
	}
	if params != nil && params.IRWavPath != "" {
		_ = p.convolver.SetIRFromWAV(params.IRWavPath)
	}
	return p
}

// NoteOn triggers a new note
func (p *Piano) NoteOn(note int, velocity int) {
	// Stub: will allocate a voice and trigger the note
	v := NewVoice(p.sampleRate, note, velocity, p.params)
	v.SetSustain(p.sustainPedal)
	p.voices = append(p.voices, v)
}

// NoteOff releases a note
func (p *Piano) NoteOff(note int) {
	// Stub: will find and release the voice
	for _, v := range p.voices {
		if v.note == note {
			v.Release()
		}
	}
}

// SetSustainPedal sets sustain pedal state (true = down, false = up).
func (p *Piano) SetSustainPedal(down bool) {
	p.sustainPedal = down
	for _, v := range p.voices {
		v.SetSustain(down)
	}
}

// Process renders a block of audio samples (stereo interleaved)
func (p *Piano) Process(numFrames int) []float32 {
	// Mix all active voices
	monoMix := make([]float32, numFrames)

	for _, v := range p.voices {
		if !v.active {
			continue
		}

		voiceOutput := v.Process(numFrames)
		for i := 0; i < numFrames; i++ {
			monoMix[i] += voiceOutput[i]
		}
	}

	// Process through convolver (currently just pass-through to stereo)
	stereoOutput := p.convolver.Process(monoMix)

	// Clean up inactive voices
	activeVoices := make([]*Voice, 0, len(p.voices))
	for _, v := range p.voices {
		if v.active {
			activeVoices = append(activeVoices, v)
		}
	}
	p.voices = activeVoices

	return stereoOutput
}

// Voice represents one note (owns 1-3 strings)
type Voice struct {
	sampleRate  int
	note        int
	velocity    int
	strikePos   float32
	hammer      *Hammer
	stringGains []float32
	strings     []*StringWaveguide
	active      bool
	age         int // samples since note on
	released    bool
	sustainDown bool
}

// NewVoice creates a new voice for a note
func NewVoice(sampleRate, note, velocity int, params *Params) *Voice {
	strikePos := float32(0.18)
	lossGain := float32(0.9998)
	highFreqDamping := float32(0.05)
	inharmonicity := float32(0.0)
	if params != nil {
		if np, ok := params.PerNote[note]; ok && np != nil {
			if np.StrikePosition > 0.0 && np.StrikePosition < 1.0 {
				strikePos = np.StrikePosition
			}
			if np.Loss > 0.0 && np.Loss <= 1.0 {
				lossGain = np.Loss
			}
			if np.Inharmonicity > 0.0 {
				inharmonicity = np.Inharmonicity
			}
		}
	}

	v := &Voice{
		sampleRate:  sampleRate,
		note:        note,
		velocity:    velocity,
		strikePos:   strikePos,
		hammer:      NewHammer(sampleRate, velocity),
		stringGains: make([]float32, 0, 3),
		strings:     make([]*StringWaveguide, 0, 3),
		active:      true,
		released:    false,
		sustainDown: false,
	}

	// Initialize unison strings with small detune and gain spread.
	freq := midiNoteToFreq(note)
	detunes, gains := defaultUnisonForNote(note)
	for i := range detunes {
		ratio := centsToRatio(detunes[i])
		str := NewStringWaveguide(sampleRate, freq*ratio)
		str.SetLoopLoss(lossGain, highFreqDamping)
		str.SetDispersion(inharmonicity)
		v.strings = append(v.strings, str)
		v.stringGains = append(v.stringGains, gains[i])
	}

	return v
}

// exciteStrings applies excitation to all strings
func (v *Voice) exciteStrings(force float32, strikePos float32) {
	// Apply a short impulse train to excite the string
	// This is a temporary excitation; Phase 3 will add the hammer model
	for _, str := range v.strings {
		str.ExciteAtPosition(force*0.3, strikePos)
	}
}

// Release triggers the note release
func (v *Voice) Release() {
	v.released = true
	if !v.sustainDown {
		for _, str := range v.strings {
			str.SetDamper(true)
		}
	}
}

// SetSustain applies sustain pedal state to the voice's damper behavior.
func (v *Voice) SetSustain(down bool) {
	v.sustainDown = down
	if down {
		for _, str := range v.strings {
			str.SetDamper(false)
		}
		return
	}
	if v.released {
		for _, str := range v.strings {
			str.SetDamper(true)
		}
	}
}

// Process renders one block of samples from this voice
func (v *Voice) Process(numFrames int) []float32 {
	output := make([]float32, numFrames)

	for i := 0; i < numFrames; i++ {
		var sample float32

		if v.hammer != nil && v.hammer.InContact() {
			contactForce := v.hammer.Step(sample)
			for _, str := range v.strings {
				str.InjectForceAtPosition(contactForce*0.002, v.strikePos)
			}
		}

		// Process all strings and mix
		for j, str := range v.strings {
			sample += str.Process() * v.stringGains[j]
		}
		if len(v.strings) > 1 {
			// Tiny bridge-like crossfeed for a gentle coupled decay.
			cross := sample * 0.0008
			for _, str := range v.strings {
				str.InjectForceAtPosition(cross, 0.92)
			}
		}

		// Simple envelope: fade out after release
		if v.released {
			// Quick decay after release (will be replaced by damper model)
			decayRate := float32(0.9995)
			sample *= approx.FastExp(float32(-v.age) * (1.0 - decayRate))
		}

		output[i] = sample
		v.age++

		// Mark as inactive when energy is very low
		if v.released && !v.sustainDown && v.age > v.sampleRate*2 {
			v.active = false
		}
		if v.released && v.sustainDown && v.age > v.sampleRate*8 {
			v.active = false
		}
	}

	return output
}

// StringWaveguide implements the digital waveguide string model
// Simplified single-delay-line implementation for Phase 1
type StringWaveguide struct {
	sampleRate  float32
	f0          float32
	delayLength float32   // fractional delay length for fine tuning
	delayLine   []float32 // circular buffer
	writePos    int

	// Frequency-independent reflection/loss coefficient.
	reflection       float32
	baseReflection   float32
	damperReflection float32
	damperEngaged    bool

	// Frequency-dependent loop loss via one-pole lowpass in loop.
	// lowpassCoeff in [0,1): higher means stronger high-frequency damping.
	lowpassCoeff float32
	loopState    float32

	// First-order allpass dispersion stage (positive coeff -> inharmonicity).
	dispersionCoeff float32
	dispersionX1    float32
	dispersionY1    float32
	dispersionX2    float32
	dispersionY2    float32
}

// NewStringWaveguide creates a new string waveguide
func NewStringWaveguide(sampleRate int, f0 float32) *StringWaveguide {
	s := &StringWaveguide{
		sampleRate:       float32(sampleRate),
		f0:               f0,
		reflection:       0.9999, // almost perfect reflection for lossless string
		baseReflection:   0.9999,
		damperReflection: 0.92,
		damperEngaged:    false,
		lowpassCoeff:     0.0,
		dispersionCoeff:  0.0,
	}

	// Calculate delay length for one complete loop
	// Period T = 1/f0, so delay = T * sampleRate
	s.delayLength = s.sampleRate / s.f0

	// Integer part for delay line size
	intDelay := int(s.delayLength)
	if intDelay < 2 {
		intDelay = 2
	}

	// Allocate delay line (add extra samples for interpolation)
	s.delayLine = make([]float32, intDelay+4)

	return s
}

// Process renders one sample from the string and advances the simulation
func (s *StringWaveguide) Process() float32 {
	// Read delayed sample with fractional delay interpolation
	delayedSample := s.readDelayFractional(s.delayLength)

	// Apply simple dispersion via first-order allpass.
	dispersed := s.processDispersion(delayedSample)

	// Frequency-dependent loop loss (one-pole lowpass) plus global reflection.
	loopSample := s.processLoopLoss(dispersed)

	// Output is the delayed sample
	output := delayedSample

	// Write filtered sample back into delay line at CURRENT position
	// This creates a feedback loop
	s.delayLine[s.writePos] = loopSample

	// Advance write position
	s.writePos = (s.writePos + 1) % len(s.delayLine)

	return output
}

// Excite applies an excitation to the string
func (s *StringWaveguide) Excite(force float32) {
	s.ExciteAtPosition(force, 0.2)
}

// ExciteAtPosition applies an excitation at a fractional string position [0,1].
// Lower strike positions (near bridge) use a narrower profile and sound brighter.
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

	// Inject excitation into the delay line
	// Use a bipolar triangular displacement profile.
	for i := 0; i < width; i++ {
		pos := (basePos + i) % len(s.delayLine)
		// Make it bipolar: linear ramp from -force to +force
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
// gain should be in (0,1], and highFreqDamping in [0, 0.99].
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
	s.loopState = lp
	return lp * s.reflection
}

func (s *StringWaveguide) processDispersion(input float32) float32 {
	a := s.dispersionCoeff
	if a == 0.0 {
		return input
	}
	// Two cascaded first-order allpasses give stronger, still-stable dispersion.
	y := -a*input + s.dispersionX1 + a*s.dispersionY1
	s.dispersionX1 = input
	s.dispersionY1 = y

	z := -a*y + s.dispersionX2 + a*s.dispersionY2
	s.dispersionX2 = y
	s.dispersionY2 = z
	return z
}

// readDelayFractional reads from the delay line with fractional delay using linear interpolation
func (s *StringWaveguide) readDelayFractional(delay float32) float32 {
	// Integer and fractional parts
	intDelay := int(delay)
	frac := delay - float32(intDelay)

	// Calculate read positions (looking back in time)
	readPos1 := (s.writePos - intDelay + len(s.delayLine)) % len(s.delayLine)
	readPos2 := (s.writePos - intDelay - 1 + len(s.delayLine)) % len(s.delayLine)

	// Linear interpolation
	sample1 := s.delayLine[readPos1]
	sample2 := s.delayLine[readPos2]

	return sample1*(1.0-frac) + sample2*frac
}

// HammerModel defines the interface for hammer models
type HammerModel interface {
	// ComputeForce computes the hammer force based on state
	ComputeForce(velocity float32, stringVelocity float32) float32
}

// Hammer is a nonlinear felt-hammer contact model with bounded contact duration.
type Hammer struct {
	sampleRate float32
	mass       float32
	stiffness  float32
	exponent   float32
	damping    float32

	contactMaxSamples int
	contactMinSamples int
	contactSamples    int
	inContact         bool

	pos float32
	vel float32
}

// NewHammer creates a hammer initialized from MIDI velocity.
func NewHammer(sampleRate int, velocity int) *Hammer {
	if velocity < 1 {
		velocity = 1
	}
	if velocity > 127 {
		velocity = 127
	}
	v := float32(velocity) / 127.0
	initialVel := 0.6 + 3.0*v

	return &Hammer{
		sampleRate:        float32(sampleRate),
		mass:              0.010,
		stiffness:         1.1e6 * (0.5 + 2.5*v),
		exponent:          2.3,
		damping:           0.10 + 0.20*v,
		contactMaxSamples: int(float32(sampleRate) * (0.0040 - 0.0030*v)),
		contactMinSamples: int(float32(sampleRate) * 0.00025),
		inContact:         true,
		pos:               0.00012,
		vel:               initialVel,
	}
}

// InContact reports whether the hammer is still in contact with the string.
func (h *Hammer) InContact() bool {
	return h.inContact
}

// Step advances the nonlinear contact model and returns contact force.
// stringDisp is a proxy for local strike-point displacement.
func (h *Hammer) Step(stringDisp float32) float32 {
	if !h.inContact {
		return 0
	}

	dt := 1.0 / h.sampleRate
	indentation := h.pos - stringDisp
	relativeVel := h.vel

	force := float32(0.0)
	if indentation > 0 {
		indPow := float32(math.Pow(float64(indentation), float64(h.exponent)))
		dissipation := 1.0 + h.damping*maxf(relativeVel, 0.0)
		force = h.stiffness * indPow * dissipation
	}

	if !isFinite(force) {
		h.inContact = false
		return 0
	}

	// Semi-implicit Euler integration.
	acc := -force / h.mass
	h.vel += acc * dt
	h.pos += h.vel * dt

	h.contactSamples++
	if h.contactSamples >= h.contactMaxSamples {
		h.inContact = false
	}
	if h.contactSamples > h.contactMinSamples && indentation <= 0 && h.vel <= 0 {
		h.inContact = false
	}

	return force
}

// ComputeForce implements HammerModel with a simplified static contact law.
func (h *Hammer) ComputeForce(velocity float32, stringVelocity float32) float32 {
	indentation := maxf(velocity-stringVelocity, 0)
	return h.stiffness * float32(math.Pow(float64(indentation), float64(h.exponent)))
}

// SoundboardConvolver implements partitioned convolution for the soundboard/body
type SoundboardConvolver struct {
	sampleRate int
	partSize   int
	irLen      int

	leftOLA  *dspconv.OverlapAdd
	rightOLA *dspconv.OverlapAdd

	tailLeft  []float64
	tailRight []float64
}

// NewSoundboardConvolver creates a new soundboard convolver
func NewSoundboardConvolver(sampleRate int) *SoundboardConvolver {
	c := &SoundboardConvolver{
		sampleRate: sampleRate,
		partSize:   128,
	}
	c.SetIR([]float32{1.0}, []float32{1.0})
	return c
}

// Process convolves mono input with IR and returns stereo output
func (c *SoundboardConvolver) Process(input []float32) []float32 {
	output := make([]float32, len(input)*2)
	if len(input) == 0 {
		return output
	}

	in64 := toFloat64(input)

	leftFull, errL := c.leftOLA.Process(in64)
	rightFull, errR := c.rightOLA.Process(in64)
	if errL != nil || errR != nil {
		// Fail-safe passthrough if convolution backend fails.
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
		// Keep previous state if construction fails.
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
// If source sample-rate differs from convolver sample-rate, IR channels are resampled.
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

// Params holds all preset parameters
type Params struct {
	// Per-note parameters
	PerNote map[int]*NoteParams

	// Global parameters
	OutputGain float32
	IRWavPath  string
}

// NoteParams holds parameters for a specific note
type NoteParams struct {
	F0             float32
	Inharmonicity  float32
	Loss           float32
	StrikePosition float32
}

// NewDefaultParams creates default parameters
func NewDefaultParams() *Params {
	return &Params{
		PerNote:    make(map[int]*NoteParams),
		OutputGain: 1.0,
		IRWavPath:  "",
	}
}

// midiNoteToFreq converts MIDI note number to frequency in Hz
func midiNoteToFreq(note int) float32 {
	// A4 (note 69) = 440 Hz
	// f = 440 * 2^((n-69)/12)
	const a4Freq = 440.0
	const a4Note = 69
	exponent := float32(note-a4Note) / 12.0
	// Using float32 approximation
	freq := a4Freq * pow2Approx(exponent)
	return freq
}

// pow2Approx computes 2^x using fast exponential approximation
// Uses the identity: 2^x = e^(x * ln(2))
func pow2Approx(x float32) float32 {
	const ln2 = 0.69314718055994530942 // math.Ln2
	return approx.FastExp(x * ln2)
}

func defaultUnisonForNote(note int) ([]float32, []float32) {
	switch {
	case note < 40:
		return []float32{0.0}, []float32{1.0}
	case note < 70:
		return []float32{-1.8, 1.8}, []float32{0.52, 0.48}
	default:
		return []float32{-3.0, 0.0, 3.0}, []float32{0.34, 0.33, 0.33}
	}
}

func centsToRatio(cents float32) float32 {
	return pow2Approx(cents / 1200.0)
}

func isFinite(x float32) bool {
	return !math.IsNaN(float64(x)) && !math.IsInf(float64(x), 0)
}

func maxf(a float32, b float32) float32 {
	if a > b {
		return a
	}
	return b
}

func toFloat64(in []float32) []float64 {
	out := make([]float64, len(in))
	for i, v := range in {
		out[i] = float64(v)
	}
	return out
}

func overlapAddBlock(convOut []float64, tail []float64, blockLen int) ([]float64, []float64) {
	if len(convOut) < blockLen {
		out := make([]float64, blockLen)
		copy(out, convOut)
		return out, nil
	}

	full := make([]float64, len(convOut))
	copy(full, convOut)
	n := len(tail)
	if n > len(full) {
		n = len(full)
	}
	for i := 0; i < n; i++ {
		full[i] += tail[i]
	}

	out := make([]float64, blockLen)
	copy(out, full[:blockLen])
	newTail := make([]float64, len(full)-blockLen)
	copy(newTail, full[blockLen:])
	return out, newTail
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
