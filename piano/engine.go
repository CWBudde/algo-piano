package piano

// Piano is the global engine managing voice allocation and polyphony.
type Piano struct {
	sampleRate    int
	voices        []*Voice
	params        *Params
	bodyConvolver *BodyConvolver
	roomConvolver *SoundboardConvolver
	resonance     *ResonanceEngine
	sustainPedal  bool
	softPedal     bool
}

// NewPiano creates a new piano engine.
func NewPiano(sampleRate int, maxPolyphony int, params *Params) *Piano {
	p := &Piano{
		sampleRate:    sampleRate,
		voices:        make([]*Voice, 0, maxPolyphony),
		params:        params,
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
	return p
}

// NoteOn triggers a new note.
func (p *Piano) NoteOn(note int, velocity int) {
	v := NewVoice(p.sampleRate, note, velocity, p.params)
	v.SetSustain(p.sustainPedal)
	v.SetSoftPedal(p.softPedal)
	p.voices = append(p.voices, v)
}

// NoteOff releases a note.
func (p *Piano) NoteOff(note int) {
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

// SetSoftPedal sets una corda / soft pedal state (true = down, false = up).
func (p *Piano) SetSoftPedal(down bool) {
	p.softPedal = down
	for _, v := range p.voices {
		v.SetSoftPedal(down)
	}
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

// Process renders a block of audio samples (stereo interleaved).
func (p *Piano) Process(numFrames int) []float32 {
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

	if p.resonance != nil {
		p.resonance.InjectFromBridge(monoMix, p.voices)
	}

	// Signal flow: voices → body convolver (mono→mono) → room convolver (mono→stereo)
	bodyMono := p.bodyConvolver.Process(monoMix)
	stereoRoom := p.roomConvolver.Process(bodyMono)

	stereoOutput := make([]float32, numFrames*2)

	// Read mix params with backwards-compatible defaults.
	outGain := float32(1.0)
	bodyDry := float32(1.0)
	bodyGain := float32(1.0)
	roomWet := float32(0.0)
	roomGain := float32(1.0)
	if p.params != nil {
		if p.params.OutputGain > 0 {
			outGain = p.params.OutputGain
		}
		// New dual-IR params.
		if p.params.BodyDryMix >= 0 {
			bodyDry = p.params.BodyDryMix
		}
		if p.params.BodyIRGain > 0 {
			bodyGain = p.params.BodyIRGain
		}
		if p.params.RoomWetMix >= 0 {
			roomWet = p.params.RoomWetMix
		}
		if p.params.RoomGain > 0 {
			roomGain = p.params.RoomGain
		}
		// Legacy compat: if old IRWetMix/IRDryMix/IRGain are set and new ones aren't,
		// map old params to new signal flow.
		if p.params.RoomIRWavPath == "" && p.params.BodyIRWavPath == "" && p.params.IRWavPath != "" {
			bodyDry = p.params.IRDryMix
			roomWet = p.params.IRWetMix
			roomGain = p.params.IRGain
			bodyGain = 1.0
		}
	}

	for i := 0; i < numFrames; i++ {
		body := bodyMono[i] * bodyGain
		l := bodyDry*body + roomWet*stereoRoom[i*2]*roomGain
		r := bodyDry*body + roomWet*stereoRoom[i*2+1]*roomGain
		stereoOutput[i*2] = l * outGain
		stereoOutput[i*2+1] = r * outGain
	}

	activeVoices := make([]*Voice, 0, len(p.voices))
	for _, v := range p.voices {
		if v.active {
			activeVoices = append(activeVoices, v)
		}
	}
	p.voices = activeVoices

	return stereoOutput
}
