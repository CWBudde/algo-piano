package analysis

import (
	"math"
	"testing"
)

// shortDecayingPair builds a reference/candidate pair of n samples whose
// partials differ slightly. Below partialWindowSize the partial and segmented
// decay metrics cannot be measured at all.
func shortDecayingPair(n, sampleRate int) (ref, cand []float64) {
	ref = make([]float64, n)
	cand = make([]float64, n)
	for i := range ref {
		env := math.Exp(-float64(i) / float64(sampleRate) * 5)
		ref[i] = env * math.Sin(2*math.Pi*262*float64(i)/float64(sampleRate))
		cand[i] = env * math.Sin(2*math.Pi*265*float64(i)/float64(sampleRate))
	}
	return ref, cand
}

// A window too short for the extended metrics must drop those terms and
// renormalize, not fold NaN into the score. Every profile weights at least one
// always-measurable legacy component, so a finite score is always reachable.
func TestScoreStaysFiniteWhenExtendedMetricsAreUnavailable(t *testing.T) {
	const sampleRate = 48000

	for _, n := range []int{600, 2000, 4000} {
		ref, cand := shortDecayingPair(n, sampleRate)
		for _, profile := range Profiles() {
			w, err := WeightsForProfile(profile)
			if err != nil {
				t.Fatalf("WeightsForProfile(%q): %v", profile, err)
			}
			m := CompareWithWeights(ref, cand, sampleRate, w)
			if !isFinite(m.Score) {
				t.Errorf("n=%d profile=%s: Score = %v, want finite", n, profile, m.Score)
			}
			if m.Score < 0 || m.Score > 1 {
				t.Errorf("n=%d profile=%s: Score = %v, want within [0,1]", n, profile, m.Score)
			}
			if !isFinite(m.Similarity) {
				t.Errorf("n=%d profile=%s: Similarity = %v, want finite", n, profile, m.Similarity)
			}
		}
	}
}

// Availability must track the metric, not the component's position in the
// table: an unmeasurable extended metric is NaN and must be reported as
// unavailable so the score assembly drops it.
func TestExtendedComponentAvailabilityFollowsFiniteness(t *testing.T) {
	const sampleRate = 48000

	extended := map[string]bool{
		ComponentPartialLevel: true,
		ComponentPartialFreq:  true,
		ComponentTristimulus:  true,
		ComponentAttack:       true,
		ComponentDecaySegment: true,
	}

	for _, n := range []int{2000, 8000} {
		ref, cand := shortDecayingPair(n, sampleRate)
		w, err := WeightsForProfile(ProfileBalancedV2)
		if err != nil {
			t.Fatal(err)
		}
		m := CompareWithWeights(ref, cand, sampleRate, w)

		sawUnavailable := false
		for _, c := range Components(m, w) {
			if !extended[c.Name] {
				continue
			}
			if !c.Available {
				sawUnavailable = true
			}
			// The attack term reports a composite norm, so its Raw may be
			// finite while the norm is not; the others map raw to norm
			// directly and must agree with their raw value.
			if c.Name != ComponentAttack && c.Available != isFinite(c.Raw) {
				t.Errorf("n=%d %s: Available = %v but Raw = %v", n, c.Name, c.Available, c.Raw)
			}
		}
		if n == 2000 && !sawUnavailable {
			t.Errorf("n=%d: expected at least one extended component to be unavailable", n)
		}
	}
}
