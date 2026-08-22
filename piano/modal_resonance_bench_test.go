package piano

import "testing"

// BenchmarkModalResonanceInjection measures the sympathetic-resonance path,
// which is the heaviest excitation consumer in the modal core: with the sustain
// pedal down every one of the bank's 88 groups (MIDI 21-108) is an undamped
// target, so the render
// loop drives injectAtPosition once per group per sample.
//
// The other modal benchmarks run with ResonanceEnabled = false, and a bank
// starts with no engine attached, so this benchmark wires one up by hand — the
// same call NewPiano makes.
func BenchmarkModalResonanceInjection(b *testing.B) {
	notes := []int{40, 44, 48, 52, 56, 60, 64, 68}

	for _, perNoteFilter := range []bool{true, false} {
		name := "flatDrive"
		if perNoteFilter {
			name = "perNoteFilter"
		}
		b.Run(name, func(b *testing.B) {
			params := NewDefaultParams()
			params.StringModel = StringModelModal
			params.ResonanceEnabled = true
			params.ResonancePerNoteFilter = perNoteFilter
			params.CouplingEnabled = true
			params.CouplingMode = CouplingModePhysical
			params.CouplingAmount = 1.0
			params.CouplingMaxNeighbors = 10

			sb := NewStringBank(48000, params)
			h := NewHammerExciter(48000, params)
			sb.SetResonanceEngine(NewResonanceEngine(48000, params.ResonanceGain, perNoteFilter))
			sb.SetSustain(true)
			for _, note := range notes {
				sb.SetKeyDown(note, true)
				h.Trigger(note, 110)
			}
			for i := 0; i < 64; i++ {
				sb.Process(128, h)
			}

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if i > 0 && i%benchmarkRetriggerEvery == 0 {
					b.StopTimer()
					retriggerBenchmarkBank(sb, h, notes)
					b.StartTimer()
				}
				sb.Process(128, nil)
			}
		})
	}
}

// BenchmarkModalInjectAtPosition isolates the excitation path itself, without
// the surrounding rotate loop. It is the direct measurement of what the shape
// cache replaces: one math.Sin per mode per call.
//
// alternatingPos models the worst realistic case for the single hammer cache
// slot, a normal and a soft-pedal-shifted strike position in strict alternation.
func BenchmarkModalInjectAtPosition(b *testing.B) {
	params := NewDefaultParams()
	params.StringModel = StringModelModal

	variants := []struct {
		name string
		run  func(g *ModalStringGroup, i int)
	}{
		{"resonance", func(g *ModalStringGroup, _ int) { g.injectResonance(1e-6) }},
		{"coupling", func(g *ModalStringGroup, _ int) { g.injectCouplingForce(1e-6) }},
		{"hammer_fixedPos", func(g *ModalStringGroup, _ int) { g.injectHammerForce(1e-6, 0.12) }},
		{"hammer_alternatingPos", func(g *ModalStringGroup, i int) {
			pos := float32(0.12)
			if i&1 == 1 {
				pos = 0.135
			}
			g.injectHammerForce(1e-6, pos)
		}},
	}

	for _, v := range variants {
		b.Run(v.name, func(b *testing.B) {
			g := newModalStringGroup(48000, 60, params)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				v.run(g, i)
			}
		})
	}
}
