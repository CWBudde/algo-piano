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

// CalibratedNorms returns the scales used by every profile other than
// legacy-v1. They were picked from the measured spread of the tracked preset
// population, not guessed: 11 renders against reference/c4.wav (note 60,
// velocity 118, release after 3.5 s, 48 kHz, decay-dbfs -90, hold 6,
// min-duration 2.0, max-duration 30) covering the seven presets in
// assets/presets plus the four per-aspect pass outputs under out/passes*.
// assets/presets/modal-calibrated.json is excluded as degenerate - it measures
// 172 dB spectral, 883 dB/s decay and 204 dB envelope, which is a broken
// preset rather than a population member, and letting it set the scale would
// re-saturate everything else.
//
// Measured 2026-08-21 on the post-#14 renderer. The DWG treble-collapse fix
// changed every render, so an earlier calibration measured on the pre-#14
// renderer does not transfer; two of its values (Decay and AttackCentroid)
// saturate against the population below and had to be raised.
//
//	metric                    raw min-max     legacy -> norm        calibrated -> norm
//	time                     0.096 - 0.151    0.25: 0.38-0.60       (unchanged)
//	envelope                 10.1 - 27.9 dB   30.0: 0.34-0.93       (unchanged)
//	spectral                 47.8 - 68.6 dB   30.0: SATURATED       80.0: 0.60-0.86
//	decay                    0.62 - 17.1 dB/s 40.0: 0.02-0.43       20.0: 0.03-0.86
//	partial_level            10.9 - 25.4 dB   12.0: SATURATED       35.0: 0.31-0.73
//	partial_freq             36.6 - 50.0 cts  50.0: SATURATED       70.0: 0.52-0.71
//	tristimulus              0.15 - 0.41      0.5:  0.30-0.82       (unchanged)
//	attack_rise              24.2 - 24.5 ms   20.0: SATURATED       50.0: 0.48-0.49
//	attack_centroid          0.12 - 1.64 oct  0.5:  SATURATED       2.0:  0.06-0.82
//	decay_segment            7.5 - 21.4 dB/s  30.0: 0.25-0.71       (unchanged)
//
// Time, Envelope, Tristimulus and DecaySegment keep the legacy constants
// because the population already spans them usefully; recalibrating a norm
// that works buys nothing and costs comparability.
//
// Caveat on attack_rise: raising its norm stops it saturating, but the metric
// is close to constant across the whole population (24.2 - 24.5 ms), because
// every candidate rises in about 1 ms against the reference's 25.5 ms. That is
// a model deficiency, not a normalization one - the synthesized onset is far
// too abrupt - and no choice of norm creates a gradient the raw metric does
// not have. The centroid half of the composite is what currently carries the
// attack term.
func CalibratedNorms() Norms {
	return Norms{
		Time:           NormTime,
		Envelope:       NormEnvelope,
		Spectral:       80.0,
		Decay:          20.0,
		PartialLevel:   35.0,
		PartialFreq:    70.0,
		Tristimulus:    NormTristimulus,
		AttackRise:     50.0,
		AttackCentroid: 2.0,
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
