package analysis

import (
	"math"
	"math/cmplx"
)

// Attack-transient analysis parameters.
//
// The metric follows PLAN.md 12.5: "onset rise + first 80 ms spectral centroid
// trajectory". Two independent things make an attack sound like an attack —
// how fast the note gets loud, and how its brightness moves while it does —
// and they are measured separately so a mismatch can be attributed.
const (
	// attackWindowSec is the span of the onset the metric looks at.
	attackWindowSec = 0.080

	// The rise time uses a much finer envelope than the 256/128 frames the
	// rest of the module works with: a 64/32 frame resolves 0.67 ms at 48 kHz,
	// which is the granularity a piano onset needs.
	attackEnvFrame = 64
	attackEnvHop   = 32

	// The centroid trajectory uses a 1024-point window hopped by 256, giving
	// twelve frames whose centres fall inside the 80 ms window at 44.1 kHz.
	attackFFTSize = 1024
	attackFFTHop  = 256

	// The centroid is taken over 50 Hz - 12 kHz on linear magnitude: below
	// 50 Hz there is only rumble and DC leakage, above 12 kHz only hiss, and
	// both would swamp a mean that is meant to describe musical brightness.
	attackCentroidLoHz = 50.0
	attackCentroidHiHz = 12000.0

	// riseLoFrac and riseHiFrac bracket the rise: peak-relative so the metric
	// does not care how loud the note is, only how quickly it arrives.
	riseLoFrac = 0.10
	riseHiFrac = 0.90

	// riseStartMaxFrac is how loud the first envelope frame may be, relative to
	// the peak, for the window to count as containing an onset at all. A window
	// that opens at half the peak level is a note already in progress, not a
	// note being struck, and its "rise time" would be an artifact of where the
	// window was cut.
	riseStartMaxFrac = 0.5
)

// Normalization scales for the two halves of the attack metric.
const (
	// NormAttackRise maps a rise-time difference in milliseconds onto [0,1].
	// The tracked C4 reference rises in roughly 58 ms, so 20 ms is a large but
	// not absurd miss.
	NormAttackRise = 20.0
	// NormAttackCentroid maps the centroid trajectory error in octaves onto
	// [0,1]. Half an octave apart is a plainly different onset colour.
	NormAttackCentroid = 0.5
)

// attackResult carries everything the attack metric produces for one pair.
type attackResult struct {
	refRiseMS       float64
	candRiseMS      float64
	riseDiffMS      float64
	refCentroidHz   float64
	candCentroidHz  float64
	centroidRMSEOct float64
	available       bool
}

// riseTimeMS returns the 10%-to-90%-of-peak rise time of x in milliseconds,
// measured on a fine RMS envelope. It returns NaN when x has no rising onset:
// the envelope peaks on the very first frame, it already opens near the peak,
// or it never crosses the 10% level on the way up. A decay-only window is exactly that case, and callers
// must treat the NaN as "not applicable" rather than as a bad score.
func riseTimeMS(x []float64, sampleRate int) float64 {
	if sampleRate <= 0 {
		return math.NaN()
	}
	env := rmsEnvelope(x, attackEnvFrame, attackEnvHop)
	if len(env) < 4 {
		return math.NaN()
	}

	peakIdx := 0
	peakVal := env[0]
	for i, v := range env {
		if v > peakVal {
			peakVal = v
			peakIdx = i
		}
	}
	if peakIdx == 0 || peakVal <= 0 || env[0] >= riseStartMaxFrac*peakVal {
		return math.NaN()
	}

	lo := riseLoFrac * peakVal
	hi := riseHiFrac * peakVal
	loIdx := firstUpwardCrossing(env, 0, peakIdx, lo)
	if loIdx < 0 {
		return math.NaN()
	}
	hiIdx := firstUpwardCrossing(env, loIdx, peakIdx, hi)
	if hiIdx < loIdx {
		return math.NaN()
	}

	t10 := crossingTimeSec(env, loIdx, lo, sampleRate)
	t90 := crossingTimeSec(env, hiIdx, hi, sampleRate)
	if !isFinite(t10) || !isFinite(t90) || t90 < t10 {
		return math.NaN()
	}
	return 1000.0 * (t90 - t10)
}

