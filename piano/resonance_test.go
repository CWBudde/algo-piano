package piano

import "testing"

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
