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
