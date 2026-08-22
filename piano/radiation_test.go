package piano

import (
	"math"
	"testing"
)

// The linear radiation path around the string bank is locked to
//
//	string-bank bridge mix -> body IR (mono->mono) -> room IR (mono->stereo)
//
// The tests in this file fence that ordering with pure-delay impulse responses.
// A delay is the cleanest probe available: convolving with a unit impulse placed
// at sample d is exactly a d-sample delay, so a serial chain of a body delay A
// and a room delay B must produce the bank signal at A+B. If the room convolver
// were ever fed the raw bridge mix instead of the body output — a parallel
// topology rather than a serial one — the room branch would land at B alone and
// every assertion here would fail.

const (
	// radiationBodyDelay and radiationRoomDelay are deliberately different and
	// mutually non-multiple so an accidental swap of the two stages, or a
	// parallel topology, cannot coincidentally satisfy the assertions.
	radiationBodyDelay = 17
	radiationRoomDelay = 53

	// radiationBlock/radiationBlocks render enough audio to cover the note
	// attack plus both delays with margin.
	radiationBlock  = 256
	radiationBlocks = 12
)

// delayIR builds a mono impulse response that is a pure delay of d samples.
func delayIR(d int) []float32 {
	ir := make([]float32, d+1)
	ir[d] = 1.0
	return ir
}

// renderRadiation renders one note through the real Piano.Process path with the
// body and room stages replaced by pure delays, so the output is the bank signal
// shifted by whichever stages the mix lets through.
//
// bodyDelay/roomDelay are in samples; bodyDry/roomWet select the branches.
func renderRadiation(t *testing.T, bodyDelay, roomDelay int, bodyDry, roomWet float32) []float32 {
	t.Helper()

	// A default preset with no IR paths: the convolvers start as passthrough and
	// are then replaced with the delay IRs below.
	params := NewDefaultParams()
	p := NewPiano(48000, 16, params)
	p.SetBodyIR(delayIR(bodyDelay))
	roomTail := delayIR(roomDelay)
	p.SetRoomIR(roomTail, roomTail)
	// Explicit dual-IR mix: unity stage gains, branch selection via dry/wet.
	p.SetIRMix(bodyDry, 1.0, roomWet, 1.0)

	p.NoteOn(60, 100)

	out := make([]float32, 0, radiationBlock*radiationBlocks)
	for i := 0; i < radiationBlocks; i++ {
		block := p.Process(radiationBlock)
		// Left channel only: the delay IRs are identical per channel.
		for f := 0; f < radiationBlock; f++ {
			out = append(out, block[f*2])
		}
	}
	return out
}

// assertShiftedEqual asserts that a[i+shiftA] == b[i+shiftB] for every sample,
// i.e. that the two renders carry the same bank signal at different delays.
func assertShiftedEqual(t *testing.T, name string, a []float32, shiftA int, b []float32, shiftB int) {
	t.Helper()

	n := len(a) - shiftA
	if m := len(b) - shiftB; m < n {
		n = m
	}
	if n <= 0 {
		t.Fatalf("%s: nothing to compare", name)
	}

	var energy float64
	worst := 0.0
	worstAt := -1
	for i := 0; i < n; i++ {
		x := float64(a[i+shiftA])
		y := float64(b[i+shiftB])
		energy += x * x
		if d := math.Abs(x - y); d > worst {
			worst = d
			worstAt = i
		}
	}
	if energy == 0 {
		t.Fatalf("%s: reference render is silent, the test would pass vacuously", name)
	}
	// The two renders run the identical string bank and differ only in which
	// linear stage delays the signal, so they must agree to float32 rounding.
	const tol = 1e-6
	if worst > tol {
		t.Fatalf("%s: renders diverge, max |diff| = %g at offset %d (tolerance %g); "+
			"the room stage is not fed by the body stage", name, worst, worstAt, tol)
	}
}

