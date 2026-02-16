package piano

import "math"

const modalMaxPartials = 8

type modalMode struct {
	order         int
	cosW          float32
	sinW          float32
	gain          float32
	decayUndamped float32
	decayDamped   float32
	decay         float32
	re            float32
	im            float32
}

type modalString struct {
	modes []modalMode
}

// ModalStringGroup is a low-CPU per-note ringing model using damped sinusoidal modes.
type ModalStringGroup struct {
	note       int
	f0         float32
	strings    []modalString
	gains      []float32
	resFilters []noteResonator
	partials   int
	gainExp    float32
	excitation float32
	undampedK  float32
	dampedK    float32

	keyDown     bool
	sustainDown bool
	active      bool
	quietBlocks int
}

func newModalStringGroup(sampleRate int, note int, params *Params) *ModalStringGroup {
	lossGain := float32(0.9998)
	inharmonicity := float32(0.0)
	unisonDetuneScale := float32(1.0)
	highFreqDamping := float32(0.05)
	maxPartials := modalMaxPartials
	gainExp := float32(1.1)
	excitation := float32(1.0)
	undampedK := float32(1.0)
	dampedK := float32(1.0)

	if params != nil {
		if params.UnisonDetuneScale >= 0 {
			unisonDetuneScale = params.UnisonDetuneScale
		}
		if params.HighFreqDamping > 0 {
			highFreqDamping = params.HighFreqDamping
		}
		if params.ModalPartials > 0 {
			maxPartials = params.ModalPartials
		}
		if params.ModalGainExponent > 0 {
			gainExp = params.ModalGainExponent
		}
		if params.ModalExcitation > 0 {
			excitation = params.ModalExcitation
		}
		if params.ModalUndampedLoss > 0 {
			undampedK = params.ModalUndampedLoss
		}
		if params.ModalDampedLoss > 0 {
			dampedK = params.ModalDampedLoss
		}
		if np, ok := params.PerNote[note]; ok && np != nil {
			if np.Loss > 0.0 && np.Loss <= 1.0 {
				lossGain = np.Loss
			}
			if np.Inharmonicity > 0.0 {
				inharmonicity = np.Inharmonicity
			}
		}
	}

	freq := midiNoteToFreq(note)
	detunes, gains := defaultUnisonForNote(note)
	strings := make([]modalString, 0, len(detunes))

	sr := float32(sampleRate)
	nyquist := 0.5 * sr
	for i := range detunes {
		ratio := centsToRatio(detunes[i] * unisonDetuneScale)
		baseF := freq * ratio
		modes := make([]modalMode, 0, maxPartials)
		for order := 1; order <= maxPartials; order++ {
			partialF := modalPartialFrequency(baseF, float32(order), inharmonicity)
			if partialF >= nyquist*0.95 {
				break
			}
			w := 2.0 * math.Pi * float64(partialF/sr)
			gain := float32(1.0 / math.Pow(float64(order), float64(gainExp)))
			m := modalMode{
				order:         order,
				cosW:          float32(math.Cos(w)),
				sinW:          float32(math.Sin(w)),
				gain:          gain,
				decayUndamped: modalDecay(lossGain, partialF, order, false, undampedK, highFreqDamping),
				decayDamped:   modalDecay(lossGain, partialF, order, true, dampedK, highFreqDamping),
			}
			m.decay = m.decayDamped
			modes = append(modes, m)
		}
		if len(modes) == 0 {
			fallbackF := minf(maxf(baseF, 20), nyquist*0.45)
			w := 2.0 * math.Pi * float64(fallbackF/sr)
			modes = append(modes, modalMode{
				order:         1,
				cosW:          float32(math.Cos(w)),
				sinW:          float32(math.Sin(w)),
				gain:          1.0,
				decayUndamped: modalDecay(lossGain, fallbackF, 1, false, undampedK, highFreqDamping),
				decayDamped:   modalDecay(lossGain, fallbackF, 1, true, dampedK, highFreqDamping),
				decay:         modalDecay(lossGain, fallbackF, 1, true, dampedK, highFreqDamping),
			})
		}
		strings = append(strings, modalString{modes: modes})
	}

	g := &ModalStringGroup{
		note:       note,
		f0:         freq,
		strings:    strings,
		gains:      append([]float32(nil), gains...),
		partials:   maxPartials,
		gainExp:    gainExp,
		excitation: excitation,
		undampedK:  undampedK,
		dampedK:    dampedK,
	}
	g.initResonanceFilters(sampleRate)
	g.updateDamperState()
	return g
}

func modalPartialFrequency(baseF float32, order float32, inharmonicity float32) float32 {
	if inharmonicity <= 0 {
		return baseF * order
	}
	stretch := float32(math.Sqrt(1.0 + float64(0.12*inharmonicity*order*order)))
	return baseF * order * stretch
}

