package main

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/cwbudde/algo-piano/analysis"
	"github.com/cwbudde/algo-piano/piano"
)

// None of these tests render audio, so they run without reference/c4.wav,
// which is gitignored and absent from CI.

func TestParseMetricConstraints(t *testing.T) {
	t.Run("empty list stays empty", func(t *testing.T) {
		got, err := parseMetricConstraints(nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("got %v, want no constraints", got)
		}
	})

	t.Run("metric:max", func(t *testing.T) {
		got, err := parseMetricConstraints([]string{" spectral_rmse_db : 62.3 "})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("got %d constraints, want 1", len(got))
		}
		if got[0].Metric != "spectral_rmse_db" {
			t.Fatalf("metric = %q, want spectral_rmse_db", got[0].Metric)
		}
		if got[0].Max != 62.3 {
			t.Fatalf("max = %v, want 62.3", got[0].Max)
		}
	})

	t.Run("repeats accumulate in order", func(t *testing.T) {
		got, err := parseMetricConstraints([]string{"spectral_rmse_db:62.3", "score:0.569"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 2 || got[0].Metric != "spectral_rmse_db" || got[1].Metric != "score" {
			t.Fatalf("got %v, want spectral_rmse_db then score", got)
		}
	})

	t.Run("blank entries are skipped, not rejected", func(t *testing.T) {
		got, err := parseMetricConstraints([]string{"  ", "score:0.569"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("got %v, want just the one real constraint", got)
		}
	})

	t.Run("errors", func(t *testing.T) {
		cases := map[string][]string{
			"unknown metric":   {"spectral_rmse:62.3"},
			"wrong case":       {"spectral_rmse_dB:62.3"},
			"not a float64":    {"attack_available:1"},
			"int field":        {"sample_rate:48000"},
			"missing colon":    {"spectral_rmse_db 62.3"},
			"empty metric":     {":62.3"},
			"empty maximum":    {"spectral_rmse_db:"},
			"malformed number": {"spectral_rmse_db:not-a-number"},
			"NaN maximum":      {"spectral_rmse_db:NaN"},
			"positive Inf":     {"spectral_rmse_db:+Inf"},
			"negative Inf":     {"spectral_rmse_db:-Inf"},
			"zero maximum":     {"spectral_rmse_db:0"},
			"negative maximum": {"spectral_rmse_db:-1"},
			"duplicate":        {"spectral_rmse_db:62.3", "spectral_rmse_db:61.0"},
		}
		for name, raw := range cases {
			t.Run(name, func(t *testing.T) {
				if got, err := parseMetricConstraints(raw); err == nil {
					t.Fatalf("expected an error, got %v", got)
				}
			})
		}
	})

	t.Run("unknown metric names the input", func(t *testing.T) {
		_, err := parseMetricConstraints([]string{"spectral_rmse:62.3"})
		if err == nil {
			t.Fatal("expected an error")
		}
		if !strings.Contains(err.Error(), "spectral_rmse") {
			t.Fatalf("error %q should name the offending metric", err)
		}
	})
}

func TestLoadGateThresholdConstraints(t *testing.T) {
	write := func(t *testing.T, body string) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), "thresholds.json")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
		return path
	}

	t.Run("null entries are listed but not enforced", func(t *testing.T) {
		path := write(t, `{"max":{"spectral_rmse_db":62.3,"score":0.569,"partial_freq_rmse_cents":null}}`)
		got, err := loadGateThresholdConstraints(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// Sorted by name, so the banner and the report are deterministic.
		want := []metricConstraint{
			{Metric: "score", Max: 0.569},
			{Metric: "spectral_rmse_db", Max: 62.3},
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %+v, want %+v", got, want)
		}
	})

	t.Run("an unknown key is an error", func(t *testing.T) {
		path := write(t, `{"max":{"spectral_rmse_dB":62.3}}`)
		_, err := loadGateThresholdConstraints(path)
		if err == nil {
			t.Fatal("expected an error for an unknown metric key")
		}
		if !strings.Contains(err.Error(), "spectral_rmse_dB") {
			t.Fatalf("error %q should name the offending key", err)
		}
	})

	t.Run("a null-ed unknown key is still an error", func(t *testing.T) {
		path := write(t, `{"max":{"attack_available":null}}`)
		if _, err := loadGateThresholdConstraints(path); err == nil {
			t.Fatal("a null-ed typo must not look like an uncalibrated metric")
		}
	})

	t.Run("a missing file is an error", func(t *testing.T) {
		if _, err := loadGateThresholdConstraints(filepath.Join(t.TempDir(), "nope.json")); err == nil {
			t.Fatal("expected an error")
		}
	})

	// The tracked file is what `just fit-sustain-constrained-c4` passes, so a
	// change to it that this loader cannot consume must fail here rather than
	// three minutes into a fitting run.
	t.Run("the tracked C4 file loads its five enforced metrics", func(t *testing.T) {
		got, err := loadGateThresholdConstraints("../../assets/thresholds/c4.json")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 5 {
			t.Fatalf("got %d constraints, want the 5 enforced ones: %+v", len(got), got)
		}
		names := make(map[string]bool, len(got))
		for _, c := range got {
			names[c.Metric] = true
			if c.Max <= 0 {
				t.Errorf("%s has a non-positive ceiling %v", c.Metric, c.Max)
			}
		}
		for _, want := range []string{"score", "time_rmse", "envelope_rmse_db", "spectral_rmse_db", "decay_diff_db_per_s"} {
			if !names[want] {
				t.Errorf("%s should be constrained", want)
			}
		}
	})
}

