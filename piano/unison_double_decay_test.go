package piano

import (
	"math"
	"testing"
)

// doubleDecayProbeParams isolates the bridge half of the unison coupling: one
// note, pedal held, no sympathetic loop, no inter-note coupling, and
// unison_crossfeed at ZERO so the older relative-motion damper is not a second
// uncontrolled path through the same loop.
func doubleDecayProbeParams(model StringModel, detuneScale float32) *Params {
	params := NewDefaultParams()
	params.StringModel = model
	params.ResonanceEnabled = false
	params.CouplingEnabled = false
	params.CouplingMode = CouplingModeOff
	params.UnisonCrossfeed = 0
	params.UnisonDetuneScale = detuneScale
	return params
}

// doubleDecayEnvelope renders one struck note through a bare StringBank and
// returns its max-held dB envelope plus the hop in seconds.
//
// A bare bank rather than a Piano: the body IR and the stereo widener smear the
// envelope, and the bank field is also the only way to ask for a coupling past
// maxBridgeCoupling, which the stress probes need.
func doubleDecayEnvelope(t *testing.T, model StringModel, note int, bridge float32, detuneScale float32, seconds int, frame int, hop int) ([]float64, float64) {
	t.Helper()

	params := doubleDecayProbeParams(model, detuneScale)
	sb := NewStringBank(48000, params)
	sb.bridgeCoupling = bridge
	h := NewHammerExciter(48000, params)
	sb.SetSustain(true)
	sb.SetKeyDown(note, true)
	h.Trigger(note, 100)

	out := make([]float32, 0, 48000*seconds)
	for range 48000 * seconds / 128 {
		for _, v := range sb.Process(128, h) {
			if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
				t.Fatalf("%s note %d, bridge %v: non-finite sample", model, note, bridge)
			}
			out = append(out, v)
		}
	}

	hopSec := float64(hop) / 48000
	span := int(math.Ceil(unisonBeatPeriod(note, detuneScale) / hopSec))
	return maxHoldDB(rmsEnvelopeDB(out, frame, hop), span), hopSec
}

// doubleDecayNotes are the multi-string groups the envelope tests measure: two
// two-string notes (MIDI 40-69) and two three-string ones (>= 70).
var doubleDecayNotes = []int{60, 67, 72, 76}

const (
	// doubleDecayRenderSeconds is long enough for every note here to fall the
	// 40 dB decayProlongation measures over, at every coupling strength swept.
	doubleDecayRenderSeconds = 30

	// minProlongation is the bound the shipped default must clear. Measured
	// 2.94-4.35 over the four notes (see DefaultBridgeCoupling for the table),
	// so this sits 32% under the smallest measurement - and, just as
	// importantly, well above the 1.17 the zero-detune control tops out at.
	minProlongation = 2.0

	// maxFlatProlongation is what a render with NO second decay stage may show.
	// A single exponential gives exactly 1.0 by construction; the uncoupled and
	// zero-detune arms measured 0.91-1.17, so this carries ~15% of headroom.
	maxFlatProlongation = 1.35
)

// TestBridgeCouplingProducesTwoStageDecay is the load-bearing test of PLAN.md
// 14.2, and the one the older unison_crossfeed could not have passed at any
// setting.
//
// A struck unison does not decay along one exponential. The hammer drives every
// string of the group in phase, and that COMMON motion is the half the bridge is
// compliant to, so it drains fast - the PROMPT sound. Detune rotates the energy
// into out-of-phase motion, which cancels at the bridge and is barely damped, and
// what is left is the AFTERSOUND. bridge_coupling is the term that makes the
// first stage fast; see bridge_coupling.go for why unison_crossfeed cannot, and
// does not, do this - it damps relative motion and is identically zero for
// in-phase motion, which is the opposite ordering.
//
// The metric is decayProlongation: how much longer the note actually takes to
// fall 40 dB than its own prompt slope predicts. One exponential gives 1.0, so
// there is no uncoupled arm to subtract - which matters, because a coupled
// render decays several times faster and any window borrowed from an uncoupled
// one lands past its end, on the floor, and measures the flush threshold.
func TestBridgeCouplingProducesTwoStageDecay(t *testing.T) {
	t.Parallel()

	for _, note := range doubleDecayNotes {
		env, hopSec := doubleDecayEnvelope(t, StringModelDWG, note, DefaultBridgeCoupling, 1.0, doubleDecayRenderSeconds, 2048, 512)
		got, prompt, ok := decayProlongation(env, hopSec)
		if !ok {
			t.Fatalf("note %d: the render never fell 40 dB in %d s, so there is no decay to measure",
				note, doubleDecayRenderSeconds)
		}
		t.Logf("note %d: prolongation %.2fx, prompt slope %.2f dB/s", note, got, prompt)
		if got < minProlongation {
			t.Errorf("note %d: decay prolongation %.2fx is under %.2fx - the note is decaying along ONE slope, "+
				"so the bridge coupling is not producing a prompt/aftersound split", note, got, minProlongation)
		}
	}
}

