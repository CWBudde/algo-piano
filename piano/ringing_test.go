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

func TestStringBankBuildsOctaveCouplingEdges(t *testing.T) {
	params := NewDefaultParams()
	params.CouplingEnabled = true
	params.CouplingMode = CouplingModeStatic
	params.CouplingOctaveGain = 0.001
	params.CouplingFifthGain = 0.0
	sb := NewStringBank(48000, params)

	edges := sb.coupling[60]
	hasUp := false
	hasDown := false
	for _, e := range edges {
		if e.to == 72 {
			hasUp = true
		}
		if e.to == 48 {
			hasDown = true
		}
	}
	if !hasUp || !hasDown {
		t.Fatalf("expected octave coupling edges from 60 to 72/48, got=%v", edges)
	}
}

func TestCouplingEnergizesOctaveWithoutResonanceEngine(t *testing.T) {
	withParams := NewDefaultParams()
	withParams.ResonanceEnabled = false
	withParams.CouplingEnabled = true
	withParams.CouplingMode = CouplingModeStatic
	withParams.CouplingOctaveGain = 0.002
	withParams.CouplingFifthGain = 0.0
	withParams.CouplingMaxForce = 0.005
	with := NewPiano(48000, 16, withParams)
	with.SetSustainPedal(true)
	with.NoteOn(60, 115)
	heldWith := with.ringing.bank.Group(72)

	withoutParams := NewDefaultParams()
	withoutParams.ResonanceEnabled = false
	withoutParams.CouplingEnabled = false
	without := NewPiano(48000, 16, withoutParams)
	without.SetSustainPedal(true)
	without.NoteOn(60, 115)
	heldWithout := without.ringing.bank.Group(72)

	for i := 0; i < 40; i++ {
		_ = with.Process(128)
		_ = without.Process(128)
	}

	withEnergy := voiceInternalEnergy(heldWith)
	withoutEnergy := voiceInternalEnergy(heldWithout)
	if withEnergy <= withoutEnergy*2.0 {
		t.Fatalf("expected coupling to energize octave string: with=%e without=%e", withEnergy, withoutEnergy)
	}
}

func TestStringBankProcessHasNoPerBlockHeapAllocs(t *testing.T) {
	params := NewDefaultParams()
	params.CouplingEnabled = true
	params.CouplingMode = CouplingModeStatic
	params.CouplingOctaveGain = 0.0015
	params.CouplingFifthGain = 0.0005
	params.CouplingMaxForce = 0.003

	sb := NewStringBank(48000, params)
	h := NewHammerExciter(48000, params)
	sb.SetSustain(true)
	sb.SetKeyDown(60, true)
	h.Trigger(60, 100)

	// Warm up graph activation and internal states before measuring allocations.
	for i := 0; i < 32; i++ {
		_ = sb.Process(128, h)
	}

	allocs := testing.AllocsPerRun(1000, func() {
		_ = sb.Process(128, h)
	})
	if allocs != 0 {
		t.Fatalf("expected zero per-block heap allocs in string-bank process path, got %.3f", allocs)
	}
}

func TestStringBankCouplingModeOffDisablesEdges(t *testing.T) {
	params := NewDefaultParams()
	params.CouplingEnabled = true
	params.CouplingMode = CouplingModeOff
	sb := NewStringBank(48000, params)
	if sb.couplingEnabled {
		t.Fatalf("expected coupling disabled in off mode")
	}
	for note := 0; note < 128; note++ {
		if len(sb.coupling[note]) != 0 {
			t.Fatalf("expected no coupling edges in off mode, note=%d edges=%v", note, sb.coupling[note])
		}
	}
}

func TestStringBankPhysicalCouplingBuildsSparseTopKGraph(t *testing.T) {
	params := NewDefaultParams()
	params.CouplingMode = CouplingModePhysical
	params.CouplingAmount = 1.0
	params.CouplingMaxNeighbors = 6
	sb := NewStringBank(48000, params)

	edges := sb.coupling[60]
	if len(edges) == 0 {
		t.Fatalf("expected physical coupling edges for note 60")
	}
	if len(edges) > 6 {
		t.Fatalf("expected top-k cap to limit edges: got %d", len(edges))
	}
	for note := 0; note < 128; note++ {
		if len(sb.coupling[note]) > 6 {
			t.Fatalf("expected <=6 edges per note, note=%d got=%d", note, len(sb.coupling[note]))
		}
	}
}

