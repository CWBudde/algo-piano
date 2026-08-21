package analysis

import (
	"errors"
	"math"
	"math/cmplx"
	"sync"

	algofft "github.com/cwbudde/algo-fft"
)

var (
	spectralPlanCache sync.Map // map[int]*spectralFFTPlan
	lagPlanCache      sync.Map // map[int]*lagFFTPlan
)

type spectralFFTPlan struct {
	mu   sync.Mutex
	fast *algofft.FastPlanReal[float64, complex128]
	safe *algofft.PlanReal[float64, complex128]
}

type lagFFTPlan struct {
	mu   sync.Mutex
	n    int
	fast *algofft.FastPlanReal[float64, complex128]
	safe *algofft.PlanReal[float64, complex128]

	inA   []float64
	inB   []float64
	specA []complex128
	specB []complex128
	corr  []float64
}

// Score weights for each metric component.
const (
	WeightTime     = 0.30
	WeightEnvelope = 0.25
	WeightSpectral = 0.30
	WeightDecay    = 0.15

	NormTime     = 0.25
	NormEnvelope = 30.0
	NormSpectral = 30.0
	NormDecay    = 40.0
)

// Options configures a comparison run.
type Options struct {
	// Weights selects the scoring profile. The zero value means DefaultWeights.
	Weights Weights
	// F0Hz pins the fundamental used for partial analysis. When 0 the value is
	// derived from MIDINote, and failing that estimated from the reference.
	F0Hz float64
	// MIDINote is the note being compared, or 0 when unknown.
	MIDINote int
	// SkipPartials disables partial, inharmonicity and tristimulus analysis.
	SkipPartials bool
	// SkipAttack disables the attack-transient metric.
	SkipAttack bool
}

// Compare returns objective distance metrics and a combined score in [0,1]
// using the default (legacy-v1) weighting profile.
func Compare(reference []float64, candidate []float64, sampleRate int) Metrics {
	return CompareWithOptions(reference, candidate, sampleRate, Options{})
}

// CompareWithWeights compares two signals using an explicit weighting profile.
func CompareWithWeights(reference []float64, candidate []float64, sampleRate int, w Weights) Metrics {
	return CompareWithOptions(reference, candidate, sampleRate, Options{Weights: w})
}

