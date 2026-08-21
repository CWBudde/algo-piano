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

// TestRegisteredProfilesResolveToLegacyNorms records the state this change
// deliberately leaves behind: the plumbing exists, but no profile uses it yet,
// so every profile still scores exactly as it did before. Recalibrating the
// non-legacy profiles is a separate, measured change.
func TestRegisteredProfilesResolveToLegacyNorms(t *testing.T) {
	legacy := LegacyNorms()
	for _, name := range Profiles() {
		w, err := WeightsForProfile(name)
		if err != nil {
			t.Fatalf("profile %q: %v", name, err)
		}
		if got := w.Norms.resolve(); got != legacy {
			t.Errorf("profile %q resolved norms %+v, want legacy %+v", name, got, legacy)
		}
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
