package piano

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strconv"
	"testing"
)

// resonanceProbeBlock is the block size the loop-gain probe drives the bank
// with, matching the AudioWorklet render quantum used everywhere else.
const resonanceProbeBlock = 128

// modalCalibratedPresetPath is the tracked preset the probes exercise besides
// the defaults. It is read rather than transcribed so that re-fitting it also
// re-points the guard.
const modalCalibratedPresetPath = "../assets/presets/modal-calibrated.json"

// resonanceLoopKnobs is the subset of a preset that multiplies the sympathetic
// resonance loop: ModalExcitation and ModalUndampedLoss scale what a group does
// with an injected force, ModalPartials and ModalGainExponent decide how many
// modes take part and how loud the upper ones are, and ResonanceGain scales the
// injection itself.
type resonanceLoopKnobs struct {
	ResonanceGain          *float32 `json:"resonance_gain"`
	ResonancePerNoteFilter *bool    `json:"resonance_per_note_filter"`
	StringModel            *string  `json:"string_model"`
	ModalPartials          *int     `json:"modal_partials"`
	ModalGainExponent      *float32 `json:"modal_gain_exponent"`
	ModalExcitation        *float32 `json:"modal_excitation"`
	ModalUndampedLoss      *float32 `json:"modal_undamped_loss"`
	ModalDampedLoss        *float32 `json:"modal_damped_loss"`
}

// modalCalibratedResonanceParams returns default params with the resonance-loop
// knobs of assets/presets/modal-calibrated.json applied.
//
// The preset is decoded here with a local struct instead of preset.LoadJSON
// because package preset imports package piano; an in-package test cannot
// import it back. Only the knobs on the loop are taken, so the probe stays a
// loop measurement and does not drag in the preset's IR paths or hammer curves.
func modalCalibratedResonanceParams(t *testing.T) *Params {
	t.Helper()

	raw, err := os.ReadFile(modalCalibratedPresetPath)
	if err != nil {
		t.Fatalf("read %s: %v", modalCalibratedPresetPath, err)
	}
	var knobs resonanceLoopKnobs
	if err := json.Unmarshal(raw, &knobs); err != nil {
		t.Fatalf("decode %s: %v", modalCalibratedPresetPath, err)
	}

	p := NewDefaultParams()
	set := func(dst *float32, src *float32) {
		if src != nil {
			*dst = *src
		}
	}
	set(&p.ResonanceGain, knobs.ResonanceGain)
	set(&p.ModalGainExponent, knobs.ModalGainExponent)
	set(&p.ModalExcitation, knobs.ModalExcitation)
	set(&p.ModalUndampedLoss, knobs.ModalUndampedLoss)
	set(&p.ModalDampedLoss, knobs.ModalDampedLoss)
	if knobs.ModalPartials != nil {
		p.ModalPartials = *knobs.ModalPartials
	}
	if knobs.ResonancePerNoteFilter != nil {
		p.ResonancePerNoteFilter = *knobs.ResonancePerNoteFilter
	}
	if knobs.StringModel == nil || *knobs.StringModel != string(StringModelModal) {
		t.Fatalf("%s no longer pins string_model=modal; revisit which cores the probes cover", modalCalibratedPresetPath)
	}
	return p
}

// resonanceProbeParams is one parameter set the loop probes are run against.
type resonanceProbeParams struct {
	name string
	// build returns a fresh params object; the probe then overrides the string
	// model, the coupling mode and the per-note filter flag.
	build func(t *testing.T) *Params
}

func resonanceProbeParamSets() []resonanceProbeParams {
	return []resonanceProbeParams{
		{name: "default", build: func(*testing.T) *Params { return NewDefaultParams() }},
		{name: "modal-calibrated", build: modalCalibratedResonanceParams},
	}
}

// newResonanceProbeBank builds a bank with its resonance engine already
// attached, ready for a probe run.
func newResonanceProbeBank(params *Params, sampleRate int) (*StringBank, *ResonanceEngine) {
	sb := NewStringBank(sampleRate, params)
	res := NewResonanceEngine(sampleRate, params.ResonanceGain, params.ResonancePerNoteFilter)
	// Wire the engine the way Piano does, so the probe runs the production
	// per-sample injection path. Where a probe needs the loop BROKEN it passes a
	// drive slice to processWithBridge rather than skipping the wiring, which is
	// what keeps the two in step.
	sb.SetResonanceEngine(res)
	return sb, res
}

