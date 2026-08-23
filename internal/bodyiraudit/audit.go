// Package bodyiraudit measures whether a body impulse response contributes
// spectral coloration or merely compensates for an absolute-level mismatch.
package bodyiraudit

import (
	"errors"
	"fmt"
	"math"

	algofft "github.com/cwbudde/algo-fft"
	"github.com/cwbudde/algo-piano/analysis"
)

const dbFloor = -180.0

// Config describes one deterministic attribution comparison.
type Config struct {
	SampleRate   int
	MIDINote     int
	ReleaseAfter float64
	Source       string
}

// Report is the stable JSON representation emitted by body-ir-audit.
type Report struct {
	Schema          string       `json:"schema"`
	Source          string       `json:"source"`
	SampleRate      int          `json:"sample_rate"`
	MIDINote        int          `json:"midi_note"`
	ReferenceFrames int          `json:"reference_frames"`
	CandidateFrames int          `json:"candidate_frames"`
	IR              IRMetrics    `json:"ir"`
	FixedGain       PairMetrics  `json:"fixed_gain"`
	EqualRMS        EqualRMSPair `json:"equal_rms"`
	Delta           MetricDelta  `json:"selected_minus_identity"`
	Attribution     Attribution  `json:"attribution"`
}

// IRMetrics describes the selected body's impulse response. Band gains are
// RMS frequency-response levels relative to a unit impulse.
type IRMetrics struct {
	Frames    int        `json:"frames"`
	Peak      float64    `json:"peak"`
	DCGain    float64    `json:"dc_gain"`
	L2Gain    float64    `json:"l2_gain"`
	BandGains BandValues `json:"band_gain_db"`
}

// PairMetrics contains fixed-gain measurements for the identity and selected
// body renders. Neither signal is normalized before these measurements.
type PairMetrics struct {
	Identity SignalMetrics `json:"identity"`
	Selected SignalMetrics `json:"selected"`
}

// SignalMetrics summarizes absolute level and fixed-level spectral mismatch.
type SignalMetrics struct {
	Peak         float64             `json:"peak"`
	RMS          float64             `json:"rms"`
	RMSDBFS      float64             `json:"rms_dbfs"`
	RMSGapDB     float64             `json:"rms_gap_db"`
	SpectralGaps []WindowSpectralGap `json:"windowed_spectral_gaps"`
}

// WindowSpectralGap is an RMS log-magnitude difference over one time window.
type WindowSpectralGap struct {
	Name     string     `json:"name"`
	StartSec float64    `json:"start_sec"`
	EndSec   float64    `json:"end_sec"`
	GapDB    BandValues `json:"gap_db"`
}

// BandValues contains an overall measurement and the project's standard
// low/mid/high bands: 0-500 Hz, 500-2000 Hz, and 2000 Hz-Nyquist.
type BandValues struct {
	Overall float64 `json:"overall"`
	Low     float64 `json:"low"`
	Mid     float64 `json:"mid"`
	High    float64 `json:"high"`
}

// EqualRMSPair contains the existing analysis metrics after explicit RMS
// matching. Scale is the multiplier applied to each candidate.
type EqualRMSPair struct {
	Identity EqualRMSMetrics `json:"identity"`
	Selected EqualRMSMetrics `json:"selected"`
}

// EqualRMSMetrics contains the level scale and compact score diagnostics for
// one candidate after matching it to the reference RMS.
type EqualRMSMetrics struct {
	Scale        float64 `json:"scale"`
	Score        float64 `json:"score"`
	SpectralDB   float64 `json:"spectral_rmse_db"`
	SpectralLow  float64 `json:"spectral_low_rmse_db"`
	SpectralMid  float64 `json:"spectral_mid_rmse_db"`
	SpectralHigh float64 `json:"spectral_high_rmse_db"`
}

