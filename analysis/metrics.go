package analysis

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

	// Per-band spectral RMSE breakdown for diagnostics.
	SpectralLowRMSEDB  float64 `json:"spectral_low_rmse_db"`  // 0-500 Hz
	SpectralMidRMSEDB  float64 `json:"spectral_mid_rmse_db"`  // 500-2000 Hz
	SpectralHighRMSEDB float64 `json:"spectral_high_rmse_db"` // 2000+ Hz

	// Extended per-partial and per-phase detail. These are undefined (NaN) when
	// the aligned window is too short or the analysis was skipped via Options.
	F0Hz                   float64 `json:"f0_hz"`
	PartialLevelRMSEDB     float64 `json:"partial_level_rmse_db"`
	PartialFreqRMSECents   float64 `json:"partial_freq_rmse_cents"`
	TristimulusDistance    float64 `json:"tristimulus_distance"`
	DecaySegmentRMSEDBPerS float64 `json:"decay_segment_rmse_db_per_s"`

	// Attack-transient detail: onset rise time plus the spectral centroid
	// trajectory over the first attackWindowSec. AttackAvailable is false when
	// the window carries no rising onset at all (a decay-only slice, say), in
	// which case the attack term is left out of the score and the remaining
	// weights are renormalized.
	RefRiseTimeMS         float64 `json:"ref_rise_time_ms"`
	CandRiseTimeMS        float64 `json:"cand_rise_time_ms"`
	AttackRiseDiffMS      float64 `json:"attack_rise_diff_ms"`
	RefAttackCentroidHz   float64 `json:"ref_attack_centroid_hz"`
	CandAttackCentroidHz  float64 `json:"cand_attack_centroid_hz"`
	AttackCentroidRMSEOct float64 `json:"attack_centroid_rmse_oct"`
	AttackAvailable       bool    `json:"attack_available"`

	// Normalized component contributions (0-1 each, weighted sum = Score).
	TimeNorm     float64 `json:"time_norm"`
	EnvelopeNorm float64 `json:"envelope_norm"`
	SpectralNorm float64 `json:"spectral_norm"`
	DecayNorm    float64 `json:"decay_norm"`
	Dominant     string  `json:"dominant"` // name of the highest-contributing component

	PartialLevelNorm float64 `json:"partial_level_norm"`
	PartialFreqNorm  float64 `json:"partial_freq_norm"`
	TristimulusNorm  float64 `json:"tristimulus_norm"`
	AttackNorm       float64 `json:"attack_norm"`
	DecaySegmentNorm float64 `json:"decay_segment_norm"`

	// Saturation flags. A component whose raw value meets or exceeds its norm
	// constant is pinned at 1.0: it still contributes its full weight to the
	// score, but it no longer varies with the underlying signal, so it gives an
	// optimizer no gradient at all. A saturated component is an invisible
	// component, and the flag is how that becomes visible.
	TimeSaturated         bool `json:"time_saturated,omitempty"`
	EnvelopeSaturated     bool `json:"envelope_saturated,omitempty"`
	SpectralSaturated     bool `json:"spectral_saturated,omitempty"`
	DecaySaturated        bool `json:"decay_saturated,omitempty"`
	PartialLevelSaturated bool `json:"partial_level_saturated,omitempty"`
	PartialFreqSaturated  bool `json:"partial_freq_saturated,omitempty"`
	TristimulusSaturated  bool `json:"tristimulus_saturated,omitempty"`
	AttackSaturated       bool `json:"attack_saturated,omitempty"`
	DecaySegmentSaturated bool `json:"decay_segment_saturated,omitempty"`

	ScoreProfile string  `json:"score_profile"` // weighting profile used for Score
	Score        float64 `json:"score"`
	Similarity   float64 `json:"similarity"`
}

// SpectralPosition records spectral RMSE at a specific time offset.
type SpectralPosition struct {
	OffsetSec float64 `json:"offset_sec"`
	RMSEDB    float64 `json:"rmse_db"`
}