// Probe timing. The drive is measured in windows of resonanceProbeWindow
// seconds; each window yields one gain reading, and the probe stops when the
// mean of the last second has stopped moving against the second before it, or
// when resonanceProbeMaxSeconds of drive have been spent, whichever is first.
//
// resonanceProbeMaxSeconds is a cost ceiling, not a settling time: an undamped
// string's transient runs far past it (see resonanceProbeSettleFraction). It is
// set where it is because 24 s is what it takes for the probe to see the
// known-diverging DWG configuration as above unity, which is the calibration
// TestResonanceProbeSeesKnownDivergingLoop pins.
const (
	resonanceProbeWindow     = 0.25
	resonanceProbeMaxSeconds = 24.0
	// resonanceProbeScanSeconds is the budget for picking the hottest note of a
	// row before the full probe is spent on it. The ranking is stable this
	// early: on the diverging DWG row the aggregate notes come out 33, 36, 28,
	// 21, 48 at 2 s of drive and in the same order at 8 s.
	resonanceProbeScanSeconds = 2.0
	// resonanceProbeSettleTol is the relative movement between two adjacent
	// one-second means below which the reading counts as settled. The slowest
	// growth the diverging DWG row shows anywhere inside the 24 s budget is
	// 1.2% per second, so 0.2% cannot be tripped by the plateaus in a transient
	// that is still climbing.
	resonanceProbeSettleTol = 0.002
	// resonanceProbeSettleRuns is how many consecutive windows must pass the
	// tolerance before the reading is called settled.
	resonanceProbeSettleRuns = 2
)

// resonanceProbeSettleFraction is the fraction of the settled gain that a probe
// run to resonanceProbeMaxSeconds recovers, measured on the hottest known
// configuration.
//
// Measured 2026-08-22 by driving the diverging DWG row (default params,
// ResonanceGain 0.0014, all 88 groups undamped, per-note filter on, note 33) for
// 400 s: the windowed reading is 1.1173 at 24 s and 1.5299 averaged over the
// last 50 s, by which point it has been flat for 300 s. So the 24 s budget
// recovers about 0.73 of the limit, and an unsettled reading of g implies a
// settled gain of roughly g/0.73. The growth is slow and monotone, so this is a
// scale factor on a lower bound, not an error bar. It is one configuration's
// factor, not a law: use it to read an unsettled number, not to certify one.
const resonanceProbeSettleFraction = 0.73

// resonanceProbeReading is what one probe run returns.
type resonanceProbeReading struct {
	// gain is the mean of the last second of windowed gain readings.
	gain float64
	// seconds is how much drive was spent.
	seconds float64
	// settled reports whether the reading stopped moving before the budget ran
	// out. When false, gain is a LOWER BOUND on the steady-state loop gain: the
	// drive was cut off while the resonators were still filling.
	settled bool
}

// String renders a reading for a test log. An unsettled reading is printed with
// the settled gain it stands for, so a log line can never be mistaken for a
// steady state.
func (r resonanceProbeReading) String() string {
	if r.settled {
		return fmt.Sprintf("%.6f (settled after %.2f s of drive)", r.gain, r.seconds)
	}
	return fmt.Sprintf("%.6f (lower bound after %.2f s of drive, not settled; ~%.4f at the %.2f recovery of the reference row)",
		r.gain, r.seconds, r.gain/resonanceProbeSettleFraction, resonanceProbeSettleFraction)
}