// CompareWithOptions returns objective distance metrics and a combined score
// in [0,1] under the supplied options.
func CompareWithOptions(reference []float64, candidate []float64, sampleRate int, opt Options) Metrics {
	w := opt.Weights
	if w.isZero() {
		w = DefaultWeights()
	}

	m := Metrics{
		SampleRate:      sampleRate,
		ReferenceFrames: len(reference),
		CandidateFrames: len(candidate),
		ScoreProfile:    w.Name,
	}
	m.markExtendedUndefined()

	if sampleRate <= 0 || len(reference) == 0 || len(candidate) == 0 {
		m.Score = 1.0
		m.Similarity = 0.0
		return m
	}

	ref := trimLeadingSilence(reference, 1e-6)
	cand := trimLeadingSilence(candidate, 1e-6)
	if len(ref) == 0 || len(cand) == 0 {
		m.Score = 1.0
		m.Similarity = 0.0
		return m
	}

	ref = normalizeRMS(ref, 0.1)
	cand = normalizeRMS(cand, 0.1)

	maxLag := sampleRate / 2
	if maxLag < 1 {
		maxLag = 1
	}
	if maxLag > len(ref)-1 {
		maxLag = len(ref) - 1
	}
	if maxLag > len(cand)-1 {
		maxLag = len(cand) - 1
	}
	if maxLag < 1 {
		maxLag = 1
	}
	lag := estimateLag(ref, cand, maxLag)
	m.LagSamples = lag

	refA, candA := alignByLag(ref, cand, lag)
	n := len(refA)
	if len(candA) < n {
		n = len(candA)
	}
	if n < 256 {
		m.Score = 1.0
		m.Similarity = 0.0
		return m
	}
	maxFrames := sampleRate * 12
	if maxFrames > 0 && n > maxFrames {
		n = maxFrames
	}
	refA = refA[:n]
	candA = candA[:n]
	m.AlignedFrames = n

	m.TimeRMSE = rmse(refA, candA)

	refEnv := rmsEnvelope(refA, 256, 128)
	candEnv := rmsEnvelope(candA, 256, 128)
	envN := len(refEnv)
	if len(candEnv) < envN {
		envN = len(candEnv)
	}
	if envN > 0 {
		envDiff := make([]float64, envN)
		for i := 0; i < envN; i++ {
			r := linToDB(refEnv[i])
			c := linToDB(candEnv[i])
			envDiff[i] = r - c
		}
		m.EnvelopeRMSEDB = rms1(envDiff)
	}

	spectResult := spectralRMSEDBMulti(refA, candA, sampleRate)
	m.SpectralRMSEDB = spectResult.overall
	m.SpectralPositions = spectResult.positions
	m.SpectralLowRMSEDB = spectResult.lowRMSE
	m.SpectralMidRMSEDB = spectResult.midRMSE
	m.SpectralHighRMSEDB = spectResult.highRMSE

	hopSec := 128.0 / float64(sampleRate)
	m.RefDecayDBPerS = decaySlopeDBPerS(refEnv, hopSec)
	m.CandDecayDBPerS = decaySlopeDBPerS(candEnv, hopSec)
	if isFinite(m.RefDecayDBPerS) && isFinite(m.CandDecayDBPerS) {
		m.DecayDiffDBPerS = math.Abs(m.RefDecayDBPerS - m.CandDecayDBPerS)
	}

	if !opt.SkipAttack {
		// Deliberately the raw inputs, not the aligned or silence-trimmed
		// ones. Rise time and centroid trajectory are properties of each
		// signal's own onset: lag alignment trims exactly that onset off
		// whichever side starts later, and the 1e-6 silence trim used for
		// alignment cuts the first millisecond off a synthesised note that
		// ramps in from below it. analyzeAttack does its own, far gentler
		// trimming.
		m.applyAttackMetrics(reference, candidate, sampleRate)
	}
	m.DecaySegmentRMSEDBPerS = decaySegmentRMSEDBPerS(refEnv, candEnv, hopSec)

	if !opt.SkipPartials {
		f0, hint := resolveF0(opt)
		m.applyPartialMetrics(refA, candA, sampleRate, f0, hint)
	}

	// Normalize sub-metrics and combine.
	components := Components(m, w)
	m.applyNorms(components)

	var sum, weightTotal float64
	dropped := false
	for _, c := range components {
		// Zero-weight terms are skipped rather than added: x + 0.0 == x
		// exactly, but 0.0 * NaN == NaN, and the extended metrics are
		// legitimately undefined on short windows.
		if c.Weight == 0 {
			continue
		}
		if !c.Available {
			// The component could not be measured at all (a decay-only
			// window has no onset to compare). Folding in a placeholder
			// would calibrate against a number that means nothing, so the
			// term is dropped and the surviving weights renormalized.
			dropped = true
			continue
		}
		sum += c.Weight * c.Norm
		weightTotal += c.Weight
	}
	// Renormalize only when something was actually dropped. A profile whose
	// weights are all present must reproduce its score bit for bit, and
	// dividing by a weightTotal that is 1.0 only to within rounding would not.
	if dropped && weightTotal > 0 {
		sum /= weightTotal
	}
	m.Score = clamp01(sum)
	m.Similarity = clamp01(math.Exp(-4.0 * m.Score))

	// Identify dominant component (highest weighted contribution).
	m.Dominant = dominantComponent(components)

	return m
}

// resolveF0 derives the fundamental to use for partial analysis from the
// options. It returns 0 for "estimate it from the reference", and hint=true
// when the value is a nominal pitch that should be refined against the actual
// spectrum rather than trusted outright: a MIDI note says which key was
// struck, not how the instrument was tuned. Options.F0Hz, by contrast, is an
// explicit pin and is used verbatim.
func resolveF0(opt Options) (float64, bool) {
	if opt.F0Hz > 0 {
		return opt.F0Hz, false
	}
	if opt.MIDINote > 0 {
		return MIDIToHz(opt.MIDINote), true
	}
	return 0, false
}

// markExtendedUndefined sets the extended metrics to NaN. They stay NaN unless
// the corresponding analysis actually produced a value.
func (m *Metrics) markExtendedUndefined() {
	nan := math.NaN()
	m.PartialLevelRMSEDB = nan
	m.PartialFreqRMSECents = nan
	m.TristimulusDistance = nan
	m.DecaySegmentRMSEDBPerS = nan
	m.RefRiseTimeMS = nan
	m.CandRiseTimeMS = nan
	m.AttackRiseDiffMS = nan
	m.RefAttackCentroidHz = nan
	m.CandAttackCentroidHz = nan
	m.AttackCentroidRMSEOct = nan
	m.AttackAvailable = false
}

