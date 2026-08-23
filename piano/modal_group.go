package piano

import "math"

const modalMaxPartials = 8

// soaFieldCount is the number of parallel float32 mode arrays carved out of
// the single backing allocation in newModalStringGroup.
const soaFieldCount = 12

// Fixed strike positions used by the non-hammer excitation paths. Their shape
// vectors are precomputed once per group, so the steady-state injection path
// evaluates no transcendental at all.
const (
	modalResonanceStrikePos = float32(0.82)
	modalCouplingStrikePos  = float32(0.9)
)

// modalNoStrikePos is a sentinel outside the clamped strike-position range, so
// the hammer shape cache always misses on its first use.
const modalNoStrikePos = float32(-1)

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

	// Cached excitation shape vectors, one entry per mode, holding
	// modalShape(order, strikePos) with inaudible modes zeroed. They are
	// group-owned and never repointed at the modal arena: unlike the evolving
	// mode state they are static, and the arena only compacts what changes.
	shapeRes    []float32 // strike position modalResonanceStrikePos
	shapeCoup   []float32 // strike position modalCouplingStrikePos
	shapeHammer []float32 // strike position hammerShapePos, filled on demand

	// hammerShapePos is the clamped strike position shapeHammer was filled for,
	// or modalNoStrikePos when it holds nothing.
	hammerShapePos float32

	order     []int32 // partial order per mode, for modalShape
	modeStart []int32 // len == stringCount+1, monotonically non-decreasing

	// stringOut holds what each unison string contributed to the last sample,
	// before its unison gain is applied. It mirrors RingingStringGroup.stringOut
	// and exists for the same reason: the bridge coupling force needs a string's
	// own contribution, not only the mix. Per-string scratch rather than
	// per-mode state, so the modal arena does not compact it.
	stringOut []float32

	gains      []float32
	resFilters []noteResonator
	partials   int
	gainExp    float32
	excitation float32
	undampedK  float32
	dampedK    float32

	// resonanceForceScale converts a per-sample bridge force into a modal state
	// increment. Injecting the force straight into g.re treats an amplitude and
	// a force-per-sample as the same quantity; the waveguide core gets that
	// conversion for free because energy written into the delay line is read
	// once per round trip, i.e. it carries an implicit f0/fs.
	//
	// Without it the sympathetic resonance loop closed once per block in
	// Piano.Process runs open-loop gains far above unity in the bass, and since
	// that loop is entirely linear it diverges to NaN within ~0.29 s. Measured
	// open-loop gain (48 kHz, default params, unit sine drive at the note's f0,
	// TestResonanceLoopGainIsBoundedAcrossCores):
	//
	//	note   modal before   modal after   dwg
	//	21     12.162535      0.006968      0.411011
	//	36      6.104038      0.008318      0.372699
	//	48      1.437305      0.003917      0.157057
	//	60      0.271781      0.001481      0.064689
	//	72      0.036856      0.000402      0.009353
	//	84      0.003611      0.000079      0.000900
	resonanceForceScale float32 // f0 / sampleRate == 1 / round-trip length

	keyDown       bool
	sustainAmount float32
	active        bool
	quietBlocks   int

	// resonanceEnergized records that injectResonance deposited energy since
	// the bank last looked. The bank clears it when it enrolls the note.
	resonanceEnergized bool
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
		shapeRes:      soaBuf[9*n : 10*n : 10*n],
		shapeCoup:     soaBuf[10*n : 11*n : 11*n],
		shapeHammer:   soaBuf[11*n : 12*n : 12*n],

		hammerShapePos: modalNoStrikePos,

		soaBuf:     soaBuf,
		stringOut:  make([]float32, len(detunes)),
		order:      make([]int32, n),
		modeStart:  modeStart,
		gains:      append([]float32(nil), gains...),
		partials:   maxPartials,
		gainExp:    gainExp,
		excitation: excitation,
		undampedK:  undampedK,
		dampedK:    dampedK,

		resonanceForceScale: freq / sr,
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

	g.fillShapeVector(g.shapeRes, modalResonanceStrikePos)
	g.fillShapeVector(g.shapeCoup, modalCouplingStrikePos)

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

// clampStrikePos keeps a strike position away from the string ends, where every
// mode shape vanishes and the excitation would be silent.
func clampStrikePos(strikePos float32) float32 {
	if strikePos < 0.01 {
		return 0.01
	}
	if strikePos > 0.99 {
		return 0.99
	}
	return strikePos
}

// fillShapeVector precomputes the per-mode shape factors for one strike
// position, with the inaudible-mode cutoff folded in as a zero so the injection
// loop needs no branch.
//
// The division by the partial order is deliberately left in the injection loop:
// folding it in here would reassociate the product and change the float32
// rounding, which the modal render is required to reproduce bit-for-bit.
//
// strikePos must already be clamped.
func (g *ModalStringGroup) fillShapeVector(dst []float32, strikePos float32) {
	for idx, order := range g.order {
		shape := modalShape(int(order), strikePos)
		if shape > -1e-6 && shape < 1e-6 {
			shape = 0
		}
		dst[idx] = shape
	}
}

