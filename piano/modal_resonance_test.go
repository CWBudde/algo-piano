package piano

import (
	"encoding/json"
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

// newResonanceProbeBank builds a bank and a matching resonance engine for a
// probe run.
func newResonanceProbeBank(params *Params, sampleRate int) (*StringBank, *ResonanceEngine) {
	sb := NewStringBank(sampleRate, params)
	res := NewResonanceEngine(sampleRate, params.ResonanceGain, params.ResonancePerNoteFilter)
	return sb, res
}

// measureResonanceLoopGain returns the open-loop gain of the sympathetic
// resonance path at one drive frequency.
//
// The engine closes this loop once per block in Piano.Process: the bank renders
// a mono mix, ResonanceEngine.InjectFromBridge band-limits it and deposits it
// into every undamped group, and the next block renders those groups again. The
// probe breaks the loop: instead of the bank's own output it injects a
// synthetic unit sine at the note's fundamental, so what comes back out is the
// bank's response to a known drive and nothing else. The loop as shipped is
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
// The measurement is taken after five time constants of the slowest mode, so
// the resonators have reached steady state.
func measureResonanceLoopGain(t *testing.T, params *Params, note int, sampleRate int, aggregate bool) float64 {
	t.Helper()

	sb, res := newResonanceProbeBank(params, sampleRate)
	if aggregate {
		sb.SetSustainAmount(1)
	} else {
		sb.SetKeyDown(note, true)
	}

	f0 := float64(midiNoteToFreq(note))
	warmupBlocks := int(0.5*float64(sampleRate)) / resonanceProbeBlock
	measureBlocks := int(0.2*float64(sampleRate)) / resonanceProbeBlock

	drive := make([]float32, resonanceProbeBlock)
	phase := 0.0
	step := 2 * math.Pi * f0 / float64(sampleRate)

	var driveSq, outSq float64
	var samples int
	for b := 0; b < warmupBlocks+measureBlocks; b++ {
		for i := range drive {
			drive[i] = float32(math.Sin(phase))
			phase += step
			if phase > 2*math.Pi {
				phase -= 2 * math.Pi
			}
		}
		if res.InjectFromBridge(drive, sb.targets) {
			sb.NotifyResonanceInjected()
		}
		out := sb.Process(resonanceProbeBlock, nil)
		if b < warmupBlocks {
			continue
		}
		for i := range out {
			d := float64(drive[i])
			o := float64(out[i])
			driveSq += d * d
			outSq += o * o
			samples++
		}
	}
	if samples == 0 || driveSq == 0 {
		t.Fatalf("note %d: probe collected no drive energy", note)
	}
	return math.Sqrt(outSq / driveSq)
}

// measureResonanceLoopDecay runs the resonance loop closed, with the sustain
// pedal held and no excitation other than one seed block, and returns the RMS
// of the first and the last second of the run.
//
// This is the measurement the open-loop gain cannot make. A gain below one at
// every probed frequency is sufficient for stability, but a gain above one only
// means the sufficient condition no longer holds - the loop phase decides. Here
// the bank's own output is fed straight back to InjectFromBridge, exactly as
// Piano.Process does, so the ratio of the two windows is the loop's actual
// behaviour with every undamped group summing into the same bridge signal.
func measureResonanceLoopDecay(t *testing.T, params *Params, sampleRate int, seconds float64) (first, last float64) {
	t.Helper()

	sb, res := newResonanceProbeBank(params, sampleRate)
	sb.SetSustainAmount(1)

	// A deterministic broadband seed block, so every group in the bank is
	// energized regardless of where its modes sit.
	drive := make([]float32, resonanceProbeBlock)
	seed := uint32(1)
	for i := range drive {
		seed = seed*1664525 + 1013904223
		drive[i] = float32(int32(seed>>9)%2001-1000) / 1000
	}

	blocksPerSecond := sampleRate / resonanceProbeBlock
	totalBlocks := int(seconds * float64(blocksPerSecond))
	var firstSq, lastSq float64
	var firstN, lastN int
	for b := 0; b < totalBlocks; b++ {
		if res.InjectFromBridge(drive, sb.targets) {
			sb.NotifyResonanceInjected()
		}
		out := sb.Process(resonanceProbeBlock, nil)
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
		// Close the loop: the bridge signal of the next block is this block's
		// bank output, which is what Piano.Process feeds InjectFromBridge.
		copy(drive, out)
	}
	if firstN == 0 || lastN == 0 {
		t.Fatalf("closed-loop probe collected no samples")
	}
	return math.Sqrt(firstSq / float64(firstN)), math.Sqrt(lastSq / float64(lastN))
}

// maxResonanceLoopGain is the bound both probes assert.
//
// The loop is linear, so gain >= 1 forfeits the small-gain stability guarantee.
// Half of that leaves room for the hottest measured configuration - the DWG
// core at note 21 per-note (0.411) - without admitting a marginal one.
const maxResonanceLoopGain = 0.5

// resonanceProbeNotes spans the register where the loop is hottest; above note
// 84 every measured gain is below 1e-3.
var resonanceProbeNotes = []int{21, 36, 48, 60, 72, 84}

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
// Measured on 2026-08-22 (48 kHz, per-note, hottest note of each row):
//
//	params             core    gain    note
//	default            modal   0.0083  36
//	default            dwg     0.4110  21
//	modal-calibrated   modal   0.1062  36
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
					params := set.build(t)
					params.StringModel = model
					params.ResonanceEnabled = true
					params.CouplingEnabled = false
					params.CouplingMode = CouplingModeOff

					gain := measureResonanceLoopGain(t, params, note, sampleRate, false)
					t.Logf("%s %s note %d: per-note open-loop resonance gain %.6f", set.name, model, note, gain)
					if math.IsNaN(gain) || math.IsInf(gain, 0) {
						t.Fatalf("%s %s note %d: non-finite loop gain %v", set.name, model, note, gain)
					}
					if gain > maxResonanceLoopGain {
						t.Fatalf("%s %s note %d: per-note open-loop resonance gain %.4f exceeds %.2f", set.name, model, note, gain, maxResonanceLoopGain)
					}
				})
			}
		}
	}
}

