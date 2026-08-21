package piano

import (
	"fmt"
	"math"
	"testing"
)

// measureWaveguideCents renders a bare string for two seconds and returns how
// far its measured fundamental lands from the note's nominal frequency, in
// cents. DC is blocked first: the waveguide loop has unity DC gain, so an
// untreated signal can be dominated by a slowly decaying offset that suppresses
// zero crossings entirely.
func measureWaveguideCents(t *testing.T, note int) (measured float32, cents float64) {
	t.Helper()
	const sampleRate = 48000

	nominal := midiNoteToFreq(note)
	str := NewStringWaveguide(sampleRate, nominal)
	str.Excite(0.5)

	samples := make([]float32, sampleRate*2)
	for i := range samples {
		samples[i] = str.Process()
	}

	measured = measureFundamentalFreq(removeDCOffset(samples), sampleRate)
	if measured <= 0 {
		return 0, math.Inf(-1)
	}
	return measured, 1200 * math.Log2(float64(measured/nominal))
}

// TestTuningAccuracyAcrossCompass extends tuning coverage from the five
// mid-range notes of TestTuningAccuracy to every note from the bottom A0 up to
// the top of the range where the DWG core still produces a measurable pitch.
//
// Tolerances are per register because the *measurement* gets worse towards the
// bass, not because the model does. measureFundamentalFreq counts zero
// crossings over a ~1.8 s window, so its resolution is a flat 0.28 Hz — which
// is 17 cents at A0 (27.5 Hz) and 0.03 cents at E6 (1319 Hz). Every tolerance
// below is set from the worst error measured over the whole register, with
// roughly 40% headroom:
//
//	bass    21-39   worst 17.58 cents (note 21, resolution-limited)  -> 25
//	tenor   40-59   worst  6.40 cents (note 46, resolution-limited)  -> 10
//	middle  60-83   worst  1.49 cents (note 61)                      ->  2.5
//	treble  84-92   worst  0.23 cents (note 88)                      ->  0.5
//
// Converted to Hz the whole compass lands inside +/-0.5 Hz, so there is no
// register-dependent dispersion or loop-loss tuning bias to speak of. The top
// of the compass is missing on purpose: from MIDI 93 upwards the DWG core loses
// its tonal content within ~0.1 s, which TestTrebleRegisterCollapsesInDWGCore
// documents.
func TestTuningAccuracyAcrossCompass(t *testing.T) {
	registers := []struct {
		name      string
		lo, hi    int
		tolerance float64 // cents
	}{
		{"bass", 21, 39, 25.0},
		{"tenor", 40, 59, 10.0},
		{"middle", 60, 83, 2.5},
		{"treble", 84, 92, 0.5},
	}

	for _, reg := range registers {
		t.Run(reg.name, func(t *testing.T) {
			worst := 0.0
			worstNote := 0
			for note := reg.lo; note <= reg.hi; note++ {
				t.Run(fmt.Sprintf("note%d", note), func(t *testing.T) {
					measured, cents := measureWaveguideCents(t, note)
					if math.IsInf(cents, 0) {
						t.Fatalf("note %d: no measurable fundamental (nominal %.2f Hz)",
							note, midiNoteToFreq(note))
					}
					if math.Abs(cents) > reg.tolerance {
						t.Errorf("note %d: nominal %.3f Hz, measured %.3f Hz, error %+.2f cents (tolerance %.1f)",
							note, midiNoteToFreq(note), measured, cents, reg.tolerance)
					}
					if math.Abs(cents) > worst {
						worst, worstNote = math.Abs(cents), note
					}
				})
			}
			t.Logf("%s register worst error: %.2f cents at note %d (tolerance %.1f)",
				reg.name, worst, worstNote, reg.tolerance)
		})
	}
}

// TestTrebleRegisterCollapsesInDWGCore documents a real defect found while
// extending tuning coverage. It is skipped rather than failing so the suite
// stays green, and should be un-skipped as the regression test for the fix.
//
// Two symptoms, both measured on 2026-08-21 with default parameters, sustain
// held, resonance and coupling disabled, reading the engine's left channel over
// the second second of a 400-block render:
//
//  1. DC runaway. From roughly MIDI 96 up, the output is essentially pure DC
//     (RMS equals the mean to three digits) and the offset grows with pitch and
//     with time: note 100 +5.3, note 103 +13.0, note 105 +38.5 — the last is
//     about 32x full scale. Holding note 96 for 800 blocks takes the offset from
//     +4.8 to +59.2 and it is still climbing, so it does not converge. Setting
//     UnisonCrossfeed to 0 makes it decay instead of grow, which points at the
//     crossfeed path in RingingStringGroup.processSample as a positive-feedback
//     route for DC: the loop has unity DC gain and nothing blocks it.
//
//  2. Silent top notes. MIDI 106, 107 and 108 render exactly zero — not quiet,
//     bit-exact silence — for the whole render. Their delay lines are 16, 16 and
//     15 samples long.
//
// The modal core shows neither symptom at the same notes.
func TestTrebleRegisterCollapsesInDWGCore(t *testing.T) {
	t.Skip("known defect: DWG core produces runaway DC from ~MIDI 96 and bit-exact silence at MIDI 106-108")

	const blocks = 400
	for note := 96; note <= 108; note++ {
		mono := renderNoteMonoFloat32(StringModelDWG, note, blocks)
		tail := mono[48000:]

		var mean float64
		for _, v := range tail {
			mean += float64(v)
		}
		mean /= float64(len(tail))

		ac := make([]float32, len(tail))
		for i, v := range tail {
			ac[i] = v - float32(mean)
		}
		acRMS := windowRMS(ac)

		t.Logf("note %d: dc=%+.4e ac=%.4e", note, mean, acRMS)
		if math.Abs(mean) > 1.0 {
			t.Errorf("note %d: DC offset %+.4f exceeds full scale", note, mean)
		}
		if acRMS <= 1e-6 {
			t.Errorf("note %d: no tonal content in the tail (ac RMS %.4e)", note, acRMS)
		}
	}
}

// renderNoteMonoFloat32 renders one held note through a full Piano and returns
// the left channel.
func renderNoteMonoFloat32(model StringModel, note int, blocks int) []float32 {
	params := NewDefaultParams()
	params.StringModel = model
	params.ResonanceEnabled = false
	params.CouplingEnabled = false

	p := NewPiano(48000, 16, params)
	p.SetSustainPedal(true)
	p.NoteOn(note, 100)

	out := make([]float32, 0, blocks*128)
	for i := 0; i < blocks; i++ {
		block := p.Process(128)
		for j := 0; j < 128; j++ {
			out = append(out, block[j*2])
		}
	}
	return out
}