// measureResonanceLoopGain drives the sympathetic resonance path with a sine at
// one note's fundamental and reports how hot the open-loop gain has become by
// the time the drive stops.
//
// The engine closes this loop inside StringBank's own per-sample render loop:
// each sample is band-limited and deposited into every undamped group, and the
// group renders the sample after with that energy already in it. The probe
// breaks the loop through the processWithBridge drive seam: instead of the
// bank's own previous sample it injects a synthetic unit sine at the note's
// fundamental, so what comes back out is the bank's response to a known drive
// and nothing else. Injection stays per-sample either way, which is the point —
// a probe that deposited a whole block at once would be measuring a loop the
// renderer no longer runs. The loop as shipped is
// entirely linear - there is no saturation anywhere on it - so a gain below one
// is sufficient for stability at any level. Above one stability is no longer
// guaranteed; whether it actually diverges then depends on the loop phase,
// which is what measureResonanceLoopDecay settles.
//
// aggregate selects what counts as undamped. With aggregate false a single note
// is held, which measures one target in isolation. With aggregate true the
// sustain pedal is fully down, so all 88 groups are injection targets and
// StringBank.Process sums all of their responses back into the same bridge
// signal - the configuration the engine is actually in whenever the pedal is
// held, and the one a per-note probe cannot see.
//
// WHAT THE NUMBER IS. The gain reported is the mean over the LAST second of
// drive, not over the whole run, so it tracks the resonators as they fill
// instead of averaging the empty start into the answer. The run stops early
// once that mean stops moving, and only then is the reading a steady state;
// reading.settled says which case it is. An undamped string's transient runs
// for minutes - the row this probe is calibrated against needs 100 s of drive
// to flatten - so at the budgets the suite can afford most readings come back
// unsettled, and an unsettled reading is a LOWER BOUND. Never read one as "the
// loop gain is this"; read it as "the loop gain is at least this".
func measureResonanceLoopGain(params *Params, note int, sampleRate int, aggregate bool, maxSeconds float64) resonanceProbeReading {
	sb, _ := newResonanceProbeBank(params, sampleRate)
	if aggregate {
		sb.SetSustainAmount(1)
	} else {
		sb.SetKeyDown(note, true)
	}

	f0 := float64(midiNoteToFreq(note))
	windowBlocks := int(resonanceProbeWindow*float64(sampleRate)) / resonanceProbeBlock
	if windowBlocks < 1 {
		windowBlocks = 1
	}
	// One second of readings, the span the settling test and the reported gain
	// are both taken over.
	perSecond := int(math.Round(1.0 / resonanceProbeWindow))
	maxWindows := int(math.Round(maxSeconds / resonanceProbeWindow))
	if maxWindows < 2*perSecond {
		maxWindows = 2 * perSecond
	}

	drive := make([]float32, resonanceProbeBlock)
	phase := 0.0
	step := 2 * math.Pi * f0 / float64(sampleRate)

	mean := func(w []float64) float64 {
		var s float64
		for _, v := range w {
			s += v
		}
		return s / float64(len(w))
	}

	windows := make([]float64, 0, maxWindows)
	runs := 0
	for w := 0; w < maxWindows; w++ {
		var driveSq, outSq float64
		for b := 0; b < windowBlocks; b++ {
			for i := range drive {
				drive[i] = float32(math.Sin(phase))
				phase += step
				if phase > 2*math.Pi {
					phase -= 2 * math.Pi
				}
			}
			out := sb.processWithBridge(resonanceProbeBlock, nil, drive)
			for i := range out {
				d := float64(drive[i])
				o := float64(out[i])
				driveSq += d * d
				outSq += o * o
			}
		}
		if driveSq == 0 {
			return resonanceProbeReading{}
		}
		windows = append(windows, math.Sqrt(outSq/driveSq))

		if len(windows) < 2*perSecond {
			continue
		}
		last := mean(windows[len(windows)-perSecond:])
		prev := mean(windows[len(windows)-2*perSecond : len(windows)-perSecond])
		// A zero previous mean is NOT convergence. Relative tolerance is
		// undefined against zero, and the one way a row reaches it is the one
		// case that must never be called settled: a resonance path that has gone
		// silent reads last == prev == 0 forever, so treating it as within
		// tolerance would report "settled, gain 0" and sail through the < 0.5
		// bound. Every row this file probes is nonzero (the smallest measured is
		// 1.6e-4), so requiring prev != 0 costs nothing on a working loop and
		// spends the full budget only on a broken one.
		if prev != 0 && math.Abs(last-prev)/prev < resonanceProbeSettleTol {
			runs++
		} else {
			runs = 0
		}
		if runs >= resonanceProbeSettleRuns {
			return resonanceProbeReading{
				gain:    last,
				seconds: float64(len(windows)) * resonanceProbeWindow,
				settled: true,
			}
		}
	}
	return resonanceProbeReading{
		gain:    mean(windows[len(windows)-perSecond:]),
		seconds: float64(len(windows)) * resonanceProbeWindow,
	}
}

