package piano

import (
	"math"
	"testing"
)

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

func TestAttackNoiseInjectsForce(t *testing.T) {
	const sampleRate = 48000
	const note = 60
	const velocity = 100

	params := NewDefaultParams()
	params.AttackNoiseLevel = 0.3
	params.AttackNoiseDurationMs = 3.0
	params.AttackNoiseColor = -3.0

	exciter := NewHammerExciter(sampleRate, params)
	bank := NewStringBank(sampleRate, params)
	bank.SetKeyDown(note, true)
	exciter.Trigger(note, velocity)

	// Run until hammer contact ends.
	maxContact := int(0.005 * sampleRate) // 5ms max contact
	for i := 0; i < maxContact; i++ {
		exciter.ProcessSample(bank)
	}

	// Check noise is still active after hammer separates.
	hasActive := false
	for _, ev := range exciter.active[note] {
		if ev != nil && ev.noiseRemaining > 0 {
			hasActive = true
			break
		}
	}

	// Noise duration (3ms = 144 samples) may extend past contact or overlap.
	// Key check: the noise was initialized and ran.
	noiseDurSamples := int(3.0 * 0.001 * sampleRate)
	if maxContact < noiseDurSamples && !hasActive {
		t.Logf("noise finished within hammer contact window, which is fine")
	}

	// Verify noise parameters were applied: run a fresh exciter and count noise samples.
	exciter2 := NewHammerExciter(sampleRate, params)
	bank2 := NewStringBank(sampleRate, params)
	bank2.SetKeyDown(note, true)
	exciter2.Trigger(note, velocity)

	noiseInjected := 0
	for i := 0; i < noiseDurSamples+50; i++ {
		// Check if any active event has noise remaining.
		for _, ev := range exciter2.active[note] {
			if ev != nil && ev.noiseRemaining > 0 {
				noiseInjected++
				break
			}
		}
		exciter2.ProcessSample(bank2)
	}
	if noiseInjected < noiseDurSamples-5 {
		t.Fatalf("expected ~%d noise samples, got %d", noiseDurSamples, noiseInjected)
	}
	t.Logf("noise injected for %d samples (expected ~%d)", noiseInjected, noiseDurSamples)
}

func TestAttackNoiseDecaysToZero(t *testing.T) {
	const sampleRate = 48000
	const note = 60

	params := NewDefaultParams()
	params.AttackNoiseLevel = 0.2
	params.AttackNoiseDurationMs = 2.0
	params.AttackNoiseColor = 0

	exciter := NewHammerExciter(sampleRate, params)
	bank := NewStringBank(sampleRate, params)
	bank.SetKeyDown(note, true)
	exciter.Trigger(note, 100)

	// Run past the noise duration.
	noiseSamples := int(2.0 * 0.001 * sampleRate)
	_ = bank.Process(noiseSamples+100, exciter)

	// After noise duration, no noise events should remain.
	remaining := 0
	for _, events := range exciter.active {
		for _, ev := range events {
			if ev != nil && ev.noiseRemaining > 0 {
				remaining++
			}
		}
	}
	if remaining > 0 {
		t.Fatalf("expected no active noise events after burst, got %d", remaining)
	}
}

func TestXorshift32Produces(t *testing.T) {
	state := uint32(12345)
	seen := make(map[uint32]bool)
	for i := 0; i < 1000; i++ {
		v := xorshift32(&state)
		seen[v] = true
	}
	if len(seen) < 990 {
		t.Fatalf("xorshift32 produced too few unique values: %d/1000", len(seen))
	}
}

func TestExpDecayPerSample(t *testing.T) {
	decay := expDecayPerSample(60, 1000)
	// After 1000 samples, amplitude should be ~-60dB = 0.001.
	final := float64(1.0)
	for i := 0; i < 1000; i++ {
		final *= float64(decay)
	}
	if math.Abs(final-0.001) > 0.0005 {
		t.Fatalf("expected ~0.001 after 1000 samples, got %f", final)
	}
}
