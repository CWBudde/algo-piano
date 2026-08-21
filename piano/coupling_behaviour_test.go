package piano

import (
	"fmt"
	"math"
	"testing"
)

// Behaviour coverage for the sparse string-bank coupling graph: what the
// listener hears when `coupling_mode` changes, and how the physical weight
// model's detune and distance penalties translate into injected energy.
//
// The structural side (which edges exist, which flags flip) is covered in
// ringing_test.go. Everything here renders audio and measures the energy that
// actually arrives in an unstruck string.
//
// All notes stay between MIDI 48 and 84. The DWG core has a known defect above
// roughly MIDI 96 (runaway DC, silent top notes; see
// TestTrebleRegisterCollapsesInDWGCore in tuning_test.go), so the mid register
// keeps these measurements clear of it.

// couplingBehaviourParams builds a parameter set in which the coupling graph is
// the only path from the struck note to a silent neighbour: the resonance
// engine is off, so nothing else can deposit energy in the target.
func couplingBehaviourParams(mode CouplingMode) *Params {
	params := NewDefaultParams()
	params.ResonanceEnabled = false
	params.CouplingEnabled = true
	params.CouplingMode = mode
	return params
}

// requireFinite rejects NaN and infinities before any ordered comparison sees
// them. Every threshold in this file is an ordered comparison, and both `x <= 0`
// and `x < y` are false for NaN, so a numerically unstable coupling render would
// otherwise satisfy the monotonicity and non-zero assertions instead of failing
// them.
func requireFinite(t *testing.T, label string, value float64) float64 {
	t.Helper()
	if math.IsNaN(value) || math.IsInf(value, 0) {
		t.Fatalf("%s: expected a finite measurement, got %v", label, value)
	}
	return value
}

// renderCouplingEnergyDWG strikes note 60 with the sustain pedal down and
// returns the energy stored in the unstruck DWG target's delay lines.
func renderCouplingEnergyDWG(t *testing.T, params *Params, target int, blocks int) float64 {
	t.Helper()
	params.StringModel = StringModelDWG
	p := NewPiano(48000, 16, params)
	p.SetSustainPedal(true)
	p.NoteOn(60, 115)

	g := p.ringing.bank.Group(target)
	if g == nil {
		t.Fatalf("no DWG group allocated for target note %d", target)
	}
	for i := 0; i < blocks; i++ {
		_ = p.Process(128)
	}
	return requireFinite(t, fmt.Sprintf("DWG note %d energy", target), voiceInternalEnergy(g))
}

// renderCouplingEnergyModal is the modal-core twin of renderCouplingEnergyDWG.
// The modal group stores its state as per-mode complex amplitudes instead of
// delay lines, so its internal energy has to be summed differently.
func renderCouplingEnergyModal(t *testing.T, params *Params, target int, blocks int) float64 {
	t.Helper()
	params.StringModel = StringModelModal
	p := NewPiano(48000, 16, params)
	p.SetSustainPedal(true)
	p.NoteOn(60, 115)

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
	return requireFinite(t, fmt.Sprintf("modal note %d energy", target), sum)
}

func couplingEdgeGain(sb *StringBank, src int, dst int) float32 {
	for _, e := range sb.coupling[src] {
		if e.to == dst {
			return e.gain
		}
	}
	return 0
}

func totalCouplingEdges(sb *StringBank) int {
	total := 0
	for note := range sb.coupling {
		total += len(sb.coupling[note])
	}
	return total
}

