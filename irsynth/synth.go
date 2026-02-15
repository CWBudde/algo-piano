package irsynth

import (
	"fmt"
	"math"
	"math/rand"
	"sort"
)

// Config controls synthetic IR generation.
type Config struct {
	SampleRate int
	DurationS  float64
	Modes      int
	Seed       int64

	Brightness  float64
	Density     float64 // Controls mode frequency clustering: >1 biases low, <1 biases high
	StereoWidth float64
	DirectLevel float64
	EarlyCount  int
	LateLevel   float64

	LowDecayS  float64
	HighDecayS float64

	NormalizePeak float64
}

func DefaultConfig() Config {
	return Config{
		SampleRate:    96000,
		DurationS:     2.0,
		Modes:         128,
		Seed:          1,
		Brightness:    1.0,
		Density:       2.0,
		StereoWidth:   0.6,
		DirectLevel:   0.6,
		EarlyCount:    16,
		LateLevel:     0.045,
		LowDecayS:     2.4,
		HighDecayS:    0.35,
		NormalizePeak: 0.9,
	}
}

func (c *Config) Validate() error {
	if c.SampleRate < 8000 {
		return fmt.Errorf("sample rate too low: %d", c.SampleRate)
	}
	if c.DurationS <= 0 {
		return fmt.Errorf("duration must be > 0")
	}
	if c.Modes < 1 {
		return fmt.Errorf("modes must be >= 1")
	}
	if c.Brightness <= 0 {
		return fmt.Errorf("brightness must be > 0")
	}
	if c.Density <= 0 {
		return fmt.Errorf("density must be > 0")
	}
	if c.StereoWidth < 0 {
		return fmt.Errorf("stereo width must be >= 0")
	}
	if c.DirectLevel < 0 {
		return fmt.Errorf("direct level must be >= 0")
	}
	if c.EarlyCount < 0 {
		return fmt.Errorf("early count must be >= 0")
	}
	if c.LateLevel < 0 {
		return fmt.Errorf("late level must be >= 0")
	}
	if c.LowDecayS <= 0 || c.HighDecayS <= 0 {
		return fmt.Errorf("decay seconds must be > 0")
	}
	if c.NormalizePeak <= 0 {
		return fmt.Errorf("normalize peak must be > 0")
	}
	return nil
}

