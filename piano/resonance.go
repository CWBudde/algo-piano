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

func NewResonanceEngine(sampleRate int, injectionGain float32, perNoteFilter bool) *ResonanceEngine {
	if sampleRate < 8000 {
		sampleRate = 8000
	}
	if injectionGain < 0 {
		injectionGain = 0
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

// InjectFromBridge feeds the band-limited bridge signal into every undamped
// target and reports whether any energy was actually deposited, so the caller
// can tell the string bank to enroll the groups that started resonating.
func (r *ResonanceEngine) InjectFromBridge(bridge []float32, targets []resonanceTarget) bool {
	if r == nil || r.injectionGain <= 0 || len(bridge) == 0 || len(targets) == 0 {
		return false
	}
	injected := false
	for i := 0; i < len(bridge); i++ {
		x := r.bandLimit(bridge[i])
		if x > -1e-8 && x < 1e-8 {
			continue
		}
		energy := x * r.injectionGain
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
	// Unity peak gain, normalised at the response's true maximum.
	//
	// H(z) = b0 / (1 - a1 z^-1 - a2 z^-2) with poles at r*e^(+-j*w0), so
	// |H(e^jw)| = b0 / |D(w)| with
	//
	//	|D(w)|^2 = (1 - a1*cos(w) - a2*cos(2w))^2 + (a1*sin(w) + a2*sin(2w))^2
	//
	// Setting b0 to |D| at the frequency where it is smallest makes the peak
	// exactly one. The naive b0 = 1-r instead peaks at roughly 1/(2*sin(w0)),
	// a 1/f0 law that reached 183x at A0 and pushed the summed sympathetic
	// loop past unity in the bass; see the Phase 9.6 notes in PLAN.md.
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