// Sanitized returns a copy of m in which every non-finite float has been
// replaced by a finite worst-case fallback, and Score/Similarity are clamped
// to [0,1]. Non-finite values are legitimate results (an un-fittable decay
// slope yields NaN, and metrics that need a long window are undefined on short
// ones), but encoding/json refuses NaN and +/-Inf, so any Metrics that leaves
// the process as JSON must pass through here first.
//
// Fallbacks, by kind:
//   - TimeRMSE                  -> 1.0   (worst-case normalized amplitude error)
//   - dB-valued error fields    -> 60.0  (a 60 dB mismatch, i.e. "nothing in common")
//   - partial frequency error   -> 1200.0 cents (one octave off)
//   - tristimulus distance      -> 1.0   (maximum distance between unit-sum triples)
//   - slope fields (dB/s)       -> 0     (unknown slope reads as "no decay measured")
//   - rise times / centroids    -> 0     (unmeasured onset reads as "no onset")
//   - attack error fields       -> their norm constant (worst-case attack error)
//   - normalized components     -> 1.0   (worst-case contribution)
//   - Score                     -> 1.0   (maximum distance)
//   - Similarity                -> 0     (no similarity)
func (m Metrics) Sanitized() Metrics {
	if !isFinite(m.TimeRMSE) {
		m.TimeRMSE = 1.0
	}
	if !isFinite(m.EnvelopeRMSEDB) {
		m.EnvelopeRMSEDB = 60.0
	}
	if !isFinite(m.SpectralRMSEDB) {
		m.SpectralRMSEDB = 60.0
	}
	if !isFinite(m.RefDecayDBPerS) {
		m.RefDecayDBPerS = 0
	}
	if !isFinite(m.CandDecayDBPerS) {
		m.CandDecayDBPerS = 0
	}
	if !isFinite(m.DecayDiffDBPerS) {
		m.DecayDiffDBPerS = 60.0
	}
	if !isFinite(m.SpectralLowRMSEDB) {
		m.SpectralLowRMSEDB = 60.0
	}
	if !isFinite(m.SpectralMidRMSEDB) {
		m.SpectralMidRMSEDB = 60.0
	}
	if !isFinite(m.SpectralHighRMSEDB) {
		m.SpectralHighRMSEDB = 60.0
	}
	if !isFinite(m.F0Hz) {
		m.F0Hz = 0
	}
	if !isFinite(m.PartialLevelRMSEDB) {
		m.PartialLevelRMSEDB = 60.0
	}
	if !isFinite(m.PartialFreqRMSECents) {
		m.PartialFreqRMSECents = 1200.0
	}
	if !isFinite(m.TristimulusDistance) {
		m.TristimulusDistance = 1.0
	}
	if !isFinite(m.RefRiseTimeMS) {
		m.RefRiseTimeMS = 0
	}
	if !isFinite(m.CandRiseTimeMS) {
		m.CandRiseTimeMS = 0
	}
	if !isFinite(m.AttackRiseDiffMS) {
		m.AttackRiseDiffMS = NormAttackRise
	}
	if !isFinite(m.RefAttackCentroidHz) {
		m.RefAttackCentroidHz = 0
	}
	if !isFinite(m.CandAttackCentroidHz) {
		m.CandAttackCentroidHz = 0
	}
	if !isFinite(m.AttackCentroidRMSEOct) {
		m.AttackCentroidRMSEOct = NormAttackCentroid
	}
	if !isFinite(m.DecaySegmentRMSEDBPerS) {
		m.DecaySegmentRMSEDBPerS = 0
	}
	if !isFinite(m.TimeNorm) {
		m.TimeNorm = 1.0
	}
	if !isFinite(m.EnvelopeNorm) {
		m.EnvelopeNorm = 1.0
	}
	if !isFinite(m.SpectralNorm) {
		m.SpectralNorm = 1.0
	}
	if !isFinite(m.DecayNorm) {
		m.DecayNorm = 1.0
	}
	if !isFinite(m.PartialLevelNorm) {
		m.PartialLevelNorm = 1.0
	}
	if !isFinite(m.PartialFreqNorm) {
		m.PartialFreqNorm = 1.0
	}
	if !isFinite(m.TristimulusNorm) {
		m.TristimulusNorm = 1.0
	}
	if !isFinite(m.AttackNorm) {
		m.AttackNorm = 1.0
	}
	if !isFinite(m.DecaySegmentNorm) {
		m.DecaySegmentNorm = 1.0
	}
	if !isFinite(m.Score) {
		m.Score = 1.0
	}
	m.Score = clamp01(m.Score)
	if !isFinite(m.Similarity) {
		m.Similarity = 0.0
	}
	m.Similarity = clamp01(m.Similarity)

	if len(m.SpectralPositions) > 0 {
		pos := make([]SpectralPosition, len(m.SpectralPositions))
		copy(pos, m.SpectralPositions)
		for i := range pos {
			if !isFinite(pos[i].OffsetSec) {
				pos[i].OffsetSec = 0
			}
			if !isFinite(pos[i].RMSEDB) {
				pos[i].RMSEDB = 60.0
			}
		}
		m.SpectralPositions = pos
	}
	return m
}
