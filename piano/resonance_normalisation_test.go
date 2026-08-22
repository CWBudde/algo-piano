package piano

import (
	"math"
	"strconv"
	"testing"
)

// TestNoteResonatorHasUnityPeakGain pins the normalisation of the two-pole
// resonator that filterResonanceDrive is built from, in both string cores.
//
// The resonator used to be built with b0 = 1-r, which is not
// unity-peak-normalised: its peak magnitude is
// b0 / ((1-r)*sqrt(1 - 2r*cos(2*w0) + r^2)), i.e. roughly 1/(2*sin(w0)). That
// is a 1/f0 law, and summed over the three partials of filterResonanceDrive it
// reached 183x at A0 against 2.6x at C7. The bass emphasis pushed the aggregate
// sympathetic loop past unity with the sustain pedal held and made the DWG core
// diverge; see the Phase 9.6 notes in PLAN.md. Nothing else in the suite
// measures this filter directly, so without this test the normalisation could
// silently regress and only surface as a runaway forty seconds into a render.
//
// The peak is checked twice, because the two checks fail for different reasons:
// analytically from the coefficients, which catches a wrong b0, and against a
// driven sine at the centre frequency, which catches the analysis and the
// implementation drifting apart.
func TestNoteResonatorHasUnityPeakGain(t *testing.T) {
	for _, sampleRate := range []int{44100, 48000, 96000} {
		for _, note := range []int{21, 33, 48, 60, 72, 84, 96, 108} {
			f0 := float64(midiNoteToFreq(note))
			for _, bandwidthHz := range []float32{35, 55, 80} {
				name := strconv.Itoa(sampleRate) + "/note" + strconv.Itoa(note) +
					"/bw" + strconv.FormatFloat(float64(bandwidthHz), 'f', 0, 64)
				t.Run(name, func(t *testing.T) {
					// gain 1 throughout: the per-partial weights of
					// filterResonanceDrive multiply on top of the normalised
					// response and are not what this test is about.
					res := newNoteResonator(sampleRate, float32(f0), bandwidthHz, 1)

					// 5e-3 rather than machine precision: the coefficients are
					// float32 and the denominator at w0 is a near-cancellation
					// (a1 is within 1e-5 of 2 at note 21 / 96 kHz), so the last
					// few digits are quantisation, not normalisation error.
					// That is a property of the direct-form resonator itself
					// and is unchanged by the normalisation.
					analytic := noteResonatorMagnitude(res, f0, float64(sampleRate))
					if math.Abs(analytic-1) > 5e-3 {
						t.Fatalf("analytic peak magnitude %.6f, want 1 within 5e-3", analytic)
					}

					// Looser: the driven check runs in float32 and reads a peak
					// off a sampled sine, so it cannot resolve the analytic
					// tolerance.
					measured := driveNoteResonatorAtPeak(res, f0, float64(sampleRate))
					if math.Abs(measured-1) > 2e-2 {
						t.Fatalf("measured peak magnitude %.6f, want 1 within 0.02", measured)
					}
				})
			}
		}
	}
}

// TestResonanceDriveBankGainIsRegisterIndependent is the bank-level statement
// of the same property: what filterResonanceDrive multiplies a bridge signal by
// at a group's own partials must not depend on which group it is.
//
// This is the property the divergence violated. Before the normalisation the
// summed peak ran 183.1 at note 21, 20.1 at note 60 and 2.6 at note 96; it is
// now the flat sum of the three partial weights, in both cores.
func TestResonanceDriveBankGainIsRegisterIndependent(t *testing.T) {
	const sampleRate = 48000
	// 1.0 + 0.55 + 0.30, the per-partial weights initResonanceFilters uses.
	const wantSummedPeak = 1.85

	for _, model := range []StringModel{StringModelDWG, StringModelModal} {
		t.Run(string(model), func(t *testing.T) {
			params := NewDefaultParams()
			params.StringModel = model
			sb := NewStringBank(sampleRate, params)

			for _, note := range []int{21, 36, 48, 60, 72, 84, 96} {
				filters := resonanceFiltersOf(t, sb, note)
				if len(filters) != 3 {
					t.Fatalf("note %d: %d resonance filters, want 3", note, len(filters))
				}
				f0 := float64(midiNoteToFreq(note))
				sum := 0.0
				for i, f := range filters {
					center := f0 * float64(i+1)
					sum += noteResonatorMagnitude(f, center, sampleRate) * float64(f.gain)
				}
				t.Logf("note %2d f0 %8.2f Hz: summed peak %.4f", note, f0, sum)
				if math.Abs(sum-wantSummedPeak) > 1e-2 {
					t.Fatalf("note %d: summed resonator peak %.4f, want %.2f within 0.01; the drive bank is register-dependent again", note, sum, wantSummedPeak)
				}
			}
		})
	}
}

// resonanceFiltersOf returns the resonance filter bank of one group, whichever
// core the bank was built with.
func resonanceFiltersOf(t *testing.T, sb *StringBank, note int) []noteResonator {
	t.Helper()

	target := sb.targets[note-sb.minNote]
	switch g := target.(type) {
	case *RingingStringGroup:
		return g.resFilters
	case *ModalStringGroup:
		return g.resFilters
	default:
		t.Fatalf("note %d: unexpected resonance target type %T", note, target)
		return nil
	}
}

