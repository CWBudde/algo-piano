package piano

import "testing"

// sympatheticParams returns a parameter set in which the only path from the
// struck note to a silent neighbour is the one under test.
func sympatheticParams(octaveGain float32, fifthGain float32) *Params {
	params := NewDefaultParams()
	params.ResonanceEnabled = false
	params.CouplingEnabled = true
	params.CouplingMode = CouplingModeStatic
	params.CouplingOctaveGain = octaveGain
	params.CouplingFifthGain = fifthGain
	params.CouplingMaxForce = 0.005
	return params
}

// renderSympathetic strikes `struck` and returns the internal energy that ended
// up in the silent neighbour `target` after `blocks` blocks.
func renderSympathetic(t *testing.T, params *Params, sustain bool, struck int, target int, blocks int) float64 {
	t.Helper()
	p := NewPiano(48000, 16, params)
	p.SetSustainPedal(sustain)
	p.NoteOn(struck, 115)

	g := p.ringing.bank.Group(target)
	if g == nil {
		t.Fatalf("no DWG group allocated for target note %d", target)
	}
	for i := 0; i < blocks; i++ {
		_ = p.Process(128)
	}
	return voiceInternalEnergy(g)
}

// TestPedalUpSuppressesSympatheticBuildup A/Bs on actual pedal state rather
// than on a feature flag. With the sustain pedal down every damper is lifted,
// so a struck note pumps its neighbours through the coupling graph. With the
// pedal up the neighbours' dampers stay engaged and the same coupling drive is
// absorbed instead of accumulating.
//
// This closes the gap left by TestSympatheticResonanceEnergizesSilentHeldString
// and TestCouplingEnergizesOctaveWithoutResonanceEngine, which both hold the
// pedal down and toggle a parameter instead.
func TestPedalUpSuppressesSympatheticBuildup(t *testing.T) {
	const struck = 60
	const target = 72 // octave above; damper is never lifted by the key
	const blocks = 40

	down := renderSympathetic(t, sympatheticParams(0.002, 0.0), true, struck, target, blocks)
	up := renderSympathetic(t, sympatheticParams(0.002, 0.0), false, struck, target, blocks)

	t.Logf("octave-neighbour energy: pedalDown=%e pedalUp=%e ratio=%.1fx", down, up, down/up)
	if down <= up*2.0 {
		t.Fatalf("expected pedal-down sympathetic buildup to exceed pedal-up: down=%e up=%e", down, up)
	}
}

// TestPedalUpSuppressesSympatheticBuildupModal repeats the pedal A/B on the
// modal core, whose damper handling is a separate code path (per-mode decay
// coefficients rather than a waveguide reflection coefficient).
func TestPedalUpSuppressesSympatheticBuildupModal(t *testing.T) {
	const struck = 60
	const target = 72
	const blocks = 40

	energy := func(sustain bool) float64 {
		params := sympatheticParams(0.002, 0.0)
		params.StringModel = StringModelModal
		p := NewPiano(48000, 16, params)
		p.SetSustainPedal(sustain)
		p.NoteOn(struck, 115)

		g := p.ringing.bank.ModalGroup(target)
		if g == nil {
			t.Fatalf("no modal group allocated for target note %d", target)
		}
		for i := 0; i < blocks; i++ {
			_ = p.Process(128)
		}
		var sum float64
		for i := range g.re {
			sum += float64(g.re[i])*float64(g.re[i]) + float64(g.im[i])*float64(g.im[i])
		}
		return sum
	}

	down := energy(true)
	up := energy(false)
	t.Logf("modal octave-neighbour energy: pedalDown=%e pedalUp=%e ratio=%.1fx", down, up, down/up)
	if down <= up*2.0 {
		t.Fatalf("expected pedal-down modal buildup to exceed pedal-up: down=%e up=%e", down, up)
	}
}

// TestFifthCouplingEnergizesNonOctaveString covers the non-octave half of the
// coupling graph. CouplingFifthGain is zeroed in every existing coupling test,
// so the seventh-semitone edges built by initStaticCouplingGraph were never
// exercised end to end.
func TestFifthCouplingEnergizesNonOctaveString(t *testing.T) {
	const struck = 60
	const fifthUp = 67 // a perfect fifth above C4, not an octave relative
	const blocks = 40

	// Octave gain is zero in both arms, so the only edge that can reach note 67
	// is the fifth edge.
	with := renderSympathetic(t, sympatheticParams(0.0, 0.002), true, struck, fifthUp, blocks)

	off := sympatheticParams(0.0, 0.0)
	off.CouplingEnabled = false
	off.CouplingMode = CouplingModeOff
	without := renderSympathetic(t, off, true, struck, fifthUp, blocks)

	t.Logf("fifth-neighbour energy: fifthGainOn=%e couplingOff=%e", with, without)
	if with <= without*2.0 {
		t.Fatalf("expected fifth coupling to energize note %d: with=%e without=%e", fifthUp, with, without)
	}
}

// TestFifthCouplingGraphIsSymmetricAroundStruckNote checks the graph itself:
// a non-zero fifth gain must produce edges seven semitones in both directions,
// and must not silently fall back to octave edges.
func TestFifthCouplingGraphIsSymmetricAroundStruckNote(t *testing.T) {
	params := sympatheticParams(0.0, 0.001)
	sb := NewStringBank(48000, params)

	edges := sb.coupling[60]
	if len(edges) != 2 {
		t.Fatalf("expected exactly the two fifth edges from note 60, got %d: %v", len(edges), edges)
	}
	var hasUp, hasDown bool
	for _, e := range edges {
		switch e.to {
		case 67:
			hasUp = true
		case 53:
			hasDown = true
		default:
			t.Fatalf("unexpected coupling edge from note 60 to %d with fifth-only gains", e.to)
		}
		if e.gain <= 0 {
			t.Fatalf("expected positive fifth coupling gain to %d, got %g", e.to, e.gain)
		}
	}
	if !hasUp || !hasDown {
		t.Fatalf("expected fifth coupling edges from 60 to both 67 and 53, got %v", edges)
	}
}