func TestMergeMetricConstraints(t *testing.T) {
	file := []metricConstraint{{Metric: "score", Max: 0.569}, {Metric: "spectral_rmse_db", Max: 62.3}}

	t.Run("an ad-hoc entry overrides the file", func(t *testing.T) {
		got := mergeMetricConstraints(file, []metricConstraint{{Metric: "spectral_rmse_db", Max: 58.0}})
		want := []metricConstraint{{Metric: "score", Max: 0.569}, {Metric: "spectral_rmse_db", Max: 58.0}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %+v, want %+v", got, want)
		}
	})

	t.Run("a metric the file omits is appended", func(t *testing.T) {
		got := mergeMetricConstraints(file, []metricConstraint{{Metric: "time_rmse", Max: 0.1}})
		if len(got) != 3 || got[2].Metric != "time_rmse" {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("either side alone is passed through", func(t *testing.T) {
		if got := mergeMetricConstraints(nil, file); !reflect.DeepEqual(got, file) {
			t.Fatalf("got %+v", got)
		}
		if got := mergeMetricConstraints(file, nil); !reflect.DeepEqual(got, file) {
			t.Fatalf("got %+v", got)
		}
		if got := mergeMetricConstraints(nil, nil); len(got) != 0 {
			t.Fatalf("got %+v, want nothing", got)
		}
	})
}

func TestMetricConstraintProfiles(t *testing.T) {
	if got, err := metricConstraintProfiles(nil); err != nil || got != nil {
		t.Fatalf("got %+v, %v; want no profile and no error", got, err)
	}
	got, err := metricConstraintProfiles([]metricConstraint{{Metric: "spectral_rmse_db", Max: 62.3}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].name != analysis.ProfileLegacyV1 {
		t.Fatalf("got %+v, want the legacy-v1 profile", got)
	}
	want, err := analysis.WeightsForProfile(analysis.ProfileLegacyV1)
	if err != nil {
		t.Fatalf("WeightsForProfile: %v", err)
	}
	if got[0].weights != want {
		t.Fatal("weights were not resolved at parse time")
	}
}

// TestScoreConstraintMetricsComparesOncePerProfile pins the "one extra compare,
// not one per metric" design point: a legacy-v1 score constraint and any number
// of raw-metric constraints share ONE comparison.
func TestScoreConstraintMetricsComparesOncePerProfile(t *testing.T) {
	cs := legacyConstraint(t, 0.5121)
	extra, err := metricConstraintProfiles([]metricConstraint{{Metric: "spectral_rmse_db", Max: 62.3}})
	if err != nil {
		t.Fatalf("metricConstraintProfiles: %v", err)
	}
	ref := make([]float64, 4096)
	cand := make([]float64, 4096)
	for i := range ref {
		ref[i] = math.Sin(float64(i) * 0.05)
		cand[i] = math.Sin(float64(i) * 0.051)
	}
	out := scoreConstraintMetrics(cs, extra, ref, cand, 48000, 60)
	if len(out) != 1 {
		t.Fatalf("got %d comparisons, want exactly 1 (legacy-v1 covers both kinds): %v", len(out), out)
	}
	if _, ok := out[analysis.ProfileLegacyV1]; !ok {
		t.Fatalf("legacy-v1 missing from %v", out)
	}

	t.Run("the extra profile alone is still measured", func(t *testing.T) {
		out := scoreConstraintMetrics(nil, extra, ref, cand, 48000, 60)
		if len(out) != 1 {
			t.Fatalf("got %d comparisons, want 1: %v", len(out), out)
		}
	})
}

func metricCS(metric string, max float64) []metricConstraint {
	return []metricConstraint{{Metric: metric, Max: max}}
}

func TestApplyMetricConstraints(t *testing.T) {
	cs := metricCS("spectral_rmse_db", 62.3)

	t.Run("compliant candidate is untouched", func(t *testing.T) {
		var rejects atomic.Int64
		ev := optimizationEval{aggregate: 0.35}
		got := applyMetricConstraints(cs, ev, map[string]float64{"spectral_rmse_db": 57.8}, &rejects)
		if got.aggregate != 0.35 {
			t.Fatalf("aggregate = %v, want the primary score 0.35", got.aggregate)
		}
		if got.constraintViolated {
			t.Fatal("a compliant candidate must not be marked violated")
		}
		if got.metricValues["spectral_rmse_db"] != 57.8 {
			t.Fatalf("metric value not recorded: %v", got.metricValues)
		}
		if rejects.Load() != 0 {
			t.Fatalf("rejects = %d, want 0", rejects.Load())
		}
	})

	t.Run("breaching candidate is penalised and counted", func(t *testing.T) {
		var rejects atomic.Int64
		ev := optimizationEval{aggregate: 0.20}
		got := applyMetricConstraints(cs, ev, map[string]float64{"spectral_rmse_db": 65.0}, &rejects)
		if !got.constraintViolated {
			t.Fatal("a breaching candidate must be marked violated")
		}
		if got.aggregate <= constraintPenaltyBase {
			t.Fatalf("aggregate = %v, want > %v", got.aggregate, constraintPenaltyBase)
		}
		if math.IsInf(got.aggregate, 0) || math.IsNaN(got.aggregate) {
			t.Fatalf("aggregate must stay finite, got %v", got.aggregate)
		}
		worse := applyMetricConstraints(cs, ev, map[string]float64{"spectral_rmse_db": 80.0}, &rejects)
		if worse.aggregate <= got.aggregate {
			t.Fatalf("a larger breach must score worse: %v vs %v", worse.aggregate, got.aggregate)
		}
		if got.metricValues["spectral_rmse_db"] != 65.0 {
			t.Fatalf("metric value not recorded: %v", got.metricValues)
		}
		if rejects.Load() != 2 {
			t.Fatalf("rejects = %d, want 2", rejects.Load())
		}
	})

	t.Run("exactly at the ceiling is compliant", func(t *testing.T) {
		got := applyMetricConstraints(cs, optimizationEval{aggregate: 0.4}, map[string]float64{"spectral_rmse_db": 62.3}, nil)
		if got.constraintViolated {
			t.Fatal("<= max must be accepted")
		}
	})

	t.Run("nil rejects counter is safe", func(t *testing.T) {
		got := applyMetricConstraints(cs, optimizationEval{aggregate: 0.4}, map[string]float64{"spectral_rmse_db": 99}, nil)
		if !got.constraintViolated {
			t.Fatal("expected a violation")
		}
	})

	t.Run("an unmeasured metric neither breaches nor is recorded", func(t *testing.T) {
		got := applyMetricConstraints(cs, optimizationEval{aggregate: 0.4}, map[string]float64{}, nil)
		if got.constraintViolated {
			t.Fatal("a metric the compare did not produce must not fabricate a breach")
		}
		if _, ok := got.metricValues["spectral_rmse_db"]; ok {
			t.Fatal("nothing was measured, so nothing may be reported")
		}
	})
}

// TestApplyMetricConstraintsViolationIsRelative is the reason the penalty
// divides instead of subtracting. Score violations live in [0,1] while a
// spectral_rmse_db violation is tens of dB, so a raw `got - max` sum would let
// the dB metric own the whole gradient and make the score breach invisible.
func TestApplyMetricConstraintsViolationIsRelative(t *testing.T) {
	base := optimizationEval{aggregate: 0.2}

	// 10% over budget on either metric must cost exactly the same.
	spectral := applyMetricConstraints(metricCS("spectral_rmse_db", 62.3), base,
		map[string]float64{"spectral_rmse_db": 62.3 * 1.1}, nil)
	score := applyMetricConstraints(metricCS("score", 0.569), base,
		map[string]float64{"score": 0.569 * 1.1}, nil)
	if math.Abs(spectral.aggregate-score.aggregate) > 1e-9 {
		t.Fatalf("equal relative breaches must cost the same: %v vs %v", spectral.aggregate, score.aggregate)
	}
	if got := spectral.aggregate - constraintPenaltyBase; math.Abs(got-0.1) > 1e-9 {
		t.Fatalf("violation = %v, want got/max-1 = 0.1", got)
	}

	// A 20% breach on the score must outrank a 10% breach on the dB metric,
	// which raw subtraction would get backwards by a factor of a hundred.
	small := applyMetricConstraints(metricCS("spectral_rmse_db", 62.3), base,
		map[string]float64{"spectral_rmse_db": 62.3 * 1.1}, nil)
	large := applyMetricConstraints(metricCS("score", 0.569), base,
		map[string]float64{"score": 0.569 * 1.2}, nil)
	if large.aggregate <= small.aggregate {
		t.Fatalf("the larger relative breach must score worse: %v vs %v", large.aggregate, small.aggregate)
	}
}

// TestApplyMetricConstraintsTreatsNonFiniteAsBreach pins the comparison the
// constraint machinery cannot get wrong. `got > max` is FALSE for NaN, and
// unlike Score — which analysis.Metrics.Sanitized() maps to the worst-case 1.0
// — the raw dB fields carry no such mapping, so an unmeasurable render would
// otherwise clear every ceiling.
func TestApplyMetricConstraintsTreatsNonFiniteAsBreach(t *testing.T) {
	cs := metricCS("spectral_rmse_db", 62.3)
	for name, v := range map[string]float64{
		"NaN":          math.NaN(),
		"positive Inf": math.Inf(1),
		"negative Inf": math.Inf(-1),
	} {
		t.Run(name, func(t *testing.T) {
			var rejects atomic.Int64
			got := applyMetricConstraints(cs, optimizationEval{aggregate: 0.2},
				map[string]float64{"spectral_rmse_db": v}, &rejects)
			if !got.constraintViolated {
				t.Fatalf("a %s metric must breach, not clear the ceiling", name)
			}
			if math.IsNaN(got.aggregate) || math.IsInf(got.aggregate, 0) {
				t.Fatalf("aggregate must stay finite, got %v", got.aggregate)
			}
			if rejects.Load() != 1 {
				t.Fatalf("rejects = %d, want 1", rejects.Load())
			}
		})
	}
}

// TestApplyMetricConstraintsEmptyListIsIdentity mirrors
// TestApplyScoreConstraintsEmptyListIsIdentity: with no constraint configured
// the eval must come back bit-identical, with no map allocated and nothing
// counted. This is what keeps every tracked report reproducible.
func TestApplyMetricConstraintsEmptyListIsIdentity(t *testing.T) {
	var rejects atomic.Int64
	in := optimizationEval{
		aggregate:    0.4321,
		notes:        []noteReport{{Note: 60, Score: 0.4321}},
		metrics:      analysis.Metrics{Score: 0.4321},
		velocity:     118,
		releaseAfter: 3.5,
	}
	got := applyMetricConstraints(nil, in, map[string]float64{"spectral_rmse_db": 999}, &rejects)
	if !reflect.DeepEqual(got, in) {
		t.Fatalf("empty constraint list changed the eval:\n got %+v\nwant %+v", got, in)
	}
	if got.metricValues != nil {
		t.Fatal("no map may be allocated when no constraint is configured")
	}
	if rejects.Load() != 0 {
		t.Fatalf("rejects = %d, want 0", rejects.Load())
	}
}

// TestBothConstraintKindsCountOneRejection is the off-by-one this feature is
// most likely to spring: a candidate breaching a score ceiling AND a metric
// ceiling is one rejected candidate, not two, but it must be charged both
// violations so the penalty still slopes towards feasibility.
func TestBothConstraintKindsCountOneRejection(t *testing.T) {
	var rejects atomic.Int64
	ev := optimizationEval{aggregate: 0.2}
	ev = applyScoreConstraints(legacyConstraint(t, 0.5121), ev,
		map[string]float64{analysis.ProfileLegacyV1: 0.5321}, &rejects)
	scoreOnly := ev.aggregate
	ev = applyMetricConstraints(metricCS("spectral_rmse_db", 62.3), ev,
		map[string]float64{"spectral_rmse_db": 65.0}, &rejects)
	if rejects.Load() != 1 {
		t.Fatalf("rejects = %d, want 1 for one candidate", rejects.Load())
	}
	if ev.aggregate <= scoreOnly {
		t.Fatalf("the metric violation must be added on top: %v vs %v", ev.aggregate, scoreOnly)
	}
	if ev.aggregate <= constraintPenaltyBase {
		t.Fatalf("aggregate = %v, want > %v", ev.aggregate, constraintPenaltyBase)
	}
}

func TestWorstMetricValues(t *testing.T) {
	cs := metricCS("spectral_rmse_db", 62.3)

	t.Run("one note is that note", func(t *testing.T) {
		got := worstMetricValues(cs, []noteReport{{Metrics: analysis.Metrics{SpectralRMSEDB: 57.8}}})
		if got["spectral_rmse_db"] != 57.8 {
			t.Fatalf("got %v", got)
		}
	})

	t.Run("several notes take the worst", func(t *testing.T) {
		got := worstMetricValues(cs, []noteReport{
			{Metrics: analysis.Metrics{SpectralRMSEDB: 57.8}},
			{Metrics: analysis.Metrics{SpectralRMSEDB: 65.1}},
			{Metrics: analysis.Metrics{SpectralRMSEDB: 60.0}},
		})
		if got["spectral_rmse_db"] != 65.1 {
			t.Fatalf("got %v, want the worst note's 65.1", got)
		}
	})

	t.Run("a non-finite note wins over a clean one", func(t *testing.T) {
		got := worstMetricValues(cs, []noteReport{
			{Metrics: analysis.Metrics{SpectralRMSEDB: math.NaN()}},
			{Metrics: analysis.Metrics{SpectralRMSEDB: 10.0}},
		})
		if !math.IsNaN(got["spectral_rmse_db"]) {
			t.Fatalf("got %v, want NaN to survive so it can breach", got)
		}
	})

	t.Run("nothing to do", func(t *testing.T) {
		if got := worstMetricValues(nil, []noteReport{{}}); got != nil {
			t.Fatalf("got %v, want nil", got)
		}
		if got := worstMetricValues(cs, nil); got != nil {
			t.Fatalf("got %v, want nil", got)
		}
	})
}

func TestMetricConstraintFieldsReachTheReport(t *testing.T) {
	defs := []knobDef{{Name: "unison_detune_scale", Min: 0.2, Max: 2.0}}
	req := outputRequest{
		outputPreset: filepath.Join(t.TempDir(), "fitted.json"),
		sampleRate:   48000,
		note:         60,
		defs:         defs,
		best:         candidate{Vals: []float64{1.75}},
		bestScore:    0.3508,
		bestMetrics:  analysis.Metrics{Score: 0.3508, ScoreProfile: analysis.ProfileDecayV1},
		bestParams:   &piano.Params{},
		pass:         passSustain,

		gateThresholds:       "assets/thresholds/c4.json",
		metricConstraints:    metricCS("spectral_rmse_db", 62.3),
		constraintRejections: 1234,
		bestMetricValues:     map[string]float64{"spectral_rmse_db": 57.8},
	}
	if err := writeOutputs(req); err != nil {
		t.Fatalf("writeOutputs: %v", err)
	}

	var rep struct {
		GateThresholds    string `json:"gate_thresholds"`
		MetricConstraints []struct {
			Metric string  `json:"metric"`
			Max    float64 `json:"max"`
		} `json:"metric_constraints"`
		ConstraintRejections int                `json:"constraint_rejections"`
		BestMetricValues     map[string]float64 `json:"best_metric_values"`
	}
	raw, err := os.ReadFile(req.outputPreset + ".report.json")
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	if err := json.Unmarshal(raw, &rep); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}
	if rep.GateThresholds != "assets/thresholds/c4.json" {
		t.Fatalf("gate_thresholds = %q", rep.GateThresholds)
	}
	if len(rep.MetricConstraints) != 1 ||
		rep.MetricConstraints[0].Metric != "spectral_rmse_db" ||
		rep.MetricConstraints[0].Max != 62.3 {
		t.Fatalf("metric_constraints = %+v", rep.MetricConstraints)
	}
	if rep.ConstraintRejections != 1234 {
		t.Fatalf("constraint_rejections = %d, want 1234", rep.ConstraintRejections)
	}
	if got := rep.BestMetricValues["spectral_rmse_db"]; got != 57.8 {
		t.Fatalf("best_metric_values[spectral_rmse_db] = %v, want 57.8", got)
	}

	t.Run("absent without constraints", func(t *testing.T) {
		plain := req
		plain.outputPreset = filepath.Join(t.TempDir(), "fitted.json")
		plain.gateThresholds = ""
		plain.metricConstraints = nil
		plain.constraintRejections = 0
		plain.bestMetricValues = nil
		if err := writeOutputs(plain); err != nil {
			t.Fatalf("writeOutputs: %v", err)
		}
		raw, err := os.ReadFile(plain.outputPreset + ".report.json")
		if err != nil {
			t.Fatalf("read report: %v", err)
		}
		for _, key := range []string{"gate_thresholds", "metric_constraints", "best_metric_values"} {
			if strings.Contains(string(raw), key) {
				t.Fatalf("unconstrained report must not carry %q", key)
			}
		}
	})
}

// TestPostMatchEvalWithMetricConstraints repeats the two corrections
// TestPostMatchEval pins, for a run constrained ONLY by raw metrics. Before
// cfg.constrained() existed, the guard tested cfg.scoreConstraints alone, and
// such a run would have kept the search penalty as its reported score and
// double-counted its own winner as a rejection.
func TestPostMatchEvalWithMetricConstraints(t *testing.T) {
	newCfg := func(cs []metricConstraint) *optimizationConfig {
		return &optimizationConfig{
			targets:           []noteTarget{{note: 60, weight: 1}},
			aggregate:         aggregateMean,
			metricConstraints: cs,
			constraintRejects: new(atomic.Int64),
		}
	}

	t.Run("a breach restores the readable primary score", func(t *testing.T) {
		cfg := newCfg(metricCS("spectral_rmse_db", 62.3))
		cfg.constraintRejects.Store(43)
		ev := optimizationEval{
			aggregate:          constraintPenaltyBase + 0.04,
			notes:              []noteReport{{Note: 60, Score: 0.3625}},
			metricValues:       map[string]float64{"spectral_rmse_db": 65.0},
			constraintViolated: true,
		}
		got := postMatchEval(cfg, ev, 42)
		if got.aggregate != 0.3625 {
			t.Fatalf("aggregate = %v, want the primary 0.3625", got.aggregate)
		}
		if !got.constraintViolated {
			t.Fatal("the breach must survive: it is what fails the run")
		}
		if n := cfg.constraintRejects.Load(); n != 42 {
			t.Fatalf("rejects = %d, want the pre-re-score 42", n)
		}
	})

	t.Run("a feasible re-score is untouched", func(t *testing.T) {
		cfg := newCfg(metricCS("spectral_rmse_db", 62.3))
		cfg.constraintRejects.Store(7)
		ev := optimizationEval{
			aggregate:    0.3625,
			notes:        []noteReport{{Note: 60, Score: 0.3625}},
			metricValues: map[string]float64{"spectral_rmse_db": 57.8},
		}
		got := postMatchEval(cfg, ev, 7)
		if !reflect.DeepEqual(got, ev) {
			t.Fatalf("got %+v, want the eval unchanged", got)
		}
		if n := cfg.constraintRejects.Load(); n != 7 {
			t.Fatalf("rejects = %d, want 7", n)
		}
	})

	t.Run("an unconstrained run is untouched", func(t *testing.T) {
		cfg := newCfg(nil)
		ev := optimizationEval{aggregate: 0.42, constraintViolated: true}
		if got := postMatchEval(cfg, ev, 0); !reflect.DeepEqual(got, ev) {
			t.Fatalf("got %+v, want the eval unchanged", got)
		}
	})
}

func TestFormatMetricHelpers(t *testing.T) {
	cs := []metricConstraint{{Metric: "score", Max: 0.569}, {Metric: "spectral_rmse_db", Max: 62.3}}
	if got := formatMetricConstraints(cs); got != "score<=0.569, spectral_rmse_db<=62.3" {
		t.Fatalf("formatMetricConstraints = %q", got)
	}
	got := formatMetricValues(cs, map[string]float64{"score": 0.6, "spectral_rmse_db": math.NaN()})
	if !strings.Contains(got, "score=0.6 (max 0.569, BREACH)") {
		t.Fatalf("formatMetricValues = %q", got)
	}
	if !strings.Contains(got, "spectral_rmse_db=NaN (max 62.3, BREACH)") {
		t.Fatalf("a non-finite value must read as a breach: %q", got)
	}
	if got := formatMetricValues(cs, nil); !strings.Contains(got, "score=n/a") {
		t.Fatalf("formatMetricValues = %q", got)
	}
}
