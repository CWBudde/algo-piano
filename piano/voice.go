package piano

import "github.com/cwbudde/algo-approx"

// Voice represents one note (owns 1-3 strings).
type Voice struct {
	sampleRate       int
	note             int
	velocity         int
	f0               float32
	baseStrike       float32
	strikePos        float32
	softStrikeOffset float32
	softHardness     float32
	unisonCrossfeed  float32
	hammer           *Hammer
	stringGains      []float32
	strings          []*StringWaveguide
	resFilters       []noteResonator
	active           bool
	age              int // samples since note on
	released         bool
	sustainDown      bool
	softDown         bool
}

func (v *Voice) isUndamped() bool {
	if !v.active {
		return false
	}
	return !v.released || v.sustainDown
}

// NewVoice creates a new voice for a note.
func NewVoice(sampleRate, note, velocity int, params *Params) *Voice {
	strikePos := float32(0.18)
	lossGain := float32(0.9998)
	highFreqDamping := float32(0.05)
	inharmonicity := float32(0.0)
	unisonDetuneScale := float32(1.0)
	unisonCrossfeed := float32(0.0008)
	softStrikeOffset := float32(0.08)
	softHardness := float32(0.78)
	if params != nil {
		if params.UnisonDetuneScale >= 0 {
			unisonDetuneScale = params.UnisonDetuneScale
		}
		if params.UnisonCrossfeed >= 0 {
			unisonCrossfeed = params.UnisonCrossfeed
		}
		if params.SoftPedalStrikeOffset >= 0 {
			softStrikeOffset = params.SoftPedalStrikeOffset
		}
		if params.SoftPedalHardness > 0 {
			softHardness = params.SoftPedalHardness
		}
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
		sampleRate:       sampleRate,
		note:             note,
		velocity:         velocity,
		f0:               0,
		baseStrike:       strikePos,
		strikePos:        strikePos,
		softStrikeOffset: softStrikeOffset,
		softHardness:     softHardness,
		unisonCrossfeed:  unisonCrossfeed,
		hammer:           NewHammer(sampleRate, velocity),
		stringGains:      make([]float32, 0, 3),
		strings:          make([]*StringWaveguide, 0, 3),
		resFilters:       nil,
		active:           true,
		released:         false,
		sustainDown:      false,
		softDown:         false,
	}
	if params != nil && v.hammer != nil {
		v.hammer.ApplyInfluenceScales(
			params.HammerStiffnessScale,
			params.HammerExponentScale,
			params.HammerDampingScale,
			params.HammerInitialVelocityScale,
			params.HammerContactTimeScale,
		)
	}

	freq := midiNoteToFreq(note)
	v.f0 = freq
	detunes, gains := defaultUnisonForNote(note)
	for i := range detunes {
		ratio := centsToRatio(detunes[i] * unisonDetuneScale)
		str := NewStringWaveguide(sampleRate, freq*ratio)
		str.SetLoopLoss(lossGain, highFreqDamping)
		str.SetDispersion(inharmonicity)
		v.strings = append(v.strings, str)
		v.stringGains = append(v.stringGains, gains[i])
	}
	v.initResonanceFilters()

	return v
}

func (v *Voice) exciteStrings(force float32, strikePos float32) {
	for _, str := range v.strings {
		str.ExciteAtPosition(force*0.3, strikePos)
	}
}

// Release triggers the note release.
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

// SetSoftPedal applies una corda behavior to this voice.
func (v *Voice) SetSoftPedal(down bool) {
	v.softDown = down
	if down {
		v.strikePos = minf(v.baseStrike+v.softStrikeOffset, 0.95)
		if v.hammer != nil {
			v.hammer.SetHardnessScale(v.softHardness)
		}
		return
	}

	v.strikePos = v.baseStrike
	if v.hammer != nil {
		v.hammer.SetHardnessScale(1.0)
	}
}

func (v *Voice) injectResonance(energy float32) {
	if energy == 0 {
		return
	}
	for i, str := range v.strings {
		g := float32(1.0)
		if i < len(v.stringGains) {
			g = v.stringGains[i]
		}
		str.InjectForceAtPosition(energy*g, 0.82)
	}
}

func (v *Voice) initResonanceFilters() {
	if v.sampleRate <= 0 || v.f0 <= 0 {
		return
	}
	nyquist := 0.5 * float32(v.sampleRate)
	partials := []struct {
		mult float32
		bwHz float32
		gain float32
	}{
		{mult: 1.0, bwHz: 35.0, gain: 1.0},
		{mult: 2.0, bwHz: 55.0, gain: 0.55},
		{mult: 3.0, bwHz: 80.0, gain: 0.30},
	}
	filters := make([]noteResonator, 0, len(partials))
	for _, p := range partials {
		center := v.f0 * p.mult
		if center <= 10 || center >= nyquist*0.95 {
			continue
		}
		filters = append(filters, newNoteResonator(v.sampleRate, center, p.bwHz, p.gain))
	}
	v.resFilters = filters
}

func (v *Voice) filterResonanceDrive(x float32) float32 {
	if len(v.resFilters) == 0 {
		return x
	}
	sum := float32(0)
	for i := range v.resFilters {
		sum += v.resFilters[i].process(x)
	}
	return sum
}

// Process renders one block of samples from this voice.
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

		for j, str := range v.strings {
			sample += str.Process() * v.stringGains[j]
		}
		if len(v.strings) > 1 {
			cross := sample * v.unisonCrossfeed
			for _, str := range v.strings {
				str.InjectForceAtPosition(cross, 0.92)
			}
		}

		if v.released {
			decayRate := float32(0.9995)
			sample *= approx.FastExp(float32(-v.age) * (1.0 - decayRate))
		}

		output[i] = sample
		v.age++

		if v.released && !v.sustainDown && v.age > v.sampleRate*2 {
			v.active = false
		}
		if v.released && v.sustainDown && v.age > v.sampleRate*8 {
			v.active = false
		}
	}

	return output
}