// firstUpwardCrossing returns the first index i in [from,peakIdx] at which env
// rises through level, or -1 when there is none.
//
// First crossing, not last: a 64-sample frame spans only a third of a period
// at C4, so the fine envelope ripples heavily within each cycle. Taking the
// last crossing before the peak would let a single intra-period dip below the
// 10% line reorder the two thresholds and throw the measurement away
// altogether, which is what a real piano recording actually does.
//
// Frame -1 is treated as silence: the buffer starts at the note's onset, so a
// rise fast enough that the 10% point falls inside the very first frame still
// has a well defined crossing. The caller has already rejected windows that
// open too loud for that reading to hold.
func firstUpwardCrossing(env []float64, from int, peakIdx int, level float64) int {
	if from < 0 {
		from = 0
	}
	for i := from; i <= peakIdx && i < len(env); i++ {
		prev := 0.0
		if i > 0 {
			prev = env[i-1]
		}
		if env[i] >= level && prev < level {
			return i
		}
	}
	return -1
}

// crossingTimeSec linearly interpolates the time at which env crosses level
// between frames idx-1 and idx. Frame times are taken at the frame centre.
func crossingTimeSec(env []float64, idx int, level float64, sampleRate int) float64 {
	if idx < 0 || idx >= len(env) {
		return math.NaN()
	}
	prev := 0.0
	if idx > 0 {
		prev = env[idx-1]
	}
	cur := env[idx]
	frac := 0.0
	if cur != prev {
		frac = (level - prev) / (cur - prev)
	}
	if frac < 0 {
		frac = 0
	}
	if frac > 1 {
		frac = 1
	}
	frame := float64(idx-1) + frac
	t := (frame*attackEnvHop + attackEnvFrame/2.0) / float64(sampleRate)
	if t < 0 {
		t = 0
	}
	return t
}

// spectralCentroidHz returns the magnitude-weighted mean frequency of spec
// over [loHz,hiHz], on linear magnitude. It returns NaN when the band carries
// no energy.
func spectralCentroidHz(spec []complex128, binHz float64, loHz float64, hiHz float64) float64 {
	if len(spec) < 2 || binHz <= 0 || hiHz <= loHz {
		return math.NaN()
	}
	lo := int(loHz/binHz + 0.5)
	hi := int(hiHz/binHz + 0.5)
	if lo < 1 {
		lo = 1
	}
	if hi > len(spec)-1 {
		hi = len(spec) - 1
	}
	if lo > hi {
		return math.NaN()
	}
	var num, den float64
	for k := lo; k <= hi; k++ {
		mag := cmplx.Abs(spec[k])
		num += float64(k) * binHz * mag
		den += mag
	}
	if den <= 0 {
		return math.NaN()
	}
	return num / den
}

// attackCentroidTrajectory returns the spectral centroid of every analysis
// frame whose centre falls inside the first attackWindowSec of x. The
// trajectory, not a single average, is what distinguishes a hammer strike that
// stays bright from one that collapses to the fundamental immediately.
func attackCentroidTrajectory(x []float64, sampleRate int) []float64 {
	if sampleRate <= 0 || len(x) < attackFFTSize {
		return nil
	}
	plan, err := getSpectralFFTPlan(attackFFTSize)
	if err != nil {
		return nil
	}
	limit := int(attackWindowSec * float64(sampleRate))
	binHz := float64(sampleRate) / float64(attackFFTSize)
	hann := hannWindow(attackFFTSize)

	sc := getScratch(attackFFTSize)
	defer putScratch(attackFFTSize, sc)

	var out []float64
	for start := 0; start+attackFFTSize <= len(x); start += attackFFTHop {
		if start+attackFFTSize/2 >= limit {
			break
		}
		for i := 0; i < attackFFTSize; i++ {
			sc.aw[i] = x[start+i] * hann[i]
		}
		if err := plan.forward(sc.specA, sc.aw); err != nil {
			return out
		}
		c := spectralCentroidHz(sc.specA[:attackFFTSize/2+1], binHz, attackCentroidLoHz, attackCentroidHiHz)
		if !isFinite(c) || c <= 0 {
			continue
		}
		out = append(out, c)
	}
	return out
}

