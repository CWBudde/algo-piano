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
func scoreConstraintMetrics(
	cs []scoreConstraint,
	reference, cand []float64,
	sampleRate, midiNote int,
) map[string]analysis.Metrics {
	if len(cs) == 0 {
		return nil
	}
	out := make(map[string]analysis.Metrics, len(cs))
	for _, c := range cs {
		out[c.Profile] = analysis.CompareWithOptions(reference, cand, sampleRate, analysis.Options{
			Weights:  c.weights,
			MIDINote: midiNote,
		})
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