// TestCouplingModeChangesSympatheticEnergyDWG asserts the audible consequence
// of `coupling_mode`, not just the graph it builds: with C4 struck and the
// sustain pedal down, how much energy ends up in the silent C5 string.
//
// Measured on 2026-08-21, DWG core, 40 blocks of 128 frames at 48 kHz,
// resonance engine off, default coupling gains:
//
//	off:      0.000000e+00 (bit-exact zero — there are no edges at all)
//	static:   2.428813e-09
//	physical: 4.560753e-10
//
// The `off` arm is exactly zero rather than merely small, so it is asserted as
// such. The other two only have to be non-zero; their absolute levels come from
// two different gain models and are not comparable to each other.
func TestCouplingModeChangesSympatheticEnergyDWG(t *testing.T) {
	const target = 72 // octave above the struck C4, damper lifted by the pedal only
	const blocks = 40

	off := renderCouplingEnergyDWG(t, couplingBehaviourParams(CouplingModeOff), target, blocks)
	static := renderCouplingEnergyDWG(t, couplingBehaviourParams(CouplingModeStatic), target, blocks)
	physical := renderCouplingEnergyDWG(t, couplingBehaviourParams(CouplingModePhysical), target, blocks)

	t.Logf("DWG note-%d energy: off=%e static=%e physical=%e", target, off, static, physical)
	if off != 0 {
		t.Fatalf("expected no coupling-driven energy in off mode, got %e", off)
	}
	if static <= 0 {
		t.Fatalf("expected measurable static-mode coupling energy, got %e", static)
	}
	if physical <= 0 {
		t.Fatalf("expected measurable physical-mode coupling energy, got %e", physical)
	}
}

// TestCouplingModeChangesSympatheticEnergyModal repeats the mode comparison on
// the modal core, whose injection and damper handling are a separate code path.
//
// Measured on 2026-08-21, modal core, same render length:
//
//	off:      0.000000e+00
//	static:   1.843514e-09
//	physical: 3.095291e-10
func TestCouplingModeChangesSympatheticEnergyModal(t *testing.T) {
	const target = 72
	const blocks = 40

	off := renderCouplingEnergyModal(t, couplingBehaviourParams(CouplingModeOff), target, blocks)
	static := renderCouplingEnergyModal(t, couplingBehaviourParams(CouplingModeStatic), target, blocks)
	physical := renderCouplingEnergyModal(t, couplingBehaviourParams(CouplingModePhysical), target, blocks)

	t.Logf("modal note-%d energy: off=%e static=%e physical=%e", target, off, static, physical)
	if off != 0 {
		t.Fatalf("expected no coupling-driven energy in off mode, got %e", off)
	}
	if static <= 0 {
		t.Fatalf("expected measurable static-mode coupling energy, got %e", static)
	}
	if physical <= 0 {
		t.Fatalf("expected measurable physical-mode coupling energy, got %e", physical)
	}
}

// TestPhysicalCouplingEnergyFollowsRelatedness checks that physical mode does
// not merely couple, but couples in the order its weight model predicts: the
// octave shares every partial with the source, the fifth shares every third
// one, and the tritone shares nothing close enough to clear
// couplingPhysicalMinScore, so it never gets an edge at all.
//
// Measured on 2026-08-21, physical mode, struck note 60, 40 blocks:
//
//	core   octave (72)    fifth (67)     tritone (66)   octave/fifth
//	DWG    4.560753e-10   4.533022e-11   0              10.06x
//	modal  3.095291e-10   1.594014e-10   0               1.94x
//
// The modal core spreads coupling energy differently across its modes, so its
// contrast is the weaker of the two. The assertion uses 1.35x, i.e. the worst
// measured ratio with ~40% headroom.
func TestPhysicalCouplingEnergyFollowsRelatedness(t *testing.T) {
	const blocks = 40
	const minRatio = 1.35

	cases := []struct {
		name   string
		render func(*testing.T, *Params, int, int) float64
	}{
		{name: "dwg", render: renderCouplingEnergyDWG},
		{name: "modal", render: renderCouplingEnergyModal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			octave := tc.render(t, couplingBehaviourParams(CouplingModePhysical), 72, blocks)
			fifth := tc.render(t, couplingBehaviourParams(CouplingModePhysical), 67, blocks)
			tritone := tc.render(t, couplingBehaviourParams(CouplingModePhysical), 66, blocks)

			t.Logf("%s physical energy: octave=%e fifth=%e tritone=%e octave/fifth=%.2fx",
				tc.name, octave, fifth, tritone, octave/fifth)
			if fifth <= 0 {
				t.Fatalf("expected the fifth to receive coupling energy, got %e", fifth)
			}
			if octave <= fifth*minRatio {
				t.Fatalf("expected the octave to outrank the fifth by at least %.2fx: octave=%e fifth=%e",
					minRatio, octave, fifth)
			}
			if tritone != 0 {
				t.Fatalf("expected the unrelated tritone to receive no coupling energy, got %e", tritone)
			}
		})
	}

	sb := NewStringBank(48000, couplingBehaviourParams(CouplingModePhysical))
	octaveGain := couplingEdgeGain(sb, 60, 72)
	fifthGain := couplingEdgeGain(sb, 60, 67)
	tritoneGain := couplingEdgeGain(sb, 60, 66)
	t.Logf("physical edge gains from 60: octave=%e fifth=%e tritone=%e", octaveGain, fifthGain, tritoneGain)
	if !(octaveGain > fifthGain && fifthGain > 0 && tritoneGain == 0) {
		t.Fatalf("edge gains contradict the measured energies: octave=%e fifth=%e tritone=%e",
			octaveGain, fifthGain, tritoneGain)
	}
}