// TestRadiationChainIsSerialBodyThenRoom is the ordering lock: the room
// contribution must land at bodyDelay+roomDelay, which is only true when the
// room convolver consumes the *body output* rather than the raw bridge mix.
func TestRadiationChainIsSerialBodyThenRoom(t *testing.T) {
	// Body-only reference: bank signal delayed by the body stage alone.
	bodyOnly := renderRadiation(t, radiationBodyDelay, radiationRoomDelay, 1.0, 0.0)
	// Room-only: the wet branch, which passes through body *and* room.
	roomOnly := renderRadiation(t, radiationBodyDelay, radiationRoomDelay, 0.0, 1.0)

	// The wet branch trails the dry branch by exactly the room delay.
	assertShiftedEqual(t, "room trails body by roomDelay",
		bodyOnly, radiationBodyDelay,
		roomOnly, radiationBodyDelay+radiationRoomDelay)

	// And a parallel topology would have put it at roomDelay alone. Guard that
	// explicitly so the test fails loudly if the stages are ever re-wired.
	if radiationRoomDelay <= radiationBodyDelay {
		t.Fatal("test setup: roomDelay must exceed bodyDelay for the parallel check")
	}
	parallelOffset := radiationRoomDelay
	var diff float64
	for i := 0; i+radiationBodyDelay < len(bodyOnly) && i+parallelOffset < len(roomOnly); i++ {
		diff += math.Abs(float64(bodyOnly[i+radiationBodyDelay]) - float64(roomOnly[i+parallelOffset]))
	}
	if diff == 0 {
		t.Fatal("room branch matches a parallel (bridge-mix) topology; the chain is not serial")
	}
}

// TestRadiationBodyOnlyPathIsUnaffectedByRoomIR pins the degenerate body-only
// case: with the wet branch muted, the room IR must not reach the output at all.
func TestRadiationBodyOnlyPathIsUnaffectedByRoomIR(t *testing.T) {
	shortRoom := renderRadiation(t, radiationBodyDelay, 3, 1.0, 0.0)
	longRoom := renderRadiation(t, radiationBodyDelay, radiationRoomDelay, 1.0, 0.0)

	assertShiftedEqual(t, "body-only output ignores the room IR",
		shortRoom, radiationBodyDelay, longRoom, radiationBodyDelay)
}

// TestRadiationRoomOnlyPathStillPassesThroughBody pins the degenerate room-only
// case: even with the dry branch muted the signal is still coloured by the body
// stage first, so changing the body delay shifts the room output one-for-one.
func TestRadiationRoomOnlyPathStillPassesThroughBody(t *testing.T) {
	shortBody := renderRadiation(t, 5, radiationRoomDelay, 0.0, 1.0)
	longBody := renderRadiation(t, radiationBodyDelay, radiationRoomDelay, 0.0, 1.0)

	assertShiftedEqual(t, "room-only output still carries the body delay",
		shortBody, 5+radiationRoomDelay,
		longBody, radiationBodyDelay+radiationRoomDelay)
}

