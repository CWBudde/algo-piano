package analysis

import (
	"math"
	"math/cmplx"
)

// Normalization scales for the extended metric components. Each maps a raw
// metric onto [0,1] the same way NormTime and friends do for the legacy four.
const (
	NormPartialLevel = 12.0 // dB RMSE across matched partials
	NormPartialFreq  = 50.0 // cents RMSE across matched partials
	NormTristimulus  = 0.5  // Euclidean distance between tristimulus triples
	NormDecaySegment = 30.0 // dB/s RMSE across the decay segments
)

const (
	// maxPartials bounds the harmonic series we track.
	maxPartials = 16
	// partialWindow is the FFT length used for partial extraction.
	partialWindow = 4096
	// partialSearchFrac is the relative half-width of the bin search around
	// each expected harmonic; it accommodates piano inharmonicity.
	partialSearchFrac = 0.035
)

// MIDIToHz converts a MIDI note number to its equal-tempered frequency.
func MIDIToHz(note int) float64 {
	return 440.0 * math.Pow(2, float64(note-69)/12.0)
}

// partialSet holds the measured harmonic series of one signal.
type partialSet struct {
	f0    float64
	freq  [maxPartials + 1]float64 // Hz, index 1..count
	amp   [maxPartials + 1]float64 // linear magnitude, index 1..count
	found [maxPartials + 1]bool
	count int
}

// analyzePartials extracts up to maxPartials harmonics from x. When f0 is not
// positive it is estimated from the spectrum, otherwise it is used verbatim.
// It returns ok=false when x is too short or no usable fundamental could be
// established.
func analyzePartials(x []float64, sampleRate int, f0 float64) (partialSet, bool) {
	return analyzePartialsMode(x, sampleRate, f0, false)
}

// analyzePartialsHint is analyzePartials with f0 treated as a starting point
// that is refined against the spectrum rather than pinned.
func analyzePartialsHint(x []float64, sampleRate int, f0Hint float64) (partialSet, bool) {
	return analyzePartialsMode(x, sampleRate, f0Hint, true)
}

func analyzePartialsMode(x []float64, sampleRate int, f0 float64, hint bool) (partialSet, bool) {
	var ps partialSet
	if sampleRate <= 0 || len(x) < partialWindow {
		return ps, false
	}
	plan, err := getSpectralFFTPlan(partialWindow)
	if err != nil {
		return ps, false
	}

	// Analyze slightly after the onset so the attack transient does not smear
	// the harmonic peaks, but stay inside the signal.
	offset := sampleRate / 20
	if offset+partialWindow > len(x) {
		offset = len(x) - partialWindow
	}
	if offset < 0 {
		offset = 0
	}

	sc := getScratch(partialWindow)
	defer putScratch(partialWindow, sc)
	hann := hannWindow(partialWindow)
	for i := 0; i < partialWindow; i++ {
		sc.aw[i] = x[offset+i] * hann[i]
	}
	if err := plan.forward(sc.specA, sc.aw); err != nil {
		return ps, false
	}

	spec := sc.specA
	bins := partialWindow / 2
	binHz := float64(sampleRate) / float64(partialWindow)

	switch {
	case f0 <= 0:
		f0 = estimateF0(spec, bins, binHz)
	case hint:
		f0 = refineF0Hint(spec, bins, binHz, f0)
	}
	if f0 <= 0 || !isFinite(f0) {
		return ps, false
	}
	ps.f0 = f0

	nyquist := float64(sampleRate) / 2
	for k := 1; k <= maxPartials; k++ {
		target := f0 * float64(k)
		if target >= 0.9*nyquist {
			break
		}
		half := int(target*partialSearchFrac/binHz) + 1
		center := int(target/binHz + 0.5)
		lo := center - half
		hi := center + half
		if lo < 1 {
			lo = 1
		}
		if hi > bins-1 {
			hi = bins - 1
		}
		if lo > hi {
			continue
		}
		bestBin := lo
		bestMag := -1.0
		for j := lo; j <= hi; j++ {
			mag := cmplx.Abs(spec[j])
			if mag > bestMag {
				bestMag = mag
				bestBin = j
			}
		}
		if bestMag <= 0 {
			continue
		}
		ps.freq[k] = interpolatePeakBin(spec, bins, bestBin) * binHz
		ps.amp[k] = bestMag
		ps.found[k] = true
		ps.count = k
	}
	if !ps.found[1] {
		return ps, false
	}
	return ps, true
}

