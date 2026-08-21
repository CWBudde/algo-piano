package main

import (
	"fmt"
	"math"
	"reflect"
	"sort"
	"strings"

	"github.com/cwbudde/algo-piano/analysis"
)

// gateSpec is the on-disk threshold file for the C4 regression gate.
//
// Only the "max" block is enforced; everything else in the file (the
// "recorded" provenance block, comments) is documentation for humans and is
// ignored here. A nil pointer in Max means "not yet calibrated": the metric is
// listed so its existence is visible, but it is NOT enforced. Calibrating one
// later is a one-line diff, not a new file.
type gateSpec struct {
	Max map[string]*float64 `json:"max"`
}

// gateBreach records one metric that exceeded its threshold.
type gateBreach struct {
	Metric string
	Got    float64
	Max    float64
}

// metricsByJSONTag flattens m into a map keyed by the JSON tag of every
// float64 field. Keying off the tags rather than a hand-maintained switch is
// what lets a newly added metric become gateable with zero code changes here:
// add the field in analysis.Metrics, and the threshold file can name it.
//
// Because an unknown key in the threshold file is an error (that is how typos
// get caught), this map must stay complete.
// TestMetricsByJSONTagCoversAllFloatFields enforces that.
func metricsByJSONTag(m analysis.Metrics) map[string]float64 {
	out := make(map[string]float64)
	v := reflect.ValueOf(m)
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.Type.Kind() != reflect.Float64 {
			continue
		}
		name := jsonTagName(f)
		if name == "" {
			continue
		}
		out[name] = v.Field(i).Float()
	}
	return out
}

// jsonTagName returns the JSON object key a struct field serializes to, or ""
// when the field is not serialized at all.
func jsonTagName(f reflect.StructField) string {
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

// evaluateGate checks m against spec.
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
func evaluateGate(spec gateSpec, m analysis.Metrics) (breaches []gateBreach, worstUsedFraction float64, worstMetric string, err error) {
	values := metricsByJSONTag(m)

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
		if *limit == 0 {
			return nil, 0, "", fmt.Errorf("gate: metric %q has a zero threshold, which cannot be met", name)
		}
		used := got / *limit
		if used > worstUsedFraction {
			worstUsedFraction = used
			worstMetric = name
		}
		if got > *limit {
			breaches = append(breaches, gateBreach{Metric: name, Got: got, Max: *limit})
		}
	}
	return breaches, worstUsedFraction, worstMetric, nil
}

// enforcedCount reports how many metrics in spec carry an actual threshold.
func (s gateSpec) enforcedCount() int {
	n := 0
	for _, v := range s.Max {
		if v != nil {
			n++
		}
	}
	return n
}

// formatBreach renders one breach as the single-line stderr form.
func formatBreach(b gateBreach) string {
	over := 0.0
	if b.Max != 0 {
		over = (b.Got/b.Max - 1.0) * 100.0
	}
	if math.IsNaN(over) || math.IsInf(over, 0) {
		over = 0
	}
	return fmt.Sprintf("gate: FAIL %s=%.2f > max %.2f (+%.1f%%)", b.Metric, b.Got, b.Max, over)
}
