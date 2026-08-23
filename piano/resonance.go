package piano

import (
	"math"

	dspcore "github.com/cwbudde/algo-dsp/dsp/core"
)

type resonanceTarget interface {
	isUndamped() bool
	filterResonanceDrive(x float32) float32
	injectResonance(energy float32)
}

// ResonanceEngine injects a band-limited bridge signal into undamped strings.
type ResonanceEngine struct {
	injectionGain float32
	perNoteFilter bool
	dcR           float32
	dcPrevIn      float32
	dcPrevOut     float32
	lpA           float32
	lpState       float32
}

// maxResonanceGain is the ceiling NewResonanceEngine clamps ResonanceGain to.
//
// This parameter had no upper bound at all until 2026-08-23, alone among the
// three that close a feedback path around the string bank: maxUnisonCrossfeed
// (ringing.go) and maxBridgeCoupling (bridge_coupling.go) have always been
// enforced. It is also the only one of the three that actually diverges the
// renderer at a value a hand-edited preset may legally ask for - resonance_gain
// 0.005 reaches 1.3e12 over 120 s.
//
// THE MARGIN RUNS DOWNWARD FROM THE CLIFF, which is the opposite of its two
// siblings and is why the factor looks small next to theirs. maxUnisonCrossfeed
// sits 25x BELOW its divergence and maxBridgeCoupling 5x below, because for
// those terms the useful range is far under the unstable one. Here the useful
// range runs right up to the cliff: the loop is linear in this gain, so every
// increase buys proportional sympathetic level, and a clamp set above the
// critical gain would enforce nothing at all. So the bound is a safety factor
// on a measured critical gain, not a headroom multiple on a shipped value.
//
// Measured 2026-08-23 (Go 1.26.5, linux/amd64) by two independent estimators,
// pooled by taking the minimum:
//
//   - the CLOSED-LOOP decay rate sigma, in dB/s, by least squares over 5 s RMS
//     windows of a 120 s pedal-held render, skipping the first five. sigma = 0
//     is the stability boundary exactly, and the critical gain is its root.
//
//   - the OPEN-LOOP certificate, g_probe * resonanceProbeSettleFraction /
//     reading. The loop is exactly linear in this gain - measured constant to
//     eight significant figures over 0.00025/0.0005/0.001 on both cores - so one
//     probe run certifies every gain, which is what made a wide sweep
//     affordable at all.
//
//     axis                                   sigma root   certificate
//     dwg 48 kHz, filter on                    0.000830      0.000756
//     dwg 44.1 kHz, filter on                  0.000796      0.000741  <- binds
//     dwg 96 kHz, filter on                           -      0.000981
//     dwg 48 kHz, filter off                          -      0.003274
//     modal, any rate                                 -      0.70-1.00
//
// 44.1 kHz diverges before 48 kHz for the same reason it does for
// bridge_coupling: the injection lag is one SAMPLE, so it is a larger fraction
// of a period at the lower rate. The certificate reads low against the sigma
// root because the DWG probe never settles inside its budget and 0.73 is
// measured at a hotter configuration than these; it is a conservative bound by
// construction, and pooling by minimum keeps it that way.
//
// So G* = 0.000741, and the bound is G*/2. The pre-committed factor is 2 rather
// than the 5 or 25 the siblings carry because G* here is measured directly by
// root-finding rather than inferred, and tighter than that because this
// parameter has a documented history of the probes measuring the wrong thing:
// the same renderer once diverged at 0.00025 while the open-loop probe read
// 0.174 against a bound of 0.5. See maxResonanceLoopGain.
//
// CONFIRMED AT THE CLAMP on every axis the certificate cannot see. The open-loop
// probe holds the pedal down and drives a synthetic sine, so it ignores which
// notes were struck, at what velocity, how far the pedal is down and whether
// inter-note coupling runs: every such row returns an identical number. Those
// questions have to be answered by the closed-loop render, and are, in sigma at
// g = maxResonanceGain against the same render at g = 0 (60 s, 48 kHz):
//
//	axis                          sigma(0)   sigma(clamp)
//	six-note chord, v100           -0.2114        -0.1888
//	bass 21/24/28/33               -0.1270        -0.1311
//	treble 72/79/84/88            (silent)        -0.1930
//	velocity 127                   -0.2179        -0.1967
//	velocity 20                    -0.2047        -0.1742
//	single note 33                 -0.1929        -0.1856
//	half pedal (0.35)              -0.2114        -0.1891
//	inter-note coupling physical   -0.2114        -0.1888
//
// Every row is negative at the clamp, and the bass row is MORE negative than its
// own uncoupled arm. Velocity moves sigma a little in both arms (the hammer
// decides which modes are energised, so a finite render sees a slightly
// different dominant mode) but it does not move the boundary: the loop is linear
// above injectSample's 1e-8 gate, which sits ~181 dB under a struck chord and is
// never reached in a 120 s render, and a deadzone can only ever REMOVE loop
// gain, so the linear reading is already the worst case.
//
// Two rows are equal to their reference by construction rather than by accident,
// and are kept because a reader will otherwise assume a harness bug. Half pedal:
// every note here is key-down, so pedal depth cannot change ITS damper state,
// and any sustain > 0 undamps all the others exactly as 1.0 does. Coupling:
// 50364 of 51200 samples differ between the two renders, so it is certainly
// running - it redistributes energy between notes without adding or removing it,
// so it moves the waveform and not the dominant eigenvalue. That invariance is
// the reason sigma is the statistic here and a peak ratio is not.
//
// The treble sigma(0) is (silent) rather than a number: that render reaches
// digital silence inside 60 s, so a fit there measures the floor and not the
// loop. Its clamp arm is a real measurement.
//
// Nothing in assets/presets is affected - every preset pins 0.00025 - and
// cmd/piano-fit has no resonance knob, so no fit can reach this either.
const maxResonanceGain = float32(0.00037)