// MetricDelta uses selected-minus-identity signs: negative error deltas are
// improvements, while a positive fixed RMS gap delta is worse.
type MetricDelta struct {
	PeakDB       float64 `json:"peak_db"`
	RMSDB        float64 `json:"rms_db"`
	AbsRMSGapDB  float64 `json:"abs_rms_gap_db"`
	Score        float64 `json:"score"`
	SpectralDB   float64 `json:"spectral_rmse_db"`
	SpectralLow  float64 `json:"spectral_low_rmse_db"`
	SpectralMid  float64 `json:"spectral_mid_rmse_db"`
	SpectralHigh float64 `json:"spectral_high_rmse_db"`
}

// Attribution is a deliberately compact conclusion from the two controls.
type Attribution struct {
	Classification string `json:"classification"`
	Explanation    string `json:"explanation"`
}

// Analyze compares an identity-body render and a selected-body render against
// the same external reference. Inputs are unclipped mono floating-point data.
func Analyze(reference, identity, selected, selectedIR []float64, cfg Config) (Report, error) {
	if cfg.SampleRate <= 0 {
		return Report{}, errors.New("sample rate must be > 0")
	}
	if cfg.ReleaseAfter < 0 {
		return Report{}, errors.New("release-after must be >= 0")
	}
	if len(reference) == 0 {
		return Report{}, errors.New("reference is empty")
	}
	if len(identity) == 0 {
		return Report{}, errors.New("identity render is empty")
	}
	if len(selected) == 0 {
		return Report{}, errors.New("selected render is empty")
	}
	if len(selectedIR) == 0 {
		return Report{}, errors.New("selected IR is empty")
	}
	if err := finiteSignals(reference, identity, selected, selectedIR); err != nil {
		return Report{}, err
	}

	irMetrics, err := measureIR(selectedIR, cfg.SampleRate)
	if err != nil {
		return Report{}, fmt.Errorf("measure IR: %w", err)
	}
	idFixed, err := measureFixed(reference, identity, cfg.SampleRate, cfg.ReleaseAfter)
	if err != nil {
		return Report{}, fmt.Errorf("measure identity: %w", err)
	}
	selectedFixed, err := measureFixed(reference, selected, cfg.SampleRate, cfg.ReleaseAfter)
	if err != nil {
		return Report{}, fmt.Errorf("measure selected: %w", err)
	}
	idEqual := measureEqualRMS(reference, identity, cfg.SampleRate, cfg.MIDINote)
	selectedEqual := measureEqualRMS(reference, selected, cfg.SampleRate, cfg.MIDINote)

	delta := MetricDelta{
		PeakDB:       ratioDB(selectedFixed.Peak, idFixed.Peak),
		RMSDB:        ratioDB(selectedFixed.RMS, idFixed.RMS),
		AbsRMSGapDB:  math.Abs(selectedFixed.RMSGapDB) - math.Abs(idFixed.RMSGapDB),
		Score:        selectedEqual.Score - idEqual.Score,
		SpectralDB:   selectedEqual.SpectralDB - idEqual.SpectralDB,
		SpectralLow:  selectedEqual.SpectralLow - idEqual.SpectralLow,
		SpectralMid:  selectedEqual.SpectralMid - idEqual.SpectralMid,
		SpectralHigh: selectedEqual.SpectralHigh - idEqual.SpectralHigh,
	}

	report := Report{
		Schema:          "body-ir-audit-v1",
		Source:          cfg.Source,
		SampleRate:      cfg.SampleRate,
		MIDINote:        cfg.MIDINote,
		ReferenceFrames: len(reference),
		CandidateFrames: min(len(identity), len(selected)),
		IR:              irMetrics,
		FixedGain:       PairMetrics{Identity: idFixed, Selected: selectedFixed},
		EqualRMS:        EqualRMSPair{Identity: idEqual, Selected: selectedEqual},
		Delta:           delta,
	}
	report.Attribution = classify(report)
	return report, nil
}

func finiteSignals(signals ...[]float64) error {
	for si, signal := range signals {
		for i, v := range signal {
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return fmt.Errorf("signal %d contains non-finite sample at %d", si, i)
			}
		}
	}
	return nil
}