// TestResolveRadiationMixNormalisesLegacyPathOnce covers the legacy remap branch
// itself. The remap used to run inside Process on every block; it now runs once,
// and must still map the deprecated single-IR mix onto the room branch while
// leaving the body branch at unity gain.
func TestResolveRadiationMixNormalisesLegacyPathOnce(t *testing.T) {
	tests := []struct {
		name     string
		params   *Params
		explicit bool
		want     radiationMix
	}{
		{
			name:     "nil params fall back to a dry unity mix",
			params:   nil,
			explicit: false,
			want:     radiationMix{bodyDry: 1.0, bodyGain: 1.0, roomWet: 0.0, roomGain: 1.0},
		},
		{
			name: "legacy single-IR path is remapped onto the room branch",
			params: &Params{
				IRWavPath: "legacy.wav",
				IRDryMix:  0.25, IRWetMix: 0.75, IRGain: 1.5,
				// A body gain the legacy form cannot express: it must be reset.
				BodyIRGain: 3.0, BodyDryMix: 1.0, RoomWetMix: 0.0, RoomGain: 1.0,
			},
			explicit: false,
			want:     radiationMix{bodyDry: 0.25, bodyGain: 1.0, roomWet: 0.75, roomGain: 1.5},
		},
		{
			name: "an explicit SetIRMix wins over the legacy remap",
			params: &Params{
				IRWavPath: "legacy.wav",
				IRDryMix:  0.25, IRWetMix: 0.75, IRGain: 1.5,
				BodyIRGain: 3.0, BodyDryMix: 1.0, RoomWetMix: 0.0, RoomGain: 1.0,
			},
			explicit: true,
			want:     radiationMix{bodyDry: 1.0, bodyGain: 3.0, roomWet: 0.0, roomGain: 1.0},
		},
		{
			name: "a room path disables the legacy remap",
			params: &Params{
				IRWavPath: "legacy.wav", RoomIRWavPath: "room.wav",
				IRDryMix: 0.25, IRWetMix: 0.75, IRGain: 1.5,
				BodyIRGain: 2.0, BodyDryMix: 0.5, RoomWetMix: 0.4, RoomGain: 1.25,
			},
			explicit: false,
			want:     radiationMix{bodyDry: 0.5, bodyGain: 2.0, roomWet: 0.4, roomGain: 1.25},
		},
		{
			name: "a body path disables the legacy remap",
			params: &Params{
				IRWavPath: "legacy.wav", BodyIRWavPath: "body.wav",
				IRDryMix: 0.25, IRWetMix: 0.75, IRGain: 1.5,
				BodyIRGain: 2.0, BodyDryMix: 0.5, RoomWetMix: 0.4, RoomGain: 1.25,
			},
			explicit: false,
			want:     radiationMix{bodyDry: 0.5, bodyGain: 2.0, roomWet: 0.4, roomGain: 1.25},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveRadiationMix(tc.params, tc.explicit)
			if got != tc.want {
				t.Fatalf("resolveRadiationMix() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestApplyDefaultRoomIRIsFallbackOnly pins the CLI default: the shipped IR is
// installed as the room stage, and only when no IR is configured at all.
func TestApplyDefaultRoomIRIsFallbackOnly(t *testing.T) {
	t.Run("installs the shipped IR as the room stage", func(t *testing.T) {
		params := NewDefaultParams()
		params.IRDryMix, params.IRWetMix, params.IRGain = 0.3, 0.9, 1.4
		ApplyDefaultRoomIR(params)

		if params.RoomIRWavPath != DefaultIRWavPath {
			t.Fatalf("RoomIRWavPath = %q, want %q", params.RoomIRWavPath, DefaultIRWavPath)
		}
		if params.IRWavPath != "" {
			t.Fatalf("IRWavPath = %q, want it left empty", params.IRWavPath)
		}
		// The mix must match what the legacy remap used to produce, otherwise the
		// swap would silently change every default render.
		want := radiationMix{bodyDry: 0.3, bodyGain: 1.0, roomWet: 0.9, roomGain: 1.4}
		if got := resolveRadiationMix(params, false); got != want {
			t.Fatalf("resolved mix = %+v, want %+v", got, want)
		}
	})

	t.Run("leaves an explicit legacy path alone", func(t *testing.T) {
		params := NewDefaultParams()
		params.IRWavPath = "explicit.wav"
		ApplyDefaultRoomIR(params)

		if params.RoomIRWavPath != "" {
			t.Fatalf("RoomIRWavPath = %q, want it left empty", params.RoomIRWavPath)
		}
		if params.IRWavPath != "explicit.wav" {
			t.Fatalf("IRWavPath = %q, want it preserved", params.IRWavPath)
		}
	})

	t.Run("leaves an explicit room path alone", func(t *testing.T) {
		params := NewDefaultParams()
		params.RoomIRWavPath = "explicit-room.wav"
		ApplyDefaultRoomIR(params)

		if params.RoomIRWavPath != "explicit-room.wav" {
			t.Fatalf("RoomIRWavPath = %q, want it preserved", params.RoomIRWavPath)
		}
	})

	t.Run("leaves a body-only configuration alone", func(t *testing.T) {
		params := NewDefaultParams()
		params.BodyIRWavPath = "body.wav"
		ApplyDefaultRoomIR(params)

		if params.RoomIRWavPath != "" {
			t.Fatalf("RoomIRWavPath = %q, want it left empty", params.RoomIRWavPath)
		}
	})

	t.Run("nil params are a no-op", func(t *testing.T) {
		ApplyDefaultRoomIR(nil)
	})
}