// TestBridgeCouplingIsFlatWithoutCoupling is the other half of the claim above:
// the two-stage envelope has to come FROM the term, not from the note.
func TestBridgeCouplingIsFlatWithoutCoupling(t *testing.T) {
	t.Parallel()

	// Note 60 is absent on purpose: uncoupled it never falls 40 dB inside 30 s,
	// so there is nothing to fit. That is itself the point - without the term
	// the note has one long slope and no knee.
	for _, note := range []int{67, 72, 76} {
		env, hopSec := doubleDecayEnvelope(t, StringModelDWG, note, 0, 1.0, doubleDecayRenderSeconds, 2048, 512)
		got, _, ok := decayProlongation(env, hopSec)
		if !ok {
			continue
		}
		t.Logf("note %d uncoupled: prolongation %.2fx", note, got)
		if got > maxFlatProlongation {
			t.Errorf("note %d: an uncoupled note already prolongs %.2fx, over %.2fx - the two-stage test above "+
				"would then be measuring the note rather than the coupling", note, got, maxFlatProlongation)
		}
	}
}

// TestBridgeCouplingHasNoProlongationWithoutDetune is the confound control, and
// the reason the metric can be trusted at all.
//
// Unison BEATING puts a ripple on the envelope at the beat rate, and a beat null
// is 10 dB deep or more - deeper than the effect. maxHoldDB erases that ripple
// without moving any fitted slope (see its comment). This test closes the
// remaining gap from the other side: with unison_detune_scale = 0 the strings of
// a group are identical, the whole motion is common motion, there is no
// out-of-phase mode left to ring on, and a bridge-admittance term must therefore
// damp UNIFORMLY - fast, but along one slope. Measured 0.99-1.17 at every
// strength from 0 to maxBridgeCoupling.
//
// The same setting also makes the older unison_crossfeed inert, since mix - y_i
// is identically zero when the strings agree and the gains sum to one. That is
// expected, not a bug.
func TestBridgeCouplingHasNoProlongationWithoutDetune(t *testing.T) {
	t.Parallel()

	for _, note := range doubleDecayNotes {
		for _, bridge := range []float32{DefaultBridgeCoupling, maxBridgeCoupling} {
			env, hopSec := doubleDecayEnvelope(t, StringModelDWG, note, bridge, 0, doubleDecayRenderSeconds, 2048, 512)
			got, _, ok := decayProlongation(env, hopSec)
			if !ok {
				continue
			}
			t.Logf("note %d, bridge %v, no detune: prolongation %.2fx", note, bridge, got)
			if got > maxFlatProlongation {
				t.Errorf("note %d at bridge %v with NO detune prolongs %.2fx, over %.2fx - with identical strings "+
					"there is no out-of-phase mode to carry an aftersound, so this is beating leaking into the "+
					"measurement rather than a second decay stage", note, bridge, got, maxFlatProlongation)
			}
		}
	}
}

