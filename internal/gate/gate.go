// Package gate holds the threshold-file model behind the C4 regression gate.
//
// It lives here rather than in `cmd/piano-distance` because two commands need
// the same answer to the same question — "does this analysis.Metrics clear the
// tracked thresholds?" — and a second, hand-written copy of the resolution
// rules would be a gate that enforces something subtly different from the one
// `just gate-c4` runs. `cmd/piano-distance` gates a finished preset after the
// fact; `cmd/piano-fit` constrains candidates DURING a search. Both must agree
// on which key names which metric, on what a null threshold means and on what
// an unknown key means, or the search would optimise against a fence that is
// not the one it is later measured by.
//
// Printing stays with the caller: this package answers, it does not report.
package gate

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strings"

	"github.com/cwbudde/algo-piano/analysis"
)

// Spec is the on-disk threshold file for the C4 regression gate.
//
// Only the "max" block is enforced; everything else in the file (the
// "recorded" provenance block, comments) is documentation for humans and is
// ignored here. A nil pointer in Max means "not yet calibrated": the metric is
// listed so its existence is visible, but it is NOT enforced. Calibrating one
// later is a one-line diff, not a new file.
type Spec struct {
	Max map[string]*float64 `json:"max"`
}

// Breach records one metric that exceeded its threshold.
type Breach struct {
	Metric string
	Got    float64
	Max    float64
}

// LoadSpec reads and parses the threshold file at path.
//
// It deliberately does NOT validate the metric names: that check needs an
// analysis.Metrics to resolve tags against and belongs to Evaluate, which is
// the one place that can name the offending key. A caller holding no
// measurement can still get the check by calling Evaluate with a zero
// analysis.Metrics, which is what TestTrackedC4ThresholdFileIsValid does.
func LoadSpec(path string) (Spec, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Spec{}, fmt.Errorf("failed to read thresholds: %w", err)
	}
	var spec Spec
	if err := json.Unmarshal(raw, &spec); err != nil {
		return Spec{}, fmt.Errorf("failed to parse thresholds %s: %w", path, err)
	}
	return spec, nil
}

// MetricsByJSONTag flattens m into a map keyed by the JSON tag of every
// float64 field. Keying off the tags rather than a hand-maintained switch is
// what lets a newly added metric become gateable with zero code changes here:
// add the field in analysis.Metrics, and the threshold file can name it.
//
// Because an unknown key in the threshold file is an error (that is how typos
// get caught), this map must stay complete.
// TestMetricsByJSONTagCoversAllFloatFields enforces that.
func MetricsByJSONTag(m analysis.Metrics) map[string]float64 {
	out := make(map[string]float64)
	v := reflect.ValueOf(m)
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.Type.Kind() != reflect.Float64 {
			continue
		}
		name := JSONTagName(f)
		if name == "" {
			continue
		}
		out[name] = v.Field(i).Float()
	}
	return out
}

// JSONTagName returns the JSON object key a struct field serializes to, or ""
// when the field is not serialized at all.
func JSONTagName(f reflect.StructField) string {
	tag, ok := f.Tag.Lookup("json")
	if !ok {
		return f.Name
	}
	name, _, _ := strings.Cut(tag, ",")
	if name == "-" {
		return ""
	}
	if name == "" {
		return f.Name
	}
	return name
}

// Evaluate checks m against spec.
//
// It returns every breached metric (sorted by name for deterministic output),
// plus the worst fraction of budget used across the ENFORCED metrics and the
// metric that used it. The headroom figure is the point of the gate on a
// passing run: a metric creeping from 70% to 97% of budget is a regression in
// progress, and it should be visible before it trips.
//
// An unknown key in spec.Max is an error. A threshold file naming a metric
// that does not exist is a typo, and a silently ignored typo is a gate that
// silently enforces nothing.
func Evaluate(spec Spec, m analysis.Metrics) (breaches []Breach, worstUsedFraction float64, worstMetric string, err error) {
	values := MetricsByJSONTag(m)

	names := make([]string, 0, len(spec.Max))
	for name := range spec.Max {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		limit := spec.Max[name]
		got, ok := values[name]
		if !ok {
			return nil, 0, "", fmt.Errorf("gate: unknown metric %q in thresholds (not a float64 field of analysis.Metrics)", name)
		}
		if limit == nil {
			// Not yet calibrated: listed for visibility, deliberately unenforced.
			continue
		}
		if *limit <= 0 {
			// Every gated metric is a non-negative error magnitude — an RMSE,
			// an absolute difference, a distance — so a non-positive ceiling
			// can only be met by a perfect measurement, if at all. It also
			// breaks the arithmetic built on top: worstUsedFraction below is
			// `got / limit`, which is undefined at zero and inverts its
			// ordering when negative, and cmd/piano-fit turns the same
			// threshold into a relative violation for its search penalty. A
			// non-positive entry is therefore a broken threshold file, not a
			// strict one, and it is refused rather than silently mis-enforced.
			return nil, 0, "", fmt.Errorf("gate: metric %q has a non-positive threshold %g, which cannot be met", name, *limit)
		}
		used := got / *limit
		if used > worstUsedFraction {
			worstUsedFraction = used
			worstMetric = name
		}
		if got > *limit {
			breaches = append(breaches, Breach{Metric: name, Got: got, Max: *limit})
		}
	}
	return breaches, worstUsedFraction, worstMetric, nil
}

// EnforcedCount reports how many metrics in spec carry an actual threshold.
func (s Spec) EnforcedCount() int {
	n := 0
	for _, v := range s.Max {
		if v != nil {
			n++
		}
	}
	return n
}
