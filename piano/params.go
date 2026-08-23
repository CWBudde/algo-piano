package piano

type CouplingMode string

const (
	CouplingModeOff      CouplingMode = "off"
	CouplingModeStatic   CouplingMode = "static"
	CouplingModePhysical CouplingMode = "physical"
)

type StringModel string

const (
	StringModelDWG   StringModel = "dwg"
	StringModelModal StringModel = "modal"
)

// Params holds all preset parameters.
type Params struct {
	PerNote map[int]*NoteParams

	OutputGain float32

	// Note range for string-bank allocation and processing (inclusive, MIDI 0..127).
	MinNote int
	MaxNote int

	// Legacy single-IR fields (backwards compat: used when Body/Room paths are empty).
	IRWavPath string
	IRWetMix  float32
	IRDryMix  float32
	IRGain    float32

	// Dual-IR fields: body (mono, short) + room (stereo, longer).
	BodyIRWavPath string
	BodyIRGain    float32 // Gain applied to the dry body branch only (not to what the room convolver is fed)
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

	// Frequency-dependent string loss: one-pole lowpass coefficient in DWG loop,
	// and order-dependent decay scaling in modal model. Higher values damp high
	// frequencies more aggressively. Based on Bensa et al. (2003) freq-dependent
	// damping terms b1/b2 in the stiff string PDE.
	HighFreqDamping float32

	UnisonDetuneScale float32
	UnisonCrossfeed   float32
	BridgeCoupling    float32
	StringModel       StringModel
	ModalPartials     int
	ModalGainExponent float32
	ModalExcitation   float32
	ModalUndampedLoss float32
	ModalDampedLoss   float32

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

	// Hammer attack noise: broadband felt-impact noise burst at note onset.
	AttackNoiseLevel      float32 // Amplitude relative to hammer force (0 = off)
	AttackNoiseDurationMs float32 // Duration of noise burst in ms (typically 1-5)
	AttackNoiseColor      float32 // Spectral tilt in dB/octave (0 = white, negative = pink/brown)
}

// NoteParams holds parameters for a specific note.
type NoteParams struct {
	F0             float32
	Inharmonicity  float32
	Loss           float32
	StrikePosition float32
}

// Defaults of the dual-IR mix fields. They live here — rather than as literals
// inside NewDefaultParams — because ApplyDefaultRoomIR has to recognise a mix
// that is still untouched, and the two must never drift apart.
const (
	DefaultBodyIRGain = float32(1.0)
	DefaultBodyDryMix = float32(1.0)
	DefaultRoomWetMix = float32(0.0)
	DefaultRoomGain   = float32(1.0)
)

// DefaultResonanceGain scales the bridge signal that ResonanceEngine deposits
// into every undamped string.
//
// It lives here rather than as a literal because NewPiano needs the same value
// when it is handed nil params, and the two must never drift apart.
//
// The value is UNCHANGED by the 2026-08-22 unity-peak normalisation of
// noteResonator, and that is a measured decision rather than an omission.
//
// The old resonator bank amplified its input by roughly 1/(2*sin(w0)) — 183x at
// A0, 20x at C4, 2.6x at C7 — so no single scalar reproduces the old per-note
// behaviour: it was frequency-dependent, and its bass end was the divergence.
// Normalising it costs sympathetic level in proportion to that error, so the
// loss is register-dependent and heaviest exactly where the runaway was:
// −45.3 dB at A0, −38.0 at MIDI 36, −26.1 at C4, −14.2 at MIDI 84, −8.3 at
// MIDI 96. The obvious compensation is to raise this gain. Measured at 48 kHz
// on the DWG core with the sustain pedal held (all 88 groups undamped), peak of
// the last five seconds of a 120 s render through Piano.Process, six notes
// struck:
//
//	gain      45 s   80 s   120 s
//	0.00018   1.59   1.53   1.75    flat
//	0.0007    1.70   2.05   2.42    creeping up
//	0.0014    4.52   12.7   91.5    diverging
//
// The loop's stability ceiling therefore sits somewhere around 3x this value,
// not the 8-20x that restoring mid-register level would need, so there is no
// room to compensate with a scalar. Raising it is a separate change that has to
// come with a mechanism that widens the loop's margin, not just more gain. See
// the Phase 9.6 notes in PLAN.md.
const DefaultResonanceGain = float32(0.00018)