// GenerateStereo synthesizes a stereo IR according to cfg.
func GenerateStereo(cfg Config) ([]float32, []float32, error) {
	if err := cfg.Validate(); err != nil {
		return nil, nil, err
	}

	n := int(math.Round(cfg.DurationS * float64(cfg.SampleRate)))
	if n < 1 {
		n = 1
	}
	left := make([]float64, n)
	right := make([]float64, n)

	rng := rand.New(rand.NewSource(cfg.Seed))

	// Direct path impulse.
	left[0] += cfg.DirectLevel * (1.0 - 0.05*cfg.StereoWidth)
	right[0] += cfg.DirectLevel * (1.0 + 0.05*cfg.StereoWidth)

	maxF := 0.47 * float64(cfg.SampleRate)
	if maxF < 500.0 {
		maxF = 500.0
	}
	minF := 35.0
	if minF >= maxF {
		minF = maxF * 0.5
	}

	// Modal body contribution with deterministic frequency placement.
	// Modes are log-spaced with density-controlled clustering instead of RNG-drawn.
	// RNG is only used for amplitude jitter, phase, and stereo pan (non-critical).
	for m := 0; m < cfg.Modes; m++ {
		fNorm := math.Pow((float64(m)+0.5)/float64(cfg.Modes), cfg.Density)
		f := minF * math.Pow(maxF/minF, fNorm)

		brightnessExp := 0.7 + 0.9*cfg.Brightness
		amp := 0.9 / math.Pow(1.0+f/120.0, brightnessExp)
		amp *= 0.7 + 0.6*rng.Float64()

		tau := lerp(cfg.LowDecayS, cfg.HighDecayS, math.Sqrt(f/maxF))
		decay := math.Exp(-1.0 / (tau * float64(cfg.SampleRate)))

		pan := (rng.Float64()*2.0 - 1.0) * cfg.StereoWidth
		lGain := 1.0 - 0.45*pan
		rGain := 1.0 + 0.45*pan
		fSkew := 0.004 * pan
		fL := f * (1.0 - fSkew)
		fR := f * (1.0 + fSkew)

		phi := rng.Float64() * 2.0 * math.Pi
		addModeRec(left, amp*lGain, fL, phi, decay, cfg.SampleRate)
		addModeRec(right, amp*rGain, fR, phi+0.01*pan, decay, cfg.SampleRate)
	}

	// Early reflections cluster.
	for i := 0; i < cfg.EarlyCount; i++ {
		t := 0.001 + 0.030*rng.Float64()
		idx := int(t * float64(cfg.SampleRate))
		if idx <= 0 || idx >= n {
			continue
		}
		amp := (0.10 + 0.35*rng.Float64()) * math.Exp(-t*28.0)
		pan := (rng.Float64()*2.0 - 1.0) * cfg.StereoWidth
		left[idx] += amp * (1.0 - 0.5*pan)
		right[idx] += amp * (1.0 + 0.5*pan)
	}

	// Diffuse late tail.
	if cfg.LateLevel > 0 {
		lpL := 0.0
		lpR := 0.0
		for i := 0; i < n; i++ {
			t := float64(i) / float64(cfg.SampleRate)
			env := math.Exp(-t / (0.75 * cfg.LowDecayS))
			nL := rng.NormFloat64()
			nR := rng.NormFloat64()
			lpL = 0.985*lpL + 0.015*nL
			lpR = 0.985*lpR + 0.015*nR
			left[i] += cfg.LateLevel * env * lpL
			right[i] += cfg.LateLevel * env * lpR
		}
	}

	// Remove tiny DC drift.
	highpassDC(left, 0.995)
	highpassDC(right, 0.995)

	// Normalize.
	peak := maxAbs(left)
	if rp := maxAbs(right); rp > peak {
		peak = rp
	}
	if peak < 1e-12 {
		peak = 1e-12
	}
	s := cfg.NormalizePeak / peak
	outL := make([]float32, n)
	outR := make([]float32, n)
	for i := 0; i < n; i++ {
		outL[i] = float32(left[i] * s)
		outR[i] = float32(right[i] * s)
	}
	return outL, outR, nil
}

// plateEigenfreqs computes eigenfrequencies for a simply-supported orthotropic
// rectangular plate and returns up to maxModes frequencies in [f11, maxF].
// R = Lx/Ly (plate ratio), S = Dx/Dy (stiffness ratio).
func plateEigenfreqs(f11, maxF float64, maxModes int, R, S float64) []float64 {
	sqrtS := math.Sqrt(S)
	R2 := R * R
	R4 := R2 * R2
	denom := math.Sqrt(S + 2*sqrtS*R2 + R4)

	// Upper bound on mode indices: f_{m,1} ~ f11 * S^0.5 * m^2 / denom,
	// so m_max ~ sqrt(maxF/f11 * denom / sqrt(S)) + 1.
	mMax := int(math.Sqrt(maxF/f11*denom/sqrtS)) + 2
	nMax := int(math.Sqrt(maxF/f11*denom)) + 2

	freqs := make([]float64, 0, mMax*nMax)
	for m := 1; m <= mMax; m++ {
		m2 := float64(m * m)
		m4 := m2 * m2
		for n := 1; n <= nMax; n++ {
			n2 := float64(n * n)
			n4 := n2 * n2
			num := math.Sqrt(S*m4 + 2*sqrtS*m2*n2*R2 + n4*R4)
			f := f11 * num / denom
			if f > maxF {
				break // n only increases f, so inner loop can break
			}
			freqs = append(freqs, f)
		}
	}

	sort.Float64s(freqs)
	if len(freqs) > maxModes {
		freqs = freqs[:maxModes]
	}
	return freqs
}

func addModeRec(out []float64, amp float64, freq float64, phase float64, decay float64, sampleRate int) {
	if len(out) == 0 {
		return
	}
	w := 2.0 * math.Pi * freq / float64(sampleRate)
	cw := math.Cos(w)
	x0 := math.Cos(phase)
	x1 := math.Cos(phase + w)
	env := 1.0

	out[0] += amp * env * x0
	env *= decay
	if len(out) == 1 {
		return
	}
	out[1] += amp * env * x1
	env *= decay
	for i := 2; i < len(out); i++ {
		x2 := 2.0*cw*x1 - x0
		x0 = x1
		x1 = x2
		out[i] += amp * env * x2
		env *= decay
	}
}

