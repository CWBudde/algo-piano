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
	fast *algofft.FastPlanReal64
	safe *algofft.PlanRealT[float64, complex128]
}

type lagFFTPlan struct {
	mu   sync.Mutex
	n    int
	fast *algofft.FastPlanReal64
	safe *algofft.PlanRealT[float64, complex128]

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

// Metrics contains distance and similarity measurements between two audio signals.
type Metrics struct {
	SampleRate int `json:"sample_rate"`

	ReferenceFrames int `json:"reference_frames"`
	CandidateFrames int `json:"candidate_frames"`
	AlignedFrames   int `json:"aligned_frames"`
	LagSamples      int `json:"lag_samples"`

	TimeRMSE        float64 `json:"time_rmse"`
	EnvelopeRMSEDB  float64 `json:"envelope_rmse_db"`
	SpectralRMSEDB  float64 `json:"spectral_rmse_db"`
	RefDecayDBPerS  float64 `json:"ref_decay_db_per_s"`
	CandDecayDBPerS float64 `json:"cand_decay_db_per_s"`
	DecayDiffDBPerS float64 `json:"decay_diff_db_per_s"`

	// Per-position spectral detail (evenly spaced across signal).
	SpectralPositions []SpectralPosition `json:"spectral_positions,omitempty"`

	// Normalized component contributions (0-1 each, weighted sum = Score).
	TimeNorm     float64 `json:"time_norm"`
	EnvelopeNorm float64 `json:"envelope_norm"`
	SpectralNorm float64 `json:"spectral_norm"`
	DecayNorm    float64 `json:"decay_norm"`
	Dominant     string  `json:"dominant"` // name of the highest-contributing component

	Score      float64 `json:"score"`
	Similarity float64 `json:"similarity"`
}

// SpectralPosition records spectral RMSE at a specific time offset.
type SpectralPosition struct {
	OffsetSec float64 `json:"offset_sec"`
	RMSEDB    float64 `json:"rmse_db"`
}

// Compare returns objective distance metrics and a combined score in [0,1].
func Compare(reference []float64, candidate []float64, sampleRate int) Metrics {
	m := Metrics{
		SampleRate:      sampleRate,
		ReferenceFrames: len(reference),
		CandidateFrames: len(candidate),
	}
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

	m.SpectralRMSEDB, m.SpectralPositions = spectralRMSEDBMulti(refA, candA, sampleRate)

	hopSec := 128.0 / float64(sampleRate)
	m.RefDecayDBPerS = decaySlopeDBPerS(refEnv, hopSec)
	m.CandDecayDBPerS = decaySlopeDBPerS(candEnv, hopSec)
	if isFinite(m.RefDecayDBPerS) && isFinite(m.CandDecayDBPerS) {
		m.DecayDiffDBPerS = math.Abs(m.RefDecayDBPerS - m.CandDecayDBPerS)
	}

	// Normalize sub-metrics and combine.
	m.TimeNorm = clamp01(m.TimeRMSE / NormTime)
	m.EnvelopeNorm = clamp01(m.EnvelopeRMSEDB / NormEnvelope)
	m.SpectralNorm = clamp01(m.SpectralRMSEDB / NormSpectral)
	m.DecayNorm = clamp01(m.DecayDiffDBPerS / NormDecay)
	m.Score = clamp01(WeightTime*m.TimeNorm + WeightEnvelope*m.EnvelopeNorm + WeightSpectral*m.SpectralNorm + WeightDecay*m.DecayNorm)
	m.Similarity = clamp01(math.Exp(-4.0 * m.Score))

	// Identify dominant component (highest weighted contribution).
	type comp struct {
		name string
		val  float64
	}
	comps := []comp{
		{"time", WeightTime * m.TimeNorm},
		{"envelope", WeightEnvelope * m.EnvelopeNorm},
		{"spectral", WeightSpectral * m.SpectralNorm},
		{"decay", WeightDecay * m.DecayNorm},
	}
	best := comps[0]
	for _, c := range comps[1:] {
		if c.val > best.val {
			best = c
		}
	}
	m.Dominant = best.name

	return m
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
	if err == nil {
		p.fast = fast
	} else if !errors.Is(err, algofft.ErrNotImplemented) {
		// Ignore fast-plan setup errors and rely on the safe plan.
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

// spectralRMSEDBMulti computes spectral RMSE across multiple time positions,
// giving a more representative comparison than a single early window.
// It also returns per-position detail for diagnostics.
func spectralRMSEDBMulti(a []float64, b []float64, sampleRate int) (float64, []SpectralPosition) {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	if n < 512 {
		return 0, nil
	}

	winSize := 4096
	if n < winSize {
		winSize = n
	}
	// Round down to even for FFT.
	winSize &^= 1
	if winSize < 512 {
		return spectralRMSEDB(a, b), nil
	}

	// Sample up to 5 positions spread across the signal.
	positions := make([]int, 0, 5)
	if n <= winSize {
		positions = append(positions, 0)
	} else {
		nPos := 5
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
	hann := make([]float64, winSize)
	for i := range hann {
		hann[i] = 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(winSize-1))
	}

	var totalSum float64
	totalCnt := 0
	detail := make([]SpectralPosition, 0, len(positions))

	for _, pos := range positions {
		aw := make([]float64, winSize)
		bw := make([]float64, winSize)
		for i := 0; i < winSize; i++ {
			aw[i] = a[pos+i] * hann[i]
			bw[i] = b[pos+i] * hann[i]
		}

		var sum float64
		cnt := bins - 1
		if err == nil {
			specA := make([]complex128, bins+1)
			specB := make([]complex128, bins+1)
			if e := plan.forward(specA, aw); e == nil {
				if e := plan.forward(specB, bw); e == nil {
					for k := 1; k < bins; k++ {
						da := linToDB(cmplx.Abs(specA[k]))
						db := linToDB(cmplx.Abs(specB[k]))
						d := da - db
						sum += d * d
					}
					totalSum += sum
					totalCnt += cnt
					detail = append(detail, SpectralPosition{
						OffsetSec: float64(pos) / float64(sampleRate),
						RMSEDB:    math.Sqrt(sum / float64(cnt)),
					})
					continue
				}
			}
		}
		// Fallback: naive DFT for this position.
		for k := 1; k < bins; k++ {
			da := linToDB(dftBinMag(aw, k))
			db := linToDB(dftBinMag(bw, k))
			d := da - db
			sum += d * d
		}
		totalSum += sum
		totalCnt += cnt
		detail = append(detail, SpectralPosition{
			OffsetSec: float64(pos) / float64(sampleRate),
			RMSEDB:    math.Sqrt(sum / float64(cnt)),
		})
	}

	if totalCnt == 0 {
		return 0, nil
	}
	return math.Sqrt(totalSum / float64(totalCnt)), detail
}

func spectralRMSEDB(a []float64, b []float64) float64 {
	aw, bw, bins := spectralWindowedInputs(a, b)
	if bins < 2 {
		return 0
	}

	plan, err := getSpectralFFTPlan(len(aw))
	if err != nil {
		return spectralRMSEDBNaiveWindowed(aw, bw, bins)
	}
	specA := make([]complex128, bins+1)
	specB := make([]complex128, bins+1)
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

func spectralWindowedInputs(a []float64, b []float64) ([]float64, []float64, int) {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	if n < 512 {
		return nil, nil, 0
	}
	if n > 4096 {
		n = 4096
	}
	// Real FFT plans require an even length.
	if n%2 != 0 {
		n--
	}
	if n < 512 {
		return nil, nil, 0
	}

	aw := make([]float64, n)
	bw := make([]float64, n)
	for i := 0; i < n; i++ {
		w := 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(n-1))
		aw[i] = a[i] * w
		bw[i] = b[i] * w
	}
	return aw, bw, n / 2
}

func getSpectralFFTPlan(n int) (*spectralFFTPlan, error) {
	if v, ok := spectralPlanCache.Load(n); ok {
		return v.(*spectralFFTPlan), nil
	}

	p := &spectralFFTPlan{}

	fast, err := algofft.NewFastPlanReal64(n)
	if err == nil {
		p.fast = fast
	} else if !errors.Is(err, algofft.ErrNotImplemented) {
		// Ignore fast-plan setup errors and rely on the safe plan.
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