// DefaultUnisonCrossfeed is how strongly the strings of a unison are coupled to
// each other through the bridge. See RingingStringGroup.processSample for what
// the coupling does and maxUnisonCrossfeed for the range it is stable over.
const DefaultUnisonCrossfeed = float32(0.0008)

// DefaultBridgeCoupling is how strongly the COMMON motion of a unison is damped
// by the bridge, which is the half of the coupling that produces double decay.
// See bridgeCouplingForce for what the term does and maxBridgeCoupling for the
// range it is stable over.
//
// It is deliberately a separate knob from DefaultUnisonCrossfeed and not a
// re-voicing of it: the two damp orthogonal motions. unison_crossfeed damps
// RELATIVE motion and vanishes when the strings already move together;
// bridge_coupling damps the common motion and vanishes when they cancel at the
// bridge.
//
// 0.035 is chosen from measured decay PROLONGATION - how much longer a note
// takes to fall 40 dB than its own prompt slope predicts, which is 1.0 for a
// single exponential (see decayProlongation). DWG core, one note struck under a
// held pedal, unison_crossfeed = 0, 30 s:
//
//	b        note 60   note 67   note 72   note 76   |  same, unison_detune_scale = 0
//	0             -      0.93      0.91      1.00    |   -     1.01   1.02   1.11
//	0.02       2.54      2.49      3.67      2.95    |  1.04   1.04   1.05   1.01
//	0.035      4.35      3.96      3.54      2.94    |  1.01   1.05   1.00   1.02
//	0.05       5.46      4.86      4.59      3.73    |  1.02   1.06   1.03   1.04
//	0.1        5.49      4.63      6.86      3.80    |  1.03   1.17   1.12   1.15
//
// The right-hand block is the control that makes the left one mean something:
// with the detune removed the whole motion is common motion, there is no
// out-of-phase mode to ring on, and the prolongation stays at 1.0 at EVERY
// strength. So the left block is the prompt/aftersound split and not an artefact
// of the term merely being present.
//
// 0.035 sits above the b >~ 0.026 the effect needs, 2.9x under
// maxBridgeCoupling, and deep inside the stable region of that constant's sweep.
// Note 60 has no b = 0 entry because an uncoupled note 60 never falls 40 dB
// inside 30 s.
const DefaultBridgeCoupling = float32(0.035)

// NewDefaultParams creates default parameters.
func NewDefaultParams() *Params {
	return &Params{
		PerNote:                    make(map[int]*NoteParams),
		OutputGain:                 1.0,
		MinNote:                    21,
		MaxNote:                    108,
		IRWavPath:                  "",
		IRWetMix:                   1.0,
		IRDryMix:                   0.0,
		IRGain:                     1.0,
		BodyIRGain:                 DefaultBodyIRGain,
		BodyDryMix:                 DefaultBodyDryMix,
		RoomWetMix:                 DefaultRoomWetMix,
		RoomGain:                   DefaultRoomGain,
		ResonanceEnabled:           false,
		ResonanceGain:              DefaultResonanceGain,
		ResonancePerNoteFilter:     true,
		HammerStiffnessScale:       1.0,
		HammerExponentScale:        1.0,
		HammerDampingScale:         1.0,
		HammerInitialVelocityScale: 1.0,
		HammerContactTimeScale:     1.0,
		HighFreqDamping:            0.05,
		UnisonDetuneScale:          1.0,
		UnisonCrossfeed:            DefaultUnisonCrossfeed,
		BridgeCoupling:             DefaultBridgeCoupling,
		StringModel:                StringModelDWG,
		ModalPartials:              8,
		ModalGainExponent:          1.1,
		ModalExcitation:            1.0,
		ModalUndampedLoss:          1.0,
		ModalDampedLoss:            1.0,
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
		AttackNoiseLevel:           0.0,
		AttackNoiseDurationMs:      2.5,
		AttackNoiseColor:           -3.0,
	}
}
