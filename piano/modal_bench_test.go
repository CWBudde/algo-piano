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

// modalPartialSweepCounts is the partial-count ladder shared by
// BenchmarkModalPartialsSweep and TestModalPartialsQualityCurve, so the CPU
// column and the quality column of the PIANO-406 table describe the same
// points. It spans the whole useful part of the [1,32] range with a fine grid
// below the shipped default of 8, which is where a cheaper profile would have
// to live if one existed.
var modalPartialSweepCounts = []int{1, 2, 3, 4, 5, 6, 8, 12, 16, 24}

// modalPartialSweepKeyRange is the polyphony the partial sweep is measured at:
// MIDI 36-69, which is 64 held strings. It matches the 64-voice row of
// BenchmarkStringBankVoiceCostPerBlock so the two tables can be read against
// each other, and stays below MIDI 91 for the reason documented there (the DWG
// treble collapse).
const (
	modalPartialSweepStartKey = 36
	modalPartialSweepEndKey   = 69
)

// BenchmarkModalPartialsSweep is the CPU half of PIANO-406: what does buying a
// lower modal_partials actually save? It renders one 128-frame block at 48 kHz
// — the shape of a single audio callback — at each count in
// modalPartialSweepCounts, and reports the same ns/voice-block and %budget
// metrics as BenchmarkStringBankVoiceCostPerBlock via the shared
// benchmarkVoiceCostPerBlockOpts helper.
//
// The DWG core is measured first in each arm, at the same polyphony and the
// same coupling setting, as the reference line: modal_partials only pays for
// itself if it moves the modal cost relative to that.
//
// Both coupling arms are measured, and they answer different questions.
//
// Coupling OFF is the regime the existing polyphony sweep and BENCHMARKS.md
// "Voice cost per block and polyphony sweep" measure, and therefore the regime
// the "modal is not cheaper than DWG" finding under PIANO-406 was made in. The
// 64 held strings are the only ones sounding, so ns/voice-block is a true
// per-string cost.
//
// Coupling ON is a heavier and more realistic bank: sympathetic injection
// enrols the whole compass, and the active set settles at 88 notes / 196
// strings — measured, and identical for both cores and for every partial count,
// so the two columns compare like with like rather than differently sized
// active sets. Two things follow for reading the numbers. First,
// ns/voice-block still divides by the 64 HELD strings while 196 are sounding,
// so in this arm it is a cost per held key, not per sounding string; it stays
// comparable across rows because the load is identical in all of them. Second,
// most of those 196 are near-silent sympathetic ringers, which the modal core
// can flush to zero modes at block rate and the DWG core cannot — so this arm
// partly measures the denormal flush rather than per-mode arithmetic.
func BenchmarkModalPartialsSweep(b *testing.B) {
	tc, ok := benchCaseForKeyRange(modalPartialSweepStartKey, modalPartialSweepEndKey, 1, true)
	if !ok {
		b.Fatalf("empty key range %d..%d", modalPartialSweepStartKey, modalPartialSweepEndKey)
	}
	voices := stringCountForNotes(tc.notes)

	for _, coupling := range []bool{false, true} {
		arm := "couplingOff"
		if coupling {
			arm = "couplingOn"
		}
		b.Run(fmt.Sprintf("%s_heldStrings%d", arm, voices), func(b *testing.B) {
			b.Run("model_dwg", func(b *testing.B) {
				benchmarkVoiceCostPerBlockOpts(b, voiceCostOpts{
					model:    StringModelDWG,
					notes:    tc.notes,
					voices:   voices,
					coupling: coupling,
				})
			})
			for _, partials := range modalPartialSweepCounts {
				b.Run(fmt.Sprintf("model_modal_partials%d", partials), func(b *testing.B) {
					benchmarkVoiceCostPerBlockOpts(b, voiceCostOpts{
						model:    StringModelModal,
						notes:    tc.notes,
						voices:   voices,
						coupling: coupling,
						partials: partials,
					})
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
	return setupBenchmarkModalStringBankOpts(model, notes, sustainDown, coupling, 0)
}

// setupBenchmarkModalStringBankOpts is the same setup with ModalPartials made
// settable, so BenchmarkModalPartialsSweep can drive the partial count without
// a second copy of the bank setup. A partials value of 0 keeps the engine
// default of 8, which is what every other caller wants.
func setupBenchmarkModalStringBankOpts(model StringModel, notes []int, sustainDown bool, coupling bool, partials int) (*StringBank, *HammerExciter) {
	params := NewDefaultParams()
	params.StringModel = model
	if partials > 0 {
		params.ModalPartials = partials
	}
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