// estimateF0 picks a fundamental from a magnitude spectrum by taking the
// strongest bin in the musical range, preferring a sub-harmonic when one
// carries comparable energy, and then refining the result to sub-bin
// resolution. A raw bin centre is far too coarse for partial tracking: at the
// 4096-point analysis window a 44.1 kHz signal resolves only 10.8 Hz, which is
// 70 cents at C4.
func estimateF0(spec []complex128, bins int, binHz float64) float64 {
	loBin := int(25.0/binHz) + 1
	hiBin := int(4200.0 / binHz)
	if loBin < 1 {
		loBin = 1
	}
	if hiBin > bins-1 {
		hiBin = bins - 1
	}
	if loBin > hiBin {
		return 0
	}
	peakBin := loBin
	peakMag := -1.0
	for j := loBin; j <= hiBin; j++ {
		mag := cmplx.Abs(spec[j])
		if mag > peakMag {
			peakMag = mag
			peakBin = j
		}
	}
	if peakMag <= 0 {
		return 0
	}
	// Prefer the lowest sub-harmonic that still carries significant energy.
	best := peakBin
	for d := 8; d >= 2; d-- {
		cand := peakBin / d
		if cand < loBin {
			continue
		}
		half := cand / 40
		if half < 1 {
			half = 1
		}
		local := -1.0
		for j := cand - half; j <= cand+half; j++ {
			if j < 1 || j > bins-1 {
				continue
			}
			if mag := cmplx.Abs(spec[j]); mag > local {
				local = mag
			}
		}
		if local >= 0.2*peakMag {
			best = cand
			break
		}
	}
	coarse := interpolatePeakBin(spec, bins, best) * binHz
	if coarse <= 0 {
		return 0
	}
	return refineF0WithPartials(spec, bins, binHz, coarse)
}

// Sub-bin refinement parameters.
const (
	// f0RefineMaxPartial bounds the harmonic used to sharpen an f0 estimate.
	// Locating partial k and dividing by k localises the fundamental with k
	// times the frequency resolution, but piano inharmonicity pushes f_k above
	// k*f1 (by roughly 600*log2(1+B*k^2) cents), so only low partials can be
	// divided down without biasing the answer more than they sharpen it.
	f0RefineMaxPartial = 3
	// f0RefineHalfCents is the half-width of the search window used when
	// locating a partial around its expected position.
	f0RefineHalfCents = 40.0
	// f0HintHalfCents is the half-width of the window a supplied fundamental
	// (typically from a MIDI note) is refined within. Real instruments are not
	// perfectly tuned; a semitone quarter is enough slack for that without
	// letting the search escape to a neighbouring note.
	f0HintHalfCents = 50.0
	// f0RefineMinMagFrac is the share of the anchor partial's magnitude a
	// higher partial must reach before it is trusted as a sharper anchor.
	f0RefineMinMagFrac = 0.10
)

// interpolatePeakBin refines an integer magnitude-peak bin to sub-bin
// resolution by fitting a parabola through the log magnitudes of the peak and
// its two neighbours. Interpolating in the log domain is markedly more accurate
// than in the linear one, because a Hann-windowed peak is close to parabolic
// in dB.
func interpolatePeakBin(spec []complex128, bins int, peak int) float64 {
	if peak <= 0 || peak >= bins-1 {
		return float64(peak)
	}
	l := linToDB(cmplx.Abs(spec[peak-1]))
	c := linToDB(cmplx.Abs(spec[peak]))
	r := linToDB(cmplx.Abs(spec[peak+1]))
	den := l - 2*c + r
	if den == 0 {
		return float64(peak)
	}
	delta := 0.5 * (l - r) / den
	if !isFinite(delta) || delta <= -1 || delta >= 1 {
		return float64(peak)
	}
	return float64(peak) + delta
}

