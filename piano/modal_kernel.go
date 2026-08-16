package piano

import vecmath "github.com/cwbudde/algo-vecmath"

// modalKernel selects the implementation used to advance a modal mode bank by
// one sample. All variants are bit-exact with one another: the algo-vecmath
// kernels use the same expression and operand order as the scalar reference,
// and the SIMD backends deliberately avoid FMA so their rounding matches.
//
// This is a debug/benchmark switch, not a preset parameter — it must not leak
// into the serialized preset schema.
type modalKernel uint8

const (
	// modalKernelAccum uses vecmath.RotateDecayAccumulateF32: the rotate,
	// decay and gain-weighting all happen in the vector kernel, leaving only a
	// segmented reduce in Go.
	modalKernelAccum modalKernel = iota

	// modalKernelScalar is the hand-written reference loop. It exists so
	// parity tests can compare against it in the same test binary.
	modalKernelScalar

	// modalKernelRotate uses vecmath.RotateDecayComplexF32 and applies the
	// per-mode gain in the Go reduce. It avoids the scratch write/clear of the
	// accumulate variant, which can win at the small mode counts (~24 per
	// group) this synth uses.
	modalKernelRotate
)

// modalKernelDefault is the kernel used unless a test or benchmark overrides
// modalKernelMode.
const modalKernelDefault = modalKernelRotate

// modalKernelMode selects the active kernel. It is process-global and not
// safe to change concurrently with rendering; tests must not run in parallel
// while mutating it.
var modalKernelMode = modalKernelDefault

// modalArenaEnabled selects the batched arena path in StringBank.Process, where
// every active modal group is advanced by a single kernel call per sample.
// Disabling it falls back to one call per group, which is what modalKernelMode
// then selects. Tests use this to compare the two against each other.
var modalArenaEnabled = true

// advanceModes advances every mode of the group by one sample and returns the
// gain- and unison-weighted group output.
func (g *ModalStringGroup) advanceModes() float32 {
	switch modalKernelMode {
	case modalKernelScalar:
		return g.advanceModesScalar()
	case modalKernelRotate:
		return g.advanceModesRotate()
	default:
		return g.advanceModesAccum()
	}
}

// advanceModesScalar is the reference implementation. Every other kernel must
// reproduce its output bit for bit.
func (g *ModalStringGroup) advanceModesScalar() float32 {
	sample := float32(0)
	for si := 0; si+1 < len(g.modeStart); si++ {
		lo, hi := int(g.modeStart[si]), int(g.modeStart[si+1])
		str := float32(0)
		for i := lo; i < hi; i++ {
			d, re, im := g.decay[i], g.re[i], g.im[i]
			nx := d * (re*g.cosW[i] - im*g.sinW[i])
			ny := d * (re*g.sinW[i] + im*g.cosW[i])
			g.re[i] = nx
			g.im[i] = ny
			str += nx * g.gain[i]
		}
		sample += str * g.stringGain(si)
	}
	return sample
}

// advanceModesAccum lets the vector kernel apply the per-mode gain into the
// acc scratch, then reduces per unison string. acc is all-zero on entry and is
// re-zeroed by the reduce, which keeps the invariant without a separate pass.
func (g *ModalStringGroup) advanceModesAccum() float32 {
	vecmath.RotateDecayAccumulateF32(g.acc, g.re, g.im, g.cosW, g.sinW, g.decay, g.gain)
	return g.reduceAcc()
}

// reduceAcc sums the accumulator per unison string, applies the unison gains,
// and re-zeroes acc so it is ready for the next sample. Keeping the clear fused
// into the reduce avoids a second pass over the data.
func (g *ModalStringGroup) reduceAcc() float32 {
	sample := float32(0)
	for si := 0; si+1 < len(g.modeStart); si++ {
		lo, hi := int(g.modeStart[si]), int(g.modeStart[si+1])
		seg := g.acc[lo:hi]
		str := float32(0)
		for i := range seg {
			str += seg[i]
			seg[i] = 0
		}
		sample += str * g.stringGain(si)
	}
	return sample
}

// advanceModesRotate vectorizes only the rotate-decay recurrence and applies
// the per-mode gain during the reduce.
func (g *ModalStringGroup) advanceModesRotate() float32 {
	vecmath.RotateDecayComplexF32(g.re, g.im, g.cosW, g.sinW, g.decay)

	sample := float32(0)
	for si := 0; si+1 < len(g.modeStart); si++ {
		lo, hi := int(g.modeStart[si]), int(g.modeStart[si+1])
		str := float32(0)
		for i := lo; i < hi; i++ {
			str += g.re[i] * g.gain[i]
		}
		sample += str * g.stringGain(si)
	}
	return sample
}
