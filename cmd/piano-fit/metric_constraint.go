package main

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/cwbudde/algo-piano/analysis"
	"github.com/cwbudde/algo-piano/internal/gate"
)

// metricConstraint is one RAW-metric ceiling: the candidate's value for the
// analysis.Metrics field named by Metric (by its JSON tag, exactly as the gate
// threshold files name it) must stay at or below Max, or the candidate is
// rejected.
//
// It exists because scoreConstraint cannot see the metric that actually fails
// the gate. The constrained sustain search optimises decay-v1 under a
// legacy-v1 ceiling, and legacy-v1 SATURATES its spectral component: clamp01
// pins it at 1.0 for everything above analysis.NormSpectral = 30.0, and every
// preset in the repo measures 47.8-68.6 dB. `spectral_rmse_db` therefore
// contributes a constant to legacy-v1's `score` with no gradient, so a search
// steered by decay-v1 and fenced by legacy-v1 is free to move it anywhere at
// all — including straight through `assets/thresholds/c4.json`, which gates it
// as a raw dB value. A score ceiling cannot fix that, whatever profile it
// names. It is a raw-metric problem, so the fence has to be a raw-metric one.
type metricConstraint struct {
	Metric string  `json:"metric"`
	Max    float64 `json:"max"`
}

// metricConstraintProfile is the weighting the constrained metrics are
// measured under.
//
// ONE EXTRA COMPARE, NOT ONE PER METRIC. Every raw field of analysis.Metrics —
// spectral_rmse_db, time_rmse, the partial and attack terms — is
// profile-independent: the weights only ever enter `Score` and `Similarity`.
// So a single comparison yields every raw metric at once. It cannot be an
// ARBITRARY comparison, though, because `score` IS a gated key in c4.json and
// `score` does depend on the weights. legacy-v1 is what the gate scores under
// and what `just distance-c4` prints, so that is the profile the constrained
// metrics come from, and a run that also carries a legacy-v1 score constraint
// reuses that comparison rather than making a second identical one.
const metricConstraintProfile = analysis.ProfileLegacyV1

// worstCaseMetricViolation is the relative violation charged for a metric that
// cannot be measured at all.
//
// A finite breach is scored as `got/max - 1` (see applyMetricConstraints), and
// a non-finite value has no such ratio. 1.0 — "100% over budget" — is a
// deliberately ordinary magnitude rather than something enormous: an
// unmeasurable render must lose to every feasible candidate, which the shared
// constraintPenaltyBase already guarantees, and beyond that the exact number
// only orders infeasible points against each other.
const worstCaseMetricViolation = 1.0

// parseMetricConstraints resolves the repeatable --metric-constraint flag.
//
// An empty list is not an error: it is the default, and it must leave the
// search path exactly as it was before this flag existed.
func parseMetricConstraints(raw []string) ([]metricConstraint, error) {
	known := gate.MetricsByJSONTag(analysis.Metrics{})
	out := make([]metricConstraint, 0, len(raw))
	seen := make(map[string]bool, len(raw))
	for _, entry := range raw {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		metricRaw, maxRaw, ok := strings.Cut(entry, ":")
		if !ok {
			return nil, fmt.Errorf("invalid metric constraint %q (want <json_tag>:<max>)", entry)
		}
		metric := strings.TrimSpace(metricRaw)
		if metric == "" {
			return nil, fmt.Errorf("invalid metric constraint %q (empty metric)", entry)
		}
		// The metric name is resolved against the SAME reflection over
		// analysis.Metrics the gate uses, so a name that works in a threshold
		// file works here and a typo fails in both places identically.
		// Deliberately case-sensitive: the JSON tags are, and
		// "spectral_rmse_dB" is a typo the gate already rejects.
		if _, ok := known[metric]; !ok {
			return nil, fmt.Errorf("unknown metric %q in metric constraint (not a float64 field of analysis.Metrics)", metric)
		}
		maxStr := strings.TrimSpace(maxRaw)
		if maxStr == "" {
			return nil, fmt.Errorf("invalid metric constraint %q (empty maximum)", entry)
		}
		maxVal, err := strconv.ParseFloat(maxStr, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid metric-constraint maximum %q in %q", maxStr, entry)
		}
		if math.IsNaN(maxVal) || math.IsInf(maxVal, 0) {
			return nil, fmt.Errorf("metric-constraint maximum in %q must be finite", entry)
		}
		// A non-positive ceiling is refused for the same reason gate.Evaluate
		// refuses one: it cannot be met by a non-negative error metric, and
		// the relative violation is meaningless below it (division by zero,
		// or an inverted ordering).
		if maxVal <= 0 {
			return nil, fmt.Errorf("metric-constraint maximum in %q must be positive", entry)
		}
		if seen[metric] {
			return nil, fmt.Errorf("duplicate metric constraint %q", metric)
		}
		seen[metric] = true
		out = append(out, metricConstraint{Metric: metric, Max: maxVal})
	}
	return out, nil
}

