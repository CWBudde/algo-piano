package piano

import (
	"math"
	"testing"
)

// Cross-core behaviour parity for PLAN.md 12.4: the sustain pedal and the
// coupling graph have to steer the DWG and the modal core in the SAME
// DIRECTION, even though the two cores do not produce the same audio.
//
// That last part is the whole design of this file, so read it before
// "tightening" anything here. TestDWGModalDistanceIsBounded and
// TestDWGModalDistanceIsBoundedOnChords in core_distance_test.go measure the
// objective distance between the cores at 0.83-0.84 on a 0..1 scale, i.e. they
// are about as far apart as two piano renders can be. Absolute agreement is
// therefore not a property this codebase has today, and asserting it would only
// produce a test that has to be deleted. What CAN be pinned, and is what a
// listener would actually notice breaking, is relative behaviour: pressing the
// pedal must lengthen the tail in both cores, and turning coupling on must
// energise a silent neighbour in both cores, by the same ordering.
//
// Every note used here stays between MIDI 48 and 84, for the reason given at
// the top of coupling_behaviour_test.go: the DWG core has a known defect above
// roughly MIDI 96, and a render that reached into it would measure the defect
// rather than the parity.

// coreParityModels is the pair under test. Every parity test below runs the
// identical scenario through both entries and compares the two verdicts.
var coreParityModels = []StringModel{StringModelDWG, StringModelModal}

// monoTailRMS reports the RMS of the last `frames` samples of a mono render.
// The tail is what the sustain pedal is audible in, so the measurement window
// starts after the release rather than covering the whole render, where the
// attack would dominate both arms equally and compress the contrast.
func monoTailRMS(samples []float64, frames int) float64 {
	if len(samples) == 0 || frames <= 0 {
		return 0
	}
	if frames > len(samples) {
		frames = len(samples)
	}
	var sum float64
	for _, v := range samples[len(samples)-frames:] {
		sum += v * v
	}
	return math.Sqrt(sum / float64(frames))
}

// TestSustainPedalTailParityAcrossCores renders one note, releases the key
// mid-render, and compares a run where the pedal stays down against one where
// the pedal lifts in the same block. The damped arm must decay faster in BOTH
// cores.
//
// This is the mid-render pedal lift, which is a different code path from
// TestPartialSustainPedalScalesResidualEnergy in sustain_partial_test.go: that
// one sets a fixed pedal depth before the note and varies the depth, so it never
// exercises a damper coming down on a string that is already ringing.
//
// Measured on 2026-08-22 (Go 1.26.5, linux/amd64), note 60 at velocity 100,
// 140 blocks of 128 frames at 48 kHz, keys released and pedal lifted at block
// 60, RMS taken over the final 80 blocks:
//
//	core   pedal held   pedal lifted   held/lifted
//	DWG    6.854180e-01 2.687549e-01   2.55x
//	modal  8.905046e-01 1.087654e-01   8.19x
//
// The bound is 1.50x, i.e. the worst measured ratio (DWG's 2.55x) with ~40%
// of headroom. The two cores are deliberately held to one shared bound and not
// to their own measured ratios: the claim under test is "the pedal works in
// both cores", not "the modal core damps 3x harder than the DWG core", which is
// a consequence of their different damper models and not a contract.
func TestSustainPedalTailParityAcrossCores(t *testing.T) {
	const blocks = 140
	const releaseAt = 60
	const tailFrames = (blocks - releaseAt) * 128
	const minRatio = 1.50

	for _, model := range coreParityModels {
		t.Run(string(model), func(t *testing.T) {
			cfg := defaultCoreRenderConfig()
			cfg.model = model
			cfg.blocks = blocks
			cfg.sustainAt = 0
			cfg.keyUpAt = releaseAt

			held := monoTailRMS(renderCoreMono(cfg), tailFrames)

			cfg.sustainOffAt = releaseAt
			lifted := monoTailRMS(renderCoreMono(cfg), tailFrames)

			requireFinite(t, string(model)+" pedal-held tail RMS", held)
			requireFinite(t, string(model)+" pedal-lifted tail RMS", lifted)
			t.Logf("%s tail RMS: pedalHeld=%e pedalLifted=%e ratio=%.2fx", model, held, lifted, held/lifted)

			if lifted <= 0 {
				t.Fatalf("%s: expected some audible tail even with the pedal lifted, got %e", model, lifted)
			}
			if held <= lifted*minRatio {
				t.Fatalf("%s: holding the sustain pedal must leave a clearly louder tail than lifting it: held=%e lifted=%e, need %.2fx",
					model, held, lifted, minRatio)
			}
		})
	}
}

