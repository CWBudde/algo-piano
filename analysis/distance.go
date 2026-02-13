package analysis

import (
	"math"
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

	Score      float64 `json:"score"`
	Similarity float64 `json:"similarity"`
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

	m.SpectralRMSEDB = spectralRMSEDB(refA, candA)

	hopSec := 128.0 / float64(sampleRate)
	m.RefDecayDBPerS = decaySlopeDBPerS(refEnv, hopSec)
	m.CandDecayDBPerS = decaySlopeDBPerS(candEnv, hopSec)
	if isFinite(m.RefDecayDBPerS) && isFinite(m.CandDecayDBPerS) {
		m.DecayDiffDBPerS = math.Abs(m.RefDecayDBPerS - m.CandDecayDBPerS)
	}

	// Normalize sub-metrics and combine.
	timeNorm := clamp01(m.TimeRMSE / 0.25)
	envNorm := clamp01(m.EnvelopeRMSEDB / 30.0)
	specNorm := clamp01(m.SpectralRMSEDB / 30.0)
	decNorm := clamp01(m.DecayDiffDBPerS / 40.0)
	m.Score = clamp01(0.30*timeNorm + 0.25*envNorm + 0.30*specNorm + 0.15*decNorm)
	m.Similarity = clamp01(math.Exp(-4.0 * m.Score))

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

func spectralRMSEDB(a []float64, b []float64) float64 {
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
	aw := make([]float64, n)
	bw := make([]float64, n)
	for i := 0; i < n; i++ {
		w := 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(n-1))
		aw[i] = a[i] * w
		bw[i] = b[i] * w
	}
	bins := n / 2
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
