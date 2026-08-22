package piano

import (
	"math"
	"strconv"
	"testing"
)

// resonanceProbeBlock is the block size the loop-gain probe drives the bank
// with, matching the AudioWorklet render quantum used everywhere else.
const resonanceProbeBlock = 128

// measureResonanceLoopGain returns the open-loop gain of the sympathetic
// resonance path for one note on one string core.
//
// The engine closes this loop once per block in Piano.Process: the bank renders
// a mono mix, ResonanceEngine.InjectFromBridge band-limits it and deposits it
// into every undamped group, and the next block renders those groups again. The
// probe breaks the loop: instead of the bank's own output it injects a
// synthetic unit sine at the note's fundamental, so what comes back out is the
// bank's response to a known drive and nothing else. The loop as shipped is
// entirely linear - there is no saturation anywhere on it - so the closed loop
// is stable exactly when this ratio stays below one, independent of level.
//
// The measurement is taken after five time constants of the slowest mode, so
// the resonators have reached steady state.
func measureResonanceLoopGain(t *testing.T, model StringModel, note int, sampleRate int) float64 {
	t.Helper()

	params := NewDefaultParams()
	params.StringModel = model
	params.ResonanceEnabled = true
	params.CouplingEnabled = false
	params.CouplingMode = CouplingModeOff

	sb := NewStringBank(sampleRate, params)
	res := NewResonanceEngine(sampleRate, params.ResonanceGain, params.ResonancePerNoteFilter)
	sb.SetKeyDown(note, true)

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
		t.Fatalf("%s note %d: probe collected no drive energy", model, note)
	}
	return math.Sqrt(outSq / driveSq)
}

// TestResonanceLoopGainIsBoundedAcrossCores pins the open-loop gain of the
// sympathetic resonance path below unity, with margin, on both string cores.
//
// This is the guard against a preset silently reopening the divergence fixed in
// modal_group.go: ModalExcitation, ModalUndampedLoss and ResonanceGain all
// multiply this loop, and nothing else in the suite would notice them pushing
// it back through one until a render turned into NaN.
func TestResonanceLoopGainIsBoundedAcrossCores(t *testing.T) {
	const sampleRate = 48000
	// The loop is linear, so gain >= 1 means unconditional divergence. Half of
	// that leaves room for the two hottest configurations measured on
	// 2026-08-22 - the DWG core at note 21 (0.411) and the hottest tracked
	// preset, assets/presets/modal-calibrated.json, whose modal_excitation 4,
	// modal_undamped_loss 0.4 and resonance_gain 0.00025 put a combined ~14x on
	// this loop and peak at 0.303 at note 48 - without admitting a marginal
	// configuration.
	const maxGain = 0.5

	notes := []int{21, 36, 48, 60, 72, 84}
	for _, model := range []StringModel{StringModelModal, StringModelDWG} {
		for _, note := range notes {
			t.Run(string(model)+"/note"+strconv.Itoa(note), func(t *testing.T) {
				gain := measureResonanceLoopGain(t, model, note, sampleRate)
				t.Logf("%s note %d: open-loop resonance gain %.6f", model, note, gain)
				if math.IsNaN(gain) || math.IsInf(gain, 0) {
					t.Fatalf("%s note %d: non-finite loop gain %v", model, note, gain)
				}
				if gain > maxGain {
					t.Fatalf("%s note %d: open-loop resonance gain %.4f exceeds %.2f; the closed loop in Piano.Process diverges at gain >= 1", model, note, gain, maxGain)
				}
			})
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