// applyAttackMetrics fills the attack-transient metrics. AttackAvailable stays
// false when the aligned window carries no rising onset, and the score loop
// then leaves the attack term out entirely.
func (m *Metrics) applyAttackMetrics(refA []float64, candA []float64, sampleRate int) {
	res := analyzeAttack(refA, candA, sampleRate)
	m.RefRiseTimeMS = res.refRiseMS
	m.CandRiseTimeMS = res.candRiseMS
	m.AttackRiseDiffMS = res.riseDiffMS
	m.RefAttackCentroidHz = res.refCentroidHz
	m.CandAttackCentroidHz = res.candCentroidHz
	m.AttackCentroidRMSEOct = res.centroidRMSEOct
	m.AttackAvailable = res.available
}

// applyPartialMetrics fills the partial-derived metrics, leaving them NaN when
// no usable harmonic series could be extracted from both signals.
func (m *Metrics) applyPartialMetrics(refA []float64, candA []float64, sampleRate int, f0 float64, hint bool) {
	var refPS partialSet
	var ok bool
	if hint {
		refPS, ok = analyzePartialsHint(refA, sampleRate, f0)
	} else {
		refPS, ok = analyzePartials(refA, sampleRate, f0)
	}
	if !ok {
		return
	}
	// Both signals are measured against the same fundamental so that a tuning
	// mismatch shows up as a frequency deviation rather than being absorbed.
	candPS, ok := analyzePartials(candA, sampleRate, refPS.f0)
	if !ok {
		return
	}
	m.F0Hz = refPS.f0
	m.PartialLevelRMSEDB = partialLevelRMSEDB(refPS, candPS)
	m.PartialFreqRMSECents = partialFreqRMSECents(refPS, candPS)
	m.TristimulusDistance = tristimulusDistance(refPS, candPS)
}

// applyNorms mirrors the component norms back onto the metric struct.
func (m *Metrics) applyNorms(components []Component) {
	for _, c := range components {
		switch c.Name {
		case ComponentTime:
			m.TimeNorm = c.Norm
			m.TimeSaturated = c.Saturated
		case ComponentEnvelope:
			m.EnvelopeNorm = c.Norm
			m.EnvelopeSaturated = c.Saturated
		case ComponentSpectral:
			m.SpectralNorm = c.Norm
			m.SpectralSaturated = c.Saturated
		case ComponentDecay:
			m.DecayNorm = c.Norm
			m.DecaySaturated = c.Saturated
		case ComponentPartialLevel:
			m.PartialLevelNorm = c.Norm
			m.PartialLevelSaturated = c.Saturated
		case ComponentPartialFreq:
			m.PartialFreqNorm = c.Norm
			m.PartialFreqSaturated = c.Saturated
		case ComponentTristimulus:
			m.TristimulusNorm = c.Norm
			m.TristimulusSaturated = c.Saturated
		case ComponentAttack:
			m.AttackNorm = c.Norm
			m.AttackSaturated = c.Saturated
		case ComponentDecaySegment:
			m.DecaySegmentNorm = c.Norm
			m.DecaySegmentSaturated = c.Saturated
		}
	}
}

// dominantComponent returns the name of the weighted component with the largest
// contribution, considering only components that carry weight.
func dominantComponent(components []Component) string {
	best := ""
	bestVal := math.Inf(-1)
	for _, c := range components {
		if c.Weight == 0 || !c.Available {
			continue
		}
		if best == "" {
			// Fall back to the first weighted component so an undefined
			// (NaN) contribution never leaves Dominant empty.
			best = c.Name
		}
		v := c.Weight * c.Norm
		if v > bestVal {
			bestVal = v
			best = c.Name
		}
	}
	return best
}

func trimLeadingSilence(x []float64, threshold float64) []float64 {
	for i := 0; i < len(x); i++ {
		if math.Abs(x[i]) > threshold {
			return x[i:]
		}
	}
	return nil
}

func normalizeRMS(x []float64, target float64) []float64 {
	if len(x) == 0 {
		return x
	}
	r := rms1(x)
	if r <= 1e-12 {
		return append([]float64(nil), x...)
	}
	g := target / r
	out := make([]float64, len(x))
	for i := range x {
		out[i] = x[i] * g
	}
	return out
}