// TestModalDecayOutrunsItsUnisonBeat records WHY the two tests above are DWG
// only, so the omission reads as a measurement and not as an oversight.
//
// Double decay needs time: the detune has to rotate energy out of phase before
// the bridge can stop draining it. The modal core does not have that time. Its
// notes fall 40 dB several times over inside a single unison beat period, so the
// out-of-phase mode never accumulates and there is no aftersound for the term to
// preserve - measured below, not assumed. The bridge term is still wired into
// the modal core (both cores must agree, and it improves the modal passivity
// argument - see ModalStringGroup.applyCrossfeed), it simply has nothing to act
// on until the modal decay rates themselves are addressed.
func TestModalDecayOutrunsItsUnisonBeat(t *testing.T) {
	t.Parallel()

	// A much finer envelope than the DWG tests use: the modal core falls 40 dB
	// in tens of milliseconds, which the 42.7 ms frame those use cannot even
	// resolve. That is the finding, so it has to be measured rather than
	// tripped over.
	const frame, hop = 256, 64

	for _, note := range doubleDecayNotes {
		env, hopSec := doubleDecayEnvelope(t, StringModelModal, note, DefaultBridgeCoupling, 1.0, 8, frame, hop)
		peak, pi := env[0], 0
		for i, v := range env {
			if v > peak {
				peak, pi = v, i
			}
		}
		fall := -1
		for i := pi; i < len(env); i++ {
			if env[i] <= peak-40 {
				fall = i
				break
			}
		}
		if fall < 0 {
			t.Fatalf("note %d: modal render did not fall 40 dB in 8 s", note)
		}
		fallSec := float64(fall-pi) * hopSec
		beat := unisonBeatPeriod(note, 1.0)
		t.Logf("note %d: modal 40 dB fall in %.3f s, unison beat period %.2f s -> %.1f decays per beat cycle",
			note, fallSec, beat, beat/fallSec)
		if fallSec > beat {
			t.Errorf("note %d: the modal note now outlives its unison beat period (%.3f s against %.2f s), so the "+
				"reason the two-stage tests above skip this core no longer holds - measure it here instead",
				note, fallSec, beat)
		}
	}
}

// bridgeRenderEnergy renders one struck note under a held pedal through either
// core and returns the whole-render energy. It is the core-generic twin of
// modalSecondEnergy, with the bridge coupling written to the bank field so the
// stress probes can ask for strengths past maxBridgeCoupling.
func bridgeRenderEnergy(t *testing.T, model StringModel, note int, bridge float32, seconds int) float64 {
	t.Helper()

	params := doubleDecayProbeParams(model, 1.0)
	sb := NewStringBank(48000, params)
	sb.bridgeCoupling = bridge
	h := NewHammerExciter(48000, params)
	sb.SetSustain(true)
	sb.SetKeyDown(note, true)
	h.Trigger(note, 100)

	energy := 0.0
	for range 48000 * seconds / 128 {
		for _, v := range sb.Process(128, h) {
			a := float64(v)
			if math.IsNaN(a) || math.IsInf(a, 0) {
				t.Fatalf("%s note %d, bridge %v: non-finite sample", model, note, bridge)
			}
			energy += a * a
		}
	}
	return energy
}

// TestBridgeCouplingDoesNotAddEnergy is the passivity fence, and the test that
// catches both ways this term can be got wrong.
//
// The force is -b*g_i*mix, and the energy pairing that decides passivity is
// sum_i y_i*F_i = -b*mix^2 <= 0 - for any gains, with no Jensen step. Both the
// SIGN and the g_i WEIGHT are load-bearing: flip the sign and the term is a
// rank-one positive feedback loop around an already resonant string (measured:
// note 60 grows 68x at b = 0.01 and 2.3e9x at b = 0.03), and drop the g_i and
// the pairing becomes -b*mix*sum(y_i), which is sign-indefinite whenever the
// unison gains are unequal - which defaultUnisonForNote makes them at every
// multi-string note. Either mistake makes some row here exceed 1.
//
// Unlike the older unison_crossfeed, the strict bound is available in BOTH
// cores: the modal injection is distributed over each string's modes through
// g.gain, the same shape the string is read through, so the argument closes
// there too. See ModalStringGroup.applyCrossfeed.
func TestBridgeCouplingDoesNotAddEnergy(t *testing.T) {
	t.Parallel()

	for _, model := range coreParityModels {
		for _, note := range []int{45, 52, 60, 72} {
			free := bridgeRenderEnergy(t, model, note, 0, 8)
			if free <= 0 {
				t.Fatalf("%s note %d: the uncoupled render is silent, the ratios would be meaningless", model, note)
			}
			for _, bridge := range []float32{DefaultBridgeCoupling, maxBridgeCoupling / 2, maxBridgeCoupling} {
				ratio := bridgeRenderEnergy(t, model, note, bridge, 8) / free
				t.Logf("%s note %d bridge %v: energy %.6fx of uncoupled", model, note, bridge, ratio)
				if ratio > 1.0+1e-6 {
					t.Errorf("%s note %d at bridge %v: the coupling ADDED energy (%.6fx) - the force must be "+
						"-b*g_i*mix, with both the minus sign and the g_i weight", model, note, bridge, ratio)
				}
			}
		}
	}
}

