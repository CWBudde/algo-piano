package analysis

import (
	"math"
	"testing"
)

// TestLegacyNormsMatchFrozenConstants pins the legacy scales to the package
// constants. Those constants are what every tracked *.report.json and
// assets/thresholds/c4.json was measured against, so a change here silently
// rewrites recorded history rather than improving anything.
func TestLegacyNormsMatchFrozenConstants(t *testing.T) {
	n := LegacyNorms()
	cases := []struct {
		name      string
		got, want float64
	}{
		{"time", n.Time, 0.25},
		{"envelope", n.Envelope, 30.0},
		{"spectral", n.Spectral, 30.0},
		{"decay", n.Decay, 40.0},
		{"partial_level", n.PartialLevel, 12.0},
		{"partial_freq", n.PartialFreq, 50.0},
		{"tristimulus", n.Tristimulus, 0.5},
		{"attack_rise", n.AttackRise, 20.0},
		{"attack_centroid", n.AttackCentroid, 0.5},
		{"decay_segment", n.DecaySegment, 30.0},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("legacy norm %q = %v, want %v (frozen)", c.name, c.got, c.want)
		}
	}
}

// TestNormsResolveFillsUnsetFields covers the "name only what you change"
// contract: a profile that recalibrates one scale must inherit the rest rather
// than dividing by zero.
func TestNormsResolveFillsUnsetFields(t *testing.T) {
	legacy := LegacyNorms()

	if got := (Norms{}).resolve(); got != legacy {
		t.Fatalf("zero Norms resolved to %+v, want legacy %+v", got, legacy)
	}

	partial := Norms{Spectral: 80.0}.resolve()
	if partial.Spectral != 80.0 {
		t.Errorf("explicit spectral norm was overwritten: got %v, want 80", partial.Spectral)
	}
	if partial.Time != legacy.Time || partial.DecaySegment != legacy.DecaySegment {
		t.Errorf("unset fields did not fall back to legacy: %+v", partial)
	}

	// Negative and non-finite values are not meaningful divisors, so they are
	// treated as unset rather than propagated into a NaN score.
	for _, bad := range []float64{-1, math.NaN(), math.Inf(1)} {
		if got := (Norms{Spectral: bad}).resolve().Spectral; got != legacy.Spectral {
			t.Errorf("Norms{Spectral: %v} resolved to %v, want legacy %v", bad, got, legacy.Spectral)
		}
	}
}

// TestComponentsUseProfileNorms is the point of the whole indirection: the same
// metrics scored with a larger norm must yield a smaller normalized value, and
// a component that saturates against the legacy scale must stop saturating
// against a calibrated one.
func TestComponentsUseProfileNorms(t *testing.T) {
	m := Metrics{SpectralRMSEDB: 60.0}

	legacyProfile := Weights{Spectral: 1.0}
	legacySpectral := findComponent(t, Components(m, legacyProfile), ComponentSpectral)
	if legacySpectral.Norm != 1.0 || !legacySpectral.Saturated {
		t.Fatalf("60 dB against the legacy norm of 30 should saturate: norm=%v saturated=%v",
			legacySpectral.Norm, legacySpectral.Saturated)
	}

	calibrated := Weights{Spectral: 1.0, Norms: Norms{Spectral: 80.0}}
	got := findComponent(t, Components(m, calibrated), ComponentSpectral)
	if got.Saturated {
		t.Errorf("60 dB against a norm of 80 must not saturate")
	}
	if want := 60.0 / 80.0; math.Abs(got.Norm-want) > 1e-12 {
		t.Errorf("spectral norm = %v, want %v", got.Norm, want)
	}
	if got.Raw != 60.0 {
		t.Errorf("Raw must stay in its natural unit, got %v", got.Raw)
	}
}

// TestAttackNormUsesProfileNorms covers the composite component, whose halves
// are normalized inside attackNormOf rather than by the generic raw/scale path.
func TestAttackNormUsesProfileNorms(t *testing.T) {
	m := Metrics{
		AttackAvailable:       true,
		AttackRiseDiffMS:      40.0,
		AttackCentroidRMSEOct: 0,
	}
	if got := attackNormOf(m, LegacyNorms()); got != 0.5 {
		t.Fatalf("40 ms against the legacy 20 ms norm saturates its half: got %v, want 0.5", got)
	}
	relaxed := Norms{AttackRise: 80.0}.resolve()
	if want, got := 0.25, attackNormOf(m, relaxed); math.Abs(got-want) > 1e-12 {
		t.Errorf("40 ms against an 80 ms norm should give %v, got %v", want, got)
	}
}