// noteResonatorMagnitude is |H(e^jw)| of the resonator at freqHz, excluding the
// per-partial gain weight.
func noteResonatorMagnitude(r noteResonator, freqHz, sampleRate float64) float64 {
	w := 2 * math.Pi * freqHz / sampleRate
	// H(z) = b0 / (1 - a1*z^-1 - a2*z^-2)
	re := 1 - float64(r.a1)*math.Cos(w) - float64(r.a2)*math.Cos(2*w)
	im := float64(r.a1)*math.Sin(w) + float64(r.a2)*math.Sin(2*w)
	return float64(r.b0) / math.Hypot(re, im)
}

// driveNoteResonatorAtPeak feeds a unit sine at freqHz through the resonator
// and returns the peak amplitude of the steady-state output.
func driveNoteResonatorAtPeak(r noteResonator, freqHz, sampleRate float64) float64 {
	// Five time constants of the narrowest bandwidth used here (35 Hz) is about
	// 45 ms; a second of settling covers every case.
	settle := int(sampleRate)
	measure := int(sampleRate/freqHz*8) + 1
	step := 2 * math.Pi * freqHz / sampleRate
	peak := 0.0
	for i := 0; i < settle+measure; i++ {
		y := r.process(float32(math.Sin(float64(i) * step)))
		if i < settle {
			continue
		}
		if v := math.Abs(float64(y)); v > peak {
			peak = v
		}
	}
	return peak
}

// dwgResonanceLongRenderSeconds is deliberately far longer than the rest of the
// stability suite. The divergence this file guards against was invisible at the
// 4 s TestLongRenderHasNoNaNOrInf renders: it only cleared the initial
// transient at around 10 s and did not reach obviously wrong magnitudes until
// 20 s.
const dwgResonanceLongRenderSeconds = 45.0

// maxDWGResonanceLongRenderGrowth bounds how much louder any later window of
// the long render may be than the first window after the attack.
//
// It is not 1.0 - "must decay monotonically" - because the same render with
// resonance switched off does not decay monotonically either: with the pedal
// held the DWG core's undamped strings beat against each other and the
// per-window peak wobbles by about 30% either way (measured 2026-08-22 over
// 45 s: 2.41, 2.33, 1.69, 1.78, 2.13, 1.65, 1.39, 1.64). 1.5 clears that wobble
// and still leaves six orders of magnitude of margin against the defect.
const maxDWGResonanceLongRenderGrowth = 1.5

// TestDWGResonanceLongRenderDecays renders the DWG core through the public
// engine for 45 s with sympathetic resonance on and the sustain pedal held, and
// requires the output not to grow.
//
// This is the regression test the Phase 9.6 defect did not have. The
// unnormalised noteResonator bank put the aggregate sympathetic loop gain above
// unity with all 88 groups undamped, and the resulting growth was slow enough
// to hide behind the 4 s renders of TestLongRenderHasNoNaNOrInf. Measured
// through Piano.Process at 48 kHz with a single note 60 at velocity 100, peak
// per window:
//
//	t          1 s   5 s    10 s   20 s   30 s     40 s
//	before     3.22  1.48   3.47   485    66393    1.23e7
//	after      3.22  1.37   1.04   0.797  0.615    0.444
//
// Finiteness is not the assertion: float32 still had headroom at 40 s, which is
// precisely why a NaN check would not have caught this.
func TestDWGResonanceLongRenderDecays(t *testing.T) {
	const sampleRate = 48000
	const windowSeconds = 5.0

	cfg := stabilityRenderConfig{
		name:       "dwg-resonance-long-decay",
		model:      StringModelDWG,
		coupling:   CouplingModePhysical,
		resonance:  true,
		sampleRate: sampleRate,
		seconds:    dwgResonanceLongRenderSeconds,
		notes:      stabilityNotes,
		pedals:     pedalScript{name: "held", sustainAmount: 1, keyUpAt: -1, sustainOffAt: -1, softPedalAt: -1},
	}

	blocksPerWindow := int(windowSeconds*float64(sampleRate)) / stabilityBlockSize
	windows := make([]float64, 0, int(dwgResonanceLongRenderSeconds/windowSeconds)+1)

	renderStabilityBlocks(t, cfg, func(block int, out []float32) {
		w := block / blocksPerWindow
		for len(windows) <= w {
			windows = append(windows, 0)
		}
		for _, s := range out {
			v := math.Abs(float64(s))
			if math.IsNaN(v) || math.IsInf(v, 0) {
				t.Fatalf("non-finite sample in block %d (%.1f s)", block, float64(block*stabilityBlockSize)/float64(sampleRate))
			}
			if v > windows[w] {
				windows[w] = v
			}
		}
	})

	if len(windows) < 8 {
		t.Fatalf("render produced %d windows of %.0f s, want at least 8", len(windows), windowSeconds)
	}
	for i, peak := range windows {
		t.Logf("%2.0f-%2.0f s: peak %.6g", float64(i)*windowSeconds, float64(i+1)*windowSeconds, peak)
	}
	if windows[0] == 0 {
		t.Fatalf("no signal in the first %.0f s", windowSeconds)
	}
	// windows[0] holds the hammer attack, so the reference is windows[1]: the
	// first window that is pure sustain.
	reference := windows[1]
	if reference == 0 {
		t.Fatalf("no signal in the %.0f-%.0f s window", windowSeconds, 2*windowSeconds)
	}
	limit := reference * maxDWGResonanceLongRenderGrowth
	for i := 2; i < len(windows); i++ {
		if windows[i] > limit {
			t.Fatalf("sympathetic resonance loop is growing: window %.0f-%.0f s peaks at %.6g, more than %.1fx the %.6g of the %.0f-%.0f s window",
				float64(i)*windowSeconds, float64(i+1)*windowSeconds, windows[i],
				maxDWGResonanceLongRenderGrowth, reference, windowSeconds, 2*windowSeconds)
		}
	}
}