func NewResonanceEngine(sampleRate int, injectionGain float32, perNoteFilter bool) *ResonanceEngine {
	if sampleRate < 8000 {
		sampleRate = 8000
	}
	if injectionGain < 0 {
		injectionGain = 0
	}
	// A preset is a JSON file a human can edit, so the gain is clamped rather
	// than trusted, exactly as NewStringBank clamps its two coupling strengths.
	// This is the single funnel: Piano and the probe banks both build their
	// engine here. Probes that must exceed the clamp write injectionGain
	// directly afterwards.
	if injectionGain > maxResonanceGain {
		injectionGain = maxResonanceGain
	}
	cutoffHz := 3200.0
	a := float32(math.Exp(-2.0 * math.Pi * cutoffHz / float64(sampleRate)))
	return &ResonanceEngine{
		injectionGain: injectionGain,
		perNoteFilter: perNoteFilter,
		dcR:           0.995,
		lpA:           a,
	}
}

func (r *ResonanceEngine) bandLimit(x float32) float32 {
	dcOut := x - r.dcPrevIn + r.dcR*r.dcPrevOut
	r.dcPrevIn = x
	r.dcPrevOut = dcOut

	lp := (1.0-r.lpA)*dcOut + r.lpA*r.lpState
	lp = float32(dspcore.FlushDenormals(float64(lp)))
	r.lpState = lp
	return lp
}

// injectSample band-limits ONE bridge sample and deposits it into every undamped
// target, reporting whether any energy was actually deposited so the caller can
// enroll the groups that started resonating.
//
// It is the ONLY injection entry point. StringBank.processWithBridge calls it
// once per sample from inside its render loop, so the string state advances
// between consecutive deposits and the drive is spread across the block in time.
//
// Until 2026-08-22 there was a second, block-at-a-time entry point
// (InjectFromBridge) that Piano.Process called with a finished block. No string
// state advanced between its deposits, so all of a block's samples landed on the
// same delay-line tap / modal state: for a mode at angular frequency w that
// deposits |sum x[i]| — the block's DC content — rather than
// |sum x[i]*exp(-jwi)|, the drive at w. A 128-tap boxcar at block rate, in other
// words, with ~+42 dB at DC and a first null at fs/128 = 375 Hz. It is gone
// rather than merely unused: closing the loop from outside the bank WAS the
// defect, and the two RingingState forwarders that let a caller reach it
// (ResonanceTargets, NotifyResonanceInjected) went with it. Probes that need to
// drive the bank with a known signal use the drive seam on
// StringBank.processWithBridge instead.
//
// It must stay allocation-free: StringBank.Process is pinned at zero heap
// allocations per block. No variadics, no closures, no per-sample slice
// construction; targets is the bank's pre-allocated slice.
func (r *ResonanceEngine) injectSample(x float32, targets []resonanceTarget) bool {
	if r == nil || r.injectionGain <= 0 || len(targets) == 0 {
		return false
	}
	x = r.bandLimit(x)
	if x > -1e-8 && x < 1e-8 {
		return false
	}
	energy := x * r.injectionGain
	injected := false
	for _, t := range targets {
		if !t.isUndamped() {
			continue
		}
		vEnergy := energy
		if r.perNoteFilter {
			vEnergy = t.filterResonanceDrive(x) * r.injectionGain
		}
		t.injectResonance(vEnergy)
		injected = true
	}
	return injected
}