// hottestResonanceLoopGain probes every note of the set with a short scan,
// keeps the hottest, and then spends the full budget on that one.
//
// Spending the full budget on all of them costs more than the whole package
// suite is worth: the modal core runs the 88-group aggregate probe at about
// real time, so five notes at 24 s each is two minutes for a single row.
func hottestResonanceLoopGain(params *Params, notes []int, sampleRate int, aggregate bool) (resonanceProbeReading, int) {
	worstNote, worst := notes[0], -1.0
	for _, note := range notes {
		scan := measureResonanceLoopGain(params, note, sampleRate, aggregate, resonanceProbeScanSeconds)
		if scan.gain > worst {
			worst, worstNote = scan.gain, note
		}
	}
	return measureResonanceLoopGain(params, worstNote, sampleRate, aggregate, resonanceProbeMaxSeconds), worstNote
}

// measureResonanceLoopDecay runs the resonance loop closed, with the sustain
// pedal held and no excitation other than one seed block, and returns the RMS
// of the first and the last second of the run.
//
// This is the measurement the open-loop gain cannot make. A gain below one at
// every probed frequency is sufficient for stability, but a gain above one only
// means the sufficient condition no longer holds - the loop phase decides. Here
// the loop runs closed inside StringBank itself — sample i is injected from the
// bank's own sample i-1 — so the ratio of the two windows is the loop's actual
// behaviour with every undamped group summing into the same bridge signal.
//
// It overlaps TestModalResonanceEnergyStaysBounded, deliberately: that test runs
// the same loop through Piano.Process, with the body and room convolvers in the
// path. This one isolates the string loop from the IR stages, so a divergence
// here cannot be blamed on a convolver.
func measureResonanceLoopDecay(t *testing.T, params *Params, sampleRate int, seconds float64) (first, last float64) {
	t.Helper()

	sb, _ := newResonanceProbeBank(params, sampleRate)
	sb.SetSustainAmount(1)

	// A deterministic broadband seed block, so every group in the bank is
	// energized regardless of where its modes sit. It is injected ONCE, through
	// the drive seam; after that the bank feeds itself.
	seedDrive := make([]float32, resonanceProbeBlock)
	seed := uint32(1)
	for i := range seedDrive {
		seed = seed*1664525 + 1013904223
		seedDrive[i] = float32(int32(seed>>9)%2001-1000) / 1000
	}

	blocksPerSecond := sampleRate / resonanceProbeBlock
	totalBlocks := int(seconds * float64(blocksPerSecond))
	var firstSq, lastSq float64
	var firstN, lastN int
	for b := 0; b < totalBlocks; b++ {
		var out []float32
		if b == 0 {
			out = sb.processWithBridge(resonanceProbeBlock, nil, seedDrive)
		} else {
			out = sb.Process(resonanceProbeBlock, nil)
		}
		for i := range out {
			v := float64(out[i])
			if math.IsNaN(v) || math.IsInf(v, 0) {
				t.Fatalf("non-finite sample in closed-loop block %d", b)
			}
			switch {
			case b < blocksPerSecond:
				firstSq += v * v
				firstN++
			case b >= totalBlocks-blocksPerSecond:
				lastSq += v * v
				lastN++
			}
		}
	}
	if firstN == 0 || lastN == 0 {
		t.Fatalf("closed-loop probe collected no samples")
	}
	return math.Sqrt(firstSq / float64(firstN)), math.Sqrt(lastSq / float64(lastN))
}

// maxResonanceLoopGain is the bound both probes assert.
//
// The loop is linear, so a settled gain >= 1 forfeits the small-gain stability
// guarantee. The probes report a lower bound on that settled gain, and
// resonanceProbeSettleFraction measures how much of it the 24 s budget
// recovers: about 0.73. A reading of 0.5 therefore stands for a settled gain of
// roughly 0.68, which is still under unity, so the bound survives being read
// through the shortfall. It also leaves the hottest configuration the probes
// cover (0.2153, the modal-calibrated knobs on the DWG core, aggregate) a factor
// of 2.3 of headroom.
//
// The unity line is not merely theoretical. Sweeping ResonanceGain on the DWG
// core - the loop is linear in it, so the settled probe scales with it exactly -
// puts the probe's unity crossing at ResonanceGain 0.00092, and the long
// renders in PLAN.md put the divergence between 0.0007 (creeps up) and 0.0014
// (peak 91.5 and climbing). The probe's unity crossing and the renderer's
// divergence are the same event.
//
// READ THE READINGS AS BOUNDS. Where a probe reports "not settled" its number
// understates the loop: the drive stopped while the resonators were still
// filling. Passing the bound with an unsettled reading is evidence, not proof;
// TestDWGResonanceLongRenderDecays is what actually pins the loop, and
// TestResonanceProbeSeesKnownDivergingLoop is what keeps these probes calibrated
// against a configuration known to diverge.
const maxResonanceLoopGain = 0.5

