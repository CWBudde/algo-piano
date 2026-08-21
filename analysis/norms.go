package analysis

// Norms carries the normalization scale of every scoring component: the value
// of the raw metric, in its natural unit, that maps to a normalized 1.0.
//
// Norms are per-profile rather than global because the legacy scale and a
// well-calibrated scale are different things and both must exist at once. The
// legacy-v1 profile has to keep the exact constants every tracked report was
// produced with, or those numbers stop meaning anything; a profile that is
// meant to steer an optimizer has to use a scale the observed population
// actually spans, or its largest component saturates and contributes no
// gradient. See the calibration note in weights.go.
//
// The zero value resolves to LegacyNorms, and so does any individual field
// left at zero, so a profile only has to name the scales it deliberately
// changes.
type Norms struct {
	Time, Envelope, Spectral, Decay float64

	PartialLevel, PartialFreq, Tristimulus float64

	// AttackRise and AttackCentroid are the two halves of the composite attack
	// component; see attackNormOf.
	AttackRise, AttackCentroid float64

	DecaySegment float64
}

// LegacyNorms returns the frozen legacy-v1 scales. These are the package-level
// Norm* constants, and they must not change: every score in a tracked
// *.report.json and in assets/thresholds/c4.json was produced with them.
func LegacyNorms() Norms {
	return Norms{
		Time:           NormTime,
		Envelope:       NormEnvelope,
		Spectral:       NormSpectral,
		Decay:          NormDecay,
		PartialLevel:   NormPartialLevel,
		PartialFreq:    NormPartialFreq,
		Tristimulus:    NormTristimulus,
		AttackRise:     NormAttackRise,
		AttackCentroid: NormAttackCentroid,
		DecaySegment:   NormDecaySegment,
	}
}

// CalibratedNorms returns scales chosen from the observed spread of the tracked
// preset population, rather than from the frozen legacy guesses. Measured
// 2026-08-21 against reference/c4.wav, note 60, velocity 118, release-after
// 3.5 s, 48 kHz, over the seven real presets in assets/presets plus the three
// per-aspect pass outputs in out/passes. assets/presets/modal-calibrated.json is
// excluded as degenerate: it renders at 172 dB spectral RMSE and 883 dB/s decay
// difference, which is a broken preset rather than a point on the distribution.
//
// The rule each value follows: a norm must be large enough that the worst
// observed candidate does not saturate, and small enough that the population
// still spans a useful fraction of [0,1]. A saturated component contributes a
// constant and therefore no gradient; an oversized one contributes a sliver of
// its nominal weight.
//
//	component       observed          legacy -> calibrated   normalized span
//	spectral        51.5 - 71.3 dB    30.0   -> 80.0         0.64 - 0.89
//	decay            0.13 - 14.7 dB/s 40.0   -> 15.0         0.01 - 0.98
//	partial_level   10.6 - 27.4 dB    12.0   -> 35.0         0.30 - 0.78
//	partial_freq    34.8 - 48.9 cents 50.0   -> 70.0         0.50 - 0.70
//	attack_rise     24.1 - 24.5 ms    20.0   -> 50.0         ~0.49
//	attack_centroid  0.08 - 1.40 oct   0.5   ->  1.5         0.06 - 0.93
//	time             0.11 - 0.17       0.25  (unchanged)     0.43 - 0.67
//	envelope         8.4 - 24.3 dB    30.0   (unchanged)     0.28 - 0.81
//	tristimulus      0.15 - 0.40       0.5   (unchanged)     0.30 - 0.80
//	decay_segment   12.8 - 23.2 dB/s  30.0   (unchanged)     0.43 - 0.77
//
// Caveat on attack_rise: raising its norm stops it saturating, but the metric is
// close to constant across the whole population anyway (24.1 - 24.5 ms), because
// every candidate rises in about 1 ms against the reference's 25.5 ms. That is a
// model deficiency, not a normalization one - the synthesized onset is far too
// abrupt - and no choice of norm creates a gradient the raw metric does not have.
// The centroid half of the composite is what currently carries the attack term.
func CalibratedNorms() Norms {
	return Norms{
		Time:           NormTime,
		Envelope:       NormEnvelope,
		Spectral:       80.0,
		Decay:          15.0,
		PartialLevel:   35.0,
		PartialFreq:    70.0,
		Tristimulus:    NormTristimulus,
		AttackRise:     50.0,
		AttackCentroid: 1.5,
		DecaySegment:   NormDecaySegment,
	}
}

// resolve fills every unset field from LegacyNorms. A norm is a divisor, so a
// zero (or negative, or non-finite) value is never a meaningful setting -
// treating it as "unset" lets a profile name only the scales it changes and
// removes any way to accidentally divide by zero.
func (n Norms) resolve() Norms {
	legacy := LegacyNorms()
	fields := []struct {
		dst *float64
		def float64
	}{
		{&n.Time, legacy.Time},
		{&n.Envelope, legacy.Envelope},
		{&n.Spectral, legacy.Spectral},
		{&n.Decay, legacy.Decay},
		{&n.PartialLevel, legacy.PartialLevel},
		{&n.PartialFreq, legacy.PartialFreq},
		{&n.Tristimulus, legacy.Tristimulus},
		{&n.AttackRise, legacy.AttackRise},
		{&n.AttackCentroid, legacy.AttackCentroid},
		{&n.DecaySegment, legacy.DecaySegment},
	}
	for _, f := range fields {
		if !isFinite(*f.dst) || *f.dst <= 0 {
			*f.dst = f.def
		}
	}
	return n
}