func modalDecay(lossGain float32, freq float32, order int, damped bool, scale float32, highFreqDamping float32) float32 {
	base := clampFloat32(lossGain, 0.94, 0.99995)
	// Frequency-dependent loss: higher partials decay faster.
	// The highFreqDamping parameter scales the order and frequency terms.
	// At default 0.05 this matches the original hardcoded behavior.
	// Higher values (e.g. 0.2-0.5) produce more realistic piano-like
	// high-frequency rolloff during sustain, matching Bensa et al.'s
	// observation of stronger damping at high wave numbers (b2 term).
	hfScale := highFreqDamping / 0.05 // normalized so default=1.0
	base -= 0.00004 * hfScale * float32(order-1)
	base -= 0.00000035 * hfScale * freq
	minVal := float32(0.90)
	maxVal := float32(0.999995)
	if damped {
		base = base*0.985 - 0.0012
		minVal = 0.86
		maxVal = 0.997
	}
	base = clampFloat32(base, minVal, maxVal)
	if scale <= 0 {
		scale = 1
	}
	scaled := 1.0 - scale*(1.0-base)
	return clampFloat32(scaled, minVal, maxVal)
}

func modalShape(order int, strikePos float32) float32 {
	return float32(math.Sin(math.Pi * float64(order) * float64(strikePos)))
}

func (g *ModalStringGroup) initResonanceFilters(sampleRate int) {
	if sampleRate <= 0 || g.f0 <= 0 {
		return
	}
	nyquist := 0.5 * float32(sampleRate)
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
		center := g.f0 * p.mult
		if center <= 10 || center >= nyquist*0.95 {
			continue
		}
		filters = append(filters, newNoteResonator(sampleRate, center, p.bwHz, p.gain))
	}
	g.resFilters = filters
}

func (g *ModalStringGroup) setKeyDown(down bool) {
	g.keyDown = down
	g.updateDamperState()
	if down {
		g.active = true
		g.quietBlocks = 0
	}
}

func (g *ModalStringGroup) setSustain(down bool) {
	g.sustainDown = down
	g.updateDamperState()
	if down {
		g.active = true
		g.quietBlocks = 0
	}
}

func (g *ModalStringGroup) updateDamperState() {
	engageDamper := !(g.keyDown || g.sustainDown)
	for si := range g.strings {
		modes := g.strings[si].modes
		for mi := range modes {
			if engageDamper {
				modes[mi].decay = modes[mi].decayDamped
			} else {
				modes[mi].decay = modes[mi].decayUndamped
			}
		}
	}
}

func (g *ModalStringGroup) isUndamped() bool {
	return g.keyDown || g.sustainDown
}

func (g *ModalStringGroup) isActive() bool {
	return g.active
}

func (g *ModalStringGroup) filterResonanceDrive(x float32) float32 {
	if len(g.resFilters) == 0 {
		return x
	}
	sum := float32(0)
	for i := range g.resFilters {
		sum += g.resFilters[i].process(x)
	}
	return sum
}

func (g *ModalStringGroup) injectAtPosition(force float32, strikePos float32, modeScale float32) {
	if force == 0 {
		return
	}
	force *= g.excitation
	if strikePos < 0.01 {
		strikePos = 0.01
	}
	if strikePos > 0.99 {
		strikePos = 0.99
	}
	for si := range g.strings {
		sg := float32(1.0)
		if si < len(g.gains) {
			sg = g.gains[si]
		}
		modes := g.strings[si].modes
		for mi := range modes {
			m := &modes[mi]
			shape := modalShape(m.order, strikePos)
			if shape > -1e-6 && shape < 1e-6 {
				continue
			}
			amp := force * sg * modeScale * shape / float32(m.order)
			m.re += amp
		}
	}
	g.active = true
	g.quietBlocks = 0
}

func (g *ModalStringGroup) injectResonance(energy float32) {
	g.injectAtPosition(energy, 0.82, 0.55)
}

func (g *ModalStringGroup) injectHammerForce(force float32, strikePos float32) {
	g.injectAtPosition(force, strikePos, 1.0)
}

func (g *ModalStringGroup) injectCouplingForce(force float32) {
	g.injectAtPosition(force, 0.9, 0.45)
}

func (g *ModalStringGroup) processSample(unisonCrossfeed float32) float32 {
	sample := float32(0)
	for si := range g.strings {
		sg := float32(1.0)
		if si < len(g.gains) {
			sg = g.gains[si]
		}
		sModes := g.strings[si].modes
		strSample := float32(0)
		for mi := range sModes {
			m := &sModes[mi]
			nx := m.decay * (m.re*m.cosW - m.im*m.sinW)
			ny := m.decay * (m.re*m.sinW + m.im*m.cosW)
			m.re = nx
			m.im = ny
			strSample += nx * m.gain
		}
		sample += strSample * sg
	}

	// Keep unison crossfeed very lightweight in modal mode (1st mode only).
	if len(g.strings) > 1 && unisonCrossfeed > 0 {
		cross := sample * unisonCrossfeed * 0.08
		for si := range g.strings {
			if len(g.strings[si].modes) == 0 {
				continue
			}
			g.strings[si].modes[0].re += cross
		}
	}
	return sample
}

func (g *ModalStringGroup) endBlock(blockEnergy float64, frames int) bool {
	if g.isUndamped() {
		g.active = true
		g.quietBlocks = 0
		return true
	}

	rms := math.Sqrt(blockEnergy / float64(maxInt(1, frames)))
	if rms > 1e-6 {
		g.active = true
		g.quietBlocks = 0
		return true
	}

	g.quietBlocks++
	if g.quietBlocks > 24 {
		g.active = false
	}
	return g.active
}

func (g *ModalStringGroup) stringCount() int {
	return len(g.strings)
}

func (g *ModalStringGroup) fundamental() float32 {
	return g.f0
}
