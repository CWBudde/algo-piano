package main

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/cwbudde/algo-piano/analysis"
)

// scoreConstraint is one secondary-profile ceiling: the candidate's score under
// Profile must stay at or below Max, or the candidate is rejected.
//
// It exists because a pass fits with its own profile (decay-v1 for --pass
// sustain) while the tracked, comparable number is legacy-v1. An unconstrained
// pass is therefore free to buy decay accuracy with legacy accuracy, which is
// exactly what every recorded sustain run did. The constraint turns "do not
// regress the comparable number" from a post-hoc observation into a search
// constraint.
type scoreConstraint struct {
	Profile string  `json:"profile"`
	Max     float64 `json:"max"`

	// weights is the resolved weighting for Profile. It is cached here so the
	// per-evaluation path never re-resolves it.
	weights analysis.Weights
}

// constraintProfile is one weighting a candidate must be compared under.
//
// It exists because two different constraint kinds need the same comparison.
// A scoreConstraint needs its own profile's `score`; the raw-metric
// constraints in metric_constraint.go need legacy-v1's metrics. Naming the
// profiles in one list is what lets scoreConstraintMetrics compare ONCE PER
// DISTINCT PROFILE instead of once per constraint.
type constraintProfile struct {
	name    string
	weights analysis.Weights
}

// constraintPenaltyBase is the aggregate a breaching candidate reports.
//
// It is a LARGE FINITE penalty rather than +Inf on purpose. github.com/cwbudde/
// mayfly v0.1.0 only sanitises the objective value on its INITIAL population
// evaluation (evaluateWithSanitization -> sanitizeCost maps +Inf to 1e100);
// every later call goes straight to ObjectiveFunc, so a +Inf would reach the
// MPMA weighted-median normalisation `1 - (cost-min)/(max-min)` unfiltered and
// turn every weight into NaN. A finite penalty cannot do that.
//
// The violation magnitude is added on top, so the penalty is ordered: an
// infeasible region still slopes back towards the feasible one instead of being
// a flat plateau the optimizer cannot navigate. It is far above any achievable
// score (scores live in [0,1]), so no feasible candidate can ever lose to an
// infeasible one.
const constraintPenaltyBase = 1e6

// worstCaseScore is the score an unmeasurable render is treated as having. It
// matches analysis.Metrics.Sanitized(), which maps a non-finite Score to 1.0,
// the maximum distance the profiles can express.
const worstCaseScore = 1.0

// isFiniteScore reports whether a profile score is usable in a comparison.
func isFiniteScore(s float64) bool {
	return !math.IsNaN(s) && !math.IsInf(s, 0)
}

// stringListFlag collects a repeatable string flag.
type stringListFlag []string

func (f *stringListFlag) String() string { return strings.Join(*f, ",") }

func (f *stringListFlag) Set(v string) error {
	*f = append(*f, v)
	return nil
}

// parseScoreConstraints resolves the repeatable --score-constraint flag.
//
// An empty list is not an error: it is the default, and it must leave the
// search path exactly as it was before this flag existed.
func parseScoreConstraints(raw []string) ([]scoreConstraint, error) {
	out := make([]scoreConstraint, 0, len(raw))
	seen := make(map[string]bool, len(raw))
	for _, entry := range raw {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		profileRaw, maxRaw, ok := strings.Cut(entry, ":")
		if !ok {
			return nil, fmt.Errorf("invalid score constraint %q (want <profile>:<max>)", entry)
		}
		profile := strings.ToLower(strings.TrimSpace(profileRaw))
		if profile == "" {
			return nil, fmt.Errorf("invalid score constraint %q (empty profile)", entry)
		}
		weights, err := analysis.WeightsForProfile(profile)
		if err != nil {
			return nil, fmt.Errorf("unknown score-constraint profile %q (known: %v)", profile, analysis.Profiles())
		}
		maxStr := strings.TrimSpace(maxRaw)
		if maxStr == "" {
			return nil, fmt.Errorf("invalid score constraint %q (empty maximum)", entry)
		}
		maxVal, err := strconv.ParseFloat(maxStr, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid score-constraint maximum %q in %q", maxStr, entry)
		}
		if math.IsNaN(maxVal) || math.IsInf(maxVal, 0) {
			return nil, fmt.Errorf("score-constraint maximum in %q must be finite", entry)
		}
		if seen[profile] {
			return nil, fmt.Errorf("duplicate score-constraint profile %q", profile)
		}
		seen[profile] = true
		out = append(out, scoreConstraint{Profile: profile, Max: maxVal, weights: weights})
	}
	return out, nil
}

// formatScoreConstraints renders the list for the run banner.
func formatScoreConstraints(cs []scoreConstraint) string {
	parts := make([]string, 0, len(cs))
	for _, c := range cs {
		parts = append(parts, fmt.Sprintf("%s<=%.4f", c.Profile, c.Max))
	}
	return strings.Join(parts, ", ")
}

// formatConstraintScores renders the measured constrained scores for the
// console, marking each one against its ceiling.
func formatConstraintScores(cs []scoreConstraint, scores map[string]float64) string {
	parts := make([]string, 0, len(cs))
	for _, c := range cs {
		s, ok := scores[c.Profile]
		if !ok {
			parts = append(parts, fmt.Sprintf("%s=n/a", c.Profile))
			continue
		}
		mark := "ok"
		if s > c.Max {
			mark = "BREACH"
		}
		parts = append(parts, fmt.Sprintf("%s=%.4f (max %.4f, %s)", c.Profile, s, c.Max, mark))
	}
	return strings.Join(parts, "; ")
}

