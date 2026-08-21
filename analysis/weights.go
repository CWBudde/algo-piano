package analysis

import (
	"fmt"
	"math"
	"sort"
)

// Component names used by Components and Metrics.Dominant.
const (
	ComponentTime         = "time"
	ComponentEnvelope     = "envelope"
	ComponentSpectral     = "spectral"
	ComponentDecay        = "decay"
	ComponentPartialLevel = "partial_level"
	ComponentPartialFreq  = "partial_freq"
	ComponentTristimulus  = "tristimulus"
	ComponentAttack       = "attack"
	ComponentDecaySegment = "decay_segment"
)

// Weights assigns a relative importance to every scoring component.
// The weights of a valid profile are non-negative and sum to 1.0.
type Weights struct {
	Name                            string
	Time, Envelope, Spectral, Decay float64

	PartialLevel, PartialFreq, Tristimulus float64

	Attack, DecaySegment float64

	// Norms is the normalization scale each component is measured against.
	// The zero value, and any field left at zero, resolves to LegacyNorms, so
	// a profile only names the scales it deliberately recalibrates.
	Norms Norms
}

// Norm calibration note for non-legacy profiles.
//
// The legacy-v1 norm constants (NormTime, NormEnvelope, NormSpectral,
// NormDecay) are frozen: every score recorded in a tracked report was produced
// with them, and changing one silently rewrites history. They are not,
// however, all well calibrated. NormSpectral = 30.0 dB in particular is
// saturated on real material — measured against reference/c4.wav at 48 kHz,
// assets/presets/default.json yields a spectral RMSE of about 62 dB and
// assets/presets/fitted-c4-mayfly.json about 58 dB, so clamp01 pins both at
// 1.0. Two materially different presets are indistinguishable in the term that
// is supposed to dominate the score, and the optimizer has been flying blind on
// its own largest component. Across the tracked preset population the raw
// value spans roughly 51 dB to 172 dB (note 60, velocity 118, release after
// 3.5 s), every one of them at or above the 30 dB norm.
//
// Any new (non-legacy) profile - a balanced-v2 successor, say - must therefore
// ship a re-calibrated NormSpectral rather than inheriting the frozen one, and
// the same check applies to every other norm it relies on: pick a scale the
// observed population actually spans, so the component varies instead of
// sitting pinned. A saturated component is an invisible component. Metrics
// carries per-component Saturated flags precisely so this stays visible; when
// they fire across a preset population, the norm is wrong, not the presets.
//
// Weights.Norms is the mechanism for exactly that: a profile names the scales
// it recalibrates and inherits the rest, so a calibrated profile and the frozen
// legacy one can coexist. legacy-v1 uses LegacyNorms and keeps saturating;
// every other registered profile uses CalibratedNorms, whose values were picked
// from the measured spread of the tracked preset population rather than
// guessed. TestCalibratedNormsCoverObservedPopulation replays the worst
// observed value of each metric and fails if any weighted component saturates.
//
// Profile names.
const (
	ProfileLegacyV1         = "legacy-v1"
	ProfileBalancedV2       = "balanced-v2"
	ProfileAttackV1         = "attack-v1"
	ProfileDecayV1          = "decay-v1"
	ProfileInharmonicityV1  = "inharmonicity-v1"
	profileWeightSumEpsilon = 1e-9
)

var profileRegistry = map[string]Weights{
	ProfileLegacyV1: {
		Name:     ProfileLegacyV1,
		Time:     WeightTime,
		Envelope: WeightEnvelope,
		Spectral: WeightSpectral,
		Decay:    WeightDecay,
	},
	ProfileBalancedV2: {
		Name:         ProfileBalancedV2,
		Norms:        CalibratedNorms(),
		Time:         0.18,
		Envelope:     0.16,
		Spectral:     0.20,
		Decay:        0.05,
		PartialLevel: 0.12,
		PartialFreq:  0.08,
		Tristimulus:  0.06,
		Attack:       0.08,
		DecaySegment: 0.07,
	},
	ProfileAttackV1: {
		Name:         ProfileAttackV1,
		Norms:        CalibratedNorms(),
		Envelope:     0.05,
		Spectral:     0.20,
		PartialLevel: 0.10,
		Tristimulus:  0.10,
		Attack:       0.55,
	},
	ProfileDecayV1: {
		Name:         ProfileDecayV1,
		Norms:        CalibratedNorms(),
		Envelope:     0.20,
		Spectral:     0.10,
		Decay:        0.15,
		DecaySegment: 0.55,
	},
	ProfileInharmonicityV1: {
		Name:         ProfileInharmonicityV1,
		Norms:        CalibratedNorms(),
		Spectral:     0.10,
		PartialLevel: 0.10,
		PartialFreq:  0.70,
		Tristimulus:  0.10,
	},
}

// DefaultWeights returns the legacy-v1 profile, which reproduces the original
// four-component score exactly.
func DefaultWeights() Weights {
	return profileRegistry[ProfileLegacyV1]
}

// WeightsForProfile looks up a registered weighting profile by name.
func WeightsForProfile(name string) (Weights, error) {
	w, ok := profileRegistry[name]
	if !ok {
		return Weights{}, fmt.Errorf("analysis: unknown weighting profile %q (known: %v)", name, Profiles())
	}
	return w, nil
}