type noteResonator struct {
	a1   float32
	a2   float32
	b0   float32
	y1   float32
	y2   float32
	gain float32
}

func newNoteResonator(sampleRate int, centerHz float32, bandwidthHz float32, gain float32) noteResonator {
	fs := float64(sampleRate)
	f0 := float64(centerHz)
	bw := float64(bandwidthHz)
	if fs <= 0 {
		fs = 48000
	}
	if f0 < 5 {
		f0 = 5
	}
	if f0 > fs*0.49 {
		f0 = fs * 0.49
	}
	if bw < 10 {
		bw = 10
	}
	r := math.Exp(-math.Pi * bw / fs)
	w0 := 2.0 * math.Pi * f0 / fs
	a1 := float32(2.0 * r * math.Cos(w0))
	a2 := float32(-(r * r))
	// Unity gain at the tuned partial.
	//
	// H(z) = b0 / (1 - a1 z^-1 - a2 z^-2) with poles at r*e^(+-j*w0), so
	// |H(e^jw)| = b0 / |D(w)| with
	//
	//	|D(w)|^2 = (1 - a1*cos(w) - a2*cos(2w))^2 + (a1*sin(w) + a2*sin(2w))^2
	//
	// Setting b0 to |D(w0)| therefore makes the gain AT the tuned partial
	// exactly one - which is the choice made here, and deliberately not the
	// |D| minimum that would instead cap the peak; see below. The naive
	// b0 = 1-r peaks at roughly 1/(2*sin(w0)), a 1/f0 law that reached 183x
	// at A0 and pushed the summed sympathetic loop past unity in the bass;
	// see the Phase 9.6 notes in PLAN.md.
	//
	// Normalisation is at w0 — the partial the resonator is tuned to — and
	// deliberately NOT at the response's true maximum.
	//
	// |D| is actually minimised at cos(wMax) = ((1+r^2)/(2r))*cos(w0), which
	// sits below w0 once the bandwidth is comparable to f0: at A0 with the
	// 35 Hz bandwidth the true peak is at 21.2 Hz and reaches 1.049 (+0.42 dB),
	// against +0.005 dB at C4. Normalising there instead would cap the response
	// at exactly one everywhere, but it would also pull the gain AT the partials
	// down to 0.953 in the bass while leaving it at 1.0 in the treble — turning
	// the summed drive bank back into a register-dependent 1.778-at-A0 against
	// 1.85 elsewhere. Register independence at the partials is the property that
	// stops the sympathetic loop diverging, so it wins; a +0.42 dB shoulder
	// between partials in the bottom octave does not. See
	// TestNoteResonatorPeakIsUnityAcrossSpectrum, which bounds that shoulder.
	//
	// |D| is evaluated from the float32 coefficients the filter will actually
	// run, not from their float64 originals. In the bass |D| is a near-total
	// cancellation — at A0 / 96 kHz, a1 is within 1e-5 of 2 — so rounding a1 to
	// float32 moves it by several percent relatively. Normalising against the
	// rounded coefficients cancels that mismatch exactly, because process()
	// divides by precisely this quantity.
	a1f, a2f := float64(a1), float64(a2)
	dRe := 1.0 - a1f*math.Cos(w0) - a2f*math.Cos(2.0*w0)
	dIm := a1f*math.Sin(w0) + a2f*math.Sin(2.0*w0)
	b0 := float32(math.Hypot(dRe, dIm))
	return noteResonator{a1: a1, a2: a2, b0: b0, gain: gain}
}

func (r *noteResonator) process(x float32) float32 {
	y := r.b0*x + r.a1*r.y1 + r.a2*r.y2
	y = float32(dspcore.FlushDenormals(float64(y)))
	r.y2 = r.y1
	r.y1 = y
	return y * r.gain
}