// TestRuntimeCouplingModeSwitchTakesEffectMidRender proves that
// SetCouplingMode changes the audio and not only the struct field: the graph is
// swapped between blocks of a running render and the target's energy has to
// follow.
//
// Measured on 2026-08-21, DWG core, note 60 struck, note 72 observed, 60 blocks
// of 128 frames, switch performed before block 30, after the string-loop DC
// blocker landed:
//
//	always off:        0.000000e+00
//	always static:     1.529363e-09
//	off -> static:     1.364843e-09   (89.2% of the always-static arm)
//	static -> off:     5.388353e-10   (35.2% of the always-static arm)
//	always physical:   2.771973e-10
//	off -> physical:   2.841634e-10   (102.5% of the always-physical arm)
//	physical -> off:   1.208579e-10   (43.6% of the always-physical arm)
//
// Physical mode gets its own pair of switched arms because SetCouplingMode
// rebuilds the graph from the physical weight model rather than the static one;
// the off/static pair alone would leave a runtime switch into physical mode
// untested (only its startup path is covered above).
//
// The two directions are NOT symmetric, and only one of them carries a
// quantitative bound:
//
//   - Switching coupling OFF midway must leave clearly less energy than leaving
//     it on, because the injection stops and what was gathered before the
//     switch decays for the rest of the render. Bound: 0.70x the constant arm,
//     against a worst measured share of 43.6%, i.e. worst measured plus ~60%
//     headroom.
//   - Switching coupling ON midway is only required to reach the audio at all,
//     i.e. to leave the exactly-zero floor the always-off arm sits on. It is
//     deliberately given no upper bound: energy is sampled at the end of the
//     render, so a late switch-on injects into a target that has decayed less,
//     and continuous injection can partially cancel against the target's own
//     motion. Both effects push the switched-on arm towards — and, for physical
//     mode, just past — the always-on arm. An earlier revision of this test
//     asserted "clearly less" in this direction too; that held only while the
//     waveguide carried a DC pedestal that front-loaded the accumulation, and
//     it is not a property of the coupling model.
func TestRuntimeCouplingModeSwitchTakesEffectMidRender(t *testing.T) {
	const struck = 60
	const target = 72
	const blocks = 60
	const switchAt = 30
	// Upper bound for the arms that switch coupling OFF midway; see the
	// doc comment for why the switch-ON direction carries no bound.
	const switchedOffShareMax = 0.70

	render := func(start CouplingMode, switchTo CouplingMode, doSwitch bool) float64 {
		p := NewPiano(48000, 16, couplingBehaviourParams(start))
		p.SetSustainPedal(true)
		p.NoteOn(struck, 115)
		g := p.ringing.bank.Group(target)
		if g == nil {
			t.Fatalf("no DWG group allocated for target note %d", target)
		}
		for i := 0; i < blocks; i++ {
			if doSwitch && i == switchAt {
				if !p.SetCouplingMode(switchTo) {
					t.Fatalf("expected runtime switch to %q to succeed", switchTo)
				}
			}
			_ = p.Process(128)
		}
		return requireFinite(t, fmt.Sprintf("DWG note %d energy", target), voiceInternalEnergy(g))
	}

	alwaysOff := render(CouplingModeOff, CouplingModeOff, false)
	alwaysStatic := render(CouplingModeStatic, CouplingModeStatic, false)
	offThenStatic := render(CouplingModeOff, CouplingModeStatic, true)
	staticThenOff := render(CouplingModeStatic, CouplingModeOff, true)
	alwaysPhysical := render(CouplingModePhysical, CouplingModePhysical, false)
	offThenPhysical := render(CouplingModeOff, CouplingModePhysical, true)
	physicalThenOff := render(CouplingModePhysical, CouplingModeOff, true)

	t.Logf("note-%d energy: alwaysOff=%e alwaysStatic=%e offThenStatic=%e (%.1f%%) staticThenOff=%e (%.1f%%)",
		target, alwaysOff, alwaysStatic, offThenStatic, 100*offThenStatic/alwaysStatic,
		staticThenOff, 100*staticThenOff/alwaysStatic)
	t.Logf("note-%d energy: alwaysPhysical=%e offThenPhysical=%e (%.1f%%) physicalThenOff=%e (%.1f%%)",
		target, alwaysPhysical, offThenPhysical, 100*offThenPhysical/alwaysPhysical,
		physicalThenOff, 100*physicalThenOff/alwaysPhysical)

	if alwaysOff != 0 {
		t.Fatalf("expected the always-off arm to stay silent, got %e", alwaysOff)
	}
	if alwaysStatic <= 0 {
		t.Fatalf("expected the always-static arm to build up energy, got %e", alwaysStatic)
	}
	if offThenStatic <= 0 {
		t.Fatalf("switching off->static mid-render did not reach the audio: energy stayed at %e", offThenStatic)
	}
	if staticThenOff <= 0 {
		t.Fatalf("expected the static->off arm to keep the energy gathered before the switch, got %e", staticThenOff)
	}
	if staticThenOff >= alwaysStatic*switchedOffShareMax {
		t.Fatalf("switching static->off mid-render did not reach the audio: switched=%e always=%e",
			staticThenOff, alwaysStatic)
	}

	if alwaysPhysical <= 0 {
		t.Fatalf("expected the always-physical arm to build up energy, got %e", alwaysPhysical)
	}
	if offThenPhysical <= 0 {
		t.Fatalf("switching off->physical mid-render did not reach the audio: energy stayed at %e", offThenPhysical)
	}
	if physicalThenOff <= 0 {
		t.Fatalf("expected the physical->off arm to keep the energy gathered before the switch, got %e", physicalThenOff)
	}
	if physicalThenOff >= alwaysPhysical*switchedOffShareMax {
		t.Fatalf("switching physical->off mid-render did not reach the audio: switched=%e always=%e",
			physicalThenOff, alwaysPhysical)
	}
}

