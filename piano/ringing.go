package piano

import (
	"math"
	"sort"
)

// RingingStringGroup is a persistent string group for one note.
type RingingStringGroup struct {
	note       int
	f0         float32
	strings    []*StringWaveguide
	gains      []float32
	resFilters []noteResonator

	keyDown     bool
	sustainDown bool
	active      bool
	quietBlocks int
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
	unisonCrossfeed := float32(0.0008)
	_ = unisonCrossfeed // configured on bank level, kept here for parameter parity.

	if params != nil {
		if params.UnisonDetuneScale >= 0 {
			unisonDetuneScale = params.UnisonDetuneScale
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
		note:    note,
		f0:      freq,
		strings: strings,
		gains:   append([]float32(nil), gains...),
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

func (g *RingingStringGroup) setSustain(down bool) {
	g.sustainDown = down
	g.updateDamperState()
	if down {
		g.active = true
		g.quietBlocks = 0
	}
}

func (g *RingingStringGroup) updateDamperState() {
	engageDamper := !(g.keyDown || g.sustainDown)
	for _, s := range g.strings {
		s.SetDamper(engageDamper)
	}
}

func (g *RingingStringGroup) isUndamped() bool {
	return g.keyDown || g.sustainDown
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
		sg := float32(1.0)
		if i < len(g.gains) {
			sg = g.gains[i]
		}
		s.InjectForceAtPosition(energy*sg, 0.82)
	}
	g.active = true
	g.quietBlocks = 0
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
		sg := float32(1.0)
		if i < len(g.gains) {
			sg = g.gains[i]
		}
		s.InjectForceAtPosition(force*sg, 0.9)
	}
	g.active = true
	g.quietBlocks = 0
}

func (g *RingingStringGroup) processSample(unisonCrossfeed float32) float32 {
	sample := float32(0)
	for i, s := range g.strings {
		sg := float32(1.0)
		if i < len(g.gains) {
			sg = g.gains[i]
		}
		sample += s.Process() * sg
	}
	if len(g.strings) > 1 && unisonCrossfeed > 0 {
		cross := sample * unisonCrossfeed
		for _, s := range g.strings {
			s.InjectForceAtPosition(cross, 0.92)
		}
	}
	return sample
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

// StringBank owns persistent ringing state for all piano notes.
type StringBank struct {
	unisonCrossfeed          float32
	couplingEnabled          bool
	couplingMode             CouplingMode
	couplingAmount           float32
	couplingMaxForce         float32
	couplingMaxNeighbors     int
	couplingHarmonicFalloff  float32
	couplingDetuneSigmaCents float32
	couplingDistanceExponent float32
	groups                   [128]*RingingStringGroup
	targets                  []resonanceTarget
	coupling                 [128][]couplingEdge
	distanceMap              [128][128]float32
	active                   [128]bool
	activeNotes              []int
	blockEnergy              [128]float64
	sampleOut                [128]float32
	outputBuf                []float32
}

func NewStringBank(sampleRate int, params *Params) *StringBank {
	unisonCrossfeed := float32(0.0008)
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
	if params != nil {
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
	}
	if !couplingEnabled || couplingAmount <= 0 {
		couplingMode = CouplingModeOff
	}

	sb := &StringBank{
		unisonCrossfeed:          unisonCrossfeed,
		couplingEnabled:          couplingMode != CouplingModeOff,
		couplingMode:             couplingMode,
		couplingAmount:           couplingAmount,
		couplingMaxForce:         couplingMaxForce,
		couplingMaxNeighbors:     couplingMaxNeighbors,
		couplingHarmonicFalloff:  couplingHarmonicFalloff,
		couplingDetuneSigmaCents: couplingDetuneSigmaCents,
		couplingDistanceExponent: couplingDistanceExponent,
		targets:                  make([]resonanceTarget, 0, 128),
		activeNotes:              make([]int, 0, 128),
	}
	for note := 0; note < 128; note++ {
		g := newRingingStringGroup(sampleRate, note, params)
		sb.groups[note] = g
		sb.targets = append(sb.targets, g)
	}
	sb.initDistanceMap()
	switch sb.couplingMode {
	case CouplingModeStatic:
		sb.initStaticCouplingGraph(couplingOctaveGain*sb.couplingAmount, couplingFifthGain*sb.couplingAmount)
	case CouplingModePhysical:
		sb.initPhysicalCouplingGraph(sampleRate)
	default:
		sb.couplingEnabled = false
	}
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

func (sb *StringBank) initStaticCouplingGraph(octaveGain float32, fifthGain float32) {
	for i := range sb.coupling {
		sb.coupling[i] = sb.coupling[i][:0]
	}
	for note := 0; note < 128; note++ {
		edges := make([]couplingEdge, 0, 4)
		if octaveGain > 0 {
			if note+12 <= 127 {
				edges = append(edges, couplingEdge{to: note + 12, gain: octaveGain})
			}
			if note-12 >= 0 {
				edges = append(edges, couplingEdge{to: note - 12, gain: octaveGain})
			}
		}
		if fifthGain > 0 {
			if note+7 <= 127 {
				edges = append(edges, couplingEdge{to: note + 7, gain: fifthGain})
			}
			if note-7 >= 0 {
				edges = append(edges, couplingEdge{to: note - 7, gain: fifthGain})
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
	if maxNeighbors > 127 {
		maxNeighbors = 127
	}
	for src := 0; src < 128; src++ {
		candidates := make([]couplingCandidate, 0, 24)
		for dst := 0; dst < 128; dst++ {
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
	srcGroup := sb.groups[src]
	dstGroup := sb.groups[dst]
	if srcGroup == nil || dstGroup == nil || srcGroup.f0 <= 0 || dstGroup.f0 <= 0 {
		return 0
	}

	sum := float32(0)
	for m := 1; m <= couplingPhysicalMaxPartials; m++ {
		srcHarm := srcGroup.f0 * float32(m)
		if srcHarm >= nyquist*0.95 {
			break
		}
		srcStrength := float32(1.0 / math.Pow(float64(m), float64(sb.couplingHarmonicFalloff)))
		for n := 1; n <= couplingPhysicalMaxPartials; n++ {
			dstHarm := dstGroup.f0 * float32(n)
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
	g := sb.Group(note)
	if g == nil {
		return 1.0
	}
	return stringCountCouplingScale(len(g.strings))
}

func (sb *StringBank) targetStringCouplingScale(note int) float32 {
	g := sb.Group(note)
	if g == nil {
		return 1.0
	}
	return stringCountCouplingScale(len(g.strings))
}

func stringCountCouplingScale(stringCount int) float32 {
	if stringCount <= 0 {
		return 1.0
	}
	const maxUnison = 3.0
	return float32(math.Sqrt(float64(stringCount) / maxUnison))
}

func (sb *StringBank) Group(note int) *RingingStringGroup {
	if note < 0 || note > 127 {
		return nil
	}
	return sb.groups[note]
}

func (sb *StringBank) markActive(note int) {
	if note < 0 || note > 127 || sb.active[note] {
		return
	}
	sb.active[note] = true
	sb.activeNotes = append(sb.activeNotes, note)
}

func (sb *StringBank) SetKeyDown(note int, down bool) {
	g := sb.Group(note)
	if g == nil {
		return
	}
	g.setKeyDown(down)
	if down {
		sb.markActive(note)
	}
}

func (sb *StringBank) SetSustain(down bool) {
	for _, g := range sb.groups {
		if g == nil {
			continue
		}
		g.setSustain(down)
	}
}

func (sb *StringBank) InjectHammerForce(note int, force float32, strikePos float32) {
	g := sb.Group(note)
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
	g := sb.Group(note)
	if g == nil {
		return
	}
	g.injectCouplingForce(force)
	sb.markActive(note)
}

func (sb *StringBank) Process(numFrames int, hammer *HammerExciter) []float32 {
	out := sb.ensureOutputBuffer(numFrames)
	if numFrames <= 0 {
		return out
	}
	if len(sb.activeNotes) == 0 {
		for i := 0; i < numFrames; i++ {
			if hammer != nil {
				hammer.ProcessSample(sb)
			}
			out[i] = 0
		}
		return out
	}

	for _, note := range sb.activeNotes {
		sb.blockEnergy[note] = 0
	}

	for i := 0; i < numFrames; i++ {
		if hammer != nil {
			hammer.ProcessSample(sb)
		}
		var mix float32
		for _, note := range sb.activeNotes {
			sb.sampleOut[note] = 0
			g := sb.groups[note]
			if g == nil || !g.active {
				continue
			}
			s := g.processSample(sb.unisonCrossfeed)
			sb.sampleOut[note] = s
			mix += s
			sf := float64(s)
			sb.blockEnergy[note] += sf * sf
		}
		if sb.couplingEnabled {
			sb.applySparseCoupling()
		}
		out[i] = mix
	}

	next := sb.activeNotes[:0]
	for _, note := range sb.activeNotes {
		g := sb.groups[note]
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

func (sb *StringBank) applySparseCoupling() {
	const eps = 1e-9
	polyScale := float32(1.0)
	if n := len(sb.activeNotes); n > 1 {
		polyScale = float32(1.0 / math.Sqrt(float64(n)))
	}
	for _, src := range sb.activeNotes {
		srcSample := sb.sampleOut[src]
		if srcSample > -eps && srcSample < eps {
			continue
		}
		edges := sb.coupling[src]
		for _, e := range edges {
			sb.InjectCouplingForce(e.to, srcSample*e.gain*polyScale)
		}
	}
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

func (r *RingingState) Process(numFrames int, hammer *HammerExciter) []float32 {
	if r == nil || r.bank == nil {
		return make([]float32, numFrames)
	}
	return r.bank.Process(numFrames, hammer)
}

func (r *RingingState) ResonanceTargets() []resonanceTarget {
	if r == nil || r.bank == nil {
		return nil
	}
	return r.bank.targets
}
