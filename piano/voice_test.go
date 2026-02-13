package piano

import (
	"math"
	"testing"
)

func TestVoiceUnisonStringCountByRange(t *testing.T) {
	vLow := NewVoice(48000, 30, 90, NewDefaultParams())
	vMid := NewVoice(48000, 60, 90, NewDefaultParams())
	vHigh := NewVoice(48000, 80, 90, NewDefaultParams())

	if len(vLow.strings) != 1 {
		t.Fatalf("expected low note to allocate 1 string, got %d", len(vLow.strings))
	}
	if len(vMid.strings) != 2 {
		t.Fatalf("expected mid note to allocate 2 strings, got %d", len(vMid.strings))
	}
	if len(vHigh.strings) != 3 {
		t.Fatalf("expected high note to allocate 3 strings, got %d", len(vHigh.strings))
	}
}

func TestUnisonDetuneScaleZeroCollapsesDetuning(t *testing.T) {
	params := NewDefaultParams()
	params.UnisonDetuneScale = 0.0

	v := NewVoice(48000, 80, 90, params)
	if len(v.strings) < 2 {
		t.Fatalf("expected multi-string voice in high register")
	}
	base := v.strings[0].f0
	for i := 1; i < len(v.strings); i++ {
		if math.Abs(float64(v.strings[i].f0-base)) > 1e-6 {
			t.Fatalf("expected detune collapse at index %d: got=%f want=%f", i, v.strings[i].f0, base)
		}
	}
}

func TestHammerInfluenceScalesApplyToVoiceHammer(t *testing.T) {
	base := NewVoice(48000, 60, 100, NewDefaultParams())
	if base.hammer == nil {
		t.Fatalf("expected baseline hammer")
	}

	params := NewDefaultParams()
	params.HammerStiffnessScale = 1.4
	params.HammerExponentScale = 0.9
	params.HammerDampingScale = 1.3
	params.HammerInitialVelocityScale = 1.2
	params.HammerContactTimeScale = 1.1

	v := NewVoice(48000, 60, 100, params)
	if v.hammer == nil {
		t.Fatalf("expected hammer")
	}
	if v.hammer.stiffness <= base.hammer.stiffness {
		t.Fatalf("expected stiffness scale to increase stiffness: got=%f base=%f", v.hammer.stiffness, base.hammer.stiffness)
	}
	if v.hammer.damping <= base.hammer.damping {
		t.Fatalf("expected damping scale to increase damping: got=%f base=%f", v.hammer.damping, base.hammer.damping)
	}
	if v.hammer.vel <= base.hammer.vel {
		t.Fatalf("expected velocity scale to increase initial velocity: got=%f base=%f", v.hammer.vel, base.hammer.vel)
	}
	if v.hammer.exponent <= 0 || v.hammer.damping <= 0 || v.hammer.vel <= 0 {
		t.Fatalf("expected positive hammer state after scaling")
	}
}
