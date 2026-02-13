package piano

// Params holds all preset parameters.
type Params struct {
	PerNote map[int]*NoteParams

	OutputGain             float32
	IRWavPath              string
	IRWetMix               float32
	IRDryMix               float32
	IRGain                 float32
	ResonanceEnabled       bool
	ResonanceGain          float32
	ResonancePerNoteFilter bool

	HammerStiffnessScale       float32
	HammerExponentScale        float32
	HammerDampingScale         float32
	HammerInitialVelocityScale float32
	HammerContactTimeScale     float32

	UnisonDetuneScale float32
	UnisonCrossfeed   float32

	SoftPedalStrikeOffset float32
	SoftPedalHardness     float32
}

// NoteParams holds parameters for a specific note.
type NoteParams struct {
	F0             float32
	Inharmonicity  float32
	Loss           float32
	StrikePosition float32
}

// NewDefaultParams creates default parameters.
func NewDefaultParams() *Params {
	return &Params{
		PerNote:                    make(map[int]*NoteParams),
		OutputGain:                 1.0,
		IRWavPath:                  "",
		IRWetMix:                   1.0,
		IRDryMix:                   0.0,
		IRGain:                     1.0,
		ResonanceEnabled:           false,
		ResonanceGain:              0.00018,
		ResonancePerNoteFilter:     true,
		HammerStiffnessScale:       1.0,
		HammerExponentScale:        1.0,
		HammerDampingScale:         1.0,
		HammerInitialVelocityScale: 1.0,
		HammerContactTimeScale:     1.0,
		UnisonDetuneScale:          1.0,
		UnisonCrossfeed:            0.0008,
		SoftPedalStrikeOffset:      0.08,
		SoftPedalHardness:          0.78,
	}
}
