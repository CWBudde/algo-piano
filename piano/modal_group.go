package piano

import "math"

const modalMaxPartials = 8

// soaFieldCount is the number of parallel float32 mode arrays carved out of
// the single backing allocation in newModalStringGroup.
const soaFieldCount = 9

// ModalStringGroup is a low-CPU per-note ringing model using damped sinusoidal modes.
//
// Mode state is held as a struct-of-arrays: every unison string's modes are
// concatenated into one flat run, so string si owns the index range
// [modeStart[si], modeStart[si+1]). This layout lets the whole group be advanced
// by a single vectorized rotate-decay kernel call per sample.
type ModalStringGroup struct {
	note int
	f0   float32

	// Flat SoA mode state, all backed by soaBuf.
	re            []float32
	im            []float32
	cosW          []float32
	sinW          []float32
	decay         []float32 // active decay: a copy of damped or undamped
	decayUndamped []float32
	decayDamped   []float32
	gain          []float32
	acc           []float32 // per-sample scratch; all-zero between samples
	soaBuf        []float32

	order     []int32 // partial order per mode, for modalShape
	modeStart []int32 // len == stringCount+1, monotonically non-decreasing

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

	sr := float32(sampleRate)
	nyquist := 0.5 * sr

	// Pass 1: count admitted modes per string. Mode counts are ragged because
	// each unison string has its own detuned base frequency, so a partial near
	// the Nyquist cutoff can be admitted for one string and rejected for
	// another. Strings whose fundamental is already above the cutoff fall back
	// to a single mode.
	modeStart := make([]int32, len(detunes)+1)
	for i := range detunes {
		baseF := freq * centsToRatio(detunes[i]*unisonDetuneScale)
		count := 0
		for order := 1; order <= maxPartials; order++ {
			if modalPartialFrequency(baseF, float32(order), inharmonicity) >= nyquist*0.95 {
				break
			}
			count++
		}
		if count == 0 {
			count = 1 // single-mode fallback
		}
		modeStart[i+1] = modeStart[i] + int32(count)
	}

	n := int(modeStart[len(modeStart)-1])
	soaBuf := make([]float32, soaFieldCount*n)
	g := &ModalStringGroup{
		note:          note,
		f0:            freq,
		re:            soaBuf[0*n : 1*n : 1*n],
		im:            soaBuf[1*n : 2*n : 2*n],
		cosW:          soaBuf[2*n : 3*n : 3*n],
		sinW:          soaBuf[3*n : 4*n : 4*n],
		decay:         soaBuf[4*n : 5*n : 5*n],
		decayUndamped: soaBuf[5*n : 6*n : 6*n],
		decayDamped:   soaBuf[6*n : 7*n : 7*n],
		gain:          soaBuf[7*n : 8*n : 8*n],
		acc:           soaBuf[8*n : 9*n : 9*n],
		soaBuf:        soaBuf,
		order:         make([]int32, n),
		modeStart:     modeStart,
		gains:         append([]float32(nil), gains...),
		partials:      maxPartials,
		gainExp:       gainExp,
		excitation:    excitation,
		undampedK:     undampedK,
		dampedK:       dampedK,
	}

	// Pass 2: fill the mode parameters.
	for i := range detunes {
		baseF := freq * centsToRatio(detunes[i]*unisonDetuneScale)
		lo, hi := int(modeStart[i]), int(modeStart[i+1])

		if hi-lo == 1 && modalPartialFrequency(baseF, 1, inharmonicity) >= nyquist*0.95 {
			fallbackF := minf(maxf(baseF, 20), nyquist*0.45)
			w := 2.0 * math.Pi * float64(fallbackF/sr)
			g.order[lo] = 1
			g.cosW[lo] = float32(math.Cos(w))
			g.sinW[lo] = float32(math.Sin(w))
			g.gain[lo] = 1.0
			g.decayUndamped[lo] = modalDecay(lossGain, fallbackF, 1, false, undampedK, highFreqDamping)
			g.decayDamped[lo] = modalDecay(lossGain, fallbackF, 1, true, dampedK, highFreqDamping)
			continue
		}

		for idx := lo; idx < hi; idx++ {
			order := idx - lo + 1
			partialF := modalPartialFrequency(baseF, float32(order), inharmonicity)
			w := 2.0 * math.Pi * float64(partialF/sr)
			g.order[idx] = int32(order)
			g.cosW[idx] = float32(math.Cos(w))
			g.sinW[idx] = float32(math.Sin(w))
			g.gain[idx] = float32(1.0 / math.Pow(float64(order), float64(gainExp)))
			g.decayUndamped[idx] = modalDecay(lossGain, partialF, order, false, undampedK, highFreqDamping)
			g.decayDamped[idx] = modalDecay(lossGain, partialF, order, true, dampedK, highFreqDamping)
		}
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
	src := g.decayDamped
	if g.keyDown || g.sustainDown {
		src = g.decayUndamped
	}
	copy(g.decay, src)
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
	for si := 0; si+1 < len(g.modeStart); si++ {
		sg := float32(1.0)
		if si < len(g.gains) {
			sg = g.gains[si]
		}
		lo, hi := int(g.modeStart[si]), int(g.modeStart[si+1])
		for idx := lo; idx < hi; idx++ {
			order := g.order[idx]
			shape := modalShape(int(order), strikePos)
			if shape > -1e-6 && shape < 1e-6 {
				continue
			}
			amp := force * sg * modeScale * shape / float32(order)
			g.re[idx] += amp
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
	return g.applyCrossfeed(g.advanceModes(), unisonCrossfeed)
}

// reduceArenaSample completes one sample for a group whose modes were already
// advanced by the arena's batched kernel call: it only reduces the accumulator
// and applies the crossfeed.
func (g *ModalStringGroup) reduceArenaSample(unisonCrossfeed float32) float32 {
	return g.applyCrossfeed(g.reduceAcc(), unisonCrossfeed)
}

func (g *ModalStringGroup) applyCrossfeed(sample float32, unisonCrossfeed float32) float32 {
	// Keep unison crossfeed very lightweight in modal mode (1st mode only).
	if g.stringCount() > 1 && unisonCrossfeed > 0 {
		cross := sample * unisonCrossfeed * 0.08
		for si := 0; si+1 < len(g.modeStart); si++ {
			lo, hi := g.modeStart[si], g.modeStart[si+1]
			if lo == hi {
				continue
			}
			g.re[lo] += cross
		}
	}
	return sample
}

// stringGain returns the unison gain for string si, defaulting to unity.
func (g *ModalStringGroup) stringGain(si int) float32 {
	if si < len(g.gains) {
		return g.gains[si]
	}
	return 1.0
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
	return len(g.modeStart) - 1
}

// modeCount returns the number of modes allocated to unison string si.
func (g *ModalStringGroup) modeCount(si int) int {
	if si < 0 || si+1 >= len(g.modeStart) {
		return 0
	}
	return int(g.modeStart[si+1] - g.modeStart[si])
}

func (g *ModalStringGroup) fundamental() float32 {
	return g.f0
}