// TestCouplingModeOrderingParityAcrossCores runs the same coupling-mode sweep
// through both cores and requires the resulting energies to be ORDERED the same
// way. It reuses the probes from coupling_behaviour_test.go, so what is new here
// is only the cross-core comparison: the existing tests assert each core's
// behaviour in isolation and would still pass if the two cores ranked the modes
// differently.
//
// Measured on 2026-08-22 (Go 1.26.5, linux/amd64), note 60 struck at velocity
// 115 with the sustain pedal down, energy sampled in the silent note 72 after
// 40 blocks of 128 frames, resonance engine off:
//
//	mode       DWG            modal          DWG/modal
//	off        0.000000e+00   0.000000e+00   —  (bit-exact zero: no edges exist)
//	static     1.892777e-09   1.843514e-09   1.03x
//	physical   3.353057e-10   3.095291e-10   1.08x
//
// Asserted: off is exactly zero in both cores, both other modes are non-zero in
// both cores, and the static/physical ordering matches between the cores.
//
// Deliberately NOT asserted: the absolute energies, and the ratio between the
// cores. The two columns happening to land within 10% of each other here is a
// coincidence of this scenario, not a contract — the cores store their state in
// completely different quantities (delay-line samples versus per-mode complex
// amplitudes) and score 0.83-0.84 apart on a full render, so a future change may
// move one column without being a regression. Only the ordering is a claim about
// the coupling model rather than about the cores.
func TestCouplingModeOrderingParityAcrossCores(t *testing.T) {
	const target = 72 // octave above the struck note, damper lifted by the pedal only
	const blocks = 40

	modes := []CouplingMode{CouplingModeOff, CouplingModeStatic, CouplingModePhysical}
	energies := map[StringModel][]float64{}

	renderers := map[StringModel]func(*testing.T, *Params, int, int) float64{
		StringModelDWG:   renderCouplingEnergyDWG,
		StringModelModal: renderCouplingEnergyModal,
	}
	for _, model := range coreParityModels {
		for _, mode := range modes {
			e := renderers[model](t, couplingBehaviourParams(mode), target, blocks)
			energies[model] = append(energies[model], e)
			t.Logf("%s coupling mode %-8s: note-%d energy=%e", model, mode, target, e)
		}
	}

	for i, mode := range modes {
		dwg, modal := energies[StringModelDWG][i], energies[StringModelModal][i]
		if mode == CouplingModeOff {
			// Exact zero rather than "small": with coupling off the graph
			// has no edges at all, so nothing can reach the target.
			if dwg != 0 || modal != 0 {
				t.Fatalf("coupling off must leave the target silent in both cores: dwg=%e modal=%e", dwg, modal)
			}
			continue
		}
		if dwg <= 0 || modal <= 0 {
			t.Fatalf("coupling mode %q must inject measurable energy in both cores: dwg=%e modal=%e", mode, dwg, modal)
		}
	}

	// Pairwise ordering has to agree between the cores. sign() is compared
	// rather than the magnitudes, which is the whole point: the cores may
	// disagree on how much, never on which way.
	for i := range modes {
		for j := i + 1; j < len(modes); j++ {
			dwgSign := compareEnergies(energies[StringModelDWG][i], energies[StringModelDWG][j])
			modalSign := compareEnergies(energies[StringModelModal][i], energies[StringModelModal][j])
			t.Logf("ordering %s vs %s: dwg=%+d modal=%+d", modes[i], modes[j], dwgSign, modalSign)
			if dwgSign != modalSign {
				t.Fatalf("cores disagree on the %s vs %s coupling ordering: dwg=%e/%e (%+d) modal=%e/%e (%+d)",
					modes[i], modes[j],
					energies[StringModelDWG][i], energies[StringModelDWG][j], dwgSign,
					energies[StringModelModal][i], energies[StringModelModal][j], modalSign)
			}
		}
	}
}

// compareEnergies returns -1, 0 or +1 for a < b, a == b, a > b. NaN is not
// silently ordered: an ordered comparison against NaN is false in both
// directions, which would report NaN as "equal" and let a numerically unstable
// render satisfy the parity assertion instead of failing it.
func compareEnergies(a float64, b float64) int {
	switch {
	case math.IsNaN(a) || math.IsNaN(b):
		return -2
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}