// couplingDetuneSweepSigmas runs from a penalty far tighter than the default
// (28 cents) to one far looser. Smaller sigma means a stronger detune penalty.
var couplingDetuneSweepSigmas = []float32{4, 8, 14, 28, 56, 112}

// TestCouplingDetuneSigmaReducesInjectedEnergy sweeps the detune penalty and
// measures the energy injected into an equal-tempered major third above the
// struck note. The third is the useful probe: its 5:4 partial pair sits about
// 13.7 cents from just intonation, so it is exactly the kind of near-miss that
// couplingDetuneSigmaCents is meant to discount, while the octave it competes
// with is detune-free.
//
// CouplingMaxNeighbors is raised to 64 so the top-K cut does not mask the
// penalty: with the default cap of 10 the third never makes the list at any
// sigma and every arm measures zero.
//
// Measured on 2026-08-21, DWG core, note 60 struck, note 64 observed, 40
// blocks. Both the precomputed edge gain and the rendered energy move together:
//
//	sigma [cents]   edge gain 60->64   energy in note 64
//	  4             0.000000e+00       0.000000e+00   (edge pruned entirely)
//	  8             3.788874e-07       4.648651e-15
//	 14             9.805589e-07       3.047614e-14
//	 28             1.368092e-06       5.890933e-14
//	 56             1.489667e-06       6.973052e-14
//	112             1.521218e-06       7.261825e-14
//
// The sweep runs from the tightest sigma to the loosest, so both the gain and
// the energy column have to be non-decreasing; the failure messages are phrased
// in that direction ("loosening ... must not reduce ...").
//
// The sweep is asserted as monotone rather than against pinned magnitudes: the
// last two steps differ by only 4%, so any fixed tolerance would either be
// meaningless or brittle.
func TestCouplingDetuneSigmaReducesInjectedEnergy(t *testing.T) {
	const target = 64 // major third above the struck note 60
	const blocks = 40

	energies := make([]float64, len(couplingDetuneSweepSigmas))
	gains := make([]float32, len(couplingDetuneSweepSigmas))
	for i, sigma := range couplingDetuneSweepSigmas {
		params := couplingBehaviourParams(CouplingModePhysical)
		params.CouplingDetuneSigmaCents = sigma
		params.CouplingMaxNeighbors = 64
		gains[i] = couplingEdgeGain(NewStringBank(48000, params), 60, target)
		requireFinite(t, fmt.Sprintf("edge gain 60->%d at sigma %.1f cents", target, sigma), float64(gains[i]))
		energies[i] = renderCouplingEnergyDWG(t, params, target, blocks)
		t.Logf("detune sigma=%6.1f cents: edge gain 60->%d=%e energy=%e", sigma, target, gains[i], energies[i])
	}

	for i := 1; i < len(energies); i++ {
		if energies[i] < energies[i-1] {
			t.Fatalf("loosening the detune penalty must not reduce injected energy: sigma %.1f cents=%e dropped to sigma %.1f cents=%e",
				couplingDetuneSweepSigmas[i-1], energies[i-1], couplingDetuneSweepSigmas[i], energies[i])
		}
		if gains[i] < gains[i-1] {
			t.Fatalf("loosening the detune penalty must not reduce the edge gain: sigma %.1f cents=%e dropped to sigma %.1f cents=%e",
				couplingDetuneSweepSigmas[i-1], gains[i-1], couplingDetuneSweepSigmas[i], gains[i])
		}
	}
	tightest := energies[0]
	loosest := energies[len(energies)-1]
	if loosest <= tightest {
		t.Fatalf("expected the loosest detune penalty to inject more than the tightest: tightest=%e loosest=%e",
			tightest, loosest)
	}
}