// resonanceProbeNotes spans the register where the loop is hottest. Notes 33 and
// 36 are both in the set because the DWG per-note peak sits on one or the other
// - not at the bottom of the keyboard - depending on how long the probe drives.
// The set deliberately keeps reaching up to 84: since the loop was interleaved
// the modal core's hottest per-note reading is up there rather than in the bass,
// so a bass-only set would no longer measure the maximum.
var resonanceProbeNotes = []int{21, 33, 36, 48, 60, 72, 84}

// TestResonanceLoopGainIsBoundedAcrossCores pins the per-note open-loop gain of
// the sympathetic resonance path below unity, with margin.
//
// This is the guard against a preset silently reopening the divergence fixed in
// modal_group.go: ModalExcitation, ModalUndampedLoss, ModalPartials and
// ResonanceGain all multiply this loop, and nothing else in the suite would
// notice them pushing it back through one until a render turned into NaN. The
// defaults are probed on both cores; assets/presets/modal-calibrated.json is
// probed on the modal core it pins in string_model, with its own resonance
// gain, 20 partials, excitation 4 and undamped loss 0.4.
//
// Re-measured on 2026-08-22 after the loop was INTERLEAVED with rendering. The
// "block deposit" column is the same windowed probe run against the old
// topology, where a whole block of drive was deposited into frozen string state
// before any of it was rendered; the difference between the columns is what
// spreading the drive across the block does to the loop.
//
//	params             core    block deposit  note  interleaved  note  settled?
//	default            modal   0.000161       36    0.000308     84    yes, 2.50 s
//	default            dwg     0.165995       36    0.174449     36    no
//	modal-calibrated   modal   0.002396       48    0.004234     60    yes, 3.00 s
//
// Both modal rows settle inside the budget, so those two numbers are the loop
// gain and not a bound on it. The DWG row does not settle: 0.174 is a lower
// bound, standing for a settled gain near 0.24.
//
// THE HOT REGISTER MOVED UP, which is the signature the interleave predicted.
// Depositing a block at once delivered |sum x[i]| - the block's DC content - so
// the old loop was hottest wherever the drive was slowest, i.e. in the bass.
// Spreading it delivers the drive AT each partial instead. On the modal core the
// per-note ranking now rises monotonically with pitch (note 21: 0.000068, 36:
// 0.000169, 60: 0.000276, 84: 0.000308) where it used to peak at note 36. The
// DWG core still peaks at note 36 - that peak is its own delay-line resonance,
// not an artefact of the deposit, and it barely moved (0.166 -> 0.174).
func TestResonanceLoopGainIsBoundedAcrossCores(t *testing.T) {
	const sampleRate = 48000

	for _, set := range resonanceProbeParamSets() {
		models := []StringModel{StringModelModal, StringModelDWG}
		if set.name != "default" {
			// The tracked preset pins string_model=modal; its resonance gain is
			// not a configuration the DWG core is ever loaded with.
			models = []StringModel{StringModelModal}
		}
		for _, model := range models {
			for _, note := range resonanceProbeNotes {
				t.Run(set.name+"/"+string(model)+"/note"+strconv.Itoa(note), func(t *testing.T) {
					// Safe to parallelise: every subtest builds its own bank and
					// engine and touches no package state. It matters here
					// because the probe now drives for seconds, not
					// milliseconds.
					t.Parallel()

					params := set.build(t)
					params.StringModel = model
					params.ResonanceEnabled = true
					params.CouplingEnabled = false
					params.CouplingMode = CouplingModeOff

					r := measureResonanceLoopGain(params, note, sampleRate, false, resonanceProbeMaxSeconds)
					t.Logf("%s %s note %d: per-note open-loop resonance gain %s", set.name, model, note, r)
					if r.seconds == 0 {
						t.Fatalf("%s %s note %d: probe collected no drive energy", set.name, model, note)
					}
					if math.IsNaN(r.gain) || math.IsInf(r.gain, 0) {
						t.Fatalf("%s %s note %d: non-finite loop gain %v", set.name, model, note, r.gain)
					}
					if r.gain > maxResonanceLoopGain {
						t.Fatalf("%s %s note %d: per-note open-loop resonance gain %.4f exceeds %.2f", set.name, model, note, r.gain, maxResonanceLoopGain)
					}
				})
			}
		}
	}
}