// Profiles returns the registered profile names in alphabetical order.
func Profiles() []string {
	names := make([]string, 0, len(profileRegistry))
	for name := range profileRegistry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Validate reports whether the profile is usable: every weight must be
// non-negative and finite, and the weights must sum to 1.0.
func (w Weights) Validate() error {
	each := []struct {
		name string
		val  float64
	}{
		{ComponentTime, w.Time},
		{ComponentEnvelope, w.Envelope},
		{ComponentSpectral, w.Spectral},
		{ComponentDecay, w.Decay},
		{ComponentPartialLevel, w.PartialLevel},
		{ComponentPartialFreq, w.PartialFreq},
		{ComponentTristimulus, w.Tristimulus},
		{ComponentAttack, w.Attack},
		{ComponentDecaySegment, w.DecaySegment},
	}
	var sum float64
	for _, e := range each {
		if !isFinite(e.val) {
			return fmt.Errorf("analysis: weight %q is not finite (%v)", e.name, e.val)
		}
		if e.val < 0 {
			return fmt.Errorf("analysis: weight %q is negative (%v)", e.name, e.val)
		}
		sum += e.val
	}
	if math.Abs(sum-1.0) > profileWeightSumEpsilon {
		return fmt.Errorf("analysis: weights sum to %v, want 1.0", sum)
	}
	return nil
}

// isZero reports whether the profile carries no weight at all (the zero value).
func (w Weights) isZero() bool {
	return w == Weights{}
}

// Component is one weighted term of the combined score.
type Component struct {
	Name         string
	Raw          float64 // the underlying metric in its natural unit
	Norm         float64 // Raw mapped into [0,1] (NaN when Raw is undefined)
	Weight       float64
	Contribution float64 // Weight * Norm, forced to 0 for zero-weight components
	// Saturated reports that Norm is pinned at 1.0 because Raw met or exceeded
	// its norm constant. Such a component contributes a constant to the score
	// and therefore no gradient: two materially different candidates score the
	// same in it. Treat it as a signal that the norm needs recalibrating.
	Saturated bool
	// Available reports whether the component was measurable at all. An
	// unavailable component is left out of the score and the remaining weights
	// are renormalized around it.
	Available bool
}

// Components expands m into the weighted terms that make up the score.
// The first four entries are the legacy time/envelope/spectral/decay terms, in
// that order; callers relying on score compatibility must preserve it.
func Components(m Metrics, w Weights) []Component {
	// The attack term is a composite of two sub-metrics rather than a single
	// raw/scale pair, so its norm is computed up front and its Raw carries the
	// rise-time difference purely for reporting.
	norms := w.Norms.resolve()
	attackNorm := attackNormOf(m, norms)

	raw := []struct {
		name      string
		raw       float64
		scale     float64
		weight    float64
		normOver  float64 // when finite, used instead of raw/scale
		available bool
	}{
		{ComponentTime, m.TimeRMSE, norms.Time, w.Time, math.NaN(), true},
		{ComponentEnvelope, m.EnvelopeRMSEDB, norms.Envelope, w.Envelope, math.NaN(), true},
		{ComponentSpectral, m.SpectralRMSEDB, norms.Spectral, w.Spectral, math.NaN(), true},
		{ComponentDecay, m.DecayDiffDBPerS, norms.Decay, w.Decay, math.NaN(), true},
		// The extended components report their own availability through the
		// finiteness of their raw metric. Partial analysis needs a full
		// partialWindowSize of post-attack signal and segmented decay needs
		// enough tail to fit a slope in each segment; on a short comparison
		// window neither can be measured and the raw value stays NaN. Marking
		// those available anyway multiplied a positive profile weight by NaN
		// and turned the whole score into NaN instead of dropping the term.
		{ComponentPartialLevel, m.PartialLevelRMSEDB, norms.PartialLevel, w.PartialLevel, math.NaN(), isFinite(m.PartialLevelRMSEDB)},
		{ComponentPartialFreq, m.PartialFreqRMSECents, norms.PartialFreq, w.PartialFreq, math.NaN(), isFinite(m.PartialFreqRMSECents)},
		{ComponentTristimulus, m.TristimulusDistance, norms.Tristimulus, w.Tristimulus, math.NaN(), isFinite(m.TristimulusDistance)},
		{ComponentAttack, m.AttackRiseDiffMS, norms.AttackRise, w.Attack, attackNorm, m.AttackAvailable && isFinite(attackNorm)},
		{ComponentDecaySegment, m.DecaySegmentRMSEDBPerS, norms.DecaySegment, w.DecaySegment, math.NaN(), isFinite(m.DecaySegmentRMSEDBPerS)},
	}

	out := make([]Component, 0, len(raw))
	for _, r := range raw {
		norm := clamp01(r.raw / r.scale)
		saturated := isFinite(r.raw) && r.raw >= r.scale
		if isFinite(r.normOver) {
			norm = r.normOver
			saturated = norm >= 1.0
		}
		c := Component{
			Name:      r.name,
			Raw:       r.raw,
			Norm:      norm,
			Weight:    r.weight,
			Saturated: saturated,
			Available: r.available,
		}
		if r.weight != 0 {
			c.Contribution = r.weight * norm
		}
		out = append(out, c)
	}
	return out
}