// couplingDistanceSweepExponents runs from no distance penalty at all to a very
// steep one; the default is 1.15.
var couplingDistanceSweepExponents = []float32{0, 0.5, 1, 2, 3}

// TestCouplingDistanceExponentReducesInjectedEnergy sweeps the register-distance
// exponent and measures the energy injected into a target two octaves above the
// struck note. Note 84 keeps its harmonic relation to note 60 constant across
// the sweep, so the only thing that changes is the distance penalty.
//
// Measured on 2026-08-21, DWG core, note 60 struck, note 84 observed, 40
// blocks:
//
//	exponent   edge gain 60->84   energy in note 84
//	0.0        2.906731e-05       8.779776e-11
//	0.5        2.531640e-05       6.760387e-11
//	1.0        2.142678e-05       4.583053e-11
//	2.0        1.455171e-05       2.026970e-11
//	3.0        9.451506e-06       8.375169e-12
//
// End to end that is a 10.5x reduction; every single step is a strict decrease,
// which is what the assertion checks.
func TestCouplingDistanceExponentReducesInjectedEnergy(t *testing.T) {
	const target = 84 // two octaves above the struck note 60
	const blocks = 40

	energies := make([]float64, len(couplingDistanceSweepExponents))
	gains := make([]float32, len(couplingDistanceSweepExponents))
	for i, exponent := range couplingDistanceSweepExponents {
		params := couplingBehaviourParams(CouplingModePhysical)
		params.CouplingDistanceExponent = exponent
		gains[i] = couplingEdgeGain(NewStringBank(48000, params), 60, target)
		requireFinite(t, fmt.Sprintf("edge gain 60->%d at distance exponent %.1f", target, exponent), float64(gains[i]))
		energies[i] = renderCouplingEnergyDWG(t, params, target, blocks)
		t.Logf("distance exponent=%.1f: edge gain 60->%d=%e energy=%e", exponent, target, gains[i], energies[i])
	}

	for i := 1; i < len(energies); i++ {
		if energies[i] >= energies[i-1] {
			t.Fatalf("a steeper distance exponent must reduce injected energy: exp %.1f=%e, exp %.1f=%e",
				couplingDistanceSweepExponents[i-1], energies[i-1], couplingDistanceSweepExponents[i], energies[i])
		}
		if gains[i] >= gains[i-1] {
			t.Fatalf("a steeper distance exponent must reduce the edge gain: exp %.1f=%e, exp %.1f=%e",
				couplingDistanceSweepExponents[i-1], gains[i-1], couplingDistanceSweepExponents[i], gains[i])
		}
	}
}

