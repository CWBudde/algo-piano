package piano

import (
	"math"
	"sort"
)

type ringingGroup interface {
	resonanceTarget
	setKeyDown(down bool)
	setSustainAmount(amount float32)
	injectHammerForce(force float32, strikePos float32)
	injectCouplingForce(force float32)
	processSample(unisonCrossfeed float32, bridgeCoupling float32) float32
	endBlock(blockEnergy float64, frames int) bool
	isActive() bool
	takeResonanceEnergy() bool
	stringCount() int
	fundamental() float32
}

// RingingStringGroup is a persistent string group for one note.
type RingingStringGroup struct {
	note       int
	f0         float32
	strings    []*StringWaveguide
	gains      []float32
	resFilters []noteResonator

	// stringOut holds this sample's per-string output so processSample can
	// subtract a string's own contribution from the bridge mix. It is sized
	// once in the constructor and only ever overwritten, so the per-sample
	// coupling stays allocation-free.
	stringOut []float32

	keyDown       bool
	sustainAmount float32
	active        bool
	quietBlocks   int

	// resonanceEnergized records that injectResonance deposited energy since
	// the bank last looked. The bank clears it when it enrolls the note.
	resonanceEnergized bool
}

type couplingEdge struct {
	to   int
	gain float32
}

const (
	couplingPhysicalBaseGain    = float32(0.0005)
	couplingPhysicalMinScore    = float32(0.0002)
	couplingPhysicalMaxPartials = 8
)

