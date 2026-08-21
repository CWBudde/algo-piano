package analysis

import (
	"math"
	"testing"
)

func TestDefaultWeightsMatchLegacyConstants(t *testing.T) {
	w := DefaultWeights()
	if w.Name != ProfileLegacyV1 {
		t.Fatalf("expected default profile %q, got %q", ProfileLegacyV1, w.Name)
	}
	if w.Time != WeightTime {
		t.Fatalf("expected Time weight %f, got %f", WeightTime, w.Time)
	}
	if w.Envelope != WeightEnvelope {
		t.Fatalf("expected Envelope weight %f, got %f", WeightEnvelope, w.Envelope)
	}
	if w.Spectral != WeightSpectral {
		t.Fatalf("expected Spectral weight %f, got %f", WeightSpectral, w.Spectral)
	}
	if w.Decay != WeightDecay {
		t.Fatalf("expected Decay weight %f, got %f", WeightDecay, w.Decay)
	}
	if w.PartialLevel != 0 || w.PartialFreq != 0 || w.Tristimulus != 0 || w.Attack != 0 || w.DecaySegment != 0 {
		t.Fatalf("expected legacy-v1 to put no weight on the extended metrics, got %f %f %f %f %f",
			w.PartialLevel, w.PartialFreq, w.Tristimulus, w.Attack, w.DecaySegment)
	}
}

func TestAllProfilesSumToOne(t *testing.T) {
	names := Profiles()
	if len(names) != 5 {
		t.Fatalf("expected 5 registered profiles, got %f", float64(len(names)))
	}
	for _, name := range names {
		w, err := WeightsForProfile(name)
		if err != nil {
			t.Fatalf("expected profile %q to resolve, got error: %v", name, err)
		}
		if w.Name != name {
			t.Fatalf("expected profile %q to carry its own name, got %q", name, w.Name)
		}
		if err := w.Validate(); err != nil {
			t.Fatalf("expected profile %q to validate, got error: %v", name, err)
		}
		sum := w.Time + w.Envelope + w.Spectral + w.Decay +
			w.PartialLevel + w.PartialFreq + w.Tristimulus + w.Attack + w.DecaySegment
		if math.Abs(sum-1.0) > 1e-9 {
			t.Fatalf("expected profile %q weights to sum to 1.000000, got %f", name, sum)
		}
	}
}

func TestWeightsForProfileRejectsUnknownName(t *testing.T) {
	if _, err := WeightsForProfile("does-not-exist"); err == nil {
		t.Fatalf("expected an error for an unknown profile, got none")
	}
}

func TestWeightsValidateRejectsBadProfiles(t *testing.T) {
	w := Weights{Name: "negative", Time: -0.1, Spectral: 1.1}
	if err := w.Validate(); err == nil {
		t.Fatalf("expected a negative weight to be rejected, got no error")
	}
	w = Weights{Name: "short", Time: 0.5}
	if err := w.Validate(); err == nil {
		t.Fatalf("expected a weight sum of 0.500000 to be rejected, got no error")
	}
}

// TestCompareScoreIsBitIdenticalToLegacyFormula guards the compatibility
// promise: scores recorded in tracked reports must keep reproducing exactly.
// The comparison is intentionally exact, not tolerance based.
func TestCompareScoreIsBitIdenticalToLegacyFormula(t *testing.T) {
	sr := 48000
	cases := [][2][]float64{
		{makeDecaySine(sr, 261.63, 1.8, 0.8), makeDecaySine(sr, 330.0, 0.8, 0.25)},
		{makeDecaySine(sr, 440.0, 1.5, 0.7), makeDecaySine(sr, 440.0, 1.5, 0.7)},
		{makeDecaySine(sr, 110.0, 2.5, 1.4), makeDecaySine(sr, 110.5, 2.5, 1.1)},
		{randomSignal(sr, 3), randomSignal(sr, 4)},
	}

	for i, c := range cases {
		m := Compare(c[0], c[1], sr)

		legacy := clamp01(WeightTime*m.TimeNorm + WeightEnvelope*m.EnvelopeNorm +
			WeightSpectral*m.SpectralNorm + WeightDecay*m.DecayNorm)
		if m.Score != legacy {
			t.Fatalf("case %d: expected Score to equal the legacy formula %.17g, got %.17g", i, legacy, m.Score)
		}

		legacySim := clamp01(math.Exp(-4.0 * legacy))
		if m.Similarity != legacySim {
			t.Fatalf("case %d: expected Similarity %.17g, got %.17g", i, legacySim, m.Similarity)
		}

		if m.ScoreProfile != ProfileLegacyV1 {
			t.Fatalf("case %d: expected score profile %q, got %q", i, ProfileLegacyV1, m.ScoreProfile)
		}

		explicit := CompareWithWeights(c[0], c[1], sr, DefaultWeights())
		if explicit.Score != m.Score {
			t.Fatalf("case %d: expected CompareWithWeights to reproduce Compare exactly, got %.17g vs %.17g", i, explicit.Score, m.Score)
		}
	}
}

func TestCompareWithOptionsSkipsDoNotAffectLegacyScore(t *testing.T) {
	sr := 48000
	a := makeDecaySine(sr, 261.63, 1.8, 0.8)
	b := makeDecaySine(sr, 277.18, 1.8, 0.6)

	full := Compare(a, b, sr)
	skipped := CompareWithOptions(a, b, sr, Options{SkipPartials: true, SkipAttack: true})
	if skipped.Score != full.Score {
		t.Fatalf("expected zero-weight extended metrics not to move the legacy score, got %.17g vs %.17g", skipped.Score, full.Score)
	}
	if skipped.AttackAvailable {
		t.Fatalf("expected SkipAttack to leave the attack metric unavailable")
	}
	if !math.IsNaN(skipped.AttackRiseDiffMS) {
		t.Fatalf("expected SkipAttack to leave AttackRiseDiffMS undefined, got %f", skipped.AttackRiseDiffMS)
	}
	if !math.IsNaN(skipped.PartialLevelRMSEDB) {
		t.Fatalf("expected SkipPartials to leave PartialLevelRMSEDB undefined, got %f", skipped.PartialLevelRMSEDB)
	}
}