// TestAggregateResonanceLoopIsBounded is the full-bank counterpart of the
// per-note probe.
//
// The per-note probe holds one key, so it measures one target at a time. The
// engine never runs that way with the pedal down: the render loop drives every
// undamped group and StringBank.Process sums all of their responses back into
// the same bridge signal, so the loop a held pedal closes is the sum over all
// 88 groups, not the maximum over them. This row therefore sustains the whole
// bank and measures the loop that actually exists.
//
// Both drive shapes are covered. With ResonancePerNoteFilter true each group
// filters the bridge signal to its own band first, which keeps the groups
// largely independent; with it false every group receives the same broadband
// drive and the responses genuinely add. The measurement is taken both ways.
//
// The open-loop gain is only sufficient, not necessary, for stability, so each
// row also runs the loop closed and requires it to decay.
//
// Re-measured on 2026-08-22 after the loop was INTERLEAVED with rendering
// (48 kHz, all 88 groups undamped, hottest probe note, 24 s of drive). The
// "block deposit" column is the same probe against the old topology:
//
//	params             core    perNoteFilter  block deposit  interleaved  note  settled?     closed loop
//	default            modal   true           0.000190       0.000188     21    no           decays
//	default            modal   false          0.000617       0.000173     36    yes, 2.75 s  decays
//	modal-calibrated   modal   true           0.001888       0.002279     48    yes, 4.50 s  decays
//	modal-calibrated   modal   false          0.005993       0.002362     48    yes, 3.75 s  decays
//	default            dwg     true           0.143659       0.173739     36    no           decays
//	default            dwg     false          0.155013       0.043302     21    no           decays
//	modal-calibrated   dwg     true           0.199527       0.241305     36    no           decays
//	modal-calibrated   dwg     false          0.215297       0.060142     21    no           decays
//
// The four DWG rows are lower bounds: 24 s of drive does not settle them.
// Divided by resonanceProbeSettleFraction the hottest of them (0.2413) stands
// for a settled gain near 0.33, against the bound of 0.5.
//
// The perNoteFilter=FALSE rows fell by 3.6x to 4.2x, the true rows rose by up to
// 1.2x, and that split is the interleave's signature. With the filter off every
// group receives the same broadband drive, so the old block deposit's DC-ward
// gain was applied to all 88 responses at once and they added coherently;
// spreading the drive removes that. With the filter on each group only ever sees
// its own narrow band, so there was little coherent addition to remove, and what
// is left is the modest rise from driving the partials properly. The bound stays
// at 0.5: the hottest configuration covered here is 0.2413, a factor of 2.1.
//
// The DWG rows were skipped against a known defect until 2026-08-22: with the
// unnormalised resonator bank the summed loop was above unity, and through the
// public Piano.Process a 40 s render grew from a peak of 3.2 at 1 s to 1.2e7.
// The 0.5 s-warmup figures for that bank were 1.4276 (default) and 1.9828
// (modal-calibrated), against 0.0237 and 0.0329 after the normalisation - a 60x
// drop with no change to ResonanceGain. So the AGGREGATION across undamped
// targets does not need a normalisation of its own: what made the sum exceed
// unity was the 1/f0 tilt of the per-note filters, not the summing. The
// perNoteFilter=false rows barely move under either probe, because that path
// never goes through the resonator bank - which is exactly why it was the one
// configuration the defect spared.
//
// The modal rows also confirm PR #23's conclusion still holds: the modal
// aggregate is bounded by three orders of magnitude, and it settles, so that is
// a measurement and not a bound.
func TestAggregateResonanceLoopIsBounded(t *testing.T) {
	const sampleRate = 48000
	const closedLoopSeconds = 3.0

	// The aggregate loop is hottest in the bass; above note 48 every measured
	// aggregate gain is an order of magnitude down.
	aggregateNotes := []int{21, 28, 33, 36, 48}

	for _, set := range resonanceProbeParamSets() {
		for _, model := range []StringModel{StringModelModal, StringModelDWG} {
			for _, perNoteFilter := range []bool{true, false} {
				name := set.name + "/" + string(model) + "/perNoteFilter=" + strconv.FormatBool(perNoteFilter)
				t.Run(name, func(t *testing.T) {
					// Safe to parallelise, and needed: the modal aggregate probe
					// runs at roughly real time, so the eight rows cost about
					// two minutes of CPU between them.
					t.Parallel()

					params := set.build(t)
					params.StringModel = model
					params.ResonanceEnabled = true
					params.CouplingEnabled = false
					params.CouplingMode = CouplingModeOff
					params.ResonancePerNoteFilter = perNoteFilter

					r, worstNote := hottestResonanceLoopGain(params, aggregateNotes, sampleRate, true)
					t.Logf("%s: aggregate open-loop gain %s (hottest of notes %v, at note %d)", name, r, aggregateNotes, worstNote)
					if r.seconds == 0 {
						t.Fatalf("%s: probe collected no drive energy", name)
					}
					if math.IsNaN(r.gain) || math.IsInf(r.gain, 0) {
						t.Fatalf("%s: non-finite aggregate loop gain %v", name, r.gain)
					}
					if r.gain > maxResonanceLoopGain {
						t.Fatalf("%s: aggregate open-loop resonance gain %.4f at note %d exceeds %.2f; every undamped group sums into the same bridge signal", name, r.gain, worstNote, maxResonanceLoopGain)
					}

					first, last := measureResonanceLoopDecay(t, params, sampleRate, closedLoopSeconds)
					t.Logf("%s: closed loop RMS %.6g in the first second, %.6g in the last", name, first, last)
					if first == 0 {
						t.Fatalf("%s: closed-loop probe produced no signal to decay from", name)
					}
					if last >= first {
						t.Fatalf("%s: closed aggregate resonance loop is not decaying: last second %.6g >= first second %.6g", name, last, first)
					}
				})
			}
		}
	}
}

