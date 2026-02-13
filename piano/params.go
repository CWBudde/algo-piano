package piano

// Params holds all preset parameters.
type Params struct {
	PerNote map[int]*NoteParams

	OutputGain             float32
	IRWavPath              string
	ResonanceEnabled       bool
	ResonanceGain          float32
	ResonancePerNoteFilter bool
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
		PerNote:                make(map[int]*NoteParams),
		OutputGain:             1.0,
		IRWavPath:              "",
		ResonanceEnabled:       false,
		ResonanceGain:          0.00018,
		ResonancePerNoteFilter: true,
	}
}
