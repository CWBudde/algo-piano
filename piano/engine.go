package piano

// Piano is the global engine managing note control, excitation, and ringing state.
type Piano struct {
	sampleRate    int
	params        *Params
	keys          *keyStateTracker
	hammerExciter *HammerExciter
	ringing       *RingingState
	bodyConvolver *BodyConvolver
	roomConvolver *SoundboardConvolver
	resonance     *ResonanceEngine
	sustainAmount float32
	softPedal     bool
	// irMixExplicit records that SetIRMix was called, so the radiation mix must
	// be taken from the dual-IR params verbatim instead of remapping the
	// deprecated single-IR params.
	irMixExplicit bool
	// mix is the resolved linear radiation mix. It is computed once — at
	// construction and again in SetIRMix — so that Process never has to
	// re-evaluate the legacy fallback per block. See resolveRadiationMix.
	mix radiationMix
}

// radiationMix holds the resolved gains of the linear radiation path that sits
// around the string bank output. The locked signal flow is
//
//	string-bank bridge mix -> body IR (mono->mono) -> room IR (mono->stereo)
//
// and this struct only decides how loud each stage appears in the output; it
// never reorders the stages. Keeping it separate from Params is what makes the
// legacy single-IR path a pure fallback: the deprecated IRWavPath/IRWetMix/
// IRDryMix/IRGain fields are folded into these four numbers exactly once, so
// the per-block render path reads dual-IR semantics and nothing else.
type radiationMix struct {
	// bodyDry scales the body-convolved (dry) branch in the output sum.
	bodyDry float32
	// bodyGain scales the *dry* body branch only: Process multiplies the body
	// convolver output by it when summing the dry signal, but the room
	// convolver is fed the unscaled body signal. A room-only mix (bodyDry = 0)
	// is therefore unaffected by BodyIRGain. This is pre-existing behaviour and
	// deliberately documented rather than changed, because moving the gain
	// ahead of the room convolution would alter every rendered sample.
	bodyGain float32
	// roomWet scales the room-convolved (wet) branch in the output sum.
	roomWet float32
	// roomGain scales the room convolver output.
	roomGain float32
}

// resolveRadiationMix folds the params into the four radiation gains once.
//
// This is the single place where the deprecated single-IR parameters are
// translated, and it is deliberately not called per block: re-evaluating the
// legacy branch on every Process call silently overrode BodyIRGain whenever a
// preset still carried a legacy IRWavPath, which made the effective body gain
// depend on how the params happened to be spelled rather than on what the
// caller asked for.
//
// irMixExplicit is set once SetIRMix supplied a dual-IR mix directly. The
// legacy parameters cannot express a body gain, so an explicit mix must win
// over the fallback remap.
func resolveRadiationMix(params *Params, irMixExplicit bool) radiationMix {
	// Backwards-compatible defaults: a fully dry body branch, no room.
	mix := radiationMix{bodyDry: 1.0, bodyGain: 1.0, roomWet: 0.0, roomGain: 1.0}
	if params == nil {
		return mix
	}

	// Preferred path: the first-class dual-IR fields.
	if params.BodyDryMix >= 0 {
		mix.bodyDry = params.BodyDryMix
	}
	if params.BodyIRGain > 0 {
		mix.bodyGain = params.BodyIRGain
	}
	if params.RoomWetMix >= 0 {
		mix.roomWet = params.RoomWetMix
	}
	if params.RoomGain > 0 {
		mix.roomGain = params.RoomGain
	}

	// Fallback only: a preset that names just the deprecated single IR gets its
	// legacy mix mapped onto the room branch, which is where that IR is loaded
	// (see NewPiano). The legacy form has no notion of a separate body gain, so
	// the body branch stays at unity.
	if !irMixExplicit && params.RoomIRWavPath == "" && params.BodyIRWavPath == "" && params.IRWavPath != "" {
		mix.bodyDry = params.IRDryMix
		mix.roomWet = params.IRWetMix
		mix.roomGain = params.IRGain
		mix.bodyGain = 1.0
	}

	return mix
}