func estimateLag(ref []float64, cand []float64, maxLag int) int {
	if len(ref) == 0 || len(cand) == 0 {
		return 0
	}
	if maxLag < 1 {
		return 0
	}
	if maxLag > len(ref)-1 {
		maxLag = len(ref) - 1
	}
	if maxLag > len(cand)-1 {
		maxLag = len(cand) - 1
	}
	if maxLag < 1 {
		return 0
	}
	if lag, ok := estimateLagFFT(ref, cand, maxLag); ok {
		return lag
	}
	return estimateLagExhaustive(ref, cand, maxLag)
}

func estimateLagExhaustive(ref []float64, cand []float64, maxLag int) int {
	step := 2
	if len(ref) > 200000 || len(cand) > 200000 {
		step = 4
	}
	bestLag := 0
	best := math.Inf(-1)
	for lag := -maxLag; lag <= maxLag; lag++ {
		s := dotAtLag(ref, cand, lag, step)
		if s > best {
			best = s
			bestLag = lag
		}
	}
	return bestLag
}

func estimateLagFFT(ref []float64, cand []float64, maxLag int) (int, bool) {
	nfft := nextPow2(len(ref) + len(cand) - 1)
	if nfft < 2 {
		nfft = 2
	}
	plan, err := getLagFFTPlan(nfft)
	if err != nil {
		return 0, false
	}

	plan.mu.Lock()
	defer plan.mu.Unlock()

	clear(plan.inA)
	clear(plan.inB)
	copy(plan.inA, ref)
	copy(plan.inB, cand)

	if err := plan.forward(plan.specA, plan.inA); err != nil {
		return 0, false
	}
	if err := plan.forward(plan.specB, plan.inB); err != nil {
		return 0, false
	}
	for i := range plan.specA {
		plan.specA[i] *= cmplx.Conj(plan.specB[i])
	}
	if err := plan.inverse(plan.corr, plan.specA); err != nil {
		return 0, false
	}

	bestLag := 0
	best := math.Inf(-1)
	for lag := -maxLag; lag <= maxLag; lag++ {
		idx := lag
		if idx < 0 {
			idx += plan.n
		}
		s := plan.corr[idx]
		if s > best {
			best = s
			bestLag = lag
		}
	}
	return bestLag, true
}

func getLagFFTPlan(n int) (*lagFFTPlan, error) {
	if v, ok := lagPlanCache.Load(n); ok {
		return v.(*lagFFTPlan), nil
	}

	p := &lagFFTPlan{
		n:     n,
		inA:   make([]float64, n),
		inB:   make([]float64, n),
		specA: make([]complex128, n/2+1),
		specB: make([]complex128, n/2+1),
		corr:  make([]float64, n),
	}

	fast, err := algofft.NewFastPlanReal64(n)
	// Any fast-plan setup failure, ErrNotImplemented included, simply leaves
	// p.fast nil so the safe plan below is used instead.
	if err == nil {
		p.fast = fast
	}

	safe, err := algofft.NewPlanReal64(n)
	if err != nil {
		if p.fast == nil {
			return nil, err
		}
	} else {
		p.safe = safe
	}

	actual, _ := lagPlanCache.LoadOrStore(n, p)
	return actual.(*lagFFTPlan), nil
}

func (p *lagFFTPlan) forward(dst []complex128, src []float64) error {
	if p.fast != nil {
		p.fast.Forward(dst, src)
		return nil
	}
	if p.safe != nil {
		return p.safe.Forward(dst, src)
	}
	return errors.New("analysis: missing lag FFT forward plan")
}

func (p *lagFFTPlan) inverse(dst []float64, src []complex128) error {
	if p.fast != nil {
		p.fast.Inverse(dst, src)
		return nil
	}
	if p.safe != nil {
		return p.safe.Inverse(dst, src)
	}
	return errors.New("analysis: missing lag FFT inverse plan")
}

func nextPow2(n int) int {
	if n <= 1 {
		return 1
	}
	p := 1
	for p < n {
		p <<= 1
	}
	return p
}

func dotAtLag(a []float64, b []float64, lag int, step int) float64 {
	var ai, bi int
	if lag >= 0 {
		ai = lag
		bi = 0
	} else {
		ai = 0
		bi = -lag
	}
	n := len(a) - ai
	if len(b)-bi < n {
		n = len(b) - bi
	}
	if n <= 0 {
		return 0
	}
	var sum float64
	for i := 0; i < n; i += step {
		sum += a[ai+i] * b[bi+i]
	}
	return sum
}

