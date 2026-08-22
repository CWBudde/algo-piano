package main

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/cwbudde/algo-piano/analysis"
	"github.com/cwbudde/algo-piano/piano"
)

// None of these tests render audio, so they run without reference/c4.wav,
// which is gitignored and absent from CI.

func TestParseScoreConstraints(t *testing.T) {
	t.Run("empty list stays empty", func(t *testing.T) {
		got, err := parseScoreConstraints(nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("got %v, want no constraints", got)
		}
	})

	t.Run("profile:max", func(t *testing.T) {
		got, err := parseScoreConstraints([]string{" Legacy-V1 : 0.5121 "})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("got %d constraints, want 1", len(got))
		}
		if got[0].Profile != analysis.ProfileLegacyV1 {
			t.Fatalf("profile = %q, want %q", got[0].Profile, analysis.ProfileLegacyV1)
		}
		if got[0].Max != 0.5121 {
			t.Fatalf("max = %v, want 0.5121", got[0].Max)
		}
		want, err := analysis.WeightsForProfile(analysis.ProfileLegacyV1)
		if err != nil {
			t.Fatalf("WeightsForProfile: %v", err)
		}
		if got[0].weights != want {
			t.Fatal("weights were not resolved at parse time")
		}
	})

	t.Run("repeats accumulate in order", func(t *testing.T) {
		got, err := parseScoreConstraints([]string{"legacy-v1:0.52", "attack-v1:0.30"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("got %d constraints, want 2", len(got))
		}
		if got[0].Profile != analysis.ProfileLegacyV1 || got[1].Profile != analysis.ProfileAttackV1 {
			t.Fatalf("got %v, want legacy-v1 then attack-v1", got)
		}
	})

	t.Run("errors", func(t *testing.T) {
		cases := map[string][]string{
			"unknown profile":  {"does-not-exist:0.5"},
			"missing colon":    {"legacy-v1 0.5"},
			"empty profile":    {":0.5"},
			"empty maximum":    {"legacy-v1:"},
			"malformed number": {"legacy-v1:not-a-number"},
			"NaN maximum":      {"legacy-v1:NaN"},
			"positive Inf":     {"legacy-v1:+Inf"},
			"negative Inf":     {"legacy-v1:-Inf"},
			"duplicate":        {"legacy-v1:0.5", "legacy-v1:0.6"},
		}
		for name, raw := range cases {
			t.Run(name, func(t *testing.T) {
				if got, err := parseScoreConstraints(raw); err == nil {
					t.Fatalf("expected an error, got %v", got)
				}
			})
		}
	})

	t.Run("unknown profile names the input", func(t *testing.T) {
		_, err := parseScoreConstraints([]string{"legcy-v1:0.5"})
		if err == nil {
			t.Fatal("expected an error")
		}
		if !strings.Contains(err.Error(), "legcy-v1") {
			t.Fatalf("error %q should name the offending profile", err)
		}
	})
}