// NewPiano creates a new piano engine.
func NewPiano(sampleRate int, maxPolyphony int, params *Params) *Piano {
	_ = maxPolyphony // Retained in API for compatibility; ringing state is persistent.
	p := &Piano{
		sampleRate:    sampleRate,
		params:        params,
		keys:          newKeyStateTracker(),
		hammerExciter: NewHammerExciter(sampleRate, params),
		ringing:       NewRingingState(sampleRate, params),
		bodyConvolver: NewBodyConvolver(sampleRate),
		roomConvolver: NewSoundboardConvolver(sampleRate),
	}
	if params == nil || params.ResonanceEnabled {
		gain := float32(0.00018)
		perNoteFilter := true
		if params != nil && params.ResonanceGain > 0 {
			gain = params.ResonanceGain
		}
		if params != nil {
			perNoteFilter = params.ResonancePerNoteFilter
		}
		p.resonance = NewResonanceEngine(sampleRate, gain, perNoteFilter)
	}
	// Load body IR from file if specified.
	if params != nil && params.BodyIRWavPath != "" {
		_ = p.bodyConvolver.SetIRFromWAV(params.BodyIRWavPath, sampleRate)
	}
	// Load room IR: prefer RoomIRWavPath, fall back to legacy IRWavPath.
	if params != nil {
		roomPath := params.RoomIRWavPath
		if roomPath == "" {
			roomPath = params.IRWavPath
		}
		if roomPath != "" {
			_ = p.roomConvolver.SetIRFromWAV(roomPath)
		}
	}
	// Normalise the radiation mix once, here, so the legacy single-IR fallback
	// is resolved before the first block instead of on every block.
	p.mix = resolveRadiationMix(params, p.irMixExplicit)
	return p
}

// NoteOn triggers a new note.
func (p *Piano) NoteOn(note int, velocity int) {
	p.keys.NoteOn(note, velocity)
	p.ringing.SetKeyDown(note, true)
	p.hammerExciter.Trigger(note, velocity)
}

// KeyDown presses a key without hammer excitation (damper lift only).
func (p *Piano) KeyDown(note int) {
	p.keys.NoteOn(note, 0)
	p.ringing.SetKeyDown(note, true)
}

// NoteOff releases a note.
func (p *Piano) NoteOff(note int) {
	p.keys.NoteOff(note)
	p.ringing.SetKeyDown(note, false)
}

// SetSustainPedal sets sustain pedal state (true = down, false = up).
// It is the fully-up / fully-down case of SetSustainPedalAmount.
func (p *Piano) SetSustainPedal(down bool) {
	if down {
		p.SetSustainPedalAmount(1)
		return
	}
	p.SetSustainPedalAmount(0)
}

// SetSustainPedalAmount sets continuous sustain pedal depth in [0,1]. The depth
// maps directly onto the physical damper contact of every string in the bank:
// 0 seats all dampers, 1 lifts them completely, and intermediate values model a
// half-pedal where the dampers only rest partly on the strings.
func (p *Piano) SetSustainPedalAmount(amount float32) {
	amount = clampf(amount, 0, 1)
	p.sustainAmount = amount
	p.ringing.SetSustainAmount(amount)
}

// SetSoftPedal sets una corda / soft pedal state (true = down, false = up).
func (p *Piano) SetSoftPedal(down bool) {
	p.softPedal = down
	p.hammerExciter.SetSoftPedal(down)
}

// SetCouplingMode updates string-bank coupling mode at runtime.
func (p *Piano) SetCouplingMode(mode CouplingMode) bool {
	if p == nil || p.ringing == nil {
		return false
	}
	ok := p.ringing.SetCouplingMode(mode)
	if ok && p.params != nil {
		p.params.CouplingMode = mode
		p.params.CouplingEnabled = mode != CouplingModeOff
	}
	return ok
}

// SetStringModel switches string core (`dwg` or `modal`) and reinitializes ringing state.
func (p *Piano) SetStringModel(model StringModel) bool {
	if p == nil {
		return false
	}
	switch model {
	case StringModelDWG, StringModelModal:
	default:
		return false
	}

	if p.params == nil {
		p.params = NewDefaultParams()
	}
	if p.params.StringModel == model && p.ringing != nil && p.ringing.StringModel() == model {
		return true
	}

	var held [128]bool
	var velocity [128]int
	if p.keys != nil {
		held = p.keys.keyDown
		velocity = p.keys.lastVelocity
	}
	sustain := p.sustainAmount
	soft := p.softPedal

	p.params.StringModel = model
	p.keys = newKeyStateTracker()
	p.hammerExciter = NewHammerExciter(p.sampleRate, p.params)
	p.hammerExciter.SetSoftPedal(soft)
	p.ringing = NewRingingState(p.sampleRate, p.params)
	p.ringing.SetSustainAmount(sustain)
	for note := 0; note < 128; note++ {
		if !held[note] {
			continue
		}
		p.keys.NoteOn(note, velocity[note])
		p.ringing.SetKeyDown(note, true)
	}
	return true
}