// divergingResonanceGain is a ResonanceGain the DWG core is known to diverge at.
//
// Re-derived twice: 2026-08-22 after the loop was interleaved with rendering,
// and 2026-08-23 after the unison bridge coupling was made dissipative. The
// render is 120 s through Piano.Process, DWG, pedal held, six notes struck once,
// coupling off, reported as the peak of the 115-120 s window over the peak of
// the 25-30 s window:
//
//	gain      block deposit  interleaved  interleaved + coupling fixed
//	0 (off)   1.21           1.21         0.1270
//	0.00018   1.13           3.35         0.1354
//	0.00025   1.14           5.06         0.1331
//	0.0007    1.40           79.8         0.2996
//	0.00092   2.28           322          0.7847
//	0.0014    20.8           8.4e3        14.02
//	0.002     938            1.3e6        702.6
//	0.005     -              -            1.3e12
//
// Read the third column against the first two. On the interleaved loop alone
// every gain in the table grew and the unity crossing was below the smallest
// gain worth using, which was taken at the time as the corrected loop being
// unstable. It was not: the plant underneath it was. With the unison coupling
// no longer feeding each string its own output back, the loop has a real
// ceiling again, and the crossing lands at roughly 0.00092 - which is exactly
// where the pre-interleave notes put it, for what turns out to be the right
// reason. The shipped resonance_gain of 0.00025 now sits 3.7x under it.
//
// 0.0014 is kept because the probe's calibration only needs a gain the renderer
// definitely diverges at, and it still diverges there by 14x over 120 s.
const divergingResonanceGain = 0.0014