func highpassDC(x []float64, r float64) {
	if len(x) == 0 {
		return
	}
	prevIn := 0.0
	prevOut := 0.0
	for i := range x {
		y := x[i] - prevIn + r*prevOut
		prevIn = x[i]
		prevOut = y
		x[i] = y
	}
}

func maxAbs(x []float64) float64 {
	m := 0.0
	for _, v := range x {
		a := math.Abs(v)
		if a > m {
			m = a
		}
	}
	return m
}

// applyFadeOut applies a cosine fade-out to the last fadeS seconds of buf.
func applyFadeOut(buf []float64, fadeS float64, sampleRate int) {
	if fadeS <= 0 || len(buf) == 0 {
		return
	}
	fadeSamples := int(math.Round(fadeS * float64(sampleRate)))
	if fadeSamples > len(buf) {
		fadeSamples = len(buf)
	}
	start := len(buf) - fadeSamples
	for i := 0; i < fadeSamples; i++ {
		t := float64(i) / float64(fadeSamples) // 0..1
		gain := 0.5 * (1.0 + math.Cos(t*math.Pi))
		buf[start+i] *= gain
	}
}

func lerp(a, b, t float64) float64 {
	if t < 0 {
		t = 0
	}
	if t > 1 {
		t = 1
	}
	return a + (b-a)*t
}

// BodyConfig controls short mono body IR generation (soundboard coloration).
//
// The body IR models soundboard coloration with two decay regimes:
// low-frequency plate-like modes (broader, longer decay) and high-frequency
// rib-localized modes (denser, shorter decay). CrossoverHz sets the transition.
//
// Mode placement uses analytical Kirchhoff plate eigenmodes for a simply-supported
// orthotropic rectangular plate (modeling the soundboard). The eigenfrequencies are:
//
//	f_{mn}/f_{11} = sqrt(S·m⁴ + 2·√S·m²n²R² + n⁴R⁴) / sqrt(S + 2·√S·R² + R⁴)
//
// where S = StiffnessRatio (Dx/Dy), R = PlateRatio (Lx/Ly), and m,n ≥ 1 are mode
// indices. This gives physically realistic mode clustering: denser at high frequencies
// (2D plate density of states ∝ f), with orthotropic splitting from wood grain direction.
//
// Future tier (deferred):
//   - Full: algo-pde Helmholtz eigensolve for arbitrary plate geometry with ribs,
//     computing actual eigenmodes of the soundboard for physically-grounded IR.
type BodyConfig struct {
	SampleRate     int
	DurationS      float64 // Typically 0.02-0.3s
	Modes          int     // Max modes to include (typically 8-96)
	Seed           int64
	Brightness     float64
	PlateRatio     float64 // Lx/Ly aspect ratio of soundboard (~1.0-3.0)
	StiffnessRatio float64 // Dx/Dy orthotropic stiffness ratio (~5-20 for spruce)
	DirectLevel    float64
	LowDecayS      float64 // Decay time for modes below CrossoverHz
	HighDecayS     float64 // Decay time for modes above CrossoverHz
	CrossoverHz    float64 // Frequency where decay transitions from low to high
	FadeOutS       float64 // Cosine fade-out at the end; 0 = no fade

	NormalizePeak float64
}

// DefaultBodyConfig returns sensible defaults for body IR.
func DefaultBodyConfig() BodyConfig {
	return BodyConfig{
		SampleRate:     96000,
		DurationS:      0.05,
		Modes:          32,
		Seed:           1,
		Brightness:     1.0,
		PlateRatio:     1.6,  // typical grand piano soundboard aspect ratio
		StiffnessRatio: 12.0, // spruce Dx/Dy (~10-15)
		DirectLevel:    0.6,
		LowDecayS:      0.15,
		HighDecayS:     0.03,
		CrossoverHz:    800.0,
		FadeOutS:       0.005,
		NormalizePeak:  0.9,
	}
}