// peakNearFreq returns the interpolated frequency and magnitude of the
// strongest spectral peak within +/-halfCents of target. The returned
// frequency is 0 when the window carries no energy or falls outside the
// spectrum.
func peakNearFreq(spec []complex128, bins int, binHz float64, target float64, halfCents float64) (float64, float64) {
	if target <= 0 || binHz <= 0 || bins < 3 {
		return 0, 0
	}
	span := math.Pow(2, halfCents/1200.0)
	lo := int(target/span/binHz + 0.5)
	hi := int(target*span/binHz+0.5) + 1
	if lo < 1 {
		lo = 1
	}
	if hi > bins-1 {
		hi = bins - 1
	}
	if lo > hi {
		return 0, 0
	}
	bestBin := lo
	bestMag := -1.0
	for j := lo; j <= hi; j++ {
		if mag := cmplx.Abs(spec[j]); mag > bestMag {
			bestMag = mag
			bestBin = j
		}
	}
	if bestMag <= 0 {
		return 0, 0
	}
	return interpolatePeakBin(spec, bins, bestBin) * binHz, bestMag
}

// refineF0WithPartials sharpens a fundamental estimate by anchoring it on the
// most reliably measurable low partial. The strongest of partials 1..3 is
// located to sub-bin resolution and divided back down; on a piano the
// fundamental is often weaker than the second or third partial, and anchoring
// on a stronger peak both reduces the interpolation error and buys k times the
// frequency resolution. Candidates that disagree with the coarse estimate are
// rejected, so a mis-picked neighbouring peak cannot drag the answer away.
func refineF0WithPartials(spec []complex128, bins int, binHz float64, coarse float64) float64 {
	base, baseMag := peakNearFreq(spec, bins, binHz, coarse, f0RefineHalfCents)
	if base <= 0 {
		return coarse
	}
	best := base
	bestMag := baseMag
	for k := 2; k <= f0RefineMaxPartial; k++ {
		fk, mag := peakNearFreq(spec, bins, binHz, base*float64(k), f0RefineHalfCents)
		if fk <= 0 || mag < f0RefineMinMagFrac*baseMag || mag <= bestMag {
			continue
		}
		cand := fk / float64(k)
		if math.Abs(1200.0*math.Log2(cand/base)) > f0RefineHalfCents {
			continue
		}
		best = cand
		bestMag = mag
	}
	return best
}

// refineF0Hint treats a supplied fundamental (a MIDI note, say) as a starting
// point rather than the truth and re-locates it against the actual spectrum
// within +/-f0HintHalfCents. Real instruments are not tuned to equal
// temperament: the tracked C4 reference sits about 6 cents sharp of 261.63 Hz,
// and tracking partials against the nominal pitch would bias every partial
// ratio. The refined value is rejected if it strays outside the window.
func refineF0Hint(spec []complex128, bins int, binHz float64, hint float64) float64 {
	if hint <= 0 {
		return hint
	}
	outside := func(f float64) bool {
		return f <= 0 || math.Abs(1200.0*math.Log2(f/hint)) > f0HintHalfCents
	}
	// The bin search brackets the window rather than sitting strictly inside
	// it, so the anchor is re-checked: a spectrum whose nearest peak is more
	// than a window away is not this note, and the nominal pitch is then the
	// better answer than a peak belonging to a neighbour.
	anchor, mag := peakNearFreq(spec, bins, binHz, hint, f0HintHalfCents)
	if mag <= 0 || outside(anchor) {
		return hint
	}
	refined := refineF0WithPartials(spec, bins, binHz, anchor)
	if outside(refined) {
		return anchor
	}
	return refined
}

// tristimulus returns the three-band timbre descriptor of a harmonic series:
// T1 is the fundamental's share, T2 partials 2-4, T3 the remainder.
func (ps partialSet) tristimulus() ([3]float64, bool) {
	var t [3]float64
	var total float64
	for k := 1; k <= ps.count; k++ {
		if ps.found[k] {
			total += ps.amp[k]
		}
	}
	if total <= 0 {
		return t, false
	}
	for k := 1; k <= ps.count; k++ {
		if !ps.found[k] {
			continue
		}
		switch {
		case k == 1:
			t[0] += ps.amp[k]
		case k <= 4:
			t[1] += ps.amp[k]
		default:
			t[2] += ps.amp[k]
		}
	}
	for i := range t {
		t[i] /= total
	}
	return t, true
}

