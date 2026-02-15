package piano

import "testing"

func TestStringBankUnisonStringCountByRange(t *testing.T) {
	sb := NewStringBank(48000, NewDefaultParams())

	low := sb.Group(30)
	mid := sb.Group(60)
	high := sb.Group(80)
	if low == nil || mid == nil || high == nil {
		t.Fatalf("expected string groups for test notes")
	}

	if len(low.strings) != 1 {
		t.Fatalf("expected low note to allocate 1 string, got %d", len(low.strings))
	}
	if len(mid.strings) != 2 {
		t.Fatalf("expected mid note to allocate 2 strings, got %d", len(mid.strings))
	}
	if len(high.strings) != 3 {
		t.Fatalf("expected high note to allocate 3 strings, got %d", len(high.strings))
	}
}

func TestStringBankDetuneScaleZeroCollapsesDetuning(t *testing.T) {
	params := NewDefaultParams()
	params.UnisonDetuneScale = 0.0

	sb := NewStringBank(48000, params)
	g := sb.Group(80)
	if g == nil || len(g.strings) < 2 {
		t.Fatalf("expected multi-string group in high register")
	}
	base := g.strings[0].f0
	for i := 1; i < len(g.strings); i++ {
		if diff := g.strings[i].f0 - base; diff > 1e-6 || diff < -1e-6 {
			t.Fatalf("expected detune collapse at index %d: got=%f want=%f", i, g.strings[i].f0, base)
		}
	}
}

func TestHammerInfluenceScalesApplyToHammerExciter(t *testing.T) {
	base := NewHammerExciter(48000, NewDefaultParams())
	base.Trigger(60, 100)
	if len(base.active[60]) == 0 {
		t.Fatalf("expected baseline hammer event")
	}
	baseHammer := base.active[60][0].hammer

	params := NewDefaultParams()
	params.HammerStiffnessScale = 1.4
	params.HammerExponentScale = 0.9
	params.HammerDampingScale = 1.3
	params.HammerInitialVelocityScale = 1.2
	params.HammerContactTimeScale = 1.1

	scaled := NewHammerExciter(48000, params)
	scaled.Trigger(60, 100)
	if len(scaled.active[60]) == 0 {
		t.Fatalf("expected scaled hammer event")
	}
	scaledHammer := scaled.active[60][0].hammer

	if scaledHammer.stiffness <= baseHammer.stiffness {
		t.Fatalf("expected stiffness scale to increase stiffness: got=%f base=%f", scaledHammer.stiffness, baseHammer.stiffness)
	}
	if scaledHammer.damping <= baseHammer.damping {
		t.Fatalf("expected damping scale to increase damping: got=%f base=%f", scaledHammer.damping, baseHammer.damping)
	}
	if scaledHammer.vel <= baseHammer.vel {
		t.Fatalf("expected velocity scale to increase initial velocity: got=%f base=%f", scaledHammer.vel, baseHammer.vel)
	}
	if scaledHammer.exponent <= 0 || scaledHammer.damping <= 0 || scaledHammer.vel <= 0 {
		t.Fatalf("expected positive hammer state after scaling")
	}
}
