package piano

import (
	"fmt"
	"testing"
)

// BenchmarkStringBankIdle measures the floor cost of owning the full persistent
// 88-key string bank with nothing playing — the price the engine pays every
// block just for existing. This is PLAN.md 9.6's "idle full-string-bank cost".
//
// Both pedal states are measured, and what the pair currently confirms is that
// damper state on its own does not activate idle groups: StringBank.SetSustain
// forwards the damper state to each group without enrolling anything in the
// active set, so a bank with the pedal held down — the state a player leaves the
// instrument in between phrases — takes the same empty-active-set fast path as
// the pedal-up bank. That is the behaviour of today's SetSustain, not a design
// invariant. If undamped groups are ever enrolled in the active set (so that
// sympathetic energy injected into them is actually rendered), the pedalDown
// case stops being an idle measurement; the b.Fatalf below is deliberately
// there so that change fails loudly here instead of quietly turning this into a
// different benchmark, and it should then be replaced by a sympathetic-resonance
// floor measurement rather than relaxed.
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