func TestStringListFlag(t *testing.T) {
	var f stringListFlag
	if err := f.Set("legacy-v1:0.5"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := f.Set("attack-v1:0.3"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if got := []string(f); !reflect.DeepEqual(got, []string{"legacy-v1:0.5", "attack-v1:0.3"}) {
		t.Fatalf("got %v", got)
	}
	if got := f.String(); got != "legacy-v1:0.5,attack-v1:0.3" {
		t.Fatalf("String() = %q", got)
	}
}

func legacyConstraint(t *testing.T, max float64) []scoreConstraint {
	t.Helper()
	cs, err := parseScoreConstraints([]string{"legacy-v1:" + strconv.FormatFloat(max, 'g', -1, 64)})
	if err != nil {
		t.Fatalf("parseScoreConstraints: %v", err)
	}
	return cs
}

func TestApplyScoreConstraints(t *testing.T) {
	cs := legacyConstraint(t, 0.5121)

	t.Run("compliant candidate is untouched", func(t *testing.T) {
		var rejects atomic.Int64
		ev := optimizationEval{aggregate: 0.35}
		got := applyScoreConstraints(cs, ev, map[string]float64{analysis.ProfileLegacyV1: 0.5100}, &rejects)
		if got.aggregate != 0.35 {
			t.Fatalf("aggregate = %v, want the primary score 0.35", got.aggregate)
		}
		if got.constraintViolated {
			t.Fatal("a compliant candidate must not be marked violated")
		}
		if got.constraintScores[analysis.ProfileLegacyV1] != 0.5100 {
			t.Fatalf("constraint score not recorded: %v", got.constraintScores)
		}
		if rejects.Load() != 0 {
			t.Fatalf("rejects = %d, want 0", rejects.Load())
		}
	})

	t.Run("breaching candidate is penalised and counted", func(t *testing.T) {
		var rejects atomic.Int64
		ev := optimizationEval{aggregate: 0.20}
		got := applyScoreConstraints(cs, ev, map[string]float64{analysis.ProfileLegacyV1: 0.5300}, &rejects)
		if !got.constraintViolated {
			t.Fatal("a breaching candidate must be marked violated")
		}
		if got.aggregate <= constraintPenaltyBase {
			t.Fatalf("aggregate = %v, want > %v", got.aggregate, constraintPenaltyBase)
		}
		if math.IsInf(got.aggregate, 0) || math.IsNaN(got.aggregate) {
			t.Fatalf("aggregate must stay finite, got %v", got.aggregate)
		}
		// The penalty is ordered by violation so the infeasible region still
		// slopes back towards the feasible one.
		worse := applyScoreConstraints(cs, ev, map[string]float64{analysis.ProfileLegacyV1: 0.6000}, &rejects)
		if worse.aggregate <= got.aggregate {
			t.Fatalf("a larger breach must score worse: %v vs %v", worse.aggregate, got.aggregate)
		}
		// The breaching score is still recorded, so the report can show it.
		if got.constraintScores[analysis.ProfileLegacyV1] != 0.5300 {
			t.Fatalf("constraint score not recorded: %v", got.constraintScores)
		}
		if rejects.Load() != 2 {
			t.Fatalf("rejects = %d, want 2", rejects.Load())
		}
	})

	t.Run("exactly at the ceiling is compliant", func(t *testing.T) {
		got := applyScoreConstraints(cs, optimizationEval{aggregate: 0.4}, map[string]float64{analysis.ProfileLegacyV1: 0.5121}, nil)
		if got.constraintViolated {
			t.Fatal("<= max must be accepted")
		}
	})

	t.Run("nil rejects counter is safe", func(t *testing.T) {
		got := applyScoreConstraints(cs, optimizationEval{aggregate: 0.4}, map[string]float64{analysis.ProfileLegacyV1: 0.9}, nil)
		if !got.constraintViolated {
			t.Fatal("expected a violation")
		}
	})
}

// TestApplyScoreConstraintsEmptyListIsIdentity is the test that protects every
// tracked report: with no constraint configured the eval must come back
// bit-identical, with no map allocated and nothing counted.
func TestApplyScoreConstraintsEmptyListIsIdentity(t *testing.T) {
	var rejects atomic.Int64
	in := optimizationEval{
		aggregate:    0.4321,
		notes:        []noteReport{{Note: 60, Score: 0.4321}},
		metrics:      analysis.Metrics{Score: 0.4321},
		velocity:     118,
		releaseAfter: 3.5,
	}
	got := applyScoreConstraints(nil, in, map[string]float64{analysis.ProfileLegacyV1: 0.9}, &rejects)
	if !reflect.DeepEqual(got, in) {
		t.Fatalf("empty constraint list changed the eval:\n got %+v\nwant %+v", got, in)
	}
	if got.constraintScores != nil {
		t.Fatal("no map may be allocated when no constraint is configured")
	}
	if rejects.Load() != 0 {
		t.Fatalf("rejects = %d, want 0", rejects.Load())
	}
	if scoreConstraintMetrics(nil, []float64{1}, []float64{1}, 48000, 60) != nil {
		t.Fatal("scoreConstraintMetrics must do no work without constraints")
	}
}

// constrainedEvaluator wraps a fake evaluator in exactly the fold
// evaluateCandidate applies, so the polish stage sees the same code path it
// sees in a real run.
func constrainedEvaluator(
	inner candidateEvaluator,
	cs []scoreConstraint,
	secondary func(candidate) map[string]float64,
	rejects *atomic.Int64,
) candidateEvaluator {
	return func(c candidate, scratch string, s evalSettings) (optimizationEval, error) {
		ev, err := inner(c, scratch, s)
		if err != nil {
			return ev, err
		}
		return applyScoreConstraints(cs, ev, secondary(c), rejects), nil
	}
}

// TestPolishRefusesConstraintBreachingImprovement is the leak this feature is
// most likely to spring: --polish accepts a step whenever it improves, and an
// improvement on the PRIMARY profile that breaches the secondary floor must
// still be refused.
func TestPolishRefusesConstraintBreachingImprovement(t *testing.T) {
	defs := polishTestDefs()
	// The primary optimum sits above the start on knob 1, so the +step probe
	// always improves.
	target := []float64{0.5, 0.9, 0.5, 0.5}
	base := quadraticEvaluator(defs, target, nil)
	start := fromNormalized([]float64{0.5, 0.5, 0.5, 0.5}, defs)

	// The secondary score IS the knob-1 position and the ceiling is exactly the
	// start, so every step towards the primary optimum breaches it while the
	// start itself stays feasible.
	secondary := func(c candidate) map[string]float64 {
		return map[string]float64{analysis.ProfileLegacyV1: toNormalized(c, defs)[1]}
	}
	ceiling := toNormalized(start, defs)[1]
	cs := legacyConstraint(t, ceiling)

	pcfg := polishConfig{
		knobIndices: []int{1},
		maxEvals:    200,
		rounds:      12,
		initialStep: 0.08,
		shrink:      0.5,
		minStep:     0.004,
	}

	// Control: without the constraint the polish stage does move up, so the
	// test below proves the constraint did the rejecting.
	unconstrainedStart, err := base(start, "", evalSettings{})
	if err != nil {
		t.Fatalf("start eval: %v", err)
	}
	freeBest, freeEval, freeSummary := polishCandidate(defs, base, pcfg, start, unconstrainedStart)
	if freeSummary.Improvements == 0 || freeEval.aggregate >= unconstrainedStart.aggregate {
		t.Fatalf("control run did not improve, summary=%+v", freeSummary)
	}
	if toNormalized(freeBest, defs)[1] <= ceiling {
		t.Fatal("control run should have moved knob 1 past the ceiling")
	}

	var rejects atomic.Int64
	eval := constrainedEvaluator(base, cs, secondary, &rejects)
	startEval, err := eval(start, "", evalSettings{})
	if err != nil {
		t.Fatalf("start eval: %v", err)
	}
	if startEval.constraintViolated {
		t.Fatalf("the start must be feasible, scores=%v", startEval.constraintScores)
	}

	best, bestEval, summary := polishCandidate(defs, eval, pcfg, start, startEval)

	if pos := toNormalized(best, defs)[1]; pos > ceiling {
		t.Fatalf("polish accepted a breaching step: knob 1 at %v, ceiling %v", pos, ceiling)
	}
	if summary.Improvements != 0 {
		t.Fatalf("polish accepted %d steps; every improving step breaches the constraint", summary.Improvements)
	}
	if bestEval.aggregate != startEval.aggregate {
		t.Fatalf("aggregate moved: %v -> %v", startEval.aggregate, bestEval.aggregate)
	}
	if rejects.Load() == 0 {
		t.Fatal("no rejection was counted; the constraint never fired")
	}
}

// TestPolishWithEmptyConstraintListIsUnchanged pins the other half of the
// bit-identity promise: wrapping the evaluator with an EMPTY constraint list
// must reproduce the unconstrained descent exactly.
func TestPolishWithEmptyConstraintListIsUnchanged(t *testing.T) {
	defs := polishTestDefs()
	target := []float64{0.5, 0.9, 0.5, 0.5}
	start := fromNormalized([]float64{0.5, 0.5, 0.5, 0.5}, defs)
	pcfg := polishConfig{
		knobIndices: []int{1},
		maxEvals:    200,
		rounds:      12,
		initialStep: 0.08,
		shrink:      0.5,
		minStep:     0.004,
	}

	plainCount, wrappedCount := 0, 0
	plain := quadraticEvaluator(defs, target, &plainCount)
	plainStart, _ := plain(start, "", evalSettings{})
	plainBest, plainEval, plainSummary := polishCandidate(defs, plain, pcfg, start, plainStart)

	var rejects atomic.Int64
	wrapped := constrainedEvaluator(
		quadraticEvaluator(defs, target, &wrappedCount),
		nil,
		func(candidate) map[string]float64 { return map[string]float64{analysis.ProfileLegacyV1: 9.9} },
		&rejects,
	)
	wrappedStart, _ := wrapped(start, "", evalSettings{})
	wrappedBest, wrappedEval, wrappedSummary := polishCandidate(defs, wrapped, pcfg, start, wrappedStart)

	if !reflect.DeepEqual(plainBest, wrappedBest) {
		t.Fatalf("candidate differs: %v vs %v", plainBest, wrappedBest)
	}
	if plainEval.aggregate != wrappedEval.aggregate {
		t.Fatalf("aggregate differs: %v vs %v", plainEval.aggregate, wrappedEval.aggregate)
	}
	if !reflect.DeepEqual(plainSummary, wrappedSummary) {
		t.Fatalf("summary differs: %+v vs %+v", plainSummary, wrappedSummary)
	}
	if plainCount != wrappedCount {
		t.Fatalf("evaluation count differs: %d vs %d", plainCount, wrappedCount)
	}
	if rejects.Load() != 0 {
		t.Fatalf("rejects = %d, want 0", rejects.Load())
	}
}

func TestConstraintRejectCount(t *testing.T) {
	if got := constraintRejectCount(&optimizationConfig{}); got != 0 {
		t.Fatalf("nil counter should read 0, got %d", got)
	}
	var rejects atomic.Int64
	rejects.Store(7)
	if got := constraintRejectCount(&optimizationConfig{constraintRejects: &rejects}); got != 7 {
		t.Fatalf("got %d, want 7", got)
	}
}

// TestConstraintFieldsReachTheReport covers the audit trail: the ceilings, the
// rejection count and the WINNER's score under every constrained profile all
// have to be in the written report, and none of them may appear when no
// constraint was configured.
func TestConstraintFieldsReachTheReport(t *testing.T) {
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

		scoreConstraints:     legacyConstraint(t, 0.5121),
		constraintRejections: 1234,
		bestConstraintScores: map[string]float64{analysis.ProfileLegacyV1: 0.5086},
	}
	if err := writeOutputs(req); err != nil {
		t.Fatalf("writeOutputs: %v", err)
	}

	var rep struct {
		ScoreConstraints []struct {
			Profile string  `json:"profile"`
			Max     float64 `json:"max"`
		} `json:"score_constraints"`
		ConstraintRejections int                `json:"constraint_rejections"`
		BestConstraintScores map[string]float64 `json:"best_constraint_scores"`
	}
	raw, err := os.ReadFile(req.outputPreset + ".report.json")
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	if err := json.Unmarshal(raw, &rep); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}
	if len(rep.ScoreConstraints) != 1 ||
		rep.ScoreConstraints[0].Profile != analysis.ProfileLegacyV1 ||
		rep.ScoreConstraints[0].Max != 0.5121 {
		t.Fatalf("score_constraints = %+v", rep.ScoreConstraints)
	}
	if rep.ConstraintRejections != 1234 {
		t.Fatalf("constraint_rejections = %d, want 1234", rep.ConstraintRejections)
	}
	if got := rep.BestConstraintScores[analysis.ProfileLegacyV1]; got != 0.5086 {
		t.Fatalf("best_constraint_scores[legacy-v1] = %v, want 0.5086", got)
	}

	t.Run("absent without constraints", func(t *testing.T) {
		plain := req
		plain.outputPreset = filepath.Join(t.TempDir(), "fitted.json")
		plain.scoreConstraints = nil
		plain.constraintRejections = 0
		plain.bestConstraintScores = nil
		if err := writeOutputs(plain); err != nil {
			t.Fatalf("writeOutputs: %v", err)
		}
		raw, err := os.ReadFile(plain.outputPreset + ".report.json")
		if err != nil {
			t.Fatalf("read report: %v", err)
		}
		for _, key := range []string{"score_constraints", "constraint_rejections", "best_constraint_scores"} {
			if strings.Contains(string(raw), key) {
				t.Fatalf("unconstrained report must not carry %q", key)
			}
		}
	})
}