// TestBridgeCouplingIsInertOnSingleStringNotes is the control that attributes
// any failure above to the coupling and to nothing else in the render loop.
//
// Notes below MIDI 40 have one string, both cores skip the coupling branch
// entirely, and the render must therefore be bit-identical at any strength. See
// bridge_coupling.go for why a lone string is deliberately left out even though
// it does physically load the bridge.
func TestBridgeCouplingIsInertOnSingleStringNotes(t *testing.T) {
	t.Parallel()

	for _, model := range coreParityModels {
		for _, note := range []int{21, 33, 36} {
			reference := bridgeRenderEnergy(t, model, note, 0, 4)
			for _, bridge := range []float32{DefaultBridgeCoupling, maxBridgeCoupling, 25 * maxBridgeCoupling} {
				if got := bridgeRenderEnergy(t, model, note, bridge, 4); got != reference {
					t.Fatalf("%s note %d, bridge %v: energy %v, want exactly %v - a single-string group has no "+
						"coupling path", model, note, bridge, got, reference)
				}
			}
		}
	}
}

// TestBridgeCouplingStaysFiniteBeyondTheClamp keeps the clamp a policy about
// LEVEL rather than the only thing between the renderer and NaN - the lesson
// PLAN.md 14.1 recorded about the crossfeed's old bound being an accident of its
// scale factor.
//
// The margin here is 5x, where the crossfeed's equivalent test uses 25x, and
// that is a deliberate trade rather than a weaker claim. maxUnisonCrossfeed
// could afford 25x because nothing needs the crossfeed to be large;
// maxBridgeCoupling has a floor under it - the effect does not exist below
// b ~ 0.026 (see bridge_coupling.go) - so the bound sits at 0.1 against a
// divergence at 0.5 instead of an order of magnitude below it. At 25x the clamp
// the DWG core does go non-finite, which is recorded here rather than fenced
// out: the honest statement is that this term has 5x of headroom, not 25x.
func TestBridgeCouplingStaysFiniteBeyondTheClamp(t *testing.T) {
	t.Parallel()

	for _, model := range coreParityModels {
		for _, note := range []int{45, 60, 72} {
			for _, mul := range []float32{2, 5} {
				bridge := mul * maxBridgeCoupling
				// bridgeRenderEnergy fails the test on the first non-finite
				// sample, so reaching the comparison is already half the claim.
				first := bridgeRenderEnergy(t, model, note, bridge, 1)
				total := bridgeRenderEnergy(t, model, note, bridge, 8)
				if total < first {
					t.Fatalf("%s note %d at %vx the clamp: 8 s of energy (%v) is under the first second (%v)",
						model, note, mul, total, first)
				}
			}
		}
	}
}

// TestBridgeCouplingIsClamped guards the bound itself. Presets are hand-editable
// JSON, so NewStringBank clamps rather than trusts.
func TestBridgeCouplingIsClamped(t *testing.T) {
	t.Parallel()

	params := doubleDecayProbeParams(StringModelDWG, 1.0)
	params.BridgeCoupling = 10 * maxBridgeCoupling
	if got := NewStringBank(48000, params).bridgeCoupling; got != maxBridgeCoupling {
		t.Fatalf("bank bridge coupling = %v, want it clamped to %v", got, maxBridgeCoupling)
	}
}