func measureIR(ir []float64, sampleRate int) (IRMetrics, error) {
	var peak, dc, energy float64
	for _, v := range ir {
		a := math.Abs(v)
		if a > peak {
			peak = a
		}
		dc += v
		energy += v * v
	}
	bands, err := responseBands(ir, sampleRate)
	if err != nil {
		return IRMetrics{}, err
	}
	return IRMetrics{Frames: len(ir), Peak: peak, DCGain: dc, L2Gain: math.Sqrt(energy), BandGains: bands}, nil
}

func measureFixed(reference, candidate []float64, sampleRate int, releaseAfter float64) (SignalMetrics, error) {
	peak, rms := signalStats(candidate)
	_, refRMS := signalStats(reference)
	gaps, err := windowedGaps(reference, candidate, sampleRate, releaseAfter)
	if err != nil {
		return SignalMetrics{}, err
	}
	return SignalMetrics{
		Peak: peak, RMS: rms, RMSDBFS: amplitudeDB(rms), RMSGapDB: ratioDB(rms, refRMS), SpectralGaps: gaps,
	}, nil
}

func measureEqualRMS(reference, candidate []float64, sampleRate, midiNote int) EqualRMSMetrics {
	_, refRMS := signalStats(reference)
	_, candRMS := signalStats(candidate)
	scale := 1.0
	if candRMS > 0 {
		scale = refRMS / candRMS
	}
	matched := make([]float64, len(candidate))
	for i, v := range candidate {
		matched[i] = v * scale
	}
	m := analysis.CompareWithOptions(reference, matched, sampleRate, analysis.Options{MIDINote: midiNote}).Sanitized()
	return EqualRMSMetrics{
		Scale: scale, Score: m.Score, SpectralDB: m.SpectralRMSEDB,
		SpectralLow: m.SpectralLowRMSEDB, SpectralMid: m.SpectralMidRMSEDB, SpectralHigh: m.SpectralHighRMSEDB,
	}
}

func classify(r Report) Attribution {
	const material = 0.05
	if math.Abs(r.Delta.RMSDB) < material && math.Abs(r.Delta.Score) < 0.0001 && math.Abs(r.Delta.SpectralDB) < material {
		return Attribution{Classification: "neutral", Explanation: "the selected body is materially indistinguishable from the identity control"}
	}
	if r.Delta.SpectralDB < -material {
		return Attribution{Classification: "coloration_contribution", Explanation: "the selected body improves the equal-RMS spectral comparison, so its benefit survives removal of level differences"}
	}
	if r.Delta.SpectralDB > material && math.Abs(r.Delta.RMSDB) >= material {
		return Attribution{Classification: "level_effect_with_coloration_regression", Explanation: "the selected body materially changes level but worsens the equal-RMS spectrum; fixed-gain gains must not be attributed to useful coloration"}
	}
	if r.Delta.SpectralDB > material {
		return Attribution{Classification: "coloration_regression", Explanation: "the selected body worsens the equal-RMS spectral comparison"}
	}
	if r.Delta.AbsRMSGapDB < -material {
		return Attribution{Classification: "level_compensation", Explanation: "the fixed-gain level gap improves, but the equal-RMS comparison does not"}
	}
	return Attribution{Classification: "coloration_regression", Explanation: "the selected body does not improve either the fixed-level gap or the equal-RMS spectrum"}
}

func signalStats(signal []float64) (peak, rms float64) {
	if len(signal) == 0 {
		return 0, 0
	}
	var energy float64
	for _, v := range signal {
		a := math.Abs(v)
		if a > peak {
			peak = a
		}
		energy += v * v
	}
	return peak, math.Sqrt(energy / float64(len(signal)))
}