// TestResonanceProbeSeesKnownDivergingLoop calibrates the open-loop probe
// against a configuration that is known to diverge.
//
// This is the test the bounded-loop probes are worth nothing without. A probe
// that reports a small number is only evidence if the same probe reports a large
// one when the loop really is unstable, and the old 0.5 s-warmup probe did not:
// it reported 0.1968 for this configuration, under half the bound it asserts,
// while the renderer was running away. The windowed probe reads 1.1033 here on
// the interleaved loop (24 s of drive, still a lower bound), so it does cross.
//
// KNOWN LIMIT OF THIS CALIBRATION. It shows the probe can see A diverging loop.
// It does not show the probe can see EVERY diverging loop, and there is a
// measured instance of it missing one: between the resonance interleave and the
// unison-coupling fix the same renderer ran away at 0.00025, where this probe
// read 0.174 against a bound of 0.5. The probe drives a sine at one note's
// fundamental into a plant it assumes is stable, and that second assumption is
// the one that failed - the plant itself was above unity, which no open-loop
// measurement of the resonance path can see. Treat a passing bound here as
// necessary, never as sufficient; TestDWGResonanceSustainedDecayIsFenced is the
// end-to-end check that would have caught it.
func TestResonanceProbeSeesKnownDivergingLoop(t *testing.T) {
	const sampleRate = 48000
	// Note 33 is where the DWG aggregate loop peaks.
	const note = 33

	params := NewDefaultParams()
	params.StringModel = StringModelDWG
	params.ResonanceEnabled = true
	params.CouplingEnabled = false
	params.CouplingMode = CouplingModeOff
	params.ResonancePerNoteFilter = true
	params.ResonanceGain = divergingResonanceGain

	r := measureResonanceLoopGain(params, note, sampleRate, true, resonanceProbeMaxSeconds)
	t.Logf("dwg resonance_gain %.4f, all groups undamped, note %d: %s", divergingResonanceGain, note, r)
	// Screen the reading before comparing it. NaN fails every ordered
	// comparison, so `r.gain < 1.0` is false for NaN and this calibration would
	// pass on a reading that measured nothing at all - the exact inversion of
	// what the test is for. A zero-length reading (driveSq == 0) means the probe
	// never ran and must not be read as a crossing either.
	if math.IsNaN(r.gain) || math.IsInf(r.gain, 0) {
		t.Fatalf("non-finite loop gain %v on the diverging calibration row; the probe measured nothing usable", r.gain)
	}
	if r.seconds <= 0 {
		t.Fatalf("the probe returned an empty reading (%.2f s of drive) on the diverging calibration row", r.seconds)
	}
	if r.gain < 1.0 {
		t.Fatalf("the probe reports %.4f for a configuration that diverges in a 120 s render; a probe that cannot see this one cannot certify the others", r.gain)
	}
}

// TestModalResonanceEnergyStaysBounded renders the modal core with the
// resonance engine live, the sustain pedal held (so all 88 groups are injection
// targets) and requires the render to be decaying by the end.
//
// Finiteness alone is not enough: the divergence this replaces grew by roughly
// 1.9x per block and still had two seconds of headroom in float32 before it
// reached NaN. Comparing the tail against an early window catches that while
// the numbers are still ordinary.
func TestModalResonanceEnergyStaysBounded(t *testing.T) {
	const sampleRate = 48000
	const seconds = 6.0

	cfg := stabilityRenderConfig{
		name:       "modal-resonance-bounded",
		model:      StringModelModal,
		coupling:   CouplingModePhysical,
		resonance:  true,
		sampleRate: sampleRate,
		seconds:    seconds,
		notes:      stabilityNotes,
		pedals:     pedalScript{name: "held", sustainAmount: 1, keyUpAt: -1, sustainOffAt: -1, softPedalAt: -1},
	}

	blocksPerSecond := sampleRate / stabilityBlockSize
	totalBlocks := int(seconds*float64(sampleRate)) / stabilityBlockSize

	var earlyPeak, latePeak float64
	renderStabilityBlocks(t, cfg, func(block int, out []float32) {
		peak := 0.0
		for _, s := range out {
			v := math.Abs(float64(s))
			if math.IsNaN(v) || math.IsInf(v, 0) {
				t.Fatalf("non-finite sample in block %d", block)
			}
			if v > peak {
				peak = v
			}
		}
		switch {
		case block >= blocksPerSecond && block < 2*blocksPerSecond:
			if peak > earlyPeak {
				earlyPeak = peak
			}
		case block >= totalBlocks-blocksPerSecond:
			if peak > latePeak {
				latePeak = peak
			}
		}
	})

	t.Logf("peak in second 2: %.6g, peak in final second: %.6g", earlyPeak, latePeak)
	if earlyPeak == 0 {
		t.Fatalf("no signal in the second second of the render")
	}
	if latePeak >= earlyPeak {
		t.Fatalf("render is not decaying: peak in the final second %.6g >= peak in the second second %.6g", latePeak, earlyPeak)
	}
}
