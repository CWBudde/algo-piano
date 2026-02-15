package piano

type CouplingMode string

const (
	CouplingModeOff      CouplingMode = "off"
	CouplingModeStatic   CouplingMode = "static"
	CouplingModePhysical CouplingMode = "physical"
)

// Params holds all preset parameters.
type Params struct {
	PerNote map[int]*NoteParams

	OutputGain float32

	// Legacy single-IR fields (backwards compat: used when Body/Room paths are empty).
	IRWavPath string
	IRWetMix  float32
	IRDryMix  float32
	IRGain    float32

	// Dual-IR fields: body (mono, short) + room (stereo, longer).
	BodyIRWavPath string
	BodyIRGain    float32 // Gain applied to body-convolved signal
	BodyDryMix    float32 // How much body-colored signal in output
	RoomIRWavPath string
	RoomWetMix    float32 // How much room reverb in output
	RoomGain      float32 // Gain applied to room-convolved signal

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

	// Sparse string-bank coupling controls.
	CouplingEnabled    bool
	CouplingOctaveGain float32
	CouplingFifthGain  float32
	CouplingMaxForce   float32
	CouplingMode       CouplingMode
	CouplingAmount     float32

	// Physically-informed coupling controls.
	CouplingHarmonicFalloff  float32
	CouplingDetuneSigmaCents float32
	CouplingDistanceExponent float32
	CouplingMaxNeighbors     int

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
		BodyIRGain:                 1.0,
		BodyDryMix:                 1.0,
		RoomWetMix:                 0.0,
		RoomGain:                   1.0,
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
		CouplingEnabled:            true,
		CouplingOctaveGain:         0.00018,
		CouplingFifthGain:          0.00008,
		CouplingMaxForce:           0.00045,
		CouplingMode:               CouplingModeStatic,
		CouplingAmount:             1.0,
		CouplingHarmonicFalloff:    1.35,
		CouplingDetuneSigmaCents:   28.0,
		CouplingDistanceExponent:   1.15,
		CouplingMaxNeighbors:       10,
		SoftPedalStrikeOffset:      0.08,
		SoftPedalHardness:          0.78,
	}
}