func windowedGaps(reference, candidate []float64, sampleRate int, releaseAfter float64) ([]WindowSpectralGap, error) {
	n := min(len(reference), len(candidate))
	windows := []struct {
		name       string
		start, end float64
	}{
		{name: "attack", start: 0, end: 0.100},
		{name: "sustain", start: 0.100, end: releaseAfter},
		{name: "decay", start: releaseAfter, end: float64(n) / float64(sampleRate)},
	}
	out := make([]WindowSpectralGap, 0, len(windows))
	for _, w := range windows {
		start := int(math.Round(w.start * float64(sampleRate)))
		end := int(math.Round(w.end * float64(sampleRate)))
		if start < 0 {
			start = 0
		}
		if end > n {
			end = n
		}
		if end-start < 32 {
			continue
		}
		gaps, err := spectralGapBands(reference[start:end], candidate[start:end], sampleRate)
		if err != nil {
			return nil, err
		}
		out = append(out, WindowSpectralGap{Name: w.name, StartSec: float64(start) / float64(sampleRate), EndSec: float64(end) / float64(sampleRate), GapDB: gaps})
	}
	return out, nil
}

func responseBands(signal []float64, sampleRate int) (BandValues, error) {
	spectrum, nfft, err := realSpectrum(signal)
	if err != nil {
		return BandValues{}, err
	}
	return aggregateBands(spectrum, nil, nfft, sampleRate, true), nil
}

func spectralGapBands(reference, candidate []float64, sampleRate int) (BandValues, error) {
	n := min(len(reference), len(candidate))
	if n < 2 {
		return BandValues{}, errors.New("spectral window too short")
	}
	refSpec, nfft, err := realSpectrum(reference[:n])
	if err != nil {
		return BandValues{}, err
	}
	candSpec, _, err := realSpectrum(candidate[:n])
	if err != nil {
		return BandValues{}, err
	}
	return aggregateBands(refSpec, candSpec, nfft, sampleRate, false), nil
}

func realSpectrum(signal []float64) ([]complex128, int, error) {
	nfft := 2
	for nfft < len(signal) {
		nfft <<= 1
	}
	in := make([]float64, nfft)
	copy(in, signal)
	out := make([]complex128, nfft/2+1)
	plan, err := algofft.NewPlanReal64(nfft)
	if err != nil {
		return nil, 0, err
	}
	if err := plan.Forward(out, in); err != nil {
		return nil, 0, err
	}
	return out, nfft, nil
}

func aggregateBands(a, b []complex128, nfft, sampleRate int, response bool) BandValues {
	type accum struct {
		sum float64
		n   int
	}
	var overall, low, mid, high accum
	for i := 0; i < len(a); i++ {
		freq := float64(i*sampleRate) / float64(nfft)
		var value float64
		if response {
			// Accumulate response power and convert the band RMS to dB after
			// averaging. Squaring dB values here would lose the sign and make
			// attenuation look like positive gain.
			value = cmplxAbs(a[i])
		} else {
			aDB := 20 * math.Log10(math.Max(cmplxAbs(a[i]), 1e-9))
			bDB := 20 * math.Log10(math.Max(cmplxAbs(b[i]), 1e-9))
			value = aDB - bDB
		}
		add := func(dst *accum) { dst.sum += value * value; dst.n++ }
		add(&overall)
		switch {
		case freq < 500:
			add(&low)
		case freq < 2000:
			add(&mid)
		default:
			add(&high)
		}
	}
	rms := func(a accum) float64 {
		if a.n == 0 {
			return 0
		}
		v := math.Sqrt(a.sum / float64(a.n))
		if response {
			return amplitudeDB(v)
		}
		return v
	}
	return BandValues{Overall: rms(overall), Low: rms(low), Mid: rms(mid), High: rms(high)}
}

func cmplxAbs(v complex128) float64 { return math.Hypot(real(v), imag(v)) }

func amplitudeDB(v float64) float64 {
	if v <= 0 {
		return dbFloor
	}
	return 20 * math.Log10(v)
}

func ratioDB(numerator, denominator float64) float64 {
	if numerator <= 0 && denominator <= 0 {
		return 0
	}
	if denominator <= 0 {
		return -dbFloor
	}
	if numerator <= 0 {
		return dbFloor
	}
	return 20 * math.Log10(numerator/denominator)
}