// partialLevelRMSEDB is the RMS level difference over the partials both signals
// share, in dB. Returns NaN when nothing can be matched.
func partialLevelRMSEDB(ref partialSet, cand partialSet) float64 {
	var sum float64
	var n int
	for k := 1; k <= maxPartials; k++ {
		if !ref.found[k] || !cand.found[k] {
			continue
		}
		d := linToDB(ref.amp[k]) - linToDB(cand.amp[k])
		sum += d * d
		n++
	}
	if n == 0 {
		return math.NaN()
	}
	return math.Sqrt(sum / float64(n))
}

// partialFreqRMSECents is the RMS frequency deviation over the shared partials,
// in cents. It captures inharmonicity and tuning mismatch.
func partialFreqRMSECents(ref partialSet, cand partialSet) float64 {
	var sum float64
	var n int
	for k := 1; k <= maxPartials; k++ {
		if !ref.found[k] || !cand.found[k] {
			continue
		}
		if ref.freq[k] <= 0 || cand.freq[k] <= 0 {
			continue
		}
		d := 1200.0 * math.Log2(cand.freq[k]/ref.freq[k])
		sum += d * d
		n++
	}
	if n == 0 {
		return math.NaN()
	}
	return math.Sqrt(sum / float64(n))
}

// tristimulusDistance is the Euclidean distance between the two tristimulus
// triples. Returns NaN when either side has no usable harmonic energy.
func tristimulusDistance(ref partialSet, cand partialSet) float64 {
	tr, ok := ref.tristimulus()
	if !ok {
		return math.NaN()
	}
	tc, ok := cand.tristimulus()
	if !ok {
		return math.NaN()
	}
	var sum float64
	for i := 0; i < 3; i++ {
		d := tr[i] - tc[i]
		sum += d * d
	}
	return math.Sqrt(sum)
}

// decaySegmentRMSEDBPerS splits the reference decay into three equal segments
// and compares the fitted slope of each segment against the candidate's slope
// over the same frames. Returns NaN when the decay is too short to segment.
func decaySegmentRMSEDBPerS(refEnv []float64, candEnv []float64, hopSec float64) float64 {
	const segments = 3
	const minFramesPerSegment = 6

	n := len(refEnv)
	if len(candEnv) < n {
		n = len(candEnv)
	}
	if hopSec <= 0 || n < segments*minFramesPerSegment {
		return math.NaN()
	}

	peakIdx := 0
	peakDB := -math.MaxFloat64
	for i := 0; i < n; i++ {
		if db := linToDB(refEnv[i]); db > peakDB {
			peakDB = db
			peakIdx = i
		}
	}
	start := peakIdx + 1
	threshold := peakDB - 60.0
	end := n
	for i := start; i < n; i++ {
		if linToDB(refEnv[i]) < threshold {
			end = i
			break
		}
	}
	span := end - start
	if span < segments*minFramesPerSegment {
		return math.NaN()
	}

	segLen := span / segments
	var sum float64
	var cnt int
	for s := 0; s < segments; s++ {
		lo := start + s*segLen
		hi := lo + segLen
		if s == segments-1 {
			hi = end
		}
		sr := fitSlopeDBPerS(refEnv, lo, hi, hopSec)
		sc := fitSlopeDBPerS(candEnv, lo, hi, hopSec)
		if !isFinite(sr) || !isFinite(sc) {
			continue
		}
		d := sr - sc
		sum += d * d
		cnt++
	}
	if cnt == 0 {
		return math.NaN()
	}
	return math.Sqrt(sum / float64(cnt))
}

// fitSlopeDBPerS least-squares fits the dB envelope over [start,end) and
// returns the slope in dB per second, or NaN when the fit is degenerate.
func fitSlopeDBPerS(env []float64, start int, end int, hopSec float64) float64 {
	if start < 0 || end > len(env) || end-start < 3 || hopSec <= 0 {
		return math.NaN()
	}
	var sx, sy, sxx, sxy float64
	n := float64(end - start)
	for i := start; i < end; i++ {
		x := float64(i-start) * hopSec
		y := linToDB(env[i])
		sx += x
		sy += y
		sxx += x * x
		sxy += x * y
	}
	den := n*sxx - sx*sx
	if math.Abs(den) < 1e-12 {
		return math.NaN()
	}
	return (n*sxy - sx*sy) / den
}