func alignByLag(ref []float64, cand []float64, lag int) ([]float64, []float64) {
	if lag >= 0 {
		if lag >= len(ref) {
			return nil, nil
		}
		return ref[lag:], cand
	}
	o := -lag
	if o >= len(cand) {
		return nil, nil
	}
	return ref, cand[o:]
}

func rmse(a []float64, b []float64) float64 {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	if n == 0 {
		return 0
	}
	var sum float64
	for i := 0; i < n; i++ {
		d := a[i] - b[i]
		sum += d * d
	}
	return math.Sqrt(sum / float64(n))
}

func rms1(x []float64) float64 {
	if len(x) == 0 {
		return 0
	}
	var sum float64
	for _, v := range x {
		sum += v * v
	}
	return math.Sqrt(sum / float64(len(x)))
}

func rmsEnvelope(x []float64, frame int, hop int) []float64 {
	if frame <= 0 || hop <= 0 || len(x) < frame {
		return nil
	}
	n := 1 + (len(x)-frame)/hop
	out := make([]float64, n)
	for i := 0; i < n; i++ {
		start := i * hop
		out[i] = rms1(x[start : start+frame])
	}
	return out
}

type spectralResult struct {
	overall   float64
	positions []SpectralPosition
	lowRMSE   float64 // 0-500 Hz
	midRMSE   float64 // 500-2000 Hz
	highRMSE  float64 // 2000+ Hz
}

// Phase weights for early/sustain/decay portions of the signal.
// Early attack carries the most perceptual weight (timbral identity),
// sustain is next, and decay is least critical.
const (
	phaseWeightAttack  = 0.40
	phaseWeightSustain = 0.35
	phaseWeightDecay   = 0.25
)

// spectralRMSEDBMulti computes spectral RMSE across multiple time positions
// with phase-aware weighting (attack > sustain > decay) and per-band breakdown.
func spectralRMSEDBMulti(a []float64, b []float64, sampleRate int) spectralResult {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	if n < 512 {
		return spectralResult{}
	}

	winSize := 4096
	if n < winSize {
		winSize = n
	}
	winSize &^= 1 // Round down to even for FFT.
	if winSize < 512 {
		v := spectralRMSEDB(a, b)
		return spectralResult{overall: v}
	}

	// Determine phase boundaries using RMS envelope of the reference.
	env := rmsEnvelope(a[:n], 256, 128)
	attackEnd, sustainEnd := detectPhases(env, 128)

	// Sample up to 8 positions spread across the signal for finer coverage.
	nPos := 8
	positions := make([]int, 0, nPos)
	if n <= winSize {
		positions = append(positions, 0)
	} else {
		stride := (n - winSize) / (nPos - 1)
		if stride < 1 {
			stride = 1
		}
		for i := 0; i < nPos; i++ {
			pos := i * stride
			if pos+winSize > n {
				pos = n - winSize
			}
			positions = append(positions, pos)
		}
	}

	plan, err := getSpectralFFTPlan(winSize)
	bins := winSize / 2
	hann := hannWindow(winSize)

	sc := getScratch(winSize)
	defer putScratch(winSize, sc)
	aw, bw := sc.aw, sc.bw

	// Band boundaries in bins.
	binHz := float64(sampleRate) / float64(winSize)
	lowBinEnd := int(500.0/binHz) + 1
	midBinEnd := int(2000.0/binHz) + 1
	if lowBinEnd < 1 {
		lowBinEnd = 1
	}
	if midBinEnd < lowBinEnd {
		midBinEnd = lowBinEnd
	}
	if lowBinEnd > bins {
		lowBinEnd = bins
	}
	if midBinEnd > bins {
		midBinEnd = bins
	}

	type bandAccum struct {
		sum float64
		cnt int
	}

	var weightedSum, weightTotal float64
	detail := make([]SpectralPosition, 0, len(positions))
	var bandLow, bandMid, bandHigh bandAccum

	for _, pos := range positions {
		// Determine phase weight for this window position.
		centerSample := pos + winSize/2
		weight := phaseWeight(centerSample, attackEnd, sustainEnd)

		for i := 0; i < winSize; i++ {
			aw[i] = a[pos+i] * hann[i]
			bw[i] = b[pos+i] * hann[i]
		}

		var posSum float64
		var lowSum, midSum, highSum float64
		cnt := bins - 1

		computeBins := func(getMag func(k int) (float64, float64)) {
			for k := 1; k < bins; k++ {
				da, db := getMag(k)
				d := da - db
				dsq := d * d
				posSum += dsq
				if k < lowBinEnd {
					lowSum += dsq
					bandLow.cnt++
				} else if k < midBinEnd {
					midSum += dsq
					bandMid.cnt++
				} else {
					highSum += dsq
					bandHigh.cnt++
				}
			}
		}

		computed := false
		if err == nil {
			specA, specB := sc.specA, sc.specB
			if e := plan.forward(specA, aw); e == nil {
				if e := plan.forward(specB, bw); e == nil {
					computeBins(func(k int) (float64, float64) {
						return linToDB(cmplx.Abs(specA[k])), linToDB(cmplx.Abs(specB[k]))
					})
					computed = true
				}
			}
		}
		if !computed {
			computeBins(func(k int) (float64, float64) {
				return linToDB(dftBinMag(aw, k)), linToDB(dftBinMag(bw, k))
			})
		}

		bandLow.sum += lowSum
		bandMid.sum += midSum
		bandHigh.sum += highSum

		posRMSE := math.Sqrt(posSum / float64(cnt))
		weightedSum += weight * posSum / float64(cnt)
		weightTotal += weight
		detail = append(detail, SpectralPosition{
			OffsetSec: float64(pos) / float64(sampleRate),
			RMSEDB:    posRMSE,
		})
	}

	var result spectralResult
	result.positions = detail
	if weightTotal > 0 {
		result.overall = math.Sqrt(weightedSum / weightTotal)
	}
	if bandLow.cnt > 0 {
		result.lowRMSE = math.Sqrt(bandLow.sum / float64(bandLow.cnt))
	}
	if bandMid.cnt > 0 {
		result.midRMSE = math.Sqrt(bandMid.sum / float64(bandMid.cnt))
	}
	if bandHigh.cnt > 0 {
		result.highRMSE = math.Sqrt(bandHigh.sum / float64(bandHigh.cnt))
	}
	return result
}