// attackCentroidRMSEOct compares two centroid trajectories in octaves rather
// than hertz. A 100 Hz centroid error means something entirely different at C1
// than at C7, and an octave-domain error is also sample-rate neutral, so the
// same threshold holds across the keyboard and across analysis rates.
func attackCentroidRMSEOct(ref []float64, cand []float64) float64 {
	n := len(ref)
	if len(cand) < n {
		n = len(cand)
	}
	if n == 0 {
		return math.NaN()
	}
	var sum float64
	var cnt int
	for i := 0; i < n; i++ {
		if ref[i] <= 0 || cand[i] <= 0 {
			continue
		}
		d := math.Log2(cand[i] / ref[i])
		if !isFinite(d) {
			continue
		}
		sum += d * d
		cnt++
	}
	if cnt == 0 {
		return math.NaN()
	}
	return math.Sqrt(sum / float64(cnt))
}

// mean returns the arithmetic mean of xs, or NaN when xs is empty.
func mean(xs []float64) float64 {
	if len(xs) == 0 {
		return math.NaN()
	}
	var sum float64
	for _, v := range xs {
		sum += v
	}
	return sum / float64(len(xs))
}

// attackSilenceThreshold is the level below which a leading sample counts as
// digital silence for attack analysis. It is far lower than the 1e-6 used for
// alignment on purpose: a synthesised note ramps in from well under -120 dBFS,
// and cutting that ramp off would hide the very onset the metric exists to
// measure, while a zero-padded file is still trimmed.
const attackSilenceThreshold = 1e-9

// attackMaxSpanSec caps how much of a signal the attack analysis looks at.
// The envelope peak that anchors the rise measurement must be the strike, not
// a swell or a pedal release seconds later.
const attackMaxSpanSec = 1.0

// attackSlice returns the portion of x the attack analysis works on: from the
// first sample above digital silence, for at most attackMaxSpanSec.
func attackSlice(x []float64, sampleRate int) []float64 {
	y := trimLeadingSilence(x, attackSilenceThreshold)
	limit := int(attackMaxSpanSec * float64(sampleRate))
	if limit > 0 && len(y) > limit {
		y = y[:limit]
	}
	return y
}

// analyzeAttack measures the onset of both signals. The result is marked
// unavailable when either signal has no rising onset or the centroid
// trajectories cannot be compared; callers must then leave the attack term out
// of the score entirely instead of folding in a meaningless number.
func analyzeAttack(ref []float64, cand []float64, sampleRate int) attackResult {
	res := attackResult{
		refRiseMS:       math.NaN(),
		candRiseMS:      math.NaN(),
		riseDiffMS:      math.NaN(),
		refCentroidHz:   math.NaN(),
		candCentroidHz:  math.NaN(),
		centroidRMSEOct: math.NaN(),
	}

	ref = attackSlice(ref, sampleRate)
	cand = attackSlice(cand, sampleRate)

	res.refRiseMS = riseTimeMS(ref, sampleRate)
	res.candRiseMS = riseTimeMS(cand, sampleRate)
	if isFinite(res.refRiseMS) && isFinite(res.candRiseMS) {
		res.riseDiffMS = math.Abs(res.candRiseMS - res.refRiseMS)
	}

	refTraj := attackCentroidTrajectory(ref, sampleRate)
	candTraj := attackCentroidTrajectory(cand, sampleRate)
	res.refCentroidHz = mean(refTraj)
	res.candCentroidHz = mean(candTraj)
	res.centroidRMSEOct = attackCentroidRMSEOct(refTraj, candTraj)

	res.available = isFinite(res.riseDiffMS) && isFinite(res.centroidRMSEOct)
	return res
}

// attackNormOf combines the two halves of the attack metric into a single
// [0,1] contribution, or NaN when the attack is not measurable. The halves
// carry equal weight: rise time and onset colour are independent failure modes
// and neither should be able to hide the other.
func attackNormOf(m Metrics) float64 {
	if !m.AttackAvailable {
		return math.NaN()
	}
	rise := clamp01(m.AttackRiseDiffMS / NormAttackRise)
	cent := clamp01(m.AttackCentroidRMSEOct / NormAttackCentroid)
	if !isFinite(rise) || !isFinite(cent) {
		return math.NaN()
	}
	return clamp01(0.5*rise + 0.5*cent)
}