func (c *BodyConfig) Validate() error {
	if c.SampleRate < 8000 {
		return fmt.Errorf("sample rate too low: %d", c.SampleRate)
	}
	if c.DurationS <= 0 {
		return fmt.Errorf("duration must be > 0")
	}
	if c.Modes < 1 {
		return fmt.Errorf("modes must be >= 1")
	}
	if c.Brightness <= 0 {
		return fmt.Errorf("brightness must be > 0")
	}
	if c.PlateRatio <= 0 {
		return fmt.Errorf("plate ratio must be > 0")
	}
	if c.StiffnessRatio <= 0 {
		return fmt.Errorf("stiffness ratio must be > 0")
	}
	if c.DirectLevel < 0 {
		return fmt.Errorf("direct level must be >= 0")
	}
	if c.LowDecayS <= 0 || c.HighDecayS <= 0 {
		return fmt.Errorf("decay seconds must be > 0")
	}
	if c.CrossoverHz <= 0 {
		return fmt.Errorf("crossover Hz must be > 0")
	}
	if c.NormalizePeak <= 0 {
		return fmt.Errorf("normalize peak must be > 0")
	}
	return nil
}

// GenerateBody synthesizes a short mono body IR (soundboard coloration).
func GenerateBody(cfg BodyConfig) ([]float32, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	n := int(math.Round(cfg.DurationS * float64(cfg.SampleRate)))
	if n < 1 {
		n = 1
	}
	buf := make([]float64, n)

	rng := rand.New(rand.NewSource(cfg.Seed))

	// Direct impulse.
	buf[0] += cfg.DirectLevel

	maxF := 0.47 * float64(cfg.SampleRate)
	if maxF < 500.0 {
		maxF = 500.0
	}
	minF := 35.0

	// Compute Kirchhoff plate eigenfrequencies.
	// f_{mn}/f_{11} = sqrt(S·m⁴ + 2·√S·m²n²R² + n⁴R⁴) / sqrt(S + 2·√S·R² + R⁴)
	freqs := plateEigenfreqs(minF, maxF, cfg.Modes, cfg.PlateRatio, cfg.StiffnessRatio)

	// Body modes with 2-way frequency-dependent decay.
	logCrossover := math.Log(cfg.CrossoverHz)
	brightnessExp := 0.7 + 0.9*cfg.Brightness
	for _, f := range freqs {
		amp := 0.9 / math.Pow(1.0+f/120.0, brightnessExp)
		amp *= 0.7 + 0.6*rng.Float64() // amplitude jitter

		// Sigmoid blend: 0 = pure LowDecayS, 1 = pure HighDecayS.
		blend := 1.0 / (1.0 + math.Exp(-3.0*(math.Log(f)-logCrossover)))
		tau := cfg.LowDecayS*(1.0-blend) + cfg.HighDecayS*blend
		decay := math.Exp(-1.0 / (tau * float64(cfg.SampleRate)))

		phi := rng.Float64() * 2.0 * math.Pi
		addModeRec(buf, amp, f, phi, decay, cfg.SampleRate)
	}

	highpassDC(buf, 0.995)
	applyFadeOut(buf, cfg.FadeOutS, cfg.SampleRate)

	peak := maxAbs(buf)
	if peak < 1e-12 {
		peak = 1e-12
	}
	s := cfg.NormalizePeak / peak
	out := make([]float32, n)
	for i := 0; i < n; i++ {
		out[i] = float32(buf[i] * s)
	}
	return out, nil
}

// RoomConfig controls stereo room/reverb IR generation.
type RoomConfig struct {
	SampleRate  int
	DurationS   float64 // Typically 0.3-2.0s
	Seed        int64
	EarlyCount  int
	LateLevel   float64
	StereoWidth float64
	Brightness  float64
	LowDecayS   float64
	HighDecayS  float64
	FadeOutS    float64 // Cosine fade-out at the end; 0 = no fade

	NormalizePeak float64
}

// DefaultRoomConfig returns sensible defaults for room IR.
func DefaultRoomConfig() RoomConfig {
	return RoomConfig{
		SampleRate:    96000,
		DurationS:     1.0,
		Seed:          1,
		EarlyCount:    24,
		LateLevel:     0.06,
		StereoWidth:   0.6,
		Brightness:    0.8,
		LowDecayS:     1.2,
		HighDecayS:    0.2,
		FadeOutS:      0.01,
		NormalizePeak: 0.9,
	}
}

