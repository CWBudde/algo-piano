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

func (r *ResonanceEngine) InjectFromBridge(bridge []float32, targets []resonanceTarget) {
	if r == nil || r.injectionGain <= 0 || len(bridge) == 0 || len(targets) == 0 {
		return
	}
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
		}
	}
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
	b0 := float32(1.0 - r)
	return noteResonator{a1: a1, a2: a2, b0: b0, gain: gain}
}

func (r *noteResonator) process(x float32) float32 {
	y := r.b0*x + r.a1*r.y1 + r.a2*r.y2
	y = float32(dspcore.FlushDenormals(float64(y)))
	r.y2 = r.y1
	r.y1 = y
	return y * r.gain
}