func TestPhysicalCouplingAmountScalesOutgoingGain(t *testing.T) {
	fullParams := NewDefaultParams()
	fullParams.CouplingMode = CouplingModePhysical
	fullParams.CouplingAmount = 1.0
	fullParams.CouplingMaxNeighbors = 12
	full := NewStringBank(48000, fullParams)

	softParams := NewDefaultParams()
	softParams.CouplingMode = CouplingModePhysical
	softParams.CouplingAmount = 0.25
	softParams.CouplingMaxNeighbors = 12
	soft := NewStringBank(48000, softParams)

	sumGain := func(edges []couplingEdge) float32 {
		total := float32(0)
		for _, e := range edges {
			total += e.gain
		}
		return total
	}
	fullGain := sumGain(full.coupling[60])
	softGain := sumGain(soft.coupling[60])
	if fullGain <= 0 || softGain <= 0 {
		t.Fatalf("expected positive coupling gains: full=%e soft=%e", fullGain, softGain)
	}
	ratio := softGain / fullGain
	if ratio < 0.2 || ratio > 0.3 {
		t.Fatalf("expected coupling amount scaling around 0.25, got ratio=%f (full=%e soft=%e)", ratio, fullGain, softGain)
	}
}

func TestPhysicalCouplingDetuneSigmaPenalizesOffHarmonicTargets(t *testing.T) {
	tightParams := NewDefaultParams()
	tightParams.CouplingMode = CouplingModePhysical
	tightParams.CouplingDetuneSigmaCents = 8.0
	tight := NewStringBank(48000, tightParams)

	looseParams := NewDefaultParams()
	looseParams.CouplingMode = CouplingModePhysical
	looseParams.CouplingDetuneSigmaCents = 80.0
	loose := NewStringBank(48000, looseParams)

	tightWeight := tight.physicalCouplingWeight(60, 73, 24000)
	looseWeight := loose.physicalCouplingWeight(60, 73, 24000)
	if tightWeight >= looseWeight {
		t.Fatalf("expected tighter detune sigma to reduce off-harmonic coupling: tight=%e loose=%e", tightWeight, looseWeight)
	}
}

func TestPhysicalCouplingDistanceExponentReducesFarTargets(t *testing.T) {
	baseParams := NewDefaultParams()
	baseParams.CouplingMode = CouplingModePhysical
	baseParams.CouplingDistanceExponent = 0.0
	base := NewStringBank(48000, baseParams)

	steepParams := NewDefaultParams()
	steepParams.CouplingMode = CouplingModePhysical
	steepParams.CouplingDistanceExponent = 3.0
	steep := NewStringBank(48000, steepParams)

	baseNear := base.physicalCouplingWeight(60, 72, 24000)
	baseFar := base.physicalCouplingWeight(60, 84, 24000)
	steepNear := steep.physicalCouplingWeight(60, 72, 24000)
	steepFar := steep.physicalCouplingWeight(60, 84, 24000)
	if baseNear <= 0 || baseFar <= 0 || steepNear <= 0 || steepFar <= 0 {
		t.Fatalf("expected positive weights for distance penalty test")
	}
	baseRatio := baseNear / baseFar
	steepRatio := steepNear / steepFar
	if steepRatio <= baseRatio*1.4 {
		t.Fatalf("expected steeper distance exponent to amplify near/far contrast: base=%f steep=%f", baseRatio, steepRatio)
	}
}

func TestPhysicalCouplingSourceStringCountScalesOutgoingGain(t *testing.T) {
	params := NewDefaultParams()
	params.CouplingMode = CouplingModePhysical
	params.CouplingAmount = 1.0
	params.CouplingMaxNeighbors = 12
	sb := NewStringBank(48000, params)

	sumGain := func(edges []couplingEdge) float32 {
		total := float32(0)
		for _, e := range edges {
			total += e.gain
		}
		return total
	}

	lowOut := sumGain(sb.coupling[30])  // 1-string regime
	midOut := sumGain(sb.coupling[60])  // 2-string regime
	highOut := sumGain(sb.coupling[84]) // 3-string regime
	if lowOut <= 0 || midOut <= 0 || highOut <= 0 {
		t.Fatalf("expected positive outgoing gains: low=%e mid=%e high=%e", lowOut, midOut, highOut)
	}
	if !(lowOut < midOut && midOut < highOut) {
		t.Fatalf("expected outgoing gains to rise with source unison count: low=%e mid=%e high=%e", lowOut, midOut, highOut)
	}
}

func TestStringCountCouplingScaleMonotonic(t *testing.T) {
	s1 := stringCountCouplingScale(1)
	s2 := stringCountCouplingScale(2)
	s3 := stringCountCouplingScale(3)
	if !(s1 > 0 && s2 > s1 && s3 > s2) {
		t.Fatalf("expected monotonic unison scaling: s1=%f s2=%f s3=%f", s1, s2, s3)
	}
	if s3 < 0.99 || s3 > 1.01 {
		t.Fatalf("expected 3-string regime scale near unity, got=%f", s3)
	}
}
