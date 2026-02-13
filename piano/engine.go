package piano

// Piano is the global engine managing voice allocation and polyphony.
type Piano struct {
	sampleRate   int
	voices       []*Voice
	params       *Params
	convolver    *SoundboardConvolver
	resonance    *ResonanceEngine
	sustainPedal bool
	softPedal    bool
}

// NewPiano creates a new piano engine.
func NewPiano(sampleRate int, maxPolyphony int, params *Params) *Piano {
	p := &Piano{
		sampleRate: sampleRate,
		voices:     make([]*Voice, 0, maxPolyphony),
		params:     params,
		convolver:  NewSoundboardConvolver(sampleRate),
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
	if params != nil && params.IRWavPath != "" {
		_ = p.convolver.SetIRFromWAV(params.IRWavPath)
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

	stereoWet := p.convolver.Process(monoMix)
	stereoOutput := make([]float32, len(stereoWet))
	wetMix := float32(1.0)
	dryMix := float32(0.0)
	irGain := float32(1.0)
	outGain := float32(1.0)
	if p.params != nil {
		if p.params.IRWetMix >= 0 {
			wetMix = p.params.IRWetMix
		}
		if p.params.IRDryMix >= 0 {
			dryMix = p.params.IRDryMix
		}
		if p.params.IRGain > 0 {
			irGain = p.params.IRGain
		}
		if p.params.OutputGain > 0 {
			outGain = p.params.OutputGain
		}
	}
	for i := 0; i < numFrames; i++ {
		dry := monoMix[i] * dryMix
		l := dry + stereoWet[i*2]*wetMix*irGain
		r := dry + stereoWet[i*2+1]*wetMix*irGain
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