// shapeVector returns the cached shape weights for a clamped strike position,
// refilling the single hammer slot when it holds a different position. The two
// fixed positions have dedicated slots so alternating between them and a hammer
// strike never thrashes the cache.
func (g *ModalStringGroup) shapeVector(strikePos float32) []float32 {
	switch strikePos {
	case modalResonanceStrikePos:
		return g.shapeRes
	case modalCouplingStrikePos:
		return g.shapeCoup
	}
	if strikePos != g.hammerShapePos {
		g.fillShapeVector(g.shapeHammer, strikePos)
		g.hammerShapePos = strikePos
	}
	return g.shapeHammer
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

func (g *ModalStringGroup) setSustainAmount(amount float32) {
	g.sustainAmount = clampf(amount, 0, 1)
	g.updateDamperState()
	if g.sustainAmount > 0 {
		g.active = true
		g.quietBlocks = 0
	}
}

// updateDamperState blends the per-mode decay factors between the damped and
// undamped tables according to how firmly the damper rests on the string. The
// fully-lifted and fully-seated cases stay exact table copies so the modal core
// remains bit-identical to the pre-partial-pedal behaviour.
func (g *ModalStringGroup) updateDamperState() {
	damping := g.damperAmount()
	switch damping {
	case 0:
		copy(g.decay, g.decayUndamped)
	case 1:
		copy(g.decay, g.decayDamped)
	default:
		undamped := g.decayUndamped
		damped := g.decayDamped
		decay := g.decay
		for i := range decay {
			decay[i] = undamped[i] + (damped[i]-undamped[i])*damping
		}
	}
}

// damperAmount is how firmly the damper rests on the string: a held key lifts
// it completely, otherwise the pedal lifts it in proportion to its depth.
func (g *ModalStringGroup) damperAmount() float32 {
	if g.keyDown {
		return 0
	}
	return 1 - g.sustainAmount
}

func (g *ModalStringGroup) isUndamped() bool {
	return g.keyDown || g.sustainAmount > 0
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
	shape := g.shapeVector(clampStrikePos(strikePos))
	for si := 0; si+1 < len(g.modeStart); si++ {
		amp := force * g.stringGain(si) * modeScale
		lo, hi := int(g.modeStart[si]), int(g.modeStart[si+1])
		for idx := lo; idx < hi; idx++ {
			g.re[idx] += amp * shape[idx] / float32(g.order[idx])
		}
	}
	g.active = true
	g.quietBlocks = 0
}

func (g *ModalStringGroup) injectResonance(energy float32) {
	if energy == 0 {
		return
	}
	g.injectAtPosition(energy*g.resonanceForceScale, modalResonanceStrikePos, 0.55)
	g.resonanceEnergized = true
}

// takeResonanceEnergy reports whether sympathetic energy was injected since the
// last call and clears the flag.
func (g *ModalStringGroup) takeResonanceEnergy() bool {
	if !g.resonanceEnergized {
		return false
	}
	g.resonanceEnergized = false
	return true
}

func (g *ModalStringGroup) injectHammerForce(force float32, strikePos float32) {
	g.injectAtPosition(force, strikePos, 1.0)
}

func (g *ModalStringGroup) injectCouplingForce(force float32) {
	g.injectAtPosition(force, modalCouplingStrikePos, 0.45)
}

func (g *ModalStringGroup) processSample(unisonCrossfeed float32, bridgeCoupling float32) float32 {
	return g.applyCrossfeed(g.advanceModes(), unisonCrossfeed, bridgeCoupling)
}

// reduceArenaSample completes one sample for a group whose modes were already
// advanced by the arena's batched kernel call: it only reduces the accumulator
// and applies the crossfeed.
func (g *ModalStringGroup) reduceArenaSample(unisonCrossfeed float32, bridgeCoupling float32) float32 {
	return g.applyCrossfeed(g.reduceAcc(), unisonCrossfeed, bridgeCoupling)
}

// modalCrossfeedScale keeps the unison crossfeed lightweight in modal mode: the
// force lands on the first mode of each string only, scaled by this factor, so
// the whole coupling costs one multiply-add per string per sample.
//
// The value is the one the pre-2026-08-23 form shipped with. It is deliberately
// unchanged by the passivity fix below: the difference form injects less than
// the old bare mix on strings that already move together, so re-voicing the
// scale is a level decision for the modal re-fit (PLAN.md 17.1), not part of
// making the term correct.
const modalCrossfeedScale = float32(0.08)

// applyCrossfeed couples the strings of a unison to each other through the
// bridge, with the force on string si proportional to sample - stringOut[si]:
// the difference between the common bridge motion and that string's own
// contribution to it. See RingingStringGroup.processSample for the energy
// argument; this is the modal half of the same fix.
//
// Until 2026-08-23 the modal force was sample*c*modalCrossfeedScale, added into
// every string including the one that produced the sample - positive feedback
// around an already resonant bank, with no subtraction anywhere. It was believed
// unobservable because the scale keeps it far under unity. It is not. Note 60
// struck once under a held pedal, resonance and string coupling off, whole-render
// energy over 8 s against the same render at c = 0
// (TestModalUnisonCouplingDoesNotPumpTheBank has the other three notes):
//
//	           c = 0.0008   c = 0.005   c = 0.02 (the clamp)
//	before         1.1171      3.0163   non-finite
//	after          1.0001      1.0006      1.0016
//
// So the old form added 9-14% of a note's energy at the shipped default, and
// diverged at maxUnisonCrossfeed - a value a hand-edited preset may legally ask
// for, which is what made the old bound an accident of the 0.08 rather than a
// property of the term.
//
// What carries over from the waveguide case is the sign: a string louder than
// the bridge is pushed back, never further, so the term can no longer add energy
// unconditionally. What does not carry over is the proof - Jensen's inequality
// does not close on the modal state, and the property is asserted by measurement
// instead.
//
// Until 2026-08-23 that was blamed on the force landing on mode 0 alone. It is
// distributed over the string's modes now (see the injection below) and the
// numbers did not move - 0.99951 to 1.00729, the same range - so the residual
// belongs to the difference form, not to the injection site.
//
// The injection site needs no equivalent of StringWaveguide.InjectForceNext:
// g.re[lo] is read by the very next rotate-decay step, which is already the
// shortest path a modal bank has. The waveguide needed its own entry point only
// because a strike position there maps onto the round trip.
func (g *ModalStringGroup) applyCrossfeed(sample float32, unisonCrossfeed float32, bridgeCoupling float32) float32 {
	if g.stringCount() > 1 && (unisonCrossfeed > 0 || bridgeCoupling > 0) {
		kc := unisonCrossfeed * modalCrossfeedScale
		kb := bridgeCoupling * modalCrossfeedScale
		for si := 0; si+1 < len(g.modeStart); si++ {
			lo, hi := int(g.modeStart[si]), int(g.modeStart[si+1])
			if lo == hi {
				continue
			}
			// The gain weight has to be the SAME one used to build the mix,
			// exactly as in the waveguide core.
			alpha := g.stringGain(si) * (kc*(sample-g.stringOut[si]) - kb*sample)
			re, gain := g.re[lo:hi], g.gain[lo:hi]
			for i := range re {
				// Collocated: the output map is y_si = sum(gain[i]*re[i]), so
				// injecting alpha*gain[i] uses the SAME shape the string is read
				// through. The change in sum(re^2+im^2) is then 2*alpha*y_si,
				// and summing over strings gives -2*b*sample^2 <= 0 for the
				// BRIDGE half - which is what lets the modal core claim the same
				// strict energy bound the waveguide core does
				// (TestBridgeCouplingDoesNotAddEnergy holds both to 1.0).
				//
				// It does NOT rescue the crossfeed half. Distributing it was
				// measured against the mode-0 form it replaced and came back at
				// 0.99951-1.00729, the same range - so the 1.02 tolerance
				// TestModalUnisonCouplingDoesNotPumpTheBank carries is not an
				// artefact of landing the force on mode 0, as the comment above
				// assumed, but of the difference form itself. PLAN.md 14.1's
				// residual stays open.
				re[i] += alpha * gain[i]
			}
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

// modalFlushThreshold is the magnitude below which a mode's state is forced to
// zero. It sits far above the float32 denormal range (~1.18e-38) yet far below
// anything audible (~-500 dBFS), so the flush is inaudible but still catches a
// decaying mode before it reaches denormal territory.
//
// Checking once per block is enough: the slowest per-sample decay factor is
// 0.86, so over 128 samples a mode shrinks by at most ~4.3e-9. A mode just
// above the threshold therefore stays normal until the next check.
const modalFlushThreshold = 1e-25

// flushDenormalModes zeroes modes that have decayed into inaudibility.
//
// Without this, high partials reach the denormal range long before the
// fundamental goes quiet, and — because a sustained group never deactivates —
// they stay there indefinitely. Denormal arithmetic is microcoded and stalls
// the whole SIMD lane group, which measurably dominates the modal render cost
// during long sustains.
func (g *ModalStringGroup) flushDenormalModes() {
	for i := range g.re {
		re, im := g.re[i], g.im[i]
		if re > -modalFlushThreshold && re < modalFlushThreshold &&
			im > -modalFlushThreshold && im < modalFlushThreshold {
			g.re[i] = 0
			g.im[i] = 0
		}
	}
}

func (g *ModalStringGroup) endBlock(blockEnergy float64, frames int) bool {
	g.flushDenormalModes()

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
