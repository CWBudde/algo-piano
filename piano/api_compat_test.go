package piano

// Public API compatibility pins.
//
// The engine is consumed from three places that a Go-only refactor does not
// break at compile time: the WASM bridge in cmd/piano-wasm (which forwards
// dynamically typed JS values), the preset JSON in assets/presets (which stores
// the enum values as strings), and the CLI renderers. A changed parameter type,
// a dropped method, or a retyped enum shows up there as a runtime failure or a
// silently ignored preset field, not as a build error.
//
// The point of this file is to move those failures to build time / test time.

import "testing"

// pianoAPI mirrors the exported method set of *Piano exactly.
//
// This is deliberately an interface satisfied by a compile-time assertion
// rather than a reflection walk over method names: a reflection test compares
// strings and reports a mismatch after the fact, while a failing interface
// assertion is a build error at the exact signature that changed, with the
// compiler naming the difference. Renaming a method, reordering parameters, or
// widening `int` to `int32` all stop the build here.
//
// Adding a method to *Piano does NOT break this assertion, which is correct:
// growing the surface is backwards compatible, shrinking or reshaping it is not.
type pianoAPI interface {
	NoteOn(note int, velocity int)
	KeyDown(note int)
	NoteOff(note int)
	SetSustainPedal(down bool)
	SetSustainPedalAmount(amount float32)
	SetSoftPedal(down bool)
	SetCouplingMode(mode CouplingMode) bool
	SetStringModel(model StringModel) bool
	SetIR(left, right []float32)
	SetBodyIR(ir []float32)
	SetRoomIR(left, right []float32)
	SetBodyIRFromBytes(data []byte) error
	SetRoomIRFromBytes(data []byte) error
	SetIRMix(bodyDry, bodyGain, roomWet, roomGain float32)
	Process(numFrames int) []float32
}

// The assertion itself. If this line stops compiling, a consumer outside this
// module is about to break.
var _ pianoAPI = (*Piano)(nil)

// The constructor signature is pinned the same way: a variable of the exact
// function type, assigned the real constructor.
var _ func(sampleRate int, maxPolyphony int, params *Params) *Piano = NewPiano

// TestEnumStringLiteralsAreStable pins the wire values of the two string-valued
// enums.
//
// These are not internal identifiers. They are what preset JSON stores
// (`"string_model": "dwg"`), what the web UI's <select> option values are, and
// what wasmSetCouplingMode/wasmSetStringModel receive from JS. Changing
// `CouplingModeStatic` from "static" to anything else silently invalidates every
// saved preset, because the JSON decode of an unknown value leaves the field at
// its zero value rather than erroring.
func TestEnumStringLiteralsAreStable(t *testing.T) {
	couplingModes := map[CouplingMode]string{
		CouplingModeOff:      "off",
		CouplingModeStatic:   "static",
		CouplingModePhysical: "physical",
	}
	for mode, want := range couplingModes {
		if string(mode) != want {
			t.Errorf("CouplingMode literal changed: got %q, want %q", string(mode), want)
		}
	}

	stringModels := map[StringModel]string{
		StringModelDWG:   "dwg",
		StringModelModal: "modal",
	}
	for model, want := range stringModels {
		if string(model) != want {
			t.Errorf("StringModel literal changed: got %q, want %q", string(model), want)
		}
	}

	// The setters must accept exactly those values and reject anything else.
	// This is the runtime half of the same contract: JS passes an arbitrary
	// string, and the bool return is the web UI's only validation signal.
	p := NewPiano(48000, 16, NewDefaultParams())
	for mode := range couplingModes {
		if !p.SetCouplingMode(mode) {
			t.Errorf("SetCouplingMode(%q) rejected a documented mode", string(mode))
		}
	}
	if p.SetCouplingMode(CouplingMode("bogus")) {
		t.Errorf("SetCouplingMode accepted an unknown mode; the web UI relies on the false return to report the error")
	}
	for model := range stringModels {
		if !p.SetStringModel(model) {
			t.Errorf("SetStringModel(%q) rejected a documented model", string(model))
		}
	}
	if p.SetStringModel(StringModel("bogus")) {
		t.Errorf("SetStringModel accepted an unknown model; the web UI relies on the false return to report the error")
	}
}

// TestProcessReturnsInterleavedStereo pins the output buffer length contract.
//
// The WASM bridge hands JS a raw pointer plus a frame count and lets the browser
// slice linear memory itself (see wasmProcessBlock / the worklet's
// deinterleaving loop), so a Process that returned mono, or a partially filled
// buffer, would be read as garbage past the end rather than caught. The frame
// counts below cover the AudioWorklet render quantum (128), the block sizes the
// CLI renderers use, and the degenerate n=1 case.
func TestProcessReturnsInterleavedStereo(t *testing.T) {
	p := NewPiano(48000, 16, NewDefaultParams())
	p.NoteOn(60, 90)

	for _, frames := range []int{1, 64, 128, 256, 512} {
		out := p.Process(frames)
		if len(out) != frames*2 {
			t.Fatalf("Process(%d) returned %d samples, want %d (stereo interleaved)", frames, len(out), frames*2)
		}
	}
}

// TestDefaultParamsPinsPublicDefaults guards the defaults that other components
// assume without asking.
//
// StringModel and CouplingMode are the two the web UI mirrors in its initial
// control state, and 21..108 is the 88-key range that the string bank allocates
// against and that every preset in assets/presets omits because it is the
// default. A change here is legitimate, but it must be a deliberate one that
// updates the UI and the presets in the same commit.
func TestDefaultParamsPinsPublicDefaults(t *testing.T) {
	params := NewDefaultParams()
	if params.StringModel != StringModelDWG {
		t.Errorf("default StringModel = %q, want %q", string(params.StringModel), string(StringModelDWG))
	}
	if params.CouplingMode != CouplingModeStatic {
		t.Errorf("default CouplingMode = %q, want %q", string(params.CouplingMode), string(CouplingModeStatic))
	}
	if params.MinNote != 21 {
		t.Errorf("default MinNote = %d, want 21 (A0, the bottom of an 88-key keyboard)", params.MinNote)
	}
	if params.MaxNote != 108 {
		t.Errorf("default MaxNote = %d, want 108 (C8, the top of an 88-key keyboard)", params.MaxNote)
	}
	if params.PerNote == nil {
		t.Errorf("default PerNote map is nil; callers write per-note overrides into it without allocating")
	}
}
