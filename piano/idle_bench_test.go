package piano

import (
	"fmt"
	"testing"
)

// BenchmarkStringBankIdle measures the floor cost of owning the full persistent
// 88-key string bank with nothing playing — the price the engine pays every
// block just for existing. This is PLAN.md 9.6's "idle full-string-bank cost".
//
// Two pedal states are measured because they take different paths: with the
// pedal up nothing is in the active set at all, while a held pedal lifts every
// damper and marks every group active, which is the state a player leaves the
// instrument in between phrases.
func BenchmarkStringBankIdle(b *testing.B) {
	for _, model := range []StringModel{StringModelDWG, StringModelModal} {
		for _, sustain := range []bool{false, true} {
			pedal := "pedalUp"
			if sustain {
				pedal = "pedalDown"
			}
			b.Run(fmt.Sprintf("model_%s_%s", model, pedal), func(b *testing.B) {
				params := NewDefaultParams()
				params.StringModel = model
				params.ResonanceEnabled = false
				params.CouplingMode = CouplingModePhysical

				sb := NewStringBank(48000, params)
				sb.SetSustain(sustain)
				if got := len(sb.activeNotes); got != 0 {
					b.Fatalf("expected an idle bank, got %d active notes", got)
				}
				// Warm the output buffer so the first iteration is not charged
				// for its allocation.
				_ = sb.Process(128, nil)

				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					_ = sb.Process(128, nil)
				}
			})
		}
	}
}