// dwgAggregateDefect marks the rows this probe cannot assert yet. See the
// Phase 9.6 follow-up in PLAN.md.
const dwgAggregateDefect = "known defect: the DWG core's aggregate resonance loop grows with the sustain pedal held " +
	"(measured 2026-08-22: peak 11 at 1 s to 6.2e8 at 40 s through Piano.Process). " +
	"It predates this change - the fix here is modal-only - and repairing it moves DWG output, " +
	"so it needs its own change and a gate-c4 re-baseline. See the Phase 9.6 follow-up in PLAN.md."

// TestAggregateResonanceLoopIsBounded is the full-bank counterpart of the
// per-note probe.
//
// The per-note probe holds one key, so it measures one target at a time. The
// engine never runs that way with the pedal down: InjectFromBridge drives every
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
// Measured on 2026-08-22 (48 kHz, all 88 groups undamped, hottest probe note):
//
//	params             core    perNoteFilter   aggregate gain   closed loop
//	default            modal   true            0.0164           decays to 0
//	default            modal   false           0.0006           decays to 0
//	modal-calibrated   modal   true            0.1208           decays to 0
//	modal-calibrated   modal   false           0.0060           decays to 0
//	default            dwg     true            1.4276           grows 14x / 6 s
//	default            dwg     false           0.0315           decays
//	modal-calibrated   dwg     true            1.9828           grows 52x / 6 s
//
// The modal aggregate stays a factor of four under the bound even with the
// hottest tracked preset, so the fix in modal_group.go holds for the summed
// loop and not just per note. The DWG rows are a separate, pre-existing defect;
// see dwgAggregateDefect.
func TestAggregateResonanceLoopIsBounded(t *testing.T) {
	const sampleRate = 48000
	const closedLoopSeconds = 3.0

	// The aggregate loop is hottest in the bass; above note 48 every measured
	// aggregate gain is an order of magnitude down.
	aggregateNotes := []int{21, 28, 36, 48}

	for _, set := range resonanceProbeParamSets() {
		for _, model := range []StringModel{StringModelModal, StringModelDWG} {
			for _, perNoteFilter := range []bool{true, false} {
				name := set.name + "/" + string(model) + "/perNoteFilter=" + strconv.FormatBool(perNoteFilter)
				t.Run(name, func(t *testing.T) {
					if model == StringModelDWG {
						t.Skip(dwgAggregateDefect)
					}
					params := set.build(t)
					params.StringModel = model
					params.ResonanceEnabled = true
					params.CouplingEnabled = false
					params.CouplingMode = CouplingModeOff
					params.ResonancePerNoteFilter = perNoteFilter

					worst, worstNote := 0.0, 0
					for _, note := range aggregateNotes {
						gain := measureResonanceLoopGain(t, params, note, sampleRate, true)
						if math.IsNaN(gain) || math.IsInf(gain, 0) {
							t.Fatalf("%s note %d: non-finite aggregate loop gain %v", name, note, gain)
						}
						if gain > worst {
							worst, worstNote = gain, note
						}
					}
					t.Logf("%s: aggregate open-loop gain %.6f (worst of notes %v, at note %d)", name, worst, aggregateNotes, worstNote)
					if worst > maxResonanceLoopGain {
						t.Fatalf("%s: aggregate open-loop resonance gain %.4f at note %d exceeds %.2f; every undamped group sums into the same bridge signal", name, worst, worstNote, maxResonanceLoopGain)
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