// SetIR sets the room impulse response from pre-computed stereo buffers.
// Deprecated: Use SetRoomIR instead.
func (p *Piano) SetIR(left, right []float32) {
	p.roomConvolver.SetIR(left, right)
}

// SetBodyIR sets the mono body impulse response from pre-computed buffer.
func (p *Piano) SetBodyIR(ir []float32) {
	p.bodyConvolver.SetIR(ir)
}

// SetRoomIR sets the stereo room impulse response from pre-computed buffers.
func (p *Piano) SetRoomIR(left, right []float32) {
	p.roomConvolver.SetIR(left, right)
}

// SetBodyIRFromBytes loads the mono body impulse response from in-memory WAV
// bytes, resampling to the engine sample rate if needed.
func (p *Piano) SetBodyIRFromBytes(data []byte) error {
	return p.bodyConvolver.SetIRFromBytes(data, p.sampleRate)
}

// SetRoomIRFromBytes loads the stereo room impulse response from in-memory WAV
// bytes, resampling to the engine sample rate if needed.
func (p *Piano) SetRoomIRFromBytes(data []byte) error {
	return p.roomConvolver.SetIRFromBytes(data)
}

// SetIRMix sets the dual-IR mix parameters at runtime.
// bodyGain and roomGain are only applied when strictly positive, matching the
// defaults used by Process.
func (p *Piano) SetIRMix(bodyDry, bodyGain, roomWet, roomGain float32) {
	if p.params == nil {
		p.params = NewDefaultParams()
	}
	p.params.BodyDryMix = bodyDry
	p.params.BodyIRGain = bodyGain
	p.params.RoomWetMix = roomWet
	p.params.RoomGain = roomGain
	// Mirror the mix onto the deprecated params so callers that still read them
	// observe the same settings, and disable the legacy remap in Process: it
	// cannot represent bodyGain and would otherwise reset it to 1.
	p.params.IRDryMix = bodyDry
	p.params.IRWetMix = roomWet
	p.params.IRGain = roomGain
	p.irMixExplicit = true
	// Re-resolve now rather than per block; irMixExplicit suppresses the legacy
	// remap, which cannot represent bodyGain and would reset it to 1.
	p.mix = resolveRadiationMix(p.params, p.irMixExplicit)
}

// Process renders a block of audio samples (stereo interleaved).
func (p *Piano) Process(numFrames int) []float32 {
	monoMix := p.ringing.Process(numFrames, p.hammerExciter)

	if p.resonance != nil && p.resonance.InjectFromBridge(monoMix, p.ringing.ResonanceTargets()) {
		p.ringing.NotifyResonanceInjected()
	}

	// Signal flow: string bank → body convolver (mono→mono) → room convolver (mono→stereo)
	bodyMono := p.bodyConvolver.Process(monoMix)
	stereoRoom := p.roomConvolver.Process(bodyMono)

	stereoOutput := make([]float32, numFrames*2)

	// The radiation mix is already resolved (construction / SetIRMix), so the
	// per-block path only ever reads the dual-IR semantics.
	bodyDry := p.mix.bodyDry
	bodyGain := p.mix.bodyGain
	roomWet := p.mix.roomWet
	roomGain := p.mix.roomGain

	outGain := float32(1.0)
	if p.params != nil && p.params.OutputGain > 0 {
		outGain = p.params.OutputGain
	}

	for i := 0; i < numFrames; i++ {
		body := bodyMono[i] * bodyGain
		l := bodyDry*body + roomWet*stereoRoom[i*2]*roomGain
		r := bodyDry*body + roomWet*stereoRoom[i*2+1]*roomGain
		stereoOutput[i*2] = l * outGain
		stereoOutput[i*2+1] = r * outGain
	}

	return stereoOutput
}