// TestPhysicalCouplingEdgeSetFollowsNeighborCap pins the top-K half of the edge
// precomputation: as long as enough candidates clear couplingPhysicalMinScore,
// every source note gets exactly CouplingMaxNeighbors edges.
//
// Measured on 2026-08-21, physical mode, default detune sigma, note range
// 21..108 (88 notes):
//
//	cap    edges from note 60   edges in the whole bank
//	 1     1                      88
//	 2     2                     176
//	 4     4                     352
//	 6     6                     528
//	10    10                     880
//	16    16                    1408
//	24    24                    2081   (cap no longer binding everywhere)
//
// Up to a cap of 16 the bank total is exactly 88 x cap. At 24 the extreme notes
// run out of candidates above the score threshold, so the total falls short of
// 88 x 24 = 2112 — hence the total is only asserted to be non-decreasing.
func TestPhysicalCouplingEdgeSetFollowsNeighborCap(t *testing.T) {
	caps := []int{1, 2, 4, 6, 10, 16, 24}

	prevTotal := 0
	for _, maxNeighbors := range caps {
		params := couplingBehaviourParams(CouplingModePhysical)
		params.CouplingMaxNeighbors = maxNeighbors
		sb := NewStringBank(48000, params)

		edges := len(sb.coupling[60])
		total := totalCouplingEdges(sb)
		t.Logf("CouplingMaxNeighbors=%2d: edges from note 60=%2d bank total=%d", maxNeighbors, edges, total)

		if edges > maxNeighbors {
			t.Fatalf("edge count must never exceed the neighbour cap: cap=%d edges=%d", maxNeighbors, edges)
		}
		if maxNeighbors <= 16 && edges != maxNeighbors {
			t.Fatalf("expected note 60 to saturate the neighbour cap: cap=%d edges=%d", maxNeighbors, edges)
		}
		if total < prevTotal {
			t.Fatalf("raising the neighbour cap must not shrink the bank edge set: cap=%d total=%d previous=%d",
				maxNeighbors, total, prevTotal)
		}
		prevTotal = total
	}
}

// TestPhysicalCouplingEdgeSetShrinksWithTighterDetunePenalty pins the other
// half of the precomputation: the couplingPhysicalMinScore threshold. With the
// neighbour cap raised out of the way, the surviving edge count is governed by
// how many candidates clear the threshold, and a tighter detune penalty pushes
// off-harmonic candidates below it.
//
// Measured on 2026-08-21, physical mode, CouplingMaxNeighbors=64:
//
//	sigma [cents]   edges in the whole bank
//	  4             1106
//	  8             2073
//	 14             2241
//	 28             2792
//	 56             3749
//	112             4448
//
// That is a 4.0x range, strictly monotone in sigma.
func TestPhysicalCouplingEdgeSetShrinksWithTighterDetunePenalty(t *testing.T) {
	totals := make([]int, len(couplingDetuneSweepSigmas))
	for i, sigma := range couplingDetuneSweepSigmas {
		params := couplingBehaviourParams(CouplingModePhysical)
		params.CouplingDetuneSigmaCents = sigma
		params.CouplingMaxNeighbors = 64
		totals[i] = totalCouplingEdges(NewStringBank(48000, params))
		t.Logf("detune sigma=%6.1f cents: bank edge total=%d", sigma, totals[i])
	}

	for i := 1; i < len(totals); i++ {
		if totals[i] <= totals[i-1] {
			t.Fatalf("a looser detune penalty must admit more edges: sigma %.1f=%d, sigma %.1f=%d",
				couplingDetuneSweepSigmas[i-1], totals[i-1], couplingDetuneSweepSigmas[i], totals[i])
		}
	}
}
