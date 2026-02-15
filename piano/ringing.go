package piano

import "math"

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
	unisonCrossfeed float32
	groups          [128]*RingingStringGroup
	targets         []resonanceTarget
	active          [128]bool
	activeNotes     []int
	blockEnergy     [128]float64
}

func NewStringBank(sampleRate int, params *Params) *StringBank {
	unisonCrossfeed := float32(0.0008)
	if params != nil && params.UnisonCrossfeed >= 0 {
		unisonCrossfeed = params.UnisonCrossfeed
	}

	sb := &StringBank{
		unisonCrossfeed: unisonCrossfeed,
		targets:         make([]resonanceTarget, 0, 128),
		activeNotes:     make([]int, 0, 32),
	}
	for note := 0; note < 128; note++ {
		g := newRingingStringGroup(sampleRate, note, params)
		sb.groups[note] = g
		sb.targets = append(sb.targets, g)
	}
	return sb
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

func (sb *StringBank) Process(numFrames int, hammer *HammerExciter) []float32 {
	out := make([]float32, numFrames)
	if numFrames <= 0 {
		return out
	}
	if len(sb.activeNotes) == 0 {
		for i := 0; i < numFrames; i++ {
			if hammer != nil {
				hammer.ProcessSample(sb)
			}
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
			g := sb.groups[note]
			if g == nil || !g.active {
				continue
			}
			s := g.processSample(sb.unisonCrossfeed)
			mix += s
			sf := float64(s)
			sb.blockEnergy[note] += sf * sf
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