// loadGateThresholdConstraints turns a gate threshold file into constraints.
//
// Every NON-NULL entry of the file's `max` block becomes a constraint, in
// sorted order so the report and the banner are deterministic. A null entry is
// skipped, matching gate.Evaluate exactly: null means "listed for visibility,
// deliberately not enforced", and a search that enforced it would be fencing
// against something `just gate-c4` does not check. An unknown key is an error,
// again exactly as in gate.Evaluate — passing a zero analysis.Metrics through
// it is the cheapest way to get that validation without duplicating it.
func loadGateThresholdConstraints(path string) ([]metricConstraint, error) {
	spec, err := gate.LoadSpec(path)
	if err != nil {
		return nil, err
	}
	if _, _, _, err := gate.Evaluate(spec, analysis.Metrics{}); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(spec.Max))
	for name, limit := range spec.Max {
		if limit == nil {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]metricConstraint, 0, len(names))
	for _, name := range names {
		out = append(out, metricConstraint{Metric: name, Max: *spec.Max[name]})
	}
	return out, nil
}

// mergeMetricConstraints combines a threshold file with the ad-hoc flags.
//
// An ad-hoc --metric-constraint OVERRIDES the file's entry for the same
// metric rather than colliding with it. The two flags are used together
// precisely to say "the tracked gate, but tighter here", and making that an
// error would force a caller to restate the whole file to move one number.
// Order follows the file, with any metric the file does not name appended in
// the order it was given.
func mergeMetricConstraints(fromFile, fromFlags []metricConstraint) []metricConstraint {
	if len(fromFile) == 0 {
		return fromFlags
	}
	override := make(map[string]float64, len(fromFlags))
	for _, c := range fromFlags {
		override[c.Metric] = c.Max
	}
	out := make([]metricConstraint, 0, len(fromFile)+len(fromFlags))
	used := make(map[string]bool, len(fromFlags))
	for _, c := range fromFile {
		if max, ok := override[c.Metric]; ok {
			c.Max = max
			used[c.Metric] = true
		}
		out = append(out, c)
	}
	for _, c := range fromFlags {
		if !used[c.Metric] {
			out = append(out, c)
		}
	}
	return out
}

// formatMetricConstraints renders the list for the run banner.
func formatMetricConstraints(cs []metricConstraint) string {
	parts := make([]string, 0, len(cs))
	for _, c := range cs {
		parts = append(parts, fmt.Sprintf("%s<=%g", c.Metric, c.Max))
	}
	return strings.Join(parts, ", ")
}

// formatMetricValues renders the measured constrained metrics for the console,
// marking each one against its ceiling.
func formatMetricValues(cs []metricConstraint, values map[string]float64) string {
	parts := make([]string, 0, len(cs))
	for _, c := range cs {
		v, ok := values[c.Metric]
		if !ok {
			parts = append(parts, fmt.Sprintf("%s=n/a", c.Metric))
			continue
		}
		mark := "ok"
		if !isFiniteScore(v) || v > c.Max {
			mark = "BREACH"
		}
		parts = append(parts, fmt.Sprintf("%s=%g (max %g, %s)", c.Metric, v, c.Max, mark))
	}
	return strings.Join(parts, "; ")
}

// metricConstraintProfiles names the extra profile the constrained metrics
// need to be measured under, or nothing when no metric constraint is active.
//
// It is resolved once, at flag-parsing time, so the per-evaluation path never
// re-resolves a weighting — the same reason scoreConstraint caches its own.
func metricConstraintProfiles(cs []metricConstraint) ([]constraintProfile, error) {
	if len(cs) == 0 {
		return nil, nil
	}
	weights, err := analysis.WeightsForProfile(metricConstraintProfile)
	if err != nil {
		return nil, fmt.Errorf("metric constraints need profile %q: %w", metricConstraintProfile, err)
	}
	return []constraintProfile{{name: metricConstraintProfile, weights: weights}}, nil
}