func (c *RoomConfig) Validate() error {
	if c.SampleRate < 8000 {
		return fmt.Errorf("sample rate too low: %d", c.SampleRate)
	}
	if c.DurationS <= 0 {
		return fmt.Errorf("duration must be > 0")
	}
	if c.EarlyCount < 0 {
		return fmt.Errorf("early count must be >= 0")
	}
	if c.LateLevel < 0 {
		return fmt.Errorf("late level must be >= 0")
	}
	if c.StereoWidth < 0 {
		return fmt.Errorf("stereo width must be >= 0")
	}
	if c.Brightness <= 0 {
		return fmt.Errorf("brightness must be > 0")
	}
	if c.LowDecayS <= 0 || c.HighDecayS <= 0 {
		return fmt.Errorf("decay seconds must be > 0")
	}
	if c.NormalizePeak <= 0 {
		return fmt.Errorf("normalize peak must be > 0")
	}
	return nil
}

// GenerateRoom synthesizes a stereo room/reverb IR (early reflections + diffuse tail).
func GenerateRoom(cfg RoomConfig) ([]float32, []float32, error) {
	if err := cfg.Validate(); err != nil {
		return nil, nil, err
	}

	n := int(math.Round(cfg.DurationS * float64(cfg.SampleRate)))
	if n < 1 {
		n = 1
	}
	left := make([]float64, n)
	right := make([]float64, n)

	rng := rand.New(rand.NewSource(cfg.Seed))

	// Early reflections (stereo, 1-50ms range).
	for i := 0; i < cfg.EarlyCount; i++ {
		t := 0.001 + 0.049*rng.Float64()
		idx := int(t * float64(cfg.SampleRate))
		if idx <= 0 || idx >= n {
			continue
		}
		amp := (0.10 + 0.35*rng.Float64()) * math.Exp(-t*20.0)
		// Brightness rolloff: dampen high-frequency reflections via simple attenuation.
		amp *= math.Pow(0.5+0.5*rng.Float64(), 1.0/cfg.Brightness)
		pan := (rng.Float64()*2.0 - 1.0) * cfg.StereoWidth
		left[idx] += amp * (1.0 - 0.5*pan)
		right[idx] += amp * (1.0 + 0.5*pan)
	}

	// Diffuse late tail (stereo, frequency-dependent decay).
	if cfg.LateLevel > 0 {
		maxF := 0.47 * float64(cfg.SampleRate)
		// Two-band noise: low-pass and band-pass filtered.
		lpL, lpR := 0.0, 0.0
		hpL, hpR := 0.0, 0.0
		for i := 0; i < n; i++ {
			t := float64(i) / float64(cfg.SampleRate)
			lowEnv := math.Exp(-t / (0.75 * cfg.LowDecayS))
			highEnv := math.Exp(-t / (0.75 * cfg.HighDecayS))
			_ = maxF

			nL := rng.NormFloat64()
			nR := rng.NormFloat64()

			// Low-pass filtered noise.
			lpL = 0.985*lpL + 0.015*nL
			lpR = 0.985*lpR + 0.015*nR

			// High-pass filtered noise (for air/brightness).
			hpL = 0.15*nL - 0.15*hpL
			hpR = 0.15*nR - 0.15*hpR

			brightnessScale := 0.3 * (cfg.Brightness - 0.3)
			if brightnessScale < 0 {
				brightnessScale = 0
			}
			left[i] += cfg.LateLevel * (lowEnv*lpL + brightnessScale*highEnv*hpL)
			right[i] += cfg.LateLevel * (lowEnv*lpR + brightnessScale*highEnv*hpR)
		}
	}

	highpassDC(left, 0.995)
	highpassDC(right, 0.995)
	applyFadeOut(left, cfg.FadeOutS, cfg.SampleRate)
	applyFadeOut(right, cfg.FadeOutS, cfg.SampleRate)

	peak := maxAbs(left)
	if rp := maxAbs(right); rp > peak {
		peak = rp
	}
	if peak < 1e-12 {
		peak = 1e-12
	}
	s := cfg.NormalizePeak / peak
	outL := make([]float32, n)
	outR := make([]float32, n)
	for i := 0; i < n; i++ {
		outL[i] = float32(left[i] * s)
		outR[i] = float32(right[i] * s)
	}
	return outL, outR, nil
}
