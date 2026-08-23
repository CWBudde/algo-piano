package piano

import (
	"math"
	"testing"
)

func TestSympatheticResonanceEnergizesSilentHeldString(t *testing.T) {
	withParams := NewDefaultParams()
	withParams.ResonanceEnabled = true
	withParams.ResonanceGain = 0.00025
	withParams.CouplingEnabled = false
	with, withHeld := setupSympatheticScenario(withParams)

	withoutParams := NewDefaultParams()
	withoutParams.ResonanceEnabled = false
	withoutParams.CouplingEnabled = false
	without, withoutHeld := setupSympatheticScenario(withoutParams)

	for i := 0; i < 40; i++ {
		_ = with.Process(128)
		_ = without.Process(128)
	}

	withEnergy := voiceInternalEnergy(withHeld)
	withoutEnergy := voiceInternalEnergy(withoutHeld)
	if withEnergy <= withoutEnergy*2.0 {
		t.Fatalf("expected resonance to energize silent held string: with=%e without=%e", withEnergy, withoutEnergy)
	}
}

func TestPerNoteResonanceFilterIsFrequencySelective(t *testing.T) {
	g := newRingingStringGroup(48000, 67, NewDefaultParams())

	near := filteredDriveRMS(g, 392.0, 4096)
	far := filteredDriveRMS(g, 139.0, 4096)
	if near <= far*1.5 {
		t.Fatalf("expected per-note filter to favor note partial region: near=%f far=%f", near, far)
	}
}

// TestPedalOnlySympatheticStringsReachTheOutput guards the case where a note is
// undamped by the sustain pedal alone: it was never struck, so it never entered
// the bank's active-note list, yet the resonance engine keeps injecting bridge
// energy into it. Unless the bank enrolls such a group, Process skips it and the
// sympathetic energy stays inaudible.
func TestPedalOnlySympatheticStringsReachTheOutput(t *testing.T) {
	const struck = 60
	const sympathetic = 72

	for _, model := range []StringModel{StringModelDWG, StringModelModal} {
		t.Run(string(model), func(t *testing.T) {
			params := NewDefaultParams()
			params.StringModel = model
			params.ResonanceEnabled = true
			params.ResonanceGain = 0.00025
			params.CouplingEnabled = false
			params.CouplingMode = CouplingModeOff

			p := NewPiano(48000, 16, params)
			p.SetSustainPedal(true)
			p.NoteOn(struck, 115)
			for i := 0; i < 20; i++ {
				_ = p.Process(256)
			}
			p.NoteOff(struck)
			for i := 0; i < 20; i++ {
				_ = p.Process(256)
			}

			sb := p.ringing.bank
			if sb.blockEnergy[sympathetic] <= 0 {
				t.Fatalf("pedal-only sympathetic note %d never contributed to the rendered block (activeNotes=%v)",
					sympathetic, sb.activeNotes)
			}
		})
	}
}

// TestSustainPedalAloneKeepsIdleBankEmpty pins the complement of the test
// above: undamping a group must not enroll it by itself, otherwise pressing the
// pedal on an idle instrument would put all 88 keys into the active-note list
// and destroy the empty-bank fast path in Process.
func TestSustainPedalAloneKeepsIdleBankEmpty(t *testing.T) {
	for _, model := range []StringModel{StringModelDWG, StringModelModal} {
		t.Run(string(model), func(t *testing.T) {
			params := NewDefaultParams()
			params.StringModel = model
			sb := NewStringBank(48000, params)
			sb.SetSustainAmount(1)
			for i := 0; i < 8; i++ {
				_ = sb.Process(128, nil)
			}
			if len(sb.activeNotes) != 0 {
				t.Fatalf("sustain pedal alone enrolled %d notes: %v", len(sb.activeNotes), sb.activeNotes)
			}
		})
	}
}

