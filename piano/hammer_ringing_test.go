package piano

import "testing"

// TestHammerContactEndsWhileRingingContinues proves both halves of the
// hammer/ringing decoupling inside a single render: the hammer separates from
// the string early in the note, and the persistent string bank keeps sounding
// long after it has, with no further excitation.
//
// Until now the two facts were only proven apart — hammerContactProfile shows
// contact ends on a bare Hammer, and TestPianoKeyDownWithoutStrikeIsSilentAndUndamped
// shows the damper and the strike are separate controls.
func TestHammerContactEndsWhileRingingContinues(t *testing.T) {
	const sampleRate = 48000
	const note = 60
	const velocity = 100

	// Reference contact length for this velocity, measured on a bare hammer.
	_, expectedContact := hammerContactProfile(NewHammer(sampleRate, velocity))
	if expectedContact <= 0 {
		t.Fatalf("expected a non-zero hammer contact window, got %d samples", expectedContact)
	}

	params := NewDefaultParams()
	params.ResonanceEnabled = false
	params.CouplingEnabled = false
	params.AttackNoiseLevel = 0 // isolate hammer contact from the noise burst

	bank := NewStringBank(sampleRate, params)
	exciter := NewHammerExciter(sampleRate, params)
	bank.SetKeyDown(note, true)
	exciter.Trigger(note, velocity)

	const renderSamples = sampleRate / 2 // 0.5 s
	samples := make([]float32, 0, renderSamples)
	contactEnd := -1
	for i := 0; i < renderSamples; i++ {
		block := bank.Process(1, exciter)
		samples = append(samples, block[0])
		if contactEnd < 0 && len(exciter.active[note]) == 0 {
			contactEnd = i
		}
	}

	if contactEnd < 0 {
		t.Fatalf("hammer never left contact within %d samples", renderSamples)
	}
	// The bank steps the hammer once per frame, so the observed contact window
	// must match the bare-hammer reference to within a sample.
	if contactEnd < expectedContact-1 || contactEnd > expectedContact+1 {
		t.Fatalf("hammer contact ended at sample %d, expected ~%d", contactEnd, expectedContact)
	}
	if contactEnd > sampleRate/100 {
		t.Fatalf("hammer contact lasted %d samples (>10 ms), which is not a piano hammer", contactEnd)
	}

	// Nothing may excite the string after separation.
	for _, events := range exciter.active {
		if len(events) != 0 {
			t.Fatalf("expected no live hammer events after contact ended, got %d", len(events))
		}
	}

	// Same render, second fact: the string is still ringing well after the
	// hammer let go, at a level comparable to the attack.
	attackRMS := windowRMS(samples[:contactEnd])
	lateStart := renderSamples - sampleRate/10 // last 100 ms
	lateRMS := windowRMS(samples[lateStart:])
	t.Logf("contact ended at sample %d (%.2f ms); attack RMS=%.4e, tail RMS=%.4e",
		contactEnd, 1000*float64(contactEnd)/sampleRate, attackRMS, lateRMS)

	if lateRMS <= 1e-4 {
		t.Fatalf("expected the string to keep ringing after hammer separation, tail RMS=%.4e", lateRMS)
	}
	if lateRMS < attackRMS*0.05 {
		t.Fatalf("ringing collapsed after hammer separation: attack RMS=%.4e tail RMS=%.4e", attackRMS, lateRMS)
	}
}
