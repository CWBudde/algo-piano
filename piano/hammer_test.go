package piano

import "testing"

func TestHammerVelocityIncreasesBrightnessProxy(t *testing.T) {
	const sampleRate = 48000
	soft := NewHammer(sampleRate, 35)
	hard := NewHammer(sampleRate, 120)

	softPeak, softContact := hammerContactProfile(soft)
	hardPeak, hardContact := hammerContactProfile(hard)

	if hardPeak <= softPeak {
		t.Fatalf("expected hard strike peak force > soft strike: hard=%f soft=%f", hardPeak, softPeak)
	}
	if hardContact >= softContact {
		t.Fatalf("expected hard strike contact duration < soft strike: hard=%d soft=%d", hardContact, softContact)
	}
}
