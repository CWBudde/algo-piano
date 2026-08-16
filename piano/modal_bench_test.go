package piano

import (
	"fmt"
	"testing"
)

// BenchmarkStringBankStringModels compares the DWG and modal cores at a fixed
// sample rate and block size. This is the DWG-vs-modal CPU comparison required
// by PIANO-405 and PLAN.md 12.4.
func BenchmarkStringBankStringModels(b *testing.B) {
	for _, tc := range benchmarkCases() {
		b.Run(tc.name, func(b *testing.B) {
			for _, model := range []StringModel{StringModelDWG, StringModelModal} {
				b.Run(fmt.Sprintf("model_%s", model), func(b *testing.B) {
					benchmarkModalStringBank(b, model, tc.notes, tc.sustainDown)
				})
			}
		})
	}
}

// BenchmarkModalKernels compares the batched arena against each per-group
// kernel. All of them produce bit-identical output, so this measures pure
// throughput. The arena wins because it makes one kernel call per sample for
// the whole bank instead of one per note over only ~24 modes.
func BenchmarkModalKernels(b *testing.B) {
	notes := []int{40, 44, 48, 52, 56, 60, 64, 68}

	variants := []struct {
		name  string
		apply func()
	}{
		{"arena", func() { modalArenaEnabled = true }},
		{"pergroup_scalar", func() { modalArenaEnabled = false; modalKernelMode = modalKernelScalar }},
		{"pergroup_accum", func() { modalArenaEnabled = false; modalKernelMode = modalKernelAccum }},
		{"pergroup_rotate", func() { modalArenaEnabled = false; modalKernelMode = modalKernelRotate }},
	}

	for _, v := range variants {
		b.Run(v.name, func(b *testing.B) {
			prevKernel, prevArena := modalKernelMode, modalArenaEnabled
			v.apply()
			b.Cleanup(func() { modalKernelMode, modalArenaEnabled = prevKernel, prevArena })

			benchmarkModalStringBank(b, StringModelModal, notes, true)
		})
	}
}

// BenchmarkModalPolyphonyScaling measures how the modal core scales with voice
// count. The sustain pedal is held so no group deactivates mid-run.
//
// Coupling is disabled here on purpose: sympathetic resonance excites most of
// the keyboard within a few blocks, so the active set saturates at ~86 notes
// regardless of how many keys are held and the sweep stops measuring polyphony.
// BenchmarkStringBankStringModels covers the coupled case.
func BenchmarkModalPolyphonyScaling(b *testing.B) {
	for _, keys := range []int{1, 8, 16, 32, 64} {
		notes := make([]int, 0, keys)
		for i := range keys {
			notes = append(notes, 36+i)
		}
		name := fmt.Sprintf("poly%d_keys%d_strings%d", keys, keys, stringCountForNotes(notes))

		b.Run(name, func(b *testing.B) {
			for _, model := range []StringModel{StringModelDWG, StringModelModal} {
				b.Run(fmt.Sprintf("model_%s", model), func(b *testing.B) {
					benchmarkModalStringBankOpts(b, model, notes, true, false)
				})
			}
		})
	}
}

// benchmarkRetriggerEvery bounds how far the bank is allowed to decay during a
// benchmark run. Without periodic re-excitation the modes ring down into
// silence and the measurement stops reflecting a realistically loaded voice.
const benchmarkRetriggerEvery = 64

func benchmarkModalStringBank(b *testing.B, model StringModel, notes []int, sustainDown bool) {
	benchmarkModalStringBankOpts(b, model, notes, sustainDown, true)
}

func benchmarkModalStringBankOpts(b *testing.B, model StringModel, notes []int, sustainDown bool, coupling bool) {
	sb, h := setupBenchmarkModalStringBank(model, notes, sustainDown, coupling)

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if i > 0 && i%benchmarkRetriggerEvery == 0 {
			b.StopTimer()
			retriggerBenchmarkBank(sb, h, notes)
			b.StartTimer()
		}
		_ = sb.Process(128, nil)
	}
}

// retriggerBenchmarkBank re-strikes every benchmark note and lets the hammer
// settle, restoring the bank to a freshly-excited state. It runs with the
// benchmark timer stopped so neither the hammer nor the strike transient is
// charged to the measurement.
func retriggerBenchmarkBank(sb *StringBank, h *HammerExciter, notes []int) {
	for _, note := range notes {
		sb.SetKeyDown(note, true)
		h.Trigger(note, 110)
	}
	for i := 0; i < 4; i++ {
		_ = sb.Process(128, h)
	}
}

func setupBenchmarkModalStringBank(model StringModel, notes []int, sustainDown bool, coupling bool) (*StringBank, *HammerExciter) {
	params := NewDefaultParams()
	params.StringModel = model
	params.ResonanceEnabled = false
	params.CouplingEnabled = coupling
	params.CouplingMode = CouplingModePhysical
	params.CouplingAmount = 1.0
	params.CouplingMaxNeighbors = 10

	sb := NewStringBank(48000, params)
	h := NewHammerExciter(48000, params)
	sb.SetSustain(sustainDown)
	for _, note := range notes {
		sb.SetKeyDown(note, true)
		h.Trigger(note, 110)
	}
	for i := 0; i < 64; i++ {
		_ = sb.Process(128, h)
	}
	return sb, h
}