func newRingingStringGroup(sampleRate int, note int, params *Params) *RingingStringGroup {
	lossGain := float32(0.9998)
	highFreqDamping := float32(0.05)
	inharmonicity := float32(0.0)
	unisonDetuneScale := float32(1.0)
	unisonCrossfeed := DefaultUnisonCrossfeed
	_ = unisonCrossfeed // configured on bank level, kept here for parameter parity.

	if params != nil {
		if params.UnisonDetuneScale >= 0 {
			unisonDetuneScale = params.UnisonDetuneScale
		}
		if params.HighFreqDamping > 0 {
			highFreqDamping = params.HighFreqDamping
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
	strings := make([]*StringWaveguide, 0, len(detunes))
	for i := range detunes {
		ratio := centsToRatio(detunes[i] * unisonDetuneScale)
		str := NewStringWaveguide(sampleRate, freq*ratio)
		str.SetLoopLoss(lossGain, highFreqDamping)
		str.SetDispersion(inharmonicity)
		// Piano starts damped unless key is held or sustain pedal is down.
		str.SetDamper(true)
		strings = append(strings, str)
	}

	g := &RingingStringGroup{
		note:      note,
		f0:        freq,
		strings:   strings,
		gains:     append([]float32(nil), gains...),
		stringOut: make([]float32, len(strings)),
	}
	g.initResonanceFilters(sampleRate)
	return g
}

func (g *RingingStringGroup) initResonanceFilters(sampleRate int) {
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

func (g *RingingStringGroup) setKeyDown(down bool) {
	g.keyDown = down
	g.updateDamperState()
	if down {
		g.active = true
		g.quietBlocks = 0
	}
}

func (g *RingingStringGroup) setSustainAmount(amount float32) {
	g.sustainAmount = clampf(amount, 0, 1)
	g.updateDamperState()
	if g.sustainAmount > 0 {
		g.active = true
		g.quietBlocks = 0
	}
}

func (g *RingingStringGroup) updateDamperState() {
	damping := g.damperAmount()
	for _, s := range g.strings {
		s.SetDamperAmount(damping)
	}
}

// damperAmount is how firmly the damper rests on the string: a held key lifts
// it completely, otherwise the pedal lifts it in proportion to its depth.
func (g *RingingStringGroup) damperAmount() float32 {
	if g.keyDown {
		return 0
	}
	return 1 - g.sustainAmount
}

func (g *RingingStringGroup) isUndamped() bool {
	return g.keyDown || g.sustainAmount > 0
}

func (g *RingingStringGroup) isActive() bool {
	return g.active
}

func (g *RingingStringGroup) filterResonanceDrive(x float32) float32 {
	if len(g.resFilters) == 0 {
		return x
	}
	sum := float32(0)
	for i := range g.resFilters {
		sum += g.resFilters[i].process(x)
	}
	return sum
}

func (g *RingingStringGroup) injectResonance(energy float32) {
	if energy == 0 {
		return
	}
	for i, s := range g.strings {
		s.InjectForceAtPosition(energy*g.stringGain(i), 0.82)
	}
	g.active = true
	g.resonanceEnergized = true
	g.quietBlocks = 0
}

// takeResonanceEnergy reports whether sympathetic energy was injected since the
// last call and clears the flag.
func (g *RingingStringGroup) takeResonanceEnergy() bool {
	if !g.resonanceEnergized {
		return false
	}
	g.resonanceEnergized = false
	return true
}

func (g *RingingStringGroup) injectHammerForce(force float32, strikePos float32) {
	if force == 0 {
		return
	}
	for _, s := range g.strings {
		s.InjectForceAtPosition(force, strikePos)
	}
	g.active = true
	g.quietBlocks = 0
}

func (g *RingingStringGroup) injectCouplingForce(force float32) {
	if force == 0 {
		return
	}
	for i, s := range g.strings {
		s.InjectForceAtPosition(force*g.stringGain(i), 0.9)
	}
	g.active = true
	g.quietBlocks = 0
}

// maxUnisonCrossfeed bounds the coupling strength a preset may ask for.
//
// The difference form below is dissipative in the instantaneous sense at any
// coupling strength, but the injection still costs one sample of delay, and at a
// large enough gain that single sample is enough to turn it around. Measured
// 2026-08-23 on the 120 s pedal-held chord render, peak of the last window over
// the 25-30 s window: 0.1322x at c = 0.02, 0.1332x at 0.1, 0.1344x at 0.25, and
// non-finite at 0.5. The transition is abrupt, so the bound is set well inside
// the stable region rather than near the cliff: 0.02 is 12x under the largest
// value verified stable and 4x above the 0.005 ceiling cmd/piano-fit/knobs.go
// gives the unison_crossfeed knob, so no fit can reach it and no shipped preset
// is affected (the largest in assets/presets is 0.005).
const maxUnisonCrossfeed = float32(0.02)

// processSample advances every string of the group by one sample, mixes them
// with the unison gains, and couples them to each other through the bridge.
//
// The coupling force on string i is proportional to mix - y_i, the difference
// between the common bridge motion and that string's own contribution to it.
// The subtraction is what makes the term dissipative, and it is not a detail:
// with unison gains that sum to one, the energy the coupling adds per sample is
// c*(mix^2 - sum(g_i*y_i^2)), which is <= 0 by Jensen's inequality and is zero
// exactly when the strings already move together. A string louder than the
// bridge gives energy away, a string quieter than the bridge takes it, and the
// group as a whole can only lose. That is also the physical picture - the
// strings of a unison exchange energy through a shared, slightly compliant
// bridge termination - and it is what produces the BEATING a real unison has.
//
// It is NOT what produces two-stage decay, and this comment claimed otherwise
// until 2026-08-23. With gains summing to one the term vanishes identically for
// in-phase motion, so it damps the aftersound and leaves the prompt alone -
// the opposite of the ordering double decay needs. That is the job of the
// separate bridge_coupling term; see bridge_coupling.go.
//
// Until 2026-08-23 the force was c*mix, with no subtraction, injected into
// every string including the one that produced the sample, at strike position
// 0.92. That is not coupling but a bare positive feedback loop wrapped around an
// already resonant string, and it added energy unconditionally: the loop
// reflection loses 2e-4 per round trip while the crossfeed injects c per
// sample. Measured over 120 s, pedal held, one note struck and nothing else
// driving the bank, peak of the 115-120 s window over the 25-30 s window:
//
//	                       before   after     c = 0 (no coupling at all)
//	note 33 (1 string)     0.1469   0.146883  0.146883
//	note 36 (1 string)     0.0980   0.098027  0.098027
//	note 45 (2 strings)    0.5583   0.003556  0.025640
//	note 52 (2 strings)    1.2545   0.000326  0.006317
//	note 60 (2 strings)    1.7159   0.000003  0.000349
//
// Single-string notes are bit-identical because they have no coupling path at
// all, which is what identified the crossfeed as the source. Note that the
// corrected coupling decays FASTER than c = 0 on every multi-string note - it
// takes energy out, as a dissipative term must.
//
// WHERE THE FORCE IS WRITTEN matters as much as its sign. The dissipativity
// argument above is instantaneous: it holds only while the force acts on the
// signal it was computed from. A force that comes back a fraction p of a round
// trip later lags 2*pi*n*p at partial n, and once that passes a half cycle the
// same term is ANTI-damping. InjectForceNext writes into the slot the taps read
// on the very next Process call, which is the shortest path that exists.
//
// A strike position cannot express that. injectionOffset maps [0,1] affinely
// onto the observable window, so its smallest input is 1% of the ROUND TRIP -
// about 1 sample at MIDI 60 but 5 at MIDI 40 and 17 at MIDI 21, and MIDI 40 is
// exactly where multi-string groups begin. Same chord render, 120 s:
//
//	              pos 0.92    pos 0.01    InjectForceNext
//	c = 0.0008      0.1166      0.1275     0.1270
//	c = 0.0025      0.2014      0.1315     0.1306
//	c = 0.0050     88.4618      0.1328     0.1325
//	diverges at      <0.005         0.1        0.5
//
// The middle column is why the plain difference form was not enough on its own:
// at c = 0.005, the value assets/presets/fitted-c4.json ships, it still grew
// 88x. Writing the force at the freshest slot instead widens the stable range
// by a further 5x. maxUnisonCrossfeed fences the rest.
//
// The DC half of this same defect was patched once already, by putting a DC
// blocker inside the string loop - see the "DC runaway" paragraph in
// tuning_test.go, which names "unison crossfeed injects into every string of the
// group on every sample" as the cause. This is its AC half, fixed at the source
// rather than downstream.
func (g *RingingStringGroup) processSample(unisonCrossfeed float32, bridgeCoupling float32) float32 {
	sample := float32(0)
	for i, s := range g.strings {
		y := s.Process()
		g.stringOut[i] = y
		sample += y * g.stringGain(i)
	}
	if len(g.strings) > 1 && (unisonCrossfeed > 0 || bridgeCoupling > 0) {
		for i, s := range g.strings {
			// The gain weight has to be the SAME one used to build the mix,
			// otherwise NEITHER energy argument closes. See
			// bridgeCouplingForce for the second one.
			//
			// The two terms damp orthogonal motions. unisonCrossfeed damps
			// RELATIVE motion and is identically zero when the strings already
			// move together; bridgeCoupling damps the COMMON motion and is
			// identically zero when they cancel at the bridge. Only the second
			// one loads the bridge, and only the second one makes the decay
			// two-stage.
			// Written as two separately-weighted terms rather than one
			// factored expression on purpose: float32 multiplication does not
			// reassociate, and keeping the crossfeed's original operand order
			// makes a bridgeCoupling = 0 render bit-identical to the one from
			// before this term existed. Every preset in assets/presets pins it
			// to zero, so that is what keeps gate-c4 honest.
			gain := g.stringGain(i)
			force := unisonCrossfeed * gain * (sample - g.stringOut[i])
			if bridgeCoupling > 0 {
				force -= bridgeCoupling * gain * sample
			}
			s.InjectForceNext(force)
		}
	}
	return sample
}

// stringGain returns the unison gain for string i, defaulting to unity. It
// mirrors ModalStringGroup.stringGain so the two cores weight their unisons the
// same way.
func (g *RingingStringGroup) stringGain(i int) float32 {
	if i < len(g.gains) {
		return g.gains[i]
	}
	return 1.0
}

func (g *RingingStringGroup) endBlock(blockEnergy float64, frames int) bool {
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

func (g *RingingStringGroup) stringCount() int {
	return len(g.strings)
}

func (g *RingingStringGroup) fundamental() float32 {
	return g.f0
}

// StringBank owns persistent ringing state for configured piano note range.
type StringBank struct {
	sampleRate               int
	minNote                  int
	maxNote                  int
	stringModel              StringModel
	unisonCrossfeed          float32
	bridgeCoupling           float32
	couplingEnabled          bool
	couplingMode             CouplingMode
	couplingAmount           float32
	couplingMaxForce         float32
	staticOctaveGain         float32
	staticFifthGain          float32
	couplingMaxNeighbors     int
	couplingHarmonicFalloff  float32
	couplingDetuneSigmaCents float32
	couplingDistanceExponent float32
	groups                   [128]*RingingStringGroup
	modalGroups              [128]*ModalStringGroup
	resonancePending         bool
	targets                  []resonanceTarget
	// resonance closes the sympathetic loop INSIDE the per-sample render loop.
	// It is nil by default — an unwired bank renders exactly as it did before
	// the loop moved in here, which is what every open-loop probe relies on.
	// A concrete pointer rather than an interface: this is read once per sample
	// in the hot loop.
	resonance *ResonanceEngine
	// prevMix is the last rendered bridge sample, carried across block
	// boundaries so the loop delay is one SAMPLE and not one block. It lives on
	// the bank rather than on the engine because it is bank output, and because
	// the engine is detached and re-attached across a core switch.
	prevMix     float32
	coupling    [128][]couplingEdge
	distanceMap [128][128]float32
	active      [128]bool
	activeNotes []int
	blockEnergy [128]float64
	couplingSum [128]float64
	couplingAbs [128]float64
	sampleOut   [128]float32
	outputBuf   []float32
	modalArena  *modalArena
}

func sanitizeNoteRange(minNote int, maxNote int) (int, int) {
	if minNote < 0 {
		minNote = 0
	}
	if minNote > 127 {
		minNote = 127
	}
	if maxNote < 0 {
		maxNote = 0
	}
	if maxNote > 127 {
		maxNote = 127
	}
	if minNote > maxNote {
		minNote, maxNote = maxNote, minNote
	}
	return minNote, maxNote
}

func NewStringBank(sampleRate int, params *Params) *StringBank {
	unisonCrossfeed := DefaultUnisonCrossfeed
	bridgeCoupling := DefaultBridgeCoupling
	stringModel := StringModelDWG
	minNote := 21
	maxNote := 108
	couplingEnabled := true
	couplingMode := CouplingModeStatic
	couplingAmount := float32(1.0)
	couplingOctaveGain := float32(0.00018)
	couplingFifthGain := float32(0.00008)
	couplingMaxForce := float32(0.00045)
	couplingHarmonicFalloff := float32(1.35)
	couplingDetuneSigmaCents := float32(28.0)
	couplingDistanceExponent := float32(1.15)
	couplingMaxNeighbors := 10

	if params != nil && params.UnisonCrossfeed >= 0 {
		unisonCrossfeed = params.UnisonCrossfeed
	}
	// A preset is a JSON file a human can edit, so the coupling strength is
	// clamped rather than trusted. See maxUnisonCrossfeed for the measurements
	// this bound comes from; nothing in assets/presets or reachable by
	// cmd/piano-fit is affected by it.
	if unisonCrossfeed > maxUnisonCrossfeed {
		unisonCrossfeed = maxUnisonCrossfeed
	}
	if params != nil && params.BridgeCoupling >= 0 {
		bridgeCoupling = params.BridgeCoupling
	}
	// Same reasoning, its own bound: see maxBridgeCoupling in
	// bridge_coupling.go. The two terms damp orthogonal motions and diverge at
	// different strengths, so they do not share a clamp.
	if bridgeCoupling > maxBridgeCoupling {
		bridgeCoupling = maxBridgeCoupling
	}
	if params != nil {
		if params.StringModel != "" {
			switch params.StringModel {
			case StringModelDWG, StringModelModal:
				stringModel = params.StringModel
			}
		}
		couplingEnabled = params.CouplingEnabled
		if params.CouplingMode != "" {
			switch params.CouplingMode {
			case CouplingModeOff, CouplingModeStatic, CouplingModePhysical:
				couplingMode = params.CouplingMode
			}
		}
		if params.CouplingAmount >= 0 {
			couplingAmount = clampFloat32(params.CouplingAmount, 0, 1)
		}
		if params.CouplingOctaveGain >= 0 {
			couplingOctaveGain = params.CouplingOctaveGain
		}
		if params.CouplingFifthGain >= 0 {
			couplingFifthGain = params.CouplingFifthGain
		}
		if params.CouplingMaxForce > 0 {
			couplingMaxForce = params.CouplingMaxForce
		}
		if params.CouplingHarmonicFalloff > 0 {
			couplingHarmonicFalloff = params.CouplingHarmonicFalloff
		}
		if params.CouplingDetuneSigmaCents > 0 {
			couplingDetuneSigmaCents = params.CouplingDetuneSigmaCents
		}
		if params.CouplingDistanceExponent >= 0 {
			couplingDistanceExponent = params.CouplingDistanceExponent
		}
		if params.CouplingMaxNeighbors > 0 {
			couplingMaxNeighbors = params.CouplingMaxNeighbors
		}
		minNote = params.MinNote
		maxNote = params.MaxNote
	}
	minNote, maxNote = sanitizeNoteRange(minNote, maxNote)
	if !couplingEnabled || couplingAmount <= 0 {
		couplingMode = CouplingModeOff
	}

	sb := &StringBank{
		sampleRate:               sampleRate,
		minNote:                  minNote,
		maxNote:                  maxNote,
		stringModel:              stringModel,
		unisonCrossfeed:          unisonCrossfeed,
		bridgeCoupling:           bridgeCoupling,
		couplingEnabled:          couplingMode != CouplingModeOff,
		couplingMode:             couplingMode,
		couplingAmount:           couplingAmount,
		couplingMaxForce:         couplingMaxForce,
		staticOctaveGain:         couplingOctaveGain,
		staticFifthGain:          couplingFifthGain,
		couplingMaxNeighbors:     couplingMaxNeighbors,
		couplingHarmonicFalloff:  couplingHarmonicFalloff,
		couplingDetuneSigmaCents: couplingDetuneSigmaCents,
		couplingDistanceExponent: couplingDistanceExponent,
		targets:                  make([]resonanceTarget, 0, 128),
		activeNotes:              make([]int, 0, 128),
	}
	for note := sb.minNote; note <= sb.maxNote; note++ {
		if stringModel == StringModelModal {
			g := newModalStringGroup(sampleRate, note, params)
			sb.modalGroups[note] = g
			sb.targets = append(sb.targets, g)
			continue
		}
		g := newRingingStringGroup(sampleRate, note, params)
		sb.groups[note] = g
		sb.targets = append(sb.targets, g)
	}
	if stringModel == StringModelModal {
		sb.modalArena = newModalArena(sb.modalGroups[:], sb.maxNote-sb.minNote+1)
	}
	sb.initDistanceMap()
	sb.rebuildCouplingGraph()
	return sb
}

func (sb *StringBank) ensureOutputBuffer(numFrames int) []float32 {
	if numFrames <= 0 {
		return sb.outputBuf[:0]
	}
	if cap(sb.outputBuf) < numFrames {
		sb.outputBuf = make([]float32, numFrames)
	}
	sb.outputBuf = sb.outputBuf[:numFrames]
	return sb.outputBuf
}

func (sb *StringBank) initDistanceMap() {
	for src := 0; src < 128; src++ {
		for dst := 0; dst < 128; dst++ {
			if src == dst {
				sb.distanceMap[src][dst] = 0
				continue
			}
			delta := float32(src - dst)
			if delta < 0 {
				delta = -delta
			}
			sb.distanceMap[src][dst] = delta / 12.0
		}
	}
}

func (sb *StringBank) noteInRange(note int) bool {
	if sb == nil {
		return false
	}
	return note >= sb.minNote && note <= sb.maxNote
}

func (sb *StringBank) initStaticCouplingGraph(octaveGain float32, fifthGain float32) {
	for i := range sb.coupling {
		sb.coupling[i] = sb.coupling[i][:0]
	}
	for note := sb.minNote; note <= sb.maxNote; note++ {
		srcScale := sb.sourceStringCouplingScale(note)
		edges := make([]couplingEdge, 0, 4)
		if octaveGain > 0 {
			if to := note + 12; sb.noteInRange(to) {
				edges = append(edges, couplingEdge{
					to:   to,
					gain: octaveGain * srcScale * sb.targetStringCouplingScale(to),
				})
			}
			if to := note - 12; sb.noteInRange(to) {
				edges = append(edges, couplingEdge{
					to:   to,
					gain: octaveGain * srcScale * sb.targetStringCouplingScale(to),
				})
			}
		}
		if fifthGain > 0 {
			if to := note + 7; sb.noteInRange(to) {
				edges = append(edges, couplingEdge{
					to:   to,
					gain: fifthGain * srcScale * sb.targetStringCouplingScale(to),
				})
			}
			if to := note - 7; sb.noteInRange(to) {
				edges = append(edges, couplingEdge{
					to:   to,
					gain: fifthGain * srcScale * sb.targetStringCouplingScale(to),
				})
			}
		}
		sb.coupling[note] = edges
	}
}

type couplingCandidate struct {
	to    int
	score float32
}

func (sb *StringBank) initPhysicalCouplingGraph(sampleRate int) {
	for i := range sb.coupling {
		sb.coupling[i] = sb.coupling[i][:0]
	}
	if sampleRate <= 0 {
		sb.couplingEnabled = false
		return
	}

	nyquist := 0.5 * float32(sampleRate)
	maxNeighbors := sb.couplingMaxNeighbors
	maxPossible := sb.maxNote - sb.minNote
	if maxNeighbors > maxPossible {
		maxNeighbors = maxPossible
	}
	for src := sb.minNote; src <= sb.maxNote; src++ {
		candidates := make([]couplingCandidate, 0, 24)
		for dst := sb.minNote; dst <= sb.maxNote; dst++ {
			if dst == src {
				continue
			}
			score := sb.physicalCouplingWeight(src, dst, nyquist)
			if score < couplingPhysicalMinScore {
				continue
			}
			candidates = append(candidates, couplingCandidate{to: dst, score: score})
		}
		if len(candidates) == 0 {
			continue
		}
		sort.Slice(candidates, func(i, j int) bool {
			return candidates[i].score > candidates[j].score
		})
		if len(candidates) > maxNeighbors {
			candidates = candidates[:maxNeighbors]
		}
		sumScore := float32(0)
		for _, c := range candidates {
			sumScore += c.score
		}
		if sumScore <= 0 {
			continue
		}
		edges := make([]couplingEdge, 0, len(candidates))
		outGain := couplingPhysicalBaseGain * sb.couplingAmount * sb.sourceStringCouplingScale(src)
		for _, c := range candidates {
			edges = append(edges, couplingEdge{
				to:   c.to,
				gain: outGain * (c.score / sumScore),
			})
		}
		sb.coupling[src] = edges
	}
}

func (sb *StringBank) physicalCouplingWeight(src int, dst int, nyquist float32) float32 {
	if src < 0 || src > 127 || dst < 0 || dst > 127 || src == dst {
		return 0
	}
	srcF0 := sb.noteFundamental(src)
	dstF0 := sb.noteFundamental(dst)
	if srcF0 <= 0 || dstF0 <= 0 {
		return 0
	}

	sum := float32(0)
	for m := 1; m <= couplingPhysicalMaxPartials; m++ {
		srcHarm := srcF0 * float32(m)
		if srcHarm >= nyquist*0.95 {
			break
		}
		srcStrength := float32(1.0 / math.Pow(float64(m), float64(sb.couplingHarmonicFalloff)))
		for n := 1; n <= couplingPhysicalMaxPartials; n++ {
			dstHarm := dstF0 * float32(n)
			if dstHarm >= nyquist*0.95 {
				break
			}
			dstStrength := float32(1.0 / math.Pow(float64(n), float64(0.65*sb.couplingHarmonicFalloff)))
			diffHz := srcHarm - dstHarm
			if diffHz < 0 {
				diffHz = -diffHz
			}
			refHz := srcHarm
			if dstHarm > refHz {
				refHz = dstHarm
			}
			bandwidthHz := 1.8 + 0.003*refHz
			ratio := diffHz / bandwidthHz
			align := float32(1.0 / (1.0 + float64(ratio*ratio)))

			cents := 1200.0 * math.Log2(float64(srcHarm/dstHarm))
			if cents < 0 {
				cents = -cents
			}
			detuneSigma := sb.couplingDetuneSigmaCents
			detuneRatio := float32(cents) / detuneSigma
			detunePenalty := float32(math.Exp(-0.5 * float64(detuneRatio*detuneRatio)))

			sum += srcStrength * dstStrength * align * detunePenalty
		}
	}

	if sum <= 0 {
		return 0
	}
	dist := sb.distanceMap[src][dst]
	if sb.couplingDistanceExponent <= 0 {
		return sum * sb.targetStringCouplingScale(dst)
	}
	distPenalty := float32(1.0 / math.Pow(float64(1.0+dist), float64(sb.couplingDistanceExponent)))
	return sum * distPenalty * sb.targetStringCouplingScale(dst)
}

func (sb *StringBank) sourceStringCouplingScale(note int) float32 {
	return stringCountCouplingScale(sb.noteStringCount(note))
}

func (sb *StringBank) targetStringCouplingScale(note int) float32 {
	return stringCountCouplingScale(sb.noteStringCount(note))
}

func stringCountCouplingScale(stringCount int) float32 {
	if stringCount <= 0 {
		return 1.0
	}
	const maxUnison = 3.0
	return float32(math.Sqrt(float64(stringCount) / maxUnison))
}

func (sb *StringBank) Group(note int) *RingingStringGroup {
	if !sb.noteInRange(note) {
		return nil
	}
	return sb.groups[note]
}

func (sb *StringBank) ModalGroup(note int) *ModalStringGroup {
	if !sb.noteInRange(note) {
		return nil
	}
	return sb.modalGroups[note]
}

func (sb *StringBank) StringModel() StringModel {
	if sb == nil {
		return StringModelDWG
	}
	return sb.stringModel
}

func (sb *StringBank) activeGroup(note int) ringingGroup {
	if !sb.noteInRange(note) {
		return nil
	}
	if sb.stringModel == StringModelModal {
		if g := sb.modalGroups[note]; g != nil {
			return g
		}
	}
	if g := sb.groups[note]; g != nil {
		return g
	}
	if g := sb.modalGroups[note]; g != nil {
		return g
	}
	return nil
}

func (sb *StringBank) noteStringCount(note int) int {
	g := sb.activeGroup(note)
	if g != nil {
		return g.stringCount()
	}
	if !sb.noteInRange(note) {
		return 0
	}
	detunes, _ := defaultUnisonForNote(note)
	if len(detunes) == 0 {
		return 0
	}
	return len(detunes)
}

func (sb *StringBank) noteFundamental(note int) float32 {
	g := sb.activeGroup(note)
	if g != nil {
		f0 := g.fundamental()
		if f0 > 0 {
			return f0
		}
	}
	if !sb.noteInRange(note) {
		return 0
	}
	return midiNoteToFreq(note)
}

func (sb *StringBank) markActive(note int) {
	if !sb.noteInRange(note) || sb.active[note] {
		return
	}
	sb.active[note] = true
	sb.activeNotes = append(sb.activeNotes, note)
}

// syncResonatingNotes enrolls groups that were energized outside the bank's own
// note handling. Sympathetic resonance is injected straight into the undamped
// groups, which marks a group active without ever touching the active-note
// list; without this sweep such a group would accumulate energy that Process
// never renders. The sweep runs at most once per block, only after an injection
// was reported, so it adds no per-sample work and no allocations (markActive
// appends into the pre-sized active-note list).
//
// It stays a once-per-block sweep even though injection is now per-sample, and
// that is deliberate: activeNotes and the modal arena layout are fixed for the
// duration of a block, so a group first energized at sample i cannot be rendered
// before the next block either way. The cost is exactly ONE block, ONCE per
// group, at first energization — after that endBlock returns true
// unconditionally for an undamped group, so with the pedal held every target
// enrolls within a block or two and never leaves. In steady state, which is what
// every stability test and every shipped preset runs, the loop is fully
// per-sample. Enrolling mid-block would need the arena's unbound fallback path
// and is a separate change.
func (sb *StringBank) syncResonatingNotes() {
	if !sb.resonancePending {
		return
	}
	sb.resonancePending = false
	for note := sb.minNote; note <= sb.maxNote; note++ {
		g := sb.activeGroup(note)
		if g == nil || !g.takeResonanceEnergy() {
			continue
		}
		sb.markActive(note)
	}
}

// SetResonanceEngine attaches (or, with nil, detaches) the sympathetic engine
// the render loop drives per sample. A bank with no engine renders exactly as it
// did before the loop moved inside StringBank, which is what the open-loop
// probes rely on. Injection is reachable no other way: it happens only from
// inside the render loop, so there is no second path that could deposit twice.
func (sb *StringBank) SetResonanceEngine(engine *ResonanceEngine) {
	if sb == nil {
		return
	}
	sb.resonance = engine
	sb.prevMix = 0
}

// injectResonanceSample closes (or, with a non-nil drive, breaks) the loop for
// one sample. Injection mid-block is safe in both cores: modalArena.acquire
// repoints g.re/g.im at the arena slab, so injectAtPosition writes into the
// arena and the next advance() picks it up — the same mechanism
// hammer.ProcessSample -> InjectHammerForce -> injectAtPosition already uses —
// and the DWG path simply adds at the delay line's write position.
func (sb *StringBank) injectResonanceSample(i int, drive []float32) {
	if sb.resonance == nil {
		return
	}
	x := sb.prevMix
	if drive != nil {
		if i >= len(drive) {
			return
		}
		x = drive[i]
	}
	if sb.resonance.injectSample(x, sb.targets) {
		sb.resonancePending = true
	}
}

func (sb *StringBank) SetKeyDown(note int, down bool) {
	g := sb.activeGroup(note)
	if g == nil {
		return
	}
	g.setKeyDown(down)
	if down {
		sb.markActive(note)
	}
}

func (sb *StringBank) SetSustain(down bool) {
	if down {
		sb.SetSustainAmount(1)
		return
	}
	sb.SetSustainAmount(0)
}

// SetSustainAmount applies a continuous pedal depth in [0,1] to every group in
// the persistent bank, including notes that have not been struck yet.
func (sb *StringBank) SetSustainAmount(amount float32) {
	for note := sb.minNote; note <= sb.maxNote; note++ {
		g := sb.activeGroup(note)
		if g == nil {
			continue
		}
		g.setSustainAmount(amount)
	}
}

func (sb *StringBank) InjectHammerForce(note int, force float32, strikePos float32) {
	g := sb.activeGroup(note)
	if g == nil {
		return
	}
	g.injectHammerForce(force, strikePos)
	sb.markActive(note)
}

func (sb *StringBank) InjectCouplingForce(note int, force float32) {
	if force == 0 {
		return
	}
	if sb.couplingMaxForce > 0 {
		if force > sb.couplingMaxForce {
			force = sb.couplingMaxForce
		} else if force < -sb.couplingMaxForce {
			force = -sb.couplingMaxForce
		}
	}
	g := sb.activeGroup(note)
	if g == nil {
		return
	}
	g.injectCouplingForce(force)
	sb.markActive(note)
}

// Process renders one block, closing the sympathetic loop internally when an
// engine is attached (SetResonanceEngine).
func (sb *StringBank) Process(numFrames int, hammer *HammerExciter) []float32 {
	return sb.processWithBridge(numFrames, hammer, nil)
}

// processWithBridge renders one block. When drive is nil the sympathetic loop is
// CLOSED: each sample is injected from the previous sample's own bank output. A
// non-nil drive BREAKS the loop and substitutes drive[i] for that output, which
// is what the open-loop probes in modal_resonance_test.go need in order to
// measure a response against a known excitation. It is unexported and only ever
// non-nil from in-package test code, so the render path never allocates it.
func (sb *StringBank) processWithBridge(numFrames int, hammer *HammerExciter, drive []float32) []float32 {
	out := sb.ensureOutputBuffer(numFrames)
	if numFrames <= 0 {
		return out
	}
	sb.syncResonatingNotes()
	if len(sb.activeNotes) == 0 {
		for i := 0; i < numFrames; i++ {
			if hammer != nil {
				hammer.ProcessSample(sb)
			}
			// Keep injecting so the per-sample filter chain (the DC blocker,
			// the 3.2 kHz pole and the per-note resonators) stays continuous
			// across an idle stretch. With no active group the mix is zero and
			// bandLimit decays under injectSample's 1e-8 gate within a few
			// samples, so this deposits nothing and costs nothing — which is
			// what TestSustainPedalAloneKeepsIdleBankEmpty pins.
			sb.injectResonanceSample(i, drive)
			out[i] = 0
			sb.prevMix = 0
		}
		return out
	}

	for _, note := range sb.activeNotes {
		sb.blockEnergy[note] = 0
		sb.couplingSum[note] = 0
		sb.couplingAbs[note] = 0
	}

	// Compact every active modal group into one contiguous mode block so the
	// whole bank advances with a single vectorized kernel call per sample.
	arena := sb.modalArena
	arenaBound := modalArenaEnabled && arena != nil && arena.acquire(sb, sb.activeNotes)

	for i := 0; i < numFrames; i++ {
		if hammer != nil {
			hammer.ProcessSample(sb)
		}
		// Inject BEFORE arena.advance() so the deposit is rotated and decayed
		// once before it is read out, exactly as hammer and coupling force are.
		sb.injectResonanceSample(i, drive)
		if arenaBound {
			arena.advance()
		}
		var mix float32
		for _, note := range sb.activeNotes {
			sb.sampleOut[note] = 0
			g := sb.activeGroup(note)
			if g == nil || !g.isActive() {
				continue
			}
			var s float32
			if arenaBound && arena.boundNote[note] {
				// Already advanced by arena.advance; only reduce and crossfeed.
				s = sb.modalGroups[note].reduceArenaSample(sb.unisonCrossfeed, sb.bridgeCoupling)
			} else {
				s = g.processSample(sb.unisonCrossfeed, sb.bridgeCoupling)
			}
			sb.sampleOut[note] = s
			mix += s
			sf := float64(s)
			sb.blockEnergy[note] += sf * sf
			sb.couplingSum[note] += sf
			if s < 0 {
				sb.couplingAbs[note] -= sf
			} else {
				sb.couplingAbs[note] += sf
			}
		}
		out[i] = mix
		sb.prevMix = mix
	}

	// Hand state back to the groups before coupling, so everything downstream
	// sees the ordinary per-group storage.
	if arenaBound {
		arena.release(sb)
	}

	if sb.couplingEnabled {
		sb.applySparseCouplingBlockwise(numFrames)
	}

	next := sb.activeNotes[:0]
	for _, note := range sb.activeNotes {
		g := sb.activeGroup(note)
		if g == nil {
			sb.active[note] = false
			continue
		}
		if g.endBlock(sb.blockEnergy[note], numFrames) {
			sb.active[note] = true
			next = append(next, note)
			continue
		}
		sb.active[note] = false
	}
	sb.activeNotes = next

	return out
}

func (sb *StringBank) applySparseCouplingBlockwise(numFrames int) {
	const eps = 1e-9
	if numFrames <= 0 {
		return
	}
	invFrames := float32(1.0 / float64(numFrames))
	polyScale := float32(1.0)
	if n := len(sb.activeNotes); n > 1 {
		polyScale = float32(1.0 / math.Sqrt(float64(n)))
	}
	for _, src := range sb.activeNotes {
		driveMag := float32(sb.couplingAbs[src]) * invFrames
		if driveMag > -eps && driveMag < eps {
			continue
		}
		driveSign := float32(sb.couplingSum[src]) * invFrames
		if driveSign > -eps && driveSign < eps {
			driveSign = sb.sampleOut[src]
		}
		srcDrive := driveMag
		if driveSign < 0 {
			srcDrive = -driveMag
		}
		edges := sb.coupling[src]
		for _, e := range edges {
			sb.InjectCouplingForce(e.to, srcDrive*e.gain*polyScale)
		}
	}
}

func (sb *StringBank) rebuildCouplingGraph() {
	for i := range sb.coupling {
		sb.coupling[i] = sb.coupling[i][:0]
	}

	if sb.couplingMode == CouplingModeOff || sb.couplingAmount <= 0 {
		sb.couplingEnabled = false
		return
	}

	sb.couplingEnabled = true
	switch sb.couplingMode {
	case CouplingModeStatic:
		sb.initStaticCouplingGraph(sb.staticOctaveGain*sb.couplingAmount, sb.staticFifthGain*sb.couplingAmount)
	case CouplingModePhysical:
		sb.initPhysicalCouplingGraph(sb.sampleRate)
	default:
		sb.couplingEnabled = false
	}
}

func (sb *StringBank) SetCouplingMode(mode CouplingMode) bool {
	if sb == nil {
		return false
	}
	switch mode {
	case CouplingModeOff, CouplingModeStatic, CouplingModePhysical:
	default:
		return false
	}
	sb.couplingMode = mode
	sb.rebuildCouplingGraph()
	return true
}

func clampFloat32(v float32, lo float32, hi float32) float32 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

type RingingState struct {
	bank *StringBank
}

func NewRingingState(sampleRate int, params *Params) *RingingState {
	return &RingingState{bank: NewStringBank(sampleRate, params)}
}

func (r *RingingState) SetKeyDown(note int, down bool) {
	if r == nil || r.bank == nil {
		return
	}
	r.bank.SetKeyDown(note, down)
}

func (r *RingingState) SetSustain(down bool) {
	if r == nil || r.bank == nil {
		return
	}
	r.bank.SetSustain(down)
}

// SetSustainAmount applies a continuous sustain pedal depth in [0,1].
func (r *RingingState) SetSustainAmount(amount float32) {
	if r == nil || r.bank == nil {
		return
	}
	r.bank.SetSustainAmount(amount)
}

func (r *RingingState) Process(numFrames int, hammer *HammerExciter) []float32 {
	if r == nil || r.bank == nil {
		return make([]float32, numFrames)
	}
	return r.bank.Process(numFrames, hammer)
}

// SetResonanceEngine attaches the sympathetic engine the bank's render loop
// drives per sample. Piano re-attaches after every rebuild of the ringing state.
func (r *RingingState) SetResonanceEngine(engine *ResonanceEngine) {
	if r == nil || r.bank == nil {
		return
	}
	r.bank.SetResonanceEngine(engine)
}

func (r *RingingState) SetCouplingMode(mode CouplingMode) bool {
	if r == nil || r.bank == nil {
		return false
	}
	return r.bank.SetCouplingMode(mode)
}

func (r *RingingState) StringModel() StringModel {
	if r == nil || r.bank == nil {
		return StringModelDWG
	}
	return r.bank.StringModel()
}