// TestZeroResonanceGainIsSilentNotDefault pins the behaviour that
// Piano.NewPiano's `params.ResonanceGain > 0` test used to make unreachable: a
// preset may say "resonance is wired, and it contributes nothing".
//
// Until 2026-08-23 that configuration silently received DefaultResonanceGain
// instead, because the strict `> 0` treated a deliberate zero exactly like an
// unset field. Nothing shipped depended on it - every preset under
// assets/presets pins a non-zero resonance_gain - but it was one of two
// independent mechanisms converting a deliberate zero into 0.00018. The other
// was omitempty in cmd/piano-fit/output.go, which meant such a preset could not
// even be written out; see TestWritePresetJSONStatesResonanceGainExplicitly.
//
// The assertion is bit-exactness against ResonanceEnabled=false rather than
// "quieter than the default", because the engine at zero gain must be inert
// rather than merely faint: injectSample returns early on injectionGain <= 0.
func TestZeroResonanceGainIsSilentNotDefault(t *testing.T) {
	for _, model := range []StringModel{StringModelDWG, StringModelModal} {
		t.Run(string(model), func(t *testing.T) {
			render := func(enabled bool, gain float32) []float32 {
				params := NewDefaultParams()
				params.StringModel = model
				params.ResonanceEnabled = enabled
				params.ResonanceGain = gain
				params.CouplingEnabled = false
				params.CouplingMode = CouplingModeOff

				p := NewPiano(48000, 16, params)
				p.SetSustainPedal(true)
				p.NoteOn(60, 100)
				out := make([]float32, 0, 64*256)
				for i := 0; i < 64; i++ {
					out = append(out, p.Process(128)...)
				}
				return out
			}

			off := render(false, 0)
			zero := render(true, 0)
			if len(off) != len(zero) {
				t.Fatalf("render lengths differ: %d vs %d", len(off), len(zero))
			}
			for i := range off {
				if off[i] != zero[i] {
					t.Fatalf("resonance_gain 0 is not inert: sample %d differs, %v vs %v", i, off[i], zero[i])
				}
			}

			// The control: the same render at the default gain must NOT match,
			// otherwise the comparison above would pass on a build where the
			// resonance engine does nothing at all.
			def := render(true, DefaultResonanceGain)
			same := true
			for i := range off {
				if off[i] != def[i] {
					same = false
					break
				}
			}
			if same {
				t.Fatalf("render at DefaultResonanceGain is bit-identical to resonance off; the probe cannot see the loop")
			}
		})
	}
}

// TestResonanceGainIsClamped pins that NewResonanceEngine enforces
// maxResonanceGain, the ceiling this parameter went without until 2026-08-23.
//
// It is a plumbing test and nothing more: it fails if the clamp is deleted or if
// a new construction path bypasses it, and it says nothing about whether the
// bound is the right number. The measurements behind the value are on
// maxResonanceGain itself, and TestResonanceGainStaysFiniteBeyondTheClamp is
// what checks the renderer survives being asked for more.
func TestResonanceGainIsClamped(t *testing.T) {
	for _, gain := range []float32{maxResonanceGain * 2, maxResonanceGain * 100} {
		res := NewResonanceEngine(48000, gain, true)
		if res.injectionGain != maxResonanceGain {
			t.Fatalf("NewResonanceEngine(%v) kept injectionGain %v, want the clamp %v", gain, res.injectionGain, maxResonanceGain)
		}
	}
	// The clamp must not disturb anything at or under the bound.
	for _, gain := range []float32{0, DefaultResonanceGain, maxResonanceGain} {
		res := NewResonanceEngine(48000, gain, true)
		if res.injectionGain != gain {
			t.Fatalf("NewResonanceEngine(%v) changed injectionGain to %v; the clamp must be inert at or under the bound", gain, res.injectionGain)
		}
	}
	// And it must survive the whole way through Piano, which is the path a
	// hand-edited preset actually takes.
	params := NewDefaultParams()
	params.ResonanceEnabled = true
	params.ResonanceGain = maxResonanceGain * 10
	p := NewPiano(48000, 16, params)
	if p.resonance == nil {
		t.Fatalf("NewPiano built no resonance engine")
	}
	if p.resonance.injectionGain != maxResonanceGain {
		t.Fatalf("Piano kept injectionGain %v, want the clamp %v", p.resonance.injectionGain, maxResonanceGain)
	}
}

// TestResonanceGainStaysFiniteBeyondTheClamp renders past the bound with the
// clamp bypassed, so the margin the clamp carries is demonstrated rather than
// asserted.
//
// 2x and 5x only. Unlike maxUnisonCrossfeed, which carries 25x, this bound sits
// just UNDER a real cliff rather than far below one - the loop is linear in the
// gain, so the useful range runs right up to the critical gain and a clamp above
// it would enforce nothing. G* is 0.000741 against a clamp of 0.00037, so 2x is
// already at the cliff and 5x is well past it. Past roughly 4x the render is
// SUPPOSED to grow; what is asserted here is only that it stays finite over a
// render long enough to matter, which is what keeps a hand-edited preset from
// putting NaN into an audio device.
func TestResonanceGainStaysFiniteBeyondTheClamp(t *testing.T) {
	for _, mult := range []float32{2, 5} {
		params := NewDefaultParams()
		params.ResonanceEnabled = true
		params.ResonanceGain = maxResonanceGain * mult
		params.CouplingEnabled = false
		params.CouplingMode = CouplingModeOff

		p := NewPiano(48000, 16, params)
		p.resonance.injectionGain = maxResonanceGain * mult
		p.SetSustainPedal(true)
		for _, n := range growthNotes {
			p.NoteOn(n, 100)
		}
		for i := 0; i < 48000*10/128; i++ {
			for _, v := range p.Process(128) {
				if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
					t.Fatalf("%.0fx the clamp produced a non-finite sample", mult)
				}
			}
		}
	}
}