func TestCompareWithWeightsAttackProfileRanksAttackMismatchWorse(t *testing.T) {
	sr := 48000
	// A struck note: a short percussive rise into an exponential decay. The
	// attack metric needs a real onset, so a bare decaying sine (which starts
	// at full amplitude) would leave it unavailable by design.
	ref := makeStruckTone(sr, 220.0, 1.5, 0.9, 0.006)

	// Same steady tone, but the onset is ramped in over 120 ms instead of
	// arriving in 6 ms: the attack differs, the sustain barely does.
	attackMismatch := makeStruckTone(sr, 220.0, 1.5, 0.9, 0.120)

	// Same attack, but the tail decays faster: the attack matches closely.
	decayMismatch := makeStruckTone(sr, 220.0, 1.5, 0.45, 0.006)

	attackProfile, err := WeightsForProfile(ProfileAttackV1)
	if err != nil {
		t.Fatalf("expected attack-v1 profile to resolve, got error: %v", err)
	}

	mAttack := CompareWithWeights(ref, attackMismatch, sr, attackProfile)
	mDecayUnderAttack := CompareWithWeights(ref, decayMismatch, sr, attackProfile)

	if !mAttack.AttackAvailable || !mDecayUnderAttack.AttackAvailable {
		t.Fatalf("expected both candidates to yield an attack metric, got %t and %t",
			mAttack.AttackAvailable, mDecayUnderAttack.AttackAvailable)
	}
	if mAttack.AttackNorm <= mDecayUnderAttack.AttackNorm {
		t.Fatalf("expected the ramped onset to have the larger attack error, got %f vs %f",
			mAttack.AttackNorm, mDecayUnderAttack.AttackNorm)
	}
	if mAttack.Score <= mDecayUnderAttack.Score {
		t.Fatalf("expected attack-v1 to score the attack mismatch worse, got %f vs %f",
			mAttack.Score, mDecayUnderAttack.Score)
	}

	// Sanity: the same pair ranks the other way round under decay-v1.
	decayProfile := decayProfileWeights(t)
	mDecay := CompareWithWeights(ref, decayMismatch, sr, decayProfile)
	mAttackUnderDecay := CompareWithWeights(ref, attackMismatch, sr, decayProfile)
	if mDecay.Score <= mAttackUnderDecay.Score {
		t.Fatalf("expected decay-v1 to score the decay mismatch worse, got %f vs %f",
			mDecay.Score, mAttackUnderDecay.Score)
	}

	if mAttack.ScoreProfile != ProfileAttackV1 {
		t.Fatalf("expected score profile %q, got %q", ProfileAttackV1, mAttack.ScoreProfile)
	}
}

func decayProfileWeights(t *testing.T) Weights {
	t.Helper()
	w, err := WeightsForProfile(ProfileDecayV1)
	if err != nil {
		t.Fatalf("expected decay-v1 profile to resolve, got error: %v", err)
	}
	return w
}

func TestComponentsSkipZeroWeightContributions(t *testing.T) {
	m := Metrics{
		TimeRMSE:               0.1,
		EnvelopeRMSEDB:         6.0,
		SpectralRMSEDB:         9.0,
		DecayDiffDBPerS:        4.0,
		PartialLevelRMSEDB:     math.NaN(),
		PartialFreqRMSECents:   math.NaN(),
		TristimulusDistance:    math.NaN(),
		AttackRiseDiffMS:       math.NaN(),
		AttackCentroidRMSEOct:  math.NaN(),
		DecaySegmentRMSEDBPerS: math.NaN(),
	}
	comps := Components(m, DefaultWeights())
	if len(comps) != 9 {
		t.Fatalf("expected 9 components, got %f", float64(len(comps)))
	}
	want := []string{
		ComponentTime, ComponentEnvelope, ComponentSpectral, ComponentDecay,
		ComponentPartialLevel, ComponentPartialFreq, ComponentTristimulus,
		ComponentAttack, ComponentDecaySegment,
	}
	for i, name := range want {
		if comps[i].Name != name {
			t.Fatalf("expected component %d to be %q, got %q", i, name, comps[i].Name)
		}
	}
	var sum float64
	for _, c := range comps {
		if c.Weight == 0 {
			if c.Contribution != 0 {
				t.Fatalf("expected zero-weight component %q to contribute 0.000000, got %f", c.Name, c.Contribution)
			}
			continue
		}
		sum += c.Contribution
	}
	if math.IsNaN(sum) {
		t.Fatalf("expected a finite score sum, got %f", sum)
	}
}

func TestMIDIToHzMatchesEqualTemperament(t *testing.T) {
	if got := MIDIToHz(69); math.Abs(got-440.0) > 1e-9 {
		t.Fatalf("expected MIDI 69 to be 440.000000 Hz, got %f", got)
	}
	if got := MIDIToHz(57); math.Abs(got-220.0) > 1e-9 {
		t.Fatalf("expected MIDI 57 to be 220.000000 Hz, got %f", got)
	}
	if got := MIDIToHz(81); math.Abs(got-880.0) > 1e-9 {
		t.Fatalf("expected MIDI 81 to be 880.000000 Hz, got %f", got)
	}
}
