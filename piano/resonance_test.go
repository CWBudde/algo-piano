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