// worstMetricValues reduces the per-note constrained metrics to the single
// value each constraint is checked against.
//
// With one note target — every fit recorded so far — this is just that note's
// value. With several it is the WORST (largest) across notes, because the gate
// is a per-note fence: a preset that clears it on one note and blows through
// it on another has not cleared it. A non-finite value wins over every finite
// one, so it cannot be averaged away by a note that measured cleanly.
func worstMetricValues(cs []metricConstraint, perNote []noteReport) map[string]float64 {
	if len(cs) == 0 || len(perNote) == 0 {
		return nil
	}
	out := make(map[string]float64, len(cs))
	for i, nr := range perNote {
		values := gate.MetricsByJSONTag(nr.Metrics)
		for _, c := range cs {
			got, ok := values[c.Metric]
			if !ok {
				continue
			}
			prev, seen := out[c.Metric]
			if i == 0 || !seen || !isFiniteScore(got) || (isFiniteScore(prev) && got > prev) {
				out[c.Metric] = got
			}
		}
	}
	return out
}

// applyMetricConstraints folds the raw-metric constraints into ev.
//
// It records every constrained value on the eval (so the winner's report can
// state them) and, on a breach, replaces the aggregate with the penalty and
// counts the rejection — the same shape applyScoreConstraints has, for the
// same reasons.
//
// RELATIVE VIOLATION. The penalty adds `(got - max)/|max|` per breached
// metric, not `got - max`. Score violations live in [0,1] while a
// spectral_rmse_db violation is measured in tens of dB, so a raw sum would let
// one metric own the entire penalty gradient and make the others invisible
// inside the infeasible region. The ratio is scale-free: 10% over budget is
// the same violation whether the budget is 0.569 or 62.3.
//
// The absolute value in the denominator is what keeps a breach POSITIVE for
// every ceiling, not just a positive one. For max > 0 the expression is
// algebraically identical to the older `got/max - 1`, so no measured number
// moves; for max <= 0 the old form went NEGATIVE on a genuine breach, and
// since the violations are SUMMED that did not merely fail to reject the
// candidate — it could cancel another metric's real breach. Both entry points
// already refuse a non-positive ceiling (parseMetricConstraints here,
// gate.Evaluate for a threshold file), so this is the third line of defence
// and it costs one math.Abs.
//
// It folds into the SAME constraintPenaltyBase + violation aggregate the score
// constraints use, and for the documented reason (see constraint.go): mayfly
// v0.1.0 only sanitises its initial population, so a +Inf penalty would reach
// the MPMA weighted-median normalisation unfiltered and turn every weight into
// NaN. A candidate breaching both kinds of constraint is charged both
// violations but counted as ONE rejection, because it is one candidate.
//
// With an empty constraint list this is a no-op returning ev unchanged, which
// is what keeps the unconstrained search path bit-identical.
func applyMetricConstraints(
	cs []metricConstraint,
	ev optimizationEval,
	values map[string]float64,
	rejects *atomic.Int64,
) optimizationEval {
	if len(cs) == 0 {
		return ev
	}
	ev.metricValues = make(map[string]float64, len(cs))
	violation := 0.0
	for _, c := range cs {
		got, ok := values[c.Metric]
		if !ok {
			continue
		}
		// The RAW value is what is recorded and what is checked. It reaches
		// the console through formatMetricValues, which marks a non-finite
		// value as a breach, and the JSON report through writeOutputs, whose
		// sanitizeNonFinite pass already records any non-finite number as 0.
		ev.metricValues[c.Metric] = got
		// A non-finite value must never clear a ceiling. `got > c.Max` is
		// FALSE for NaN, and the sanitized stand-ins for the raw dB fields are
		// not worst-case values the way Sanitized() maps a non-finite Score to
		// 1.0 — spectral_rmse_db becomes 60.0, which sits BELOW the tracked C4
		// ceiling of 62.30 — so a sanitized value would let an unmeasurable
		// render pass the one check that exists to stop it. That is why
		// scoreConstraintMetrics hands the raw metrics through unsanitized.
		if !isFiniteScore(got) {
			violation += worstCaseMetricViolation
			continue
		}
		if got > c.Max {
			violation += (got - c.Max) / math.Abs(c.Max)
		}
	}
	if violation <= 0 {
		return ev
	}
	if ev.constraintViolated {
		// A score constraint already turned the aggregate into a penalty and
		// already counted this candidate. Add the magnitude, count nothing.
		ev.aggregate += violation
		return ev
	}
	ev.constraintViolated = true
	ev.aggregate = constraintPenaltyBase + violation
	if rejects != nil {
		rejects.Add(1)
	}
	return ev
}