// detectPhases finds the sample indices marking the end of the attack phase
// and the end of the sustain phase, using the RMS envelope.
// Attack ends at the first envelope peak. Sustain ends when the envelope
// drops 20 dB below the peak.
func detectPhases(env []float64, envHop int) (attackEndSample int, sustainEndSample int) {
	if len(env) == 0 {
		return 0, 0
	}

	// Find peak envelope frame.
	peakIdx := 0
	peakVal := env[0]
	for i, v := range env {
		if v > peakVal {
			peakVal = v
			peakIdx = i
		}
	}
	attackEndSample = (peakIdx + 1) * envHop

	// Sustain ends when envelope drops 20 dB from peak.
	thresholdDB := linToDB(peakVal) - 20.0
	sustainEndSample = len(env) * envHop // default: whole signal is sustain
	for i := peakIdx; i < len(env); i++ {
		if linToDB(env[i]) < thresholdDB {
			sustainEndSample = i * envHop
			break
		}
	}
	return attackEndSample, sustainEndSample
}

// phaseWeight returns the weight for a spectral window centered at the given sample,
// based on which phase of the signal it falls in.
func phaseWeight(centerSample int, attackEnd int, sustainEnd int) float64 {
	if centerSample < attackEnd {
		return phaseWeightAttack
	}
	if centerSample < sustainEnd {
		return phaseWeightSustain
	}
	return phaseWeightDecay
}

func spectralRMSEDB(a []float64, b []float64) float64 {
	n := spectralWindowLen(a, b)
	bins := n / 2
	if bins < 2 {
		return 0
	}

	sc := getScratch(n)
	defer putScratch(n, sc)
	aw, bw := sc.aw, sc.bw
	hann := hannWindow(n)
	for i := 0; i < n; i++ {
		aw[i] = a[i] * hann[i]
		bw[i] = b[i] * hann[i]
	}

	plan, err := getSpectralFFTPlan(n)
	if err != nil {
		return spectralRMSEDBNaiveWindowed(aw, bw, bins)
	}
	specA, specB := sc.specA, sc.specB
	if err := plan.forward(specA, aw); err != nil {
		return spectralRMSEDBNaiveWindowed(aw, bw, bins)
	}
	if err := plan.forward(specB, bw); err != nil {
		return spectralRMSEDBNaiveWindowed(aw, bw, bins)
	}

	var sum float64
	for k := 1; k < bins; k++ {
		ma := cmplx.Abs(specA[k])
		mb := cmplx.Abs(specB[k])
		da := linToDB(ma)
		db := linToDB(mb)
		d := da - db
		sum += d * d
	}
	return math.Sqrt(sum / float64(bins-1))
}