// TestLegacyProfileKeepsFrozenNorms is the compatibility guard: legacy-v1 must
// never pick up a recalibrated scale, or every number recorded in a tracked
// *.report.json and in assets/thresholds/c4.json stops meaning what it says.
func TestLegacyProfileKeepsFrozenNorms(t *testing.T) {
	w, err := WeightsForProfile(ProfileLegacyV1)
	if err != nil {
		t.Fatalf("legacy profile: %v", err)
	}
	if got := w.Norms.resolve(); got != LegacyNorms() {
		t.Errorf("legacy-v1 resolved norms %+v, want %+v", got, LegacyNorms())
	}
}

// TestNonLegacyProfilesUseCalibratedNorms covers the other half of the rule
// recorded in the calibration note: a profile meant to steer an optimizer must
// ship recalibrated norms rather than inheriting the frozen ones.
func TestNonLegacyProfilesUseCalibratedNorms(t *testing.T) {
	for _, name := range Profiles() {
		if name == ProfileLegacyV1 {
			continue
		}
		w, err := WeightsForProfile(name)
		if err != nil {
			t.Fatalf("profile %q: %v", name, err)
		}
		if got := w.Norms.resolve(); got != CalibratedNorms() {
			t.Errorf("profile %q resolved norms %+v, want calibrated %+v", name, got, CalibratedNorms())
		}
	}
}

// TestCalibratedNormsCoverObservedPopulation is the test that would have caught
// the NormSpectral problem years earlier. It replays the worst raw value each
// metric reached across the tracked preset population (measured 2026-08-21
// against reference/c4.wav, note 60, velocity 118, release-after 3.5 s; see
// CalibratedNorms for the table and for why modal-calibrated.json is excluded)
// and asserts that no weighted component saturates. A saturated component
// contributes a constant, so the optimizer cannot see it move.
func TestCalibratedNormsCoverObservedPopulation(t *testing.T) {
	worst := Metrics{
		TimeRMSE:               0.1678,
		EnvelopeRMSEDB:         24.3471,
		SpectralRMSEDB:         71.3031,
		DecayDiffDBPerS:        14.662,
		PartialLevelRMSEDB:     27.3943,
		PartialFreqRMSECents:   48.9185,
		TristimulusDistance:    0.3998,
		DecaySegmentRMSEDBPerS: 23.2282,
		AttackAvailable:        true,
		AttackRiseDiffMS:       24.487,
		AttackCentroidRMSEOct:  1.3976,
	}

	for _, name := range Profiles() {
		if name == ProfileLegacyV1 {
			continue // deliberately frozen and known to saturate
		}
		w, err := WeightsForProfile(name)
		if err != nil {
			t.Fatalf("profile %q: %v", name, err)
		}
		for _, c := range Components(worst, w) {
			if c.Weight == 0 {
				continue
			}
			if c.Saturated {
				t.Errorf("profile %q: component %q saturates at its worst observed value %v (norm gives no gradient)",
					name, c.Name, c.Raw)
			}
			t.Logf("profile %-18s %-14s raw=%-9.4g norm=%.3f", name, c.Name, c.Raw, c.Norm)
		}
	}
}

// TestLegacySpectralStillSaturates records, rather than fixes, the state of the
// frozen profile. legacy-v1 keeps NormSpectral = 30.0 and therefore keeps
// saturating on every real preset; that is the cost of comparability, and
// piano-distance warns about it on every run. If this ever stops being true,
// the legacy score has changed and every recorded number needs re-measuring.
func TestLegacySpectralStillSaturates(t *testing.T) {
	m := Metrics{SpectralRMSEDB: 51.5} // the BEST spectral RMSE in the population
	c := findComponent(t, Components(m, DefaultWeights()), ComponentSpectral)
	if !c.Saturated || c.Norm != 1.0 {
		t.Errorf("legacy spectral component unexpectedly unsaturated (norm=%v): "+
			"the frozen legacy score has changed and every recorded number needs re-measuring", c.Norm)
	}
}

func findComponent(t *testing.T, comps []Component, name string) Component {
	t.Helper()
	for _, c := range comps {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("component %q not found", name)
	return Component{}
}