// scoreConstraintMetrics scores an ALREADY RENDERED candidate buffer under
// every constrained profile.
//
// This is the same trick newSweepEvaluator uses: the render dominates the
// per-evaluation cost by orders of magnitude, so an extra
// analysis.CompareWithOptions per profile against the same buffer is nearly
// free, and it keeps every bit-identity risk out of analysis/.
//
// The comparison is deliberately UNWINDOWED even when the pass windows its own
// primary score: the constraint exists to protect a number that is comparable
// to `just distance-c4`, and a windowed compare re-runs trimLeadingSilence,
// normalizeRMS and lag estimation inside the window, producing a score that is
// comparable to nothing.
//
// Every result is Sanitized() before it leaves this function, and that is a
// correctness requirement rather than cosmetics. A constraint is enforced as
// `score > max`, which is FALSE for a NaN score — so an undefined comparison
// would pass the ceiling silently and let a degenerate candidate through the
// one check that exists to stop it. Sanitized() maps a non-finite Score to
// 1.0, the maximum distance, so an unmeasurable render breaches every ceiling
// below 1.0 instead of clearing all of them. It also keeps the numbers written
// into best_constraint_scores finite and printable.
//
// `extra` names profiles that must be measured even though no scoreConstraint
// asks for them — today that is the single legacy-v1 comparison the raw-metric
// constraints read their values from (see metric_constraint.go). A profile
// already covered by cs is NOT compared twice: the raw metrics are
// profile-independent and the score is fully determined by the weighting, so
// the second comparison would be bit-identical work.
func scoreConstraintMetrics(
	cs []scoreConstraint,
	extra []constraintProfile,
	reference, cand []float64,
	sampleRate, midiNote int,
) map[string]analysis.Metrics {
	if len(cs) == 0 && len(extra) == 0 {
		return nil
	}
	profiles := make([]constraintProfile, 0, len(cs)+len(extra))
	seen := make(map[string]bool, len(cs)+len(extra))
	for _, c := range cs {
		if seen[c.Profile] {
			continue
		}
		seen[c.Profile] = true
		profiles = append(profiles, constraintProfile{name: c.Profile, weights: c.weights})
	}
	for _, p := range extra {
		if seen[p.name] {
			continue
		}
		seen[p.name] = true
		profiles = append(profiles, p)
	}
	out := make(map[string]analysis.Metrics, len(profiles))
	for _, p := range profiles {
		out[p.name] = analysis.CompareWithOptions(reference, cand, sampleRate, analysis.Options{
			Weights:  p.weights,
			MIDINote: midiNote,
		}).Sanitized()
	}
	return out
}

// applyScoreConstraints folds the per-profile constraint scores into ev.
//
// It records every constrained score on the eval (so the winner's report can
// state them) and, on a breach, replaces the aggregate with the penalty and
// counts the rejection. Rejections are counted rather than silently dropped:
// a run whose search space was mostly infeasible is a very different result
// from one that was mostly feasible, and the report has to say which it was.
//
// With an empty constraint list this is a no-op returning ev unchanged, which
// is what keeps the unconstrained search path bit-identical.
func applyScoreConstraints(
	cs []scoreConstraint,
	ev optimizationEval,
	scores map[string]float64,
	rejects *atomic.Int64,
) optimizationEval {
	if len(cs) == 0 {
		return ev
	}
	ev.constraintScores = make(map[string]float64, len(cs))
	violation := 0.0
	for _, c := range cs {
		s, ok := scores[c.Profile]
		if !ok {
			continue
		}
		// A non-finite score must never clear a ceiling. scoreConstraintMetrics
		// already sanitises, so this is the second line of defence at the one
		// place the comparison actually happens: `s > c.Max` is false for NaN,
		// so without the explicit test an undefined score would read as a pass.
		if !isFiniteScore(s) {
			ev.constraintScores[c.Profile] = worstCaseScore
			violation += worstCaseScore - c.Max
			continue
		}
		ev.constraintScores[c.Profile] = s
		if s > c.Max {
			violation += s - c.Max
		}
	}
	if violation <= 0 {
		return ev
	}
	ev.constraintViolated = true
	ev.aggregate = constraintPenaltyBase + violation
	if rejects != nil {
		rejects.Add(1)
	}
	return ev
}

// postMatchEval finishes the winner's post-gain-match re-score.
//
// That re-score goes through scoreParams, which is the right thing — it
// measures every constrained profile on the same fresh render, so
// best_constraint_scores describes the preset that is written — but it folds
// the constraints in the way it does for a SEARCH candidate. Two corrections
// are needed, because the winner is not a search candidate:
//
//   - A breach there must not overwrite the primary score with the search
//     penalty. The report still has to state a readable score; the breach is
//     carried by constraintViolated and by the run's non-zero exit.
//   - The re-score must not be counted as a rejection, or constraint_rejections
//     would be off by one exactly when the run failed.
//
// A feasible re-score needs neither correction and is returned unchanged.
//
// Both constraint kinds are handled here, because both write the same
// penalty into the same aggregate and both increment the same counter.
func postMatchEval(cfg *optimizationConfig, ev optimizationEval, rejectsBefore int) optimizationEval {
	if !cfg.constrained() || !ev.constraintViolated {
		return ev
	}
	ev.aggregate = aggregateScores(ev.notes, targetWeights(cfg.targets), cfg.aggregate)
	if cfg.constraintRejects != nil {
		cfg.constraintRejects.Store(int64(rejectsBefore))
	}
	return ev
}