// spectralWindowLen returns the analysis length shared by a and b, or 0 when
// the signals are too short for a meaningful spectral comparison.
func spectralWindowLen(a []float64, b []float64) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	if n < 512 {
		return 0
	}
	if n > 4096 {
		n = 4096
	}
	// Real FFT plans require an even length.
	if n%2 != 0 {
		n--
	}
	if n < 512 {
		return 0
	}
	return n
}

// spectralWindowedInputs returns freshly allocated Hann-windowed copies of a
// and b plus the usable bin count. It is kept for callers that need to own the
// buffers; the hot path uses the pooled scratch buffers instead.
func spectralWindowedInputs(a []float64, b []float64) ([]float64, []float64, int) {
	n := spectralWindowLen(a, b)
	if n == 0 {
		return nil, nil, 0
	}
	hann := hannWindow(n)
	aw := make([]float64, n)
	bw := make([]float64, n)
	for i := 0; i < n; i++ {
		aw[i] = a[i] * hann[i]
		bw[i] = b[i] * hann[i]
	}
	return aw, bw, n / 2
}

func getSpectralFFTPlan(n int) (*spectralFFTPlan, error) {
	if v, ok := spectralPlanCache.Load(n); ok {
		return v.(*spectralFFTPlan), nil
	}

	p := &spectralFFTPlan{}

	fast, err := algofft.NewFastPlanReal64(n)
	// Any fast-plan setup failure, ErrNotImplemented included, simply leaves
	// p.fast nil so the safe plan below is used instead.
	if err == nil {
		p.fast = fast
	}

	safe, err := algofft.NewPlanReal64(n)
	if err != nil {
		if p.fast == nil {
			return nil, err
		}
	} else {
		p.safe = safe
	}

	actual, _ := spectralPlanCache.LoadOrStore(n, p)
	return actual.(*spectralFFTPlan), nil
}

func (p *spectralFFTPlan) forward(dst []complex128, src []float64) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.fast != nil {
		p.fast.Forward(dst, src)
		return nil
	}
	if p.safe != nil {
		return p.safe.Forward(dst, src)
	}
	return errors.New("analysis: missing FFT plan")
}

func spectralRMSEDBNaiveWindowed(aw []float64, bw []float64, bins int) float64 {
	if bins < 2 {
		return 0
	}
	var sum float64
	for k := 1; k < bins; k++ {
		ma := dftBinMag(aw, k)
		mb := dftBinMag(bw, k)
		da := linToDB(ma)
		db := linToDB(mb)
		d := da - db
		sum += d * d
	}
	return math.Sqrt(sum / float64(bins-1))
}

func dftBinMag(x []float64, bin int) float64 {
	n := len(x)
	var re, im float64
	for i := 0; i < n; i++ {
		phi := -2.0 * math.Pi * float64(bin*i) / float64(n)
		re += x[i] * math.Cos(phi)
		im += x[i] * math.Sin(phi)
	}
	return math.Hypot(re, im)
}

func linToDB(x float64) float64 {
	if x < 1e-12 {
		x = 1e-12
	}
	return 20.0 * math.Log10(x)
}

func decaySlopeDBPerS(env []float64, hopSec float64) float64 {
	if len(env) < 8 || hopSec <= 0 {
		return math.NaN()
	}
	peak := -math.MaxFloat64
	peakIdx := 0
	for i, v := range env {
		db := linToDB(v)
		if db > peak {
			peak = db
			peakIdx = i
		}
	}
	start := peakIdx + 1
	if start >= len(env)-4 {
		return math.NaN()
	}

	threshold := peak - 60.0
	end := len(env)
	for i := start; i < len(env); i++ {
		if linToDB(env[i]) < threshold {
			end = i
			break
		}
	}
	if end-start < 6 {
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

func clamp01(x float64) float64 {
	if x < 0 {
		return 0
	}
	if x > 1 {
		return 1
	}
	return x
}

func isFinite(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}
