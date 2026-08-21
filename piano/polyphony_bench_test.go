package piano

import (
	"fmt"
	"testing"
)

// voiceSweepRanges are contiguous key ranges chosen so the number of sounding
// strings lands on (or just above) the 16/32/64/128 voice targets PLAN.md 13
// asks for. A voice here is one sounding string, not one key: the bank spans
// only 88 keys, so 128 simultaneous voices is unreachable if a voice is a key,
// and the per-string cost is what the DSP actually pays.
//
// Every range stops at or below MIDI 91, deliberately. The DWG core has a known
// defect above roughly MIDI 96 — runaway DC growth, and bit-exact silence at
// MIDI 106-108, reproduced by the skipped TestTrebleRegisterCollapsesInDWGCore
// in tuning_test.go. A naive 128-voice sweep would run straight through that
// region and the DWG figures would then be measuring a broken filter loop
// rather than voice cost. Keeping the top of the sweep at 91 makes the DWG and
// modal columns comparable.
var voiceSweepRanges = []struct {
	targetVoices int
	startKey     int
	endKey       int
}{
	{16, 36, 45},
	{32, 36, 53},
	{64, 36, 69},
	{128, 36, 91},
}

// BenchmarkStringBankVoiceCostPerBlock renders one 128-frame block at 48 kHz —
// the shape of a single audio callback — at 16, 32, 64 and 128 sounding voices.
// It closes both of PLAN.md 13's remaining benchmark boxes: "Voice cost per
// block at 48k/128 frames" and "Polyphony sweep (16/32/64/128 voices)".
//
// Besides sec/op it reports two derived figures:
//
//	ns/voice-block  cost of one voice for one 128-frame block
//	%budget         sec/op as a percentage of the 2.67 ms realtime budget
//
// Coupling is disabled, matching BenchmarkModalPolyphonyScaling: with coupling
// on, sympathetic injection enrols most of the keyboard in the active set
// within a few blocks, the active set saturates at ~86 notes regardless of how
// many keys are held, and the sweep stops measuring polyphony at all. The
// coupled case is covered by BenchmarkStringBankCouplingModes and
// BenchmarkStringBankCouplingGraphDensity.
//
// The sustain pedal is held so no group deactivates mid-run, and every note is
// re-struck every benchmarkRetriggerEvery iterations with the timer stopped, so
// the bank stays realistically loaded instead of ringing down into silence.
func BenchmarkStringBankVoiceCostPerBlock(b *testing.B) {
	for _, sweep := range voiceSweepRanges {
		tc, ok := benchCaseForKeyRange(sweep.startKey, sweep.endKey, 1, true)
		if !ok {
			b.Fatalf("empty key range %d..%d", sweep.startKey, sweep.endKey)
		}
		voices := stringCountForNotes(tc.notes)
		name := fmt.Sprintf("voices%d_actual%d_keys%d", sweep.targetVoices, voices, len(tc.notes))

		b.Run(name, func(b *testing.B) {
			for _, model := range []StringModel{StringModelDWG, StringModelModal} {
				b.Run(fmt.Sprintf("model_%s", model), func(b *testing.B) {
					benchmarkVoiceCostPerBlock(b, model, tc.notes, voices)
				})
			}
		})
	}
}

// realtimeBlockBudgetNs is the wall-clock budget for one 128-frame block at
// 48 kHz: 128 / 48000 s.
const realtimeBlockBudgetNs = 128.0 / 48000.0 * 1e9

func benchmarkVoiceCostPerBlock(b *testing.B, model StringModel, notes []int, voices int) {
	b.Helper()

	sb, h := setupBenchmarkModalStringBank(model, notes, true, false)

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
	b.StopTimer()

	if b.N == 0 || voices == 0 {
		return
	}
	nsPerBlock := float64(b.Elapsed().Nanoseconds()) / float64(b.N)
	b.ReportMetric(nsPerBlock/float64(voices), "ns/voice-block")
	b.ReportMetric(nsPerBlock/realtimeBlockBudgetNs*100, "%budget")
}
